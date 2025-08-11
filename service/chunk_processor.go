package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ayo-mwr/database"
	"ayo-mwr/recording"
	"ayo-mwr/storage"
)

// ChunkProcessor handles physical chunk creation from segments
type ChunkProcessor struct {
	db             database.Database
	storageManager *storage.DiskManager
}

// NewChunkProcessor creates a new chunk processor
func NewChunkProcessor(db database.Database, storageManager *storage.DiskManager) *ChunkProcessor {
	return &ChunkProcessor{
		db:             db,
		storageManager: storageManager,
	}
}

// SegmentFile represents a segment file found on disk
type SegmentFile struct {
	FilePath    string
	FileName    string
	Timestamp   time.Time
	SizeBytes   int64
}

// ProcessChunks scans for new segments and creates 15-minute physical chunks
func (cp *ChunkProcessor) ProcessChunks(ctx context.Context) error {
	log.Println("[ChunkProcessor] Starting chunk processing...")
	
	startTime := time.Now()
	totalChunksCreated := 0

	// Get all storage disks
	disks, err := cp.db.GetStorageDisks()
	if err != nil {
		return fmt.Errorf("failed to get storage disks: %v", err)
	}

	// Process each camera on each disk
	cameras := []string{"CAMERA_1", "CAMERA_2", "CAMERA_3", "CAMERA_4"}
	
	for _, disk := range disks {
		for _, camera := range cameras {
			hlsPath := filepath.Join(disk.Path, "recordings", camera, "hls")
			chunksPath := filepath.Join(disk.Path, "recordings", camera, "chunks")
			
			// Check if HLS directory exists
			if _, err := os.Stat(hlsPath); os.IsNotExist(err) {
				log.Printf("[ChunkProcessor] HLS directory not found: %s", hlsPath)
				continue
			}

			// Create chunks directory if it doesn't exist
			if err := os.MkdirAll(chunksPath, 0755); err != nil {
				log.Printf("[ChunkProcessor] Failed to create chunks directory %s: %v", chunksPath, err)
				continue
			}

			log.Printf("[ChunkProcessor] Processing %s...", camera)
			
			chunksCreated, err := cp.processCameraChunks(ctx, camera, hlsPath, chunksPath, disk.ID)
			if err != nil {
				log.Printf("[ChunkProcessor] Error processing camera %s: %v", camera, err)
				continue
			}

			totalChunksCreated += chunksCreated
			
			if chunksCreated > 0 {
				log.Printf("[ChunkProcessor] Camera %s: %d chunks created", camera, chunksCreated)
			}
		}
	}

	duration := time.Since(startTime)
	log.Printf("[ChunkProcessor] ✅ Chunk processing completed in %v: %d chunks created", 
		duration, totalChunksCreated)

	return nil
}

// processCameraChunks processes segments for a specific camera and creates physical chunks
func (cp *ChunkProcessor) processCameraChunks(ctx context.Context, cameraName, hlsPath, chunksPath, diskID string) (int, error) {
	// Get the timestamp of the last processed segment from database
	lastProcessedTime, err := cp.getLastProcessedSegmentTime(cameraName)
	if err != nil {
		log.Printf("[ChunkProcessor] Error getting last processed time for %s: %v", cameraName, err)
		// Start from 1 hour ago if no record found
		lastProcessedTime = time.Now().Add(-1 * time.Hour)
	}

	log.Printf("[ChunkProcessor] %s: Last processed segment: %s", cameraName, lastProcessedTime.Format("2006-01-02 15:04:05"))

	// Scan for new segments after the last processed time
	newSegments, err := cp.scanNewSegments(hlsPath, lastProcessedTime)
	if err != nil {
		return 0, fmt.Errorf("failed to scan segments: %v", err)
	}

	if len(newSegments) == 0 {
		log.Printf("[ChunkProcessor] %s: No new segments found", cameraName)
		return 0, nil
	}

	log.Printf("[ChunkProcessor] %s: Found %d new segments", cameraName, len(newSegments))

	// Group segments into 15-minute chunks
	chunkGroups := cp.groupSegmentsIntoChunks(newSegments, 15*time.Minute)
	
	chunksCreated := 0
	var latestSegmentTime time.Time

	for _, group := range chunkGroups {
		select {
		case <-ctx.Done():
			return chunksCreated, ctx.Err()
		default:
		}

		// Only process complete chunks (minimum 10 segments = 40 seconds of content)
		if len(group.Segments) < 10 {
			log.Printf("[ChunkProcessor] %s: Incomplete chunk with %d segments, skipping", 
				cameraName, len(group.Segments))
			continue
		}

		// Create physical chunk file
		chunkPath, err := cp.createPhysicalChunk(ctx, cameraName, group, chunksPath)
		if err != nil {
			log.Printf("[ChunkProcessor] %s: Failed to create physical chunk: %v", cameraName, err)
			continue
		}

		// Record chunk in database
		err = cp.recordChunkInDatabase(cameraName, chunkPath, group, diskID)
		if err != nil {
			log.Printf("[ChunkProcessor] %s: Failed to record chunk in database: %v", cameraName, err)
			// Delete the physical file if database recording failed
			os.Remove(chunkPath)
			continue
		}

		chunksCreated++
		
		// Track the latest segment time for next iteration
		for _, segment := range group.Segments {
			if segment.Timestamp.After(latestSegmentTime) {
				latestSegmentTime = segment.Timestamp
			}
		}

		log.Printf("[ChunkProcessor] ✅ %s: Created chunk %s with %d segments", 
			cameraName, filepath.Base(chunkPath), len(group.Segments))
	}

	// Update the last processed segment time
	if !latestSegmentTime.IsZero() {
		err = cp.updateLastProcessedSegmentTime(cameraName, latestSegmentTime)
		if err != nil {
			log.Printf("[ChunkProcessor] Warning: Failed to update last processed time for %s: %v", cameraName, err)
		}
	}

	return chunksCreated, nil
}

// scanNewSegments scans for segment files newer than the given timestamp
func (cp *ChunkProcessor) scanNewSegments(hlsPath string, afterTime time.Time) ([]SegmentFile, error) {
	// Pattern to match HLS segment files: segment_20250811_234500.ts
	segmentPattern := regexp.MustCompile(`^segment_(\d{8})_(\d{6})\.ts$`)
	
	var segments []SegmentFile

	// Read directory contents
	files, err := os.ReadDir(hlsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %v", hlsPath, err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Check if this is a segment file
		matches := segmentPattern.FindStringSubmatch(file.Name())
		if len(matches) != 3 {
			continue // Not a segment file
		}

		// Parse timestamp from filename
		segmentTime, err := parseSegmentTimestamp(matches[1], matches[2])
		if err != nil {
			log.Printf("[ChunkProcessor] Warning: Could not parse timestamp for %s: %v", file.Name(), err)
			continue
		}

		// Only include segments newer than afterTime
		if !segmentTime.After(afterTime) {
			continue
		}

		// Get file info
		fileInfo, err := file.Info()
		if err != nil {
			log.Printf("[ChunkProcessor] Warning: Could not get file info for %s: %v", file.Name(), err)
			continue
		}

		segment := SegmentFile{
			FilePath:  filepath.Join(hlsPath, file.Name()),
			FileName:  file.Name(),
			Timestamp: segmentTime,
			SizeBytes: fileInfo.Size(),
		}

		segments = append(segments, segment)
	}

	// Sort segments by timestamp
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Timestamp.Before(segments[j].Timestamp)
	})

	return segments, nil
}

// ChunkGroup represents a group of segments for chunk creation
type ChunkGroup struct {
	StartTime time.Time
	EndTime   time.Time
	Segments  []SegmentFile
}

// groupSegmentsIntoChunks groups segments into 15-minute chunks
func (cp *ChunkProcessor) groupSegmentsIntoChunks(segments []SegmentFile, chunkDuration time.Duration) []ChunkGroup {
	if len(segments) == 0 {
		return nil
	}

	var groups []ChunkGroup
	
	// Start from the first segment's time, rounded down to chunk boundary
	firstSegment := segments[0]
	currentChunkStart := cp.roundDownToChunkBoundary(firstSegment.Timestamp, chunkDuration)
	
	for len(segments) > 0 {
		chunkEnd := currentChunkStart.Add(chunkDuration)
		
		var chunkSegments []SegmentFile
		var remainingSegments []SegmentFile
		
		// Collect segments that belong to this chunk
		for _, segment := range segments {
			if segment.Timestamp.After(currentChunkStart) && segment.Timestamp.Before(chunkEnd) {
				chunkSegments = append(chunkSegments, segment)
			} else if segment.Timestamp.After(chunkEnd) {
				remainingSegments = append(remainingSegments, segment)
			}
			// Segments before currentChunkStart are ignored (already processed)
		}
		
		if len(chunkSegments) > 0 {
			groups = append(groups, ChunkGroup{
				StartTime: currentChunkStart,
				EndTime:   chunkEnd,
				Segments:  chunkSegments,
			})
		}
		
		// Move to next chunk
		currentChunkStart = chunkEnd
		segments = remainingSegments
		
		// If no more segments in the current or future chunks, break
		if len(remainingSegments) == 0 {
			break
		}
	}

	return groups
}

// roundDownToChunkBoundary rounds time down to chunk boundary (15-minute intervals)
func (cp *ChunkProcessor) roundDownToChunkBoundary(t time.Time, chunkDuration time.Duration) time.Time {
	minutes := int(chunkDuration.Minutes())
	roundedMinute := (t.Minute() / minutes) * minutes
	
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), roundedMinute, 0, 0, t.Location())
}

// createPhysicalChunk creates an actual 15-minute MP4 file from segments
func (cp *ChunkProcessor) createPhysicalChunk(ctx context.Context, cameraName string, group ChunkGroup, chunksPath string) (string, error) {
	// Generate chunk filename
	chunkFilename := fmt.Sprintf("%s_%s_chunk.mp4", cameraName, group.StartTime.Format("20060102_1504"))
	chunkPath := filepath.Join(chunksPath, chunkFilename)
	
	// Check if chunk already exists
	if _, err := os.Stat(chunkPath); err == nil {
		log.Printf("[ChunkProcessor] Chunk already exists: %s", chunkFilename)
		return chunkPath, nil
	}

	// Create segment list file for FFmpeg concatenation
	segmentListPath := filepath.Join(chunksPath, fmt.Sprintf("%s_segments.txt", chunkFilename[:len(chunkFilename)-4]))
	
	// Write segment list
	segmentListFile, err := os.Create(segmentListPath)
	if err != nil {
		return "", fmt.Errorf("failed to create segment list file: %v", err)
	}

	for _, segment := range group.Segments {
		// Use relative path or absolute path depending on FFmpeg requirements
		fmt.Fprintf(segmentListFile, "file '%s'\n", segment.FilePath)
	}
	segmentListFile.Close()

	// Ensure cleanup of segment list file
	defer os.Remove(segmentListPath)

	// Use recording package to merge segments into chunk
	log.Printf("[ChunkProcessor] Creating chunk %s from %d segments...", chunkFilename, len(group.Segments))
	
	// Create a temporary directory for the merge operation
	tempDir := filepath.Join(chunksPath, "temp")
	os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	// Use the recording.MergeSessionVideos function to create the chunk
	err = recording.MergeSessionVideos(filepath.Dir(group.Segments[0].FilePath), group.StartTime, group.EndTime, chunkPath, "1920x1080")
	if err != nil {
		return "", fmt.Errorf("failed to merge segments into chunk: %v", err)
	}

	// Verify the chunk file was created and has content
	chunkInfo, err := os.Stat(chunkPath)
	if err != nil {
		return "", fmt.Errorf("chunk file was not created: %v", err)
	}

	if chunkInfo.Size() == 0 {
		os.Remove(chunkPath)
		return "", fmt.Errorf("chunk file is empty")
	}

	log.Printf("[ChunkProcessor] ✅ Chunk created: %s (%.2f MB)", chunkFilename, float64(chunkInfo.Size())/1024/1024)
	
	return chunkPath, nil
}

// recordChunkInDatabase records the physical chunk in the database
func (cp *ChunkProcessor) recordChunkInDatabase(cameraName, chunkPath string, group ChunkGroup, diskID string) error {
	// Get file info
	chunkInfo, err := os.Stat(chunkPath)
	if err != nil {
		return fmt.Errorf("failed to get chunk file info: %v", err)
	}

	chunkID := fmt.Sprintf("%s_%s_chunk", cameraName, group.StartTime.Format("20060102_1504"))
	relativePath := strings.TrimPrefix(chunkPath, filepath.Dir(filepath.Dir(filepath.Dir(chunkPath)))+"/") // Remove disk path prefix
	
	duration := len(group.Segments) * 4 // Each segment is 4 seconds
	
	// Create the chunk record
	chunk := database.RecordingSegment{
		ID:                   chunkID,
		CameraName:           cameraName,
		StorageDiskID:        diskID,
		MP4Path:              relativePath,
		SegmentStart:         group.StartTime,
		SegmentEnd:           group.EndTime,
		FileSizeBytes:        chunkInfo.Size(),
		CreatedAt:            time.Now(),
		ChunkType:            database.ChunkTypeChunk,
		SourceSegmentsCount:  len(group.Segments),
		ChunkDurationSeconds: &duration,
		ProcessingStatus:     database.ProcessingStatusReady,
	}

	err = cp.db.CreateRecordingSegment(chunk)
	if err != nil {
		return fmt.Errorf("failed to create chunk record: %v", err)
	}

	log.Printf("[ChunkProcessor] 📝 Chunk recorded in database: %s", chunkID)
	return nil
}

// getLastProcessedSegmentTime retrieves the timestamp of the last processed segment for a camera
func (cp *ChunkProcessor) getLastProcessedSegmentTime(cameraName string) (time.Time, error) {
	// Get the latest chunk for this camera
	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour) // Look back 30 days
	
	chunks, err := cp.db.FindChunksInTimeRange(cameraName, startTime, endTime)
	if err != nil {
		return time.Time{}, err
	}

	if len(chunks) == 0 {
		// No chunks found, return zero time to start from beginning
		return time.Time{}, nil
	}

	// Find the latest chunk end time
	var latestTime time.Time
	for _, chunk := range chunks {
		if chunk.EndTime.After(latestTime) {
			latestTime = chunk.EndTime
		}
	}

	return latestTime, nil
}

// updateLastProcessedSegmentTime updates the last processed segment time (stored as system config)
func (cp *ChunkProcessor) updateLastProcessedSegmentTime(cameraName string, timestamp time.Time) error {
	configKey := fmt.Sprintf("last_processed_segment_%s", cameraName)
	
	config := database.SystemConfig{
		Key:       configKey,
		Value:     timestamp.Format(time.RFC3339),
		Type:      "string",
		UpdatedAt: time.Now(),
		UpdatedBy: "chunk_processor",
	}

	return cp.db.SetSystemConfig(config)
}

// parseSegmentTimestamp parses segment timestamp from filename components
func parseSegmentTimestamp(dateStr, timeStr string) (time.Time, error) {
	// dateStr: "20250811", timeStr: "234500"
	
	if len(dateStr) != 8 || len(timeStr) != 6 {
		return time.Time{}, fmt.Errorf("invalid timestamp format")
	}

	year, err := strconv.Atoi(dateStr[:4])
	if err != nil {
		return time.Time{}, err
	}

	month, err := strconv.Atoi(dateStr[4:6])
	if err != nil {
		return time.Time{}, err
	}

	day, err := strconv.Atoi(dateStr[6:8])
	if err != nil {
		return time.Time{}, err
	}

	hour, err := strconv.Atoi(timeStr[:2])
	if err != nil {
		return time.Time{}, err
	}

	minute, err := strconv.Atoi(timeStr[2:4])
	if err != nil {
		return time.Time{}, err
	}

	second, err := strconv.Atoi(timeStr[4:6])
	if err != nil {
		return time.Time{}, err
	}

	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC), nil
}

// GetProcessingStats returns statistics about chunk processing
func (cp *ChunkProcessor) GetProcessingStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Count chunks by camera
	cameras := []string{"CAMERA_1", "CAMERA_2", "CAMERA_3", "CAMERA_4"}
	
	totalChunks := 0
	for _, camera := range cameras {
		count, err := cp.getChunkCountForCamera(camera)
		if err != nil {
			log.Printf("[ChunkProcessor] Warning: Could not get chunk count for %s: %v", camera, err)
			continue
		}
		stats[fmt.Sprintf("%s_chunks", camera)] = count
		totalChunks += count
	}

	stats["total_chunks"] = totalChunks

	// Get recent chunks (last 24 hours)
	recentCount, err := cp.getRecentChunkCount(24)
	if err == nil {
		stats["recent_24h_chunks"] = recentCount
	}

	return stats, nil
}

// getChunkCountForCamera gets the total number of chunks for a camera
func (cp *ChunkProcessor) getChunkCountForCamera(cameraName string) (int, error) {
	startTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Now().Add(24 * time.Hour)

	chunks, err := cp.db.FindChunksInTimeRange(cameraName, startTime, endTime)
	if err != nil {
		return 0, err
	}

	return len(chunks), nil
}

// getRecentChunkCount gets the number of chunks from the last N hours
func (cp *ChunkProcessor) getRecentChunkCount(hours int) (int, error) {
	cutoffTime := time.Now().Add(-time.Duration(hours) * time.Hour)
	endTime := time.Now()

	totalCount := 0
	cameras := []string{"CAMERA_1", "CAMERA_2", "CAMERA_3", "CAMERA_4"}

	for _, camera := range cameras {
		chunks, err := cp.db.FindChunksInTimeRange(camera, cutoffTime, endTime)
		if err != nil {
			continue
		}
		totalCount += len(chunks)
	}

	return totalCount, nil
}
