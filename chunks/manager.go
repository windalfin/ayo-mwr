package chunks

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// ChunkManager handles the creation and management of pre-concatenated video chunks
type ChunkManager struct {
	db                database.Database
	config            *config.Config
	storageManager    *storage.DiskManager
	chunkConfig       *config.ChunkProcessingConfig
	mu                sync.RWMutex
	isProcessing      map[string]bool // Track cameras currently being processed
	processingTimeout time.Duration
}

// NewChunkManager creates a new chunk manager instance
func NewChunkManager(db database.Database, cfg *config.Config, storageManager *storage.DiskManager) *ChunkManager {
	// Load chunk configuration from database with fallback to defaults
	chunkConfigService := config.NewChunkConfigService(db)
	chunkConfig, err := chunkConfigService.GetChunkConfig()
	if err != nil {
		log.Printf("[ChunkManager] Warning: Failed to load chunk config from database, using defaults: %v", err)
		// Create a basic default config as fallback
		chunkConfig = &config.ChunkProcessingConfig{
			Enabled:                  true,
			ChunkDurationMinutes:     15,
			MinSegmentsForChunk:      10,
			RetentionDays:            7,
			ProcessingTimeoutMinutes: 10,
			MaxConcurrentProcessing:  2,
		}
	}

	log.Printf("[ChunkManager] Initialized with config: enabled=%v, duration=%dm, retention=%dd",
		chunkConfig.Enabled, chunkConfig.ChunkDurationMinutes, chunkConfig.RetentionDays)

	return &ChunkManager{
		db:                db,
		config:            cfg,
		storageManager:    storageManager,
		chunkConfig:       chunkConfig,
		isProcessing:      make(map[string]bool),
		processingTimeout: time.Duration(chunkConfig.ProcessingTimeoutMinutes) * time.Minute,
	}
}

// ProcessPendingChunks processes all pending chunks for all cameras
func (cm *ChunkManager) ProcessPendingChunks(ctx context.Context) error {
	if !cm.chunkConfig.Enabled {
		log.Println("[ChunkManager] Chunk processing is disabled")
		return nil
	}

	log.Println("[ChunkManager] Starting chunk processing...")

	// Get all enabled cameras
	cameras, err := cm.db.GetCameras()
	if err != nil {
		return fmt.Errorf("failed to get cameras: %v", err)
	}

	// Process chunks for each camera
	var wg sync.WaitGroup
	sem := make(chan struct{}, cm.chunkConfig.MaxConcurrentProcessing)

	for _, camera := range cameras {
		if !camera.Enabled {
			continue
		}

		wg.Add(1)
		go func(cameraName string) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			if err := cm.ProcessCameraChunks(ctx, cameraName); err != nil {
				log.Printf("[ChunkManager] Error processing chunks for camera %s: %v", cameraName, err)
			}
		}(camera.Name)
	}

	wg.Wait()
	log.Println("[ChunkManager] Chunk processing completed")
	return nil
}

// ProcessCameraChunks processes pending chunks for a specific camera
func (cm *ChunkManager) ProcessCameraChunks(ctx context.Context, cameraName string) error {
	// Check if already processing this camera
	cm.mu.Lock()
	if cm.isProcessing[cameraName] {
		cm.mu.Unlock()
		log.Printf("[ChunkManager] Camera %s is already being processed, skipping", cameraName)
		return nil
	}
	cm.isProcessing[cameraName] = true
	cm.mu.Unlock()

	defer func() {
		cm.mu.Lock()
		delete(cm.isProcessing, cameraName)
		cm.mu.Unlock()
	}()

	log.Printf("[ChunkManager] Processing chunks for camera: %s", cameraName)

	// Calculate the chunk time windows that need to be processed
	chunkWindows := cm.calculateChunkWindows()

	for _, window := range chunkWindows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := cm.processChunkWindow(ctx, cameraName, window)
		if err != nil {
			log.Printf("[ChunkManager] Error processing chunk window %s for camera %s: %v",
				window.Start.Format("15:04"), cameraName, err)
			continue
		}
	}

	return nil
}

// ChunkWindow represents a time window for chunk processing
type ChunkWindow struct {
	Start time.Time
	End   time.Time
}

// calculateChunkWindows calculates the 15-minute windows that need chunk processing
func (cm *ChunkManager) calculateChunkWindows() []ChunkWindow {
	now := time.Now()

	// We process chunks with a 2-minute delay to ensure all segments are available
	processingDelay := 2 * time.Minute
	latestTime := now.Add(-processingDelay)

	// Round down to the nearest 15-minute boundary
	minute := latestTime.Minute()
	roundedMinute := (minute / cm.chunkConfig.ChunkDurationMinutes) * cm.chunkConfig.ChunkDurationMinutes
	latestChunkStart := time.Date(
		latestTime.Year(), latestTime.Month(), latestTime.Day(),
		latestTime.Hour(), roundedMinute, 0, 0, latestTime.Location(),
	)

	// Generate chunk windows for the last few periods (to catch any missed chunks)
	var windows []ChunkWindow
	for i := 0; i < 4; i++ { // Process last 4 windows (1 hour)
		start := latestChunkStart.Add(-time.Duration(i*cm.chunkConfig.ChunkDurationMinutes) * time.Minute)
		end := start.Add(time.Duration(cm.chunkConfig.ChunkDurationMinutes) * time.Minute)

		windows = append(windows, ChunkWindow{
			Start: start,
			End:   end,
		})
	}

	return windows
}

// processChunkWindow processes a specific chunk window for a camera
func (cm *ChunkManager) processChunkWindow(ctx context.Context, cameraName string, window ChunkWindow) error {
	chunkID := fmt.Sprintf("%s_%s_chunk", cameraName, window.Start.Format("20060102_1504"))

	// Check if chunk already exists
	existingChunks, err := cm.db.FindChunksInTimeRange(cameraName, window.Start, window.End)
	if err != nil {
		return fmt.Errorf("error checking existing chunks: %v", err)
	}

	if len(existingChunks) > 0 {
		log.Printf("[ChunkManager] Chunk already exists for %s at %s, skipping",
			cameraName, window.Start.Format("15:04"))
		return nil
	}

	// Get segments for this time window
	segments, err := cm.db.GetPendingChunkSegments(cameraName, window.Start, cm.chunkConfig.ChunkDurationMinutes)
	if err != nil {
		return fmt.Errorf("error getting pending segments: %v", err)
	}

	if len(segments) < cm.chunkConfig.MinSegmentsForChunk {
		log.Printf("[ChunkManager] Not enough segments for chunk (%d < %d) for camera %s at %s",
			len(segments), cm.chunkConfig.MinSegmentsForChunk, cameraName, window.Start.Format("15:04"))
		return nil
	}

	log.Printf("[ChunkManager] Creating chunk from %d segments for camera %s (%s)",
		len(segments), cameraName, window.Start.Format("15:04"))

	// Create the chunk
	err = cm.createChunk(ctx, cameraName, chunkID, segments, window)
	if err != nil {
		return fmt.Errorf("error creating chunk: %v", err)
	}

	return nil
}

// createChunk creates a pre-concatenated chunk from individual segments
func (cm *ChunkManager) createChunk(ctx context.Context, cameraName, chunkID string, segments []database.RecordingSegment, window ChunkWindow) error {
	// Mark chunk as processing
	processingChunk := database.RecordingSegment{
		ID:                   chunkID,
		CameraName:           cameraName,
		StorageDiskID:        "", // Will be set when chunk is created
		MP4Path:              "", // Will be set when chunk is created
		SegmentStart:         window.Start,
		SegmentEnd:           window.End,
		FileSizeBytes:        0, // Will be set when chunk is created
		CreatedAt:            time.Now(),
		ChunkType:            database.ChunkTypeChunk,
		SourceSegmentsCount:  len(segments),
		ChunkDurationSeconds: &[]int{cm.chunkConfig.ChunkDurationMinutes * 60}[0],
		ProcessingStatus:     database.ProcessingStatusProcessing,
	}

	err := cm.db.CreateChunk(processingChunk)
	if err != nil {
		return fmt.Errorf("error creating processing chunk record: %v", err)
	}

	// Ensure we update status on exit
	defer func() {
		if err != nil {
			cm.db.UpdateChunkProcessingStatus(chunkID, database.ProcessingStatusFailed)
		}
	}()

	// Get active disk for storing the chunk
	activeDiskPath, err := cm.storageManager.GetActiveDiskPath()
	if err != nil {
		return fmt.Errorf("error getting active disk: %v", err)
	}

	// Create chunk directory
	chunkDir := filepath.Join(activeDiskPath, "recordings", cameraName, "chunks")
	err = os.MkdirAll(chunkDir, 0755)
	if err != nil {
		return fmt.Errorf("error creating chunk directory: %v", err)
	}

	// Create chunk file path
	chunkFileName := fmt.Sprintf("%s.ts", chunkID)
	chunkFilePath := filepath.Join(chunkDir, chunkFileName)

	// Create segment list for FFmpeg
	segmentListPath := filepath.Join(chunkDir, fmt.Sprintf("%s_segments.txt", chunkID))
	err = cm.createSegmentList(segments, segmentListPath)
	if err != nil {
		return fmt.Errorf("error creating segment list: %v", err)
	}
	defer os.Remove(segmentListPath) // Clean up segment list

	// Run FFmpeg with timeout
	chunkCtx, cancel := context.WithTimeout(ctx, cm.processingTimeout)
	defer cancel()

	err = cm.concatenateSegments(chunkCtx, segmentListPath, chunkFilePath)
	if err != nil {
		return fmt.Errorf("error concatenating segments: %v", err)
	}

	// Get file size
	fileInfo, err := os.Stat(chunkFilePath)
	if err != nil {
		return fmt.Errorf("error getting chunk file size: %v", err)
	}

	// Get active disk info
	activeDisk, err := cm.db.GetActiveDisk()
	if err != nil {
		return fmt.Errorf("error getting active disk info: %v", err)
	}

	// Update chunk record with final information
	processingChunk.StorageDiskID = activeDisk.ID
	processingChunk.MP4Path = filepath.Join("recordings", cameraName, "chunks", chunkFileName)
	processingChunk.FileSizeBytes = fileInfo.Size()
	processingChunk.ProcessingStatus = database.ProcessingStatusReady

	err = cm.db.CreateChunk(processingChunk)
	if err != nil {
		return fmt.Errorf("error updating chunk record: %v", err)
	}

	log.Printf("[ChunkManager] ✅ Created chunk %s (%.1f MB, %d segments) for camera %s",
		chunkID, float64(fileInfo.Size())/(1024*1024), len(segments), cameraName)

	return nil
}

// createSegmentList creates a file list for FFmpeg concatenation
func (cm *ChunkManager) createSegmentList(segments []database.RecordingSegment, listPath string) error {
	file, err := os.Create(listPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Get disk paths
	diskPaths := make(map[string]string)

	for _, segment := range segments {
		// Get disk path if not cached
		if _, exists := diskPaths[segment.StorageDiskID]; !exists {
			disk, err := cm.db.GetStorageDisk(segment.StorageDiskID)
			if err != nil {
				return fmt.Errorf("error getting disk %s: %v", segment.StorageDiskID, err)
			}
			diskPaths[segment.StorageDiskID] = disk.Path
		}

		// Construct full path
		fullPath := filepath.Join(diskPaths[segment.StorageDiskID], segment.MP4Path)

		// Check if file exists
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			log.Printf("[ChunkManager] Warning: Segment file not found: %s", fullPath)
			continue
		}

		// Write to list (escape path for FFmpeg)
		escapedPath := strings.ReplaceAll(fullPath, "'", "'\\''")
		_, err = fmt.Fprintf(file, "file '%s'\n", escapedPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// concatenateSegments uses FFmpeg to concatenate segments into a chunk
func (cm *ChunkManager) concatenateSegments(ctx context.Context, segmentListPath, outputPath string) error {
	// FFmpeg command for fast concatenation using stream copy
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", segmentListPath,
		"-c", "copy",
		"-movflags", "faststart",
		"-y", // Overwrite output file
		outputPath,
	)

	// Capture output for logging
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("FFmpeg error: %v\nOutput: %s", err, string(output))
	}

	return nil
}

// CleanupOldChunks removes old chunks based on retention policy
func (cm *ChunkManager) CleanupOldChunks(ctx context.Context) error {
	if !cm.chunkConfig.Enabled {
		return nil
	}

	log.Println("[ChunkManager] Starting chunk cleanup...")

	retentionTime := time.Now().Add(-time.Duration(cm.chunkConfig.RetentionDays) * 24 * time.Hour)

	// Get old chunks
	oldChunks, err := cm.db.GetChunksByProcessingStatus(database.ProcessingStatusReady)
	if err != nil {
		return fmt.Errorf("error getting chunks for cleanup: %v", err)
	}

	deletedCount := 0
	for _, chunk := range oldChunks {
		if chunk.CreatedAt.After(retentionTime) {
			continue // Skip recent chunks
		}

		// Get full file path
		disk, err := cm.db.GetStorageDisk(chunk.StorageDiskID)
		if err != nil {
			log.Printf("[ChunkManager] Warning: Could not get disk %s for chunk %s: %v",
				chunk.StorageDiskID, chunk.ID, err)
			continue
		}

		fullPath := filepath.Join(disk.Path, chunk.MP4Path)

		// Remove file
		if err := os.Remove(fullPath); err != nil {
			log.Printf("[ChunkManager] Warning: Could not remove chunk file %s: %v", fullPath, err)
		}

		// Remove database record
		if err := cm.db.DeleteRecordingSegment(chunk.ID); err != nil {
			log.Printf("[ChunkManager] Warning: Could not remove chunk record %s: %v", chunk.ID, err)
		} else {
			deletedCount++
		}
	}

	if deletedCount > 0 {
		log.Printf("[ChunkManager] ✅ Cleaned up %d old chunks", deletedCount)
	}

	return nil
}

// GetChunkStatistics returns statistics about chunk processing
func (cm *ChunkManager) GetChunkStatistics() (map[string]interface{}, error) {
	stats, err := cm.db.GetChunkStatistics()
	if err != nil {
		return nil, err
	}

	// Add configuration info
	stats["config"] = map[string]interface{}{
		"enabled":                    cm.chunkConfig.Enabled,
		"chunk_duration_minutes":     cm.chunkConfig.ChunkDurationMinutes,
		"min_segments_for_chunk":     cm.chunkConfig.MinSegmentsForChunk,
		"retention_days":             cm.chunkConfig.RetentionDays,
		"processing_timeout_minutes": cm.chunkConfig.ProcessingTimeoutMinutes,
		"max_concurrent_processing":  cm.chunkConfig.MaxConcurrentProcessing,
	}

	// Add processing status
	cm.mu.RLock()
	currentlyProcessing := make([]string, 0, len(cm.isProcessing))
	for camera := range cm.isProcessing {
		currentlyProcessing = append(currentlyProcessing, camera)
	}
	cm.mu.RUnlock()
	stats["currently_processing"] = currentlyProcessing

	return stats, nil
}

// UpdateConfiguration updates the chunk processing configuration
func (cm *ChunkManager) UpdateConfiguration(config *config.ChunkProcessingConfig) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.chunkConfig = config
	cm.processingTimeout = time.Duration(config.ProcessingTimeoutMinutes) * time.Minute

	log.Printf("[ChunkManager] Configuration updated: enabled=%v, duration=%dm, min_segments=%d",
		config.Enabled, config.ChunkDurationMinutes, config.MinSegmentsForChunk)
}

// GetChunksByProcessingStatus gets chunks by their processing status
func (cm *ChunkManager) GetChunksByProcessingStatus(status database.ProcessingStatus) ([]database.RecordingSegment, error) {
	return cm.db.GetChunksByProcessingStatus(status)
}

// UpdateChunkProcessingStatus updates the processing status of a chunk
func (cm *ChunkManager) UpdateChunkProcessingStatus(chunkID string, status database.ProcessingStatus) error {
	return cm.db.UpdateChunkProcessingStatus(chunkID, status)
}
