package recording

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// RecordingManager manages camera recording processes with restart capabilities
type RecordingManager struct {
	cameras      map[string]*CameraRecording
	config       *config.Config
	db           database.Database
	diskManager  *storage.DiskManager
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
	isRunning    bool
}

// CameraRecording represents an active camera recording process
type CameraRecording struct {
	Name     string
	Cancel   context.CancelFunc
	DiskID   string
	Camera   config.CameraConfig
}

// NewRecordingManager creates a new recording manager
func NewRecordingManager(cfg *config.Config, db database.Database, diskManager *storage.DiskManager) *RecordingManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &RecordingManager{
		cameras:     make(map[string]*CameraRecording),
		config:      cfg,
		db:          db,
		diskManager: diskManager,
		ctx:         ctx,
		cancel:      cancel,
		isRunning:   false,
	}
}

// StartAllCameras starts recording for all enabled cameras
func (rm *RecordingManager) StartAllCameras() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.isRunning {
		log.Printf("[RecordingManager] Cameras are already running")
		return nil
	}

	// Get current active disk
	activeDisk, err := rm.db.GetActiveDisk()
	if err != nil {
		return fmt.Errorf("failed to get active disk: %v", err)
	}

	if activeDisk == nil {
		return fmt.Errorf("no active disk available")
	}

	// Update config with active disk path
	rm.config.StoragePath = activeDisk.Path
	os.Setenv("STORAGE_PATH", activeDisk.Path)

	log.Printf("[RecordingManager] Starting all cameras on disk: %s (%s)", activeDisk.Path, activeDisk.ID)

	// Start each enabled camera
	for i, camera := range rm.config.Cameras {
		if !camera.Enabled {
			log.Printf("[RecordingManager] Skipping disabled camera: %s", camera.Name)
			continue
		}

		if err := rm.startSingleCamera(camera, i, activeDisk.ID); err != nil {
			log.Printf("[RecordingManager] Failed to start camera %s: %v", camera.Name, err)
			continue
		}
	}

	rm.isRunning = true
	log.Printf("[RecordingManager] ✅ All enabled cameras started successfully")
	return nil
}

// startSingleCamera starts recording for a single camera
func (rm *RecordingManager) startSingleCamera(camera config.CameraConfig, cameraID int, diskID string) error {
	cameraName := camera.Name
	if cameraName == "" {
		cameraName = fmt.Sprintf("camera_%d", cameraID)
	}

	// Stop existing recording if running
	if existing, exists := rm.cameras[cameraName]; exists {
		log.Printf("[RecordingManager] Stopping existing recording for camera: %s", cameraName)
		existing.Cancel()
		delete(rm.cameras, cameraName)
	}

	// Create camera-specific context
	ctx, cancel := context.WithCancel(rm.ctx)

	// Start recording goroutine
	go func(cam config.CameraConfig, camID int, camName string) {
		defer func() {
			rm.mu.Lock()
			delete(rm.cameras, camName)
			rm.mu.Unlock()
			log.Printf("[RecordingManager] Camera %s recording stopped", camName)
		}()

		log.Printf("[RecordingManager] Starting recording for camera: %s", camName)
		captureRTSPStreamForCameraWithGracefulShutdown(ctx, rm.config, cam, camID)
	}(camera, cameraID, cameraName)

	// Track the recording
	rm.cameras[cameraName] = &CameraRecording{
		Name:   cameraName,
		Cancel: cancel,
		DiskID: diskID,
		Camera: camera,
	}

	return nil
}

// RestartAllCameras gracefully stops all recordings and restarts them on the new active disk
func (rm *RecordingManager) RestartAllCameras() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	log.Printf("[RecordingManager] 🔄 Restarting all cameras due to disk change...")

	// Get new active disk
	activeDisk, err := rm.db.GetActiveDisk()
	if err != nil {
		return fmt.Errorf("failed to get new active disk: %v", err)
	}

	if activeDisk == nil {
		return fmt.Errorf("no active disk available for restart")
	}

	// Update config with new disk path
	oldPath := rm.config.StoragePath
	rm.config.StoragePath = activeDisk.Path
	os.Setenv("STORAGE_PATH", activeDisk.Path)

	log.Printf("[RecordingManager] Disk changed: %s → %s", oldPath, activeDisk.Path)

	// Stop all existing recordings
	stoppedCameras := make([]config.CameraConfig, 0)
	for cameraName, recording := range rm.cameras {
		log.Printf("[RecordingManager] Stopping camera %s for restart", cameraName)
		recording.Cancel()
		stoppedCameras = append(stoppedCameras, recording.Camera)
	}

	// Clear cameras map
	rm.cameras = make(map[string]*CameraRecording)

	// Wait briefly for graceful shutdown
	time.Sleep(3 * time.Second)

	// Restart all cameras on new disk
	log.Printf("[RecordingManager] Starting cameras on new disk: %s (%s)", activeDisk.Path, activeDisk.ID)

	for i, camera := range stoppedCameras {
		if err := rm.startSingleCamera(camera, i, activeDisk.ID); err != nil {
			log.Printf("[RecordingManager] Failed to restart camera %s: %v", camera.Name, err)
			continue
		}
	}

	log.Printf("[RecordingManager] ✅ All cameras restarted on new disk successfully")
	return nil
}

// StopAllCameras gracefully stops all camera recordings
func (rm *RecordingManager) StopAllCameras() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if !rm.isRunning {
		log.Printf("[RecordingManager] Cameras are not running")
		return
	}

	log.Printf("[RecordingManager] Stopping all camera recordings...")

	// Stop all recordings
	for cameraName, recording := range rm.cameras {
		log.Printf("[RecordingManager] Stopping camera: %s", cameraName)
		recording.Cancel()
	}

	// Clear cameras map
	rm.cameras = make(map[string]*CameraRecording)
	rm.isRunning = false

	// Cancel main context
	rm.cancel()

	log.Printf("[RecordingManager] ✅ All camera recordings stopped")
}

// GetStatus returns the current status of all camera recordings
func (rm *RecordingManager) GetStatus() map[string]interface{} {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	status := make(map[string]interface{})
	status["is_running"] = rm.isRunning
	status["storage_path"] = rm.config.StoragePath

	cameras := make([]map[string]interface{}, 0)
	for _, recording := range rm.cameras {
		cameras = append(cameras, map[string]interface{}{
			"name":    recording.Name,
			"disk_id": recording.DiskID,
			"enabled": recording.Camera.Enabled,
		})
	}

	status["cameras"] = cameras
	status["total_cameras"] = len(cameras)

	return status
}

// IsRunning returns whether the recording manager is currently running
func (rm *RecordingManager) IsRunning() bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.isRunning
}