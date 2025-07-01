package recording

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// ResilienceManager handles automatic restart, health checks, and failure recovery
type ResilienceManager struct {
	cfg           *config.Config
	db            database.Database
	diskManager   *storage.DiskManager
	networkMgr    *RTSPConnectionManager
	stateMgr      *StatePersistenceManager
	recordingState *RecordingState
	workers       map[string]*WorkerState
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
}

// WorkerState tracks the state and health of a camera worker
type WorkerState struct {
	CameraName      string
	Camera          config.CameraConfig
	Index           int
	RestartCount    int
	LastRestart     time.Time
	LastHealthCheck time.Time
	IsHealthy       bool
	Process         *os.Process
	Context         context.Context
	Cancel          context.CancelFunc
	BackoffDelay    time.Duration
	MaxRestarts     int
}

// NewResilienceManager creates a new resilience manager
func NewResilienceManager(cfg *config.Config, db database.Database, diskManager *storage.DiskManager) *ResilienceManager {
	ctx, cancel := context.WithCancel(context.Background())
	stateMgr := NewStatePersistenceManager(cfg)
	
	// Load existing state
	recordingState, err := stateMgr.LoadState()
	if err != nil {
		log.Printf("[resilience] Failed to load recording state: %v, using default", err)
		recordingState = &RecordingState{
			CameraStates:  make(map[string]CameraState),
			SystemStarted: time.Now(),
		}
	}
	
	return &ResilienceManager{
		cfg:            cfg,
		db:             db,
		diskManager:    diskManager,
		networkMgr:     NewRTSPConnectionManager(),
		stateMgr:       stateMgr,
		recordingState: recordingState,
		workers:        make(map[string]*WorkerState),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start begins the resilience monitoring
func (rm *ResilienceManager) Start() {
	log.Println("[resilience] Starting resilience manager")
	
	// Clean up old camera states
	rm.stateMgr.CleanupOldStates(rm.recordingState)
	
	// Save initial state
	if err := rm.stateMgr.SaveState(rm.recordingState); err != nil {
		log.Printf("[resilience] Failed to save initial state: %v", err)
	}
	
	// Start health check routine
	go rm.healthCheckLoop()
	
	// Start disk space monitoring
	go rm.diskSpaceMonitor()
	
	// Start worker recovery monitor
	go rm.workerRecoveryLoop()
	
	// Start state persistence routine
	go rm.statePersistenceLoop()
}

// Stop stops the resilience manager
func (rm *ResilienceManager) Stop() {
	log.Println("[resilience] Stopping resilience manager")
	rm.cancel()
	
	// Stop all workers
	rm.mu.Lock()
	for _, worker := range rm.workers {
		if worker.Cancel != nil {
			worker.Cancel()
		}
	}
	rm.mu.Unlock()
}

// StartCameraWithResilience starts a camera with automatic restart capabilities
func (rm *ResilienceManager) StartCameraWithResilience(cam config.CameraConfig, idx int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	if _, exists := rm.workers[cam.Name]; exists {
		log.Printf("[resilience] Camera %s already running", cam.Name)
		return
	}
	
	worker := &WorkerState{
		CameraName:   cam.Name,
		Camera:       cam,
		Index:        idx,
		BackoffDelay: 1 * time.Second, // Start with 1 second
		MaxRestarts:  50,              // Allow up to 50 restarts per hour
		IsHealthy:    true,
	}
	
	rm.workers[cam.Name] = worker
	rm.startWorker(worker)
}

// startWorker starts or restarts a camera worker
func (rm *ResilienceManager) startWorker(worker *WorkerState) {
	// Cancel existing worker if running
	if worker.Cancel != nil {
		worker.Cancel()
	}
	
	// Create new context
	worker.Context, worker.Cancel = context.WithCancel(rm.ctx)
	
	// Start the worker goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[resilience] Camera %s panic recovered: %v", worker.CameraName, r)
				rm.scheduleRestart(worker, fmt.Errorf("panic: %v", r))
			}
		}()
		
		log.Printf("[resilience] Starting camera worker %s (restart #%d)", worker.CameraName, worker.RestartCount)
		
		// Start the actual capture process with network resilience
		outputDir := filepath.Join(rm.cfg.StoragePath, "recordings", worker.Camera.Name)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("[resilience] Failed to create output directory for %s: %v", worker.CameraName, err)
			rm.scheduleRestart(worker, err)
			return
		}
		
		// Update state to recording
		rm.stateMgr.UpdateCameraState(rm.recordingState, worker.CameraName, true, true)
		
		err := rm.networkMgr.StartResilientRTSPCapture(worker.Context, worker.Camera, outputDir)
		
		// Update state to not recording
		rm.stateMgr.UpdateCameraState(rm.recordingState, worker.CameraName, false, err == nil)
		
		if err != nil && worker.Context.Err() == nil {
			log.Printf("[resilience] Camera %s failed: %v", worker.CameraName, err)
			rm.scheduleRestart(worker, err)
		}
	}()
	
	worker.LastRestart = time.Now()
	worker.LastHealthCheck = time.Now()
}


// scheduleRestart schedules a restart with exponential backoff
func (rm *ResilienceManager) scheduleRestart(worker *WorkerState, err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	worker.RestartCount++
	worker.IsHealthy = false
	
	// Record restart in persistent state
	rm.stateMgr.RecordRestart(rm.recordingState, worker.CameraName)
	
	// Check if we've exceeded max restarts in the last hour
	if time.Since(worker.LastRestart) < time.Hour && worker.RestartCount > worker.MaxRestarts {
		log.Printf("[resilience] Camera %s exceeded max restarts (%d), backing off for 1 hour", 
			worker.CameraName, worker.MaxRestarts)
		worker.BackoffDelay = time.Hour
	} else {
		// Reset restart count if it's been more than an hour
		if time.Since(worker.LastRestart) > time.Hour {
			worker.RestartCount = 1
		}
		
		// Exponential backoff with jitter (max 5 minutes)
		backoff := time.Duration(math.Min(float64(worker.BackoffDelay*2), float64(5*time.Minute)))
		worker.BackoffDelay = backoff
	}
	
	log.Printf("[resilience] Scheduling restart for camera %s in %v (restart #%d, error: %v)", 
		worker.CameraName, worker.BackoffDelay, worker.RestartCount, err)
	
	// Schedule restart
	go func() {
		select {
		case <-time.After(worker.BackoffDelay):
			rm.mu.Lock()
			if w, exists := rm.workers[worker.CameraName]; exists {
				rm.startWorker(w)
			}
			rm.mu.Unlock()
		case <-rm.ctx.Done():
			return
		}
	}()
}

// healthCheckLoop performs periodic health checks
func (rm *ResilienceManager) healthCheckLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rm.performHealthChecks()
		case <-rm.ctx.Done():
			return
		}
	}
}

// performHealthChecks checks the health of all workers
func (rm *ResilienceManager) performHealthChecks() {
	rm.mu.RLock()
	workers := make([]*WorkerState, 0, len(rm.workers))
	for _, worker := range rm.workers {
		workers = append(workers, worker)
	}
	rm.mu.RUnlock()
	
	for _, worker := range workers {
		rm.checkWorkerHealth(worker)
	}
}

// checkWorkerHealth checks if a worker is healthy
func (rm *ResilienceManager) checkWorkerHealth(worker *WorkerState) {
	// Check if process is still running
	if worker.Process != nil {
		// Send signal 0 to check if process exists
		if err := worker.Process.Signal(syscall.Signal(0)); err != nil {
			log.Printf("[resilience] Camera %s process died, marking unhealthy", worker.CameraName)
			worker.IsHealthy = false
			rm.scheduleRestart(worker, fmt.Errorf("process died"))
			return
		}
	}
	
	// Check if recent segments are being created
	outputDir := filepath.Join(rm.cfg.StoragePath, "recordings", worker.Camera.Name)
	if !rm.checkRecentSegments(outputDir, 5*time.Minute) {
		log.Printf("[resilience] Camera %s no recent segments, marking unhealthy", worker.CameraName)
		worker.IsHealthy = false
		rm.scheduleRestart(worker, fmt.Errorf("no recent segments"))
		return
	}
	
	worker.IsHealthy = true
	worker.LastHealthCheck = time.Now()
	
	// Update persistent state with health status
	rm.stateMgr.UpdateCameraState(rm.recordingState, worker.CameraName, true, true)
}

// checkRecentSegments checks if recent video segments exist
func (rm *ResilienceManager) checkRecentSegments(dir string, maxAge time.Duration) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		info, err := entry.Info()
		if err != nil {
			continue
		}
		
		if info.ModTime().After(cutoff) && filepath.Ext(info.Name()) == ".ts" {
			return true
		}
	}
	
	return false
}

// diskSpaceMonitor monitors available disk space
func (rm *ResilienceManager) diskSpaceMonitor() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			if !rm.checkDiskSpace() {
				log.Println("[resilience] CRITICAL: Low disk space detected!")
				// Could implement emergency cleanup here
			}
		case <-rm.ctx.Done():
			return
		}
	}
}

// checkDiskSpace checks if there's sufficient disk space
func (rm *ResilienceManager) checkDiskSpace() bool {
	if rm.diskManager == nil {
		return true // Skip check if no disk manager
	}
	
	// Update disk space info
	if err := rm.diskManager.ScanAndUpdateDiskSpace(); err != nil {
		log.Printf("[resilience] Failed to scan disk space: %v", err)
		return true // Assume OK if can't check
	}
	
	// Check if any disk has less than 5GB free
	disks, err := rm.diskManager.ListDisks()
	if err != nil {
		return true
	}
	
	for _, disk := range disks {
		if disk.AvailableSpaceGB < 5 {
			log.Printf("[resilience] Disk %s has only %dGB free", disk.Path, disk.AvailableSpaceGB)
			return false
		}
	}
	
	return true
}

// workerRecoveryLoop handles worker recovery after system restart
func (rm *ResilienceManager) workerRecoveryLoop() {
	// Wait a bit for system to stabilize after startup
	time.Sleep(10 * time.Second)
	
	// Get recovery information from persistent state
	recoveryInfo := rm.stateMgr.GetRecoveryInfo(rm.recordingState)
	
	// Check for any cameras that should be running but aren't
	for _, camera := range rm.cfg.Cameras {
		if !camera.Enabled {
			continue
		}
		
		rm.mu.RLock()
		_, exists := rm.workers[camera.Name]
		rm.mu.RUnlock()
		
		if !exists {
			info, hasInfo := recoveryInfo[camera.Name]
			if hasInfo && !info.ShouldRestart {
				log.Printf("[resilience] Skipping recovery for camera %s (too many failures)", camera.Name)
				continue
			}
			
			log.Printf("[resilience] Recovery: Starting camera %s (priority: %s)", 
				camera.Name, 
				func() string {
					if hasInfo {
						return info.Priority
					}
					return "new"
				}())
			rm.StartCameraWithResilience(camera, 0)
		}
	}
}

// statePersistenceLoop periodically saves state to disk
func (rm *ResilienceManager) statePersistenceLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			if err := rm.stateMgr.SaveState(rm.recordingState); err != nil {
				log.Printf("[resilience] Failed to save state: %v", err)
			}
		case <-rm.ctx.Done():
			// Save final state before shutdown
			if err := rm.stateMgr.SaveState(rm.recordingState); err != nil {
				log.Printf("[resilience] Failed to save final state: %v", err)
			}
			return
		}
	}
}

// GetWorkerStatus returns the status of all workers
func (rm *ResilienceManager) GetWorkerStatus() map[string]WorkerStatus {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	
	status := make(map[string]WorkerStatus)
	for name, worker := range rm.workers {
		status[name] = WorkerStatus{
			CameraName:      worker.CameraName,
			IsHealthy:       worker.IsHealthy,
			RestartCount:    worker.RestartCount,
			LastRestart:     worker.LastRestart,
			LastHealthCheck: worker.LastHealthCheck,
			BackoffDelay:    worker.BackoffDelay,
		}
	}
	
	return status
}

// WorkerStatus represents the status of a worker
type WorkerStatus struct {
	CameraName      string        `json:"camera_name"`
	IsHealthy       bool          `json:"is_healthy"`
	RestartCount    int           `json:"restart_count"`
	LastRestart     time.Time     `json:"last_restart"`
	LastHealthCheck time.Time     `json:"last_health_check"`
	BackoffDelay    time.Duration `json:"backoff_delay"`
}