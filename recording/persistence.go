package recording

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"ayo-mwr/config"
)

// RecordingState represents the persistent state of recording operations
type RecordingState struct {
	CameraStates   map[string]CameraState `json:"camera_states"`
	LastUpdate     time.Time              `json:"last_update"`
	SystemStarted  time.Time              `json:"system_started"`
	TotalRestarts  int                    `json:"total_restarts"`
}

// CameraState represents the persistent state of a single camera
type CameraState struct {
	CameraName       string    `json:"camera_name"`
	IsRecording      bool      `json:"is_recording"`
	LastSegmentTime  time.Time `json:"last_segment_time"`
	RestartCount     int       `json:"restart_count"`
	LastRestart      time.Time `json:"last_restart"`
	TotalUptime      int64     `json:"total_uptime_seconds"`
	LastHealthy      time.Time `json:"last_healthy"`
	ConsecutiveFailures int   `json:"consecutive_failures"`
}

// StatePersistenceManager handles saving and loading recording state
type StatePersistenceManager struct {
	statePath string
	cfg       *config.Config
}

// NewStatePersistenceManager creates a new state persistence manager
func NewStatePersistenceManager(cfg *config.Config) *StatePersistenceManager {
	statePath := filepath.Join(cfg.StoragePath, "recording_state.json")
	return &StatePersistenceManager{
		statePath: statePath,
		cfg:       cfg,
	}
}

// LoadState loads the recording state from disk
func (spm *StatePersistenceManager) LoadState() (*RecordingState, error) {
	if _, err := os.Stat(spm.statePath); os.IsNotExist(err) {
		// No state file exists, return default state
		return &RecordingState{
			CameraStates:  make(map[string]CameraState),
			LastUpdate:    time.Now(),
			SystemStarted: time.Now(),
			TotalRestarts: 0,
		}, nil
	}

	data, err := os.ReadFile(spm.statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %v", err)
	}

	var state RecordingState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %v", err)
	}

	log.Printf("[persistence] Loaded recording state with %d camera states", len(state.CameraStates))
	
	// Check if this is a system restart (last update more than 5 minutes ago)
	if time.Since(state.LastUpdate) > 5*time.Minute {
		state.TotalRestarts++
		state.SystemStarted = time.Now()
		log.Printf("[persistence] System restart detected (restart #%d)", state.TotalRestarts)
		
		// Mark all cameras as not recording since system restarted
		for name, camState := range state.CameraStates {
			camState.IsRecording = false
			camState.ConsecutiveFailures++
			state.CameraStates[name] = camState
		}
	}

	return &state, nil
}

// SaveState saves the recording state to disk
func (spm *StatePersistenceManager) SaveState(state *RecordingState) error {
	state.LastUpdate = time.Now()

	// Create directory if it doesn't exist
	dir := filepath.Dir(spm.statePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %v", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %v", err)
	}

	// Write to temporary file first, then rename for atomic operation
	tempPath := spm.statePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary state file: %v", err)
	}

	if err := os.Rename(tempPath, spm.statePath); err != nil {
		return fmt.Errorf("failed to rename state file: %v", err)
	}

	return nil
}

// UpdateCameraState updates the state for a specific camera
func (spm *StatePersistenceManager) UpdateCameraState(state *RecordingState, cameraName string, isRecording bool, isHealthy bool) {
	camState, exists := state.CameraStates[cameraName]
	if !exists {
		camState = CameraState{
			CameraName: cameraName,
		}
	}

	now := time.Now()
	
	// Update recording status
	if camState.IsRecording != isRecording {
		if isRecording {
			log.Printf("[persistence] Camera %s started recording", cameraName)
		} else {
			log.Printf("[persistence] Camera %s stopped recording", cameraName)
			// Calculate uptime if it was recording
			if camState.IsRecording && !camState.LastRestart.IsZero() {
				uptime := int64(now.Sub(camState.LastRestart).Seconds())
				camState.TotalUptime += uptime
			}
		}
		camState.IsRecording = isRecording
	}

	// Update health status
	if isHealthy {
		camState.LastHealthy = now
		camState.ConsecutiveFailures = 0
	} else if camState.IsRecording {
		camState.ConsecutiveFailures++
	}

	// Update last segment time if recording
	if isRecording {
		camState.LastSegmentTime = now
	}

	state.CameraStates[cameraName] = camState
}

// RecordRestart records a camera restart
func (spm *StatePersistenceManager) RecordRestart(state *RecordingState, cameraName string) {
	camState, exists := state.CameraStates[cameraName]
	if !exists {
		camState = CameraState{
			CameraName: cameraName,
		}
	}

	now := time.Now()
	
	// Calculate uptime if it was recording
	if camState.IsRecording && !camState.LastRestart.IsZero() {
		uptime := int64(now.Sub(camState.LastRestart).Seconds())
		camState.TotalUptime += uptime
	}

	camState.RestartCount++
	camState.LastRestart = now
	camState.IsRecording = false // Will be set to true when recording starts again

	state.CameraStates[cameraName] = camState
	
	log.Printf("[persistence] Recorded restart for camera %s (restart #%d)", cameraName, camState.RestartCount)
}

// GetRecoveryInfo returns information needed for recovery
func (spm *StatePersistenceManager) GetRecoveryInfo(state *RecordingState) map[string]CameraRecoveryInfo {
	recovery := make(map[string]CameraRecoveryInfo)
	
	for _, camState := range state.CameraStates {
		info := CameraRecoveryInfo{
			CameraName:           camState.CameraName,
			ShouldRestart:        camState.ConsecutiveFailures < 10, // Don't restart if too many failures
			RestartCount:         camState.RestartCount,
			LastSuccessfulRecord: camState.LastHealthy,
			TotalUptime:          time.Duration(camState.TotalUptime) * time.Second,
		}
		
		// Determine restart priority based on failure count
		if camState.ConsecutiveFailures == 0 {
			info.Priority = "high"
		} else if camState.ConsecutiveFailures < 3 {
			info.Priority = "medium"
		} else {
			info.Priority = "low"
		}
		
		recovery[camState.CameraName] = info
	}
	
	return recovery
}

// CameraRecoveryInfo contains information for camera recovery decisions
type CameraRecoveryInfo struct {
	CameraName           string        `json:"camera_name"`
	ShouldRestart        bool          `json:"should_restart"`
	RestartCount         int           `json:"restart_count"`
	LastSuccessfulRecord time.Time     `json:"last_successful_record"`
	TotalUptime          time.Duration `json:"total_uptime"`
	Priority             string        `json:"priority"` // high, medium, low
}

// CleanupOldStates removes state entries for cameras that no longer exist
func (spm *StatePersistenceManager) CleanupOldStates(state *RecordingState) {
	activeCameras := make(map[string]bool)
	for _, camera := range spm.cfg.Cameras {
		activeCameras[camera.Name] = true
	}
	
	for cameraName := range state.CameraStates {
		if !activeCameras[cameraName] {
			log.Printf("[persistence] Removing state for inactive camera %s", cameraName)
			delete(state.CameraStates, cameraName)
		}
	}
}

// GetStateSummary returns a summary of the current state
func (spm *StatePersistenceManager) GetStateSummary(state *RecordingState) StateSummary {
	summary := StateSummary{
		SystemUptime:     time.Since(state.SystemStarted),
		TotalRestarts:    state.TotalRestarts,
		CameraCount:      len(state.CameraStates),
		RecordingCameras: 0,
		HealthyCameras:   0,
		LastUpdate:       state.LastUpdate,
	}
	
	now := time.Now()
	for _, camState := range state.CameraStates {
		if camState.IsRecording {
			summary.RecordingCameras++
		}
		
		// Consider healthy if last health check was within 2 minutes
		if now.Sub(camState.LastHealthy) < 2*time.Minute {
			summary.HealthyCameras++
		}
	}
	
	return summary
}

// StateSummary provides a summary of the recording state
type StateSummary struct {
	SystemUptime     time.Duration `json:"system_uptime"`
	TotalRestarts    int           `json:"total_restarts"`
	CameraCount      int           `json:"camera_count"`
	RecordingCameras int           `json:"recording_cameras"`
	HealthyCameras   int           `json:"healthy_cameras"`
	LastUpdate       time.Time     `json:"last_update"`
}