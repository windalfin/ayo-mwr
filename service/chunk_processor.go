package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ayo-mwr/database"
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

// ProcessChunks scans for new segments and creates 10-minute physical chunks
func (cp *ChunkProcessor) ProcessChunks(ctx context.Context) error {
	log.Println("[ChunkProcessor] Starting chunk processing...")
	
	startTime := time.Now()
	totalChunksCreated := 0

	// Get the active disk from database
	activeDisk, err := cp.db.GetActiveDisk()
	if err != nil {
		return fmt.Errorf("failed to get active disk: %v", err)
	}
	if activeDisk == nil {
		return fmt.Errorf("no active disk available for chunk processing")
	}

	log.Printf("[ChunkProcessor] Using active disk: %s (ID: %s)", activeDisk.Path, activeDisk.ID)

	// Process each camera on the active disk
	cameras := []string{"CAMERA_1", "CAMERA_2", "CAMERA_3", "CAMERA_4"}
	
	for _, camera := range cameras {
		// Build absolute paths using the active disk path from storage_disks table
		// Convert activeDisk.Path to absolute path first
		absActiveDiskPath, err := filepath.Abs(activeDisk.Path)
		if err != nil {
			log.Printf("[ChunkProcessor] Failed to get absolute path for disk %s: %v", activeDisk.Path, err)
			continue
		}
		
		hlsPath := filepath.Join(absActiveDiskPath, "recordings", camera, "hls")
		chunksPath := filepath.Join(absActiveDiskPath, "recordings", camera, "chunks")
		
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

		log.Printf("[ChunkProcessor] Processing %s on disk %s...", camera, activeDisk.Path)
		
		chunksCreated, err := cp.processCameraChunks(ctx, camera, hlsPath, chunksPath, activeDisk.ID)
		if err != nil {
			log.Printf("[ChunkProcessor] Error processing camera %s: %v", camera, err)
			continue
		}

		totalChunksCreated += chunksCreated
		
		if chunksCreated > 0 {
			log.Printf("[ChunkProcessor] Camera %s: %d chunks created", camera, chunksCreated)
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
	
	// Safety check: if lastProcessedTime is zero/default, set to 1 hour ago
	if lastProcessedTime.IsZero() || lastProcessedTime.Year() < 2020 {
		log.Printf("[ChunkProcessor] %s: Invalid last processed time, starting from 1 hour ago", cameraName)
		lastProcessedTime = time.Now().Add(-1 * time.Hour)
	}

	// Determine the time window for processing
	// We want to process only the most recent complete 10-minute chunk
	now := time.Now()
	chunkDuration := 10 * time.Minute
	
	// Round down current time to chunk boundary
	currentChunkStart := cp.roundDownToChunkBoundary(now, chunkDuration)
	
	// We'll process the previous complete chunk (not the current incomplete one)
	targetChunkStart := currentChunkStart.Add(-chunkDuration)
	targetChunkEnd := currentChunkStart
	
	// Skip if this chunk period is before our last processed time
	if targetChunkEnd.Before(lastProcessedTime) || targetChunkEnd.Equal(lastProcessedTime) {
		log.Printf("[ChunkProcessor] %s: No new complete chunks to process (last: %s, target: %s)", 
			cameraName, lastProcessedTime.Format("15:04:05"), targetChunkEnd.Format("15:04:05"))
		return 0, nil
	}

	log.Printf("[ChunkProcessor] %s: Processing chunk window %s to %s", 
		cameraName, targetChunkStart.Format("15:04:05"), targetChunkEnd.Format("15:04:05"))
	log.Printf("[ChunkProcessor] %s: HLS path: %s", cameraName, hlsPath)
	log.Printf("[ChunkProcessor] %s: Chunks path: %s", cameraName, chunksPath)

	// Check if chunk already exists in database
	chunkID := fmt.Sprintf("%s_%s_chunk", cameraName, targetChunkStart.Format("20060102_1504"))
	existingChunks, err := cp.db.FindChunksInTimeRange(cameraName, targetChunkStart, targetChunkEnd)
	if err == nil && len(existingChunks) > 0 {
		for _, chunk := range existingChunks {
			if chunk.ID == chunkID {
				log.Printf("[ChunkProcessor] %s: Chunk %s already exists in database, skipping", cameraName, chunkID)
				// Update last processed time even if chunk exists
				cp.updateLastProcessedSegmentTime(cameraName, targetChunkEnd)
				return 0, nil
			}
		}
	}

	// Scan for segments only within the target chunk window
	segments, err := cp.scanSegmentsInTimeRange(hlsPath, targetChunkStart, targetChunkEnd)
	if err != nil {
		return 0, fmt.Errorf("failed to scan segments: %v", err)
	}

	// For 10-minute chunks, we expect around 150 segments (10 min * 60 sec / 4 sec per segment)
	// But we'll accept chunks with at least 10 segments for testing
	if len(segments) < 10 {
		log.Printf("[ChunkProcessor] %s: Insufficient segments (%d) for chunk %s, skipping", 
			cameraName, len(segments), chunkID)
		return 0, nil
	}

	log.Printf("[ChunkProcessor] %s: Found %d segments for chunk %s", cameraName, len(segments), chunkID)

	// Create the chunk group for this specific time window
	group := ChunkGroup{
		StartTime: targetChunkStart,
		EndTime:   targetChunkEnd,
		Segments:  segments,
	}

	// Create physical chunk file
	chunkPath, err := cp.createPhysicalChunk(ctx, cameraName, group, chunksPath)
	if err != nil {
		log.Printf("[ChunkProcessor] %s: Failed to create physical chunk: %v", cameraName, err)
		return 0, err
	}

	// Record chunk in database
	err = cp.recordChunkInDatabase(cameraName, chunkPath, group, diskID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			log.Printf("[ChunkProcessor] %s: Chunk %s already exists in database", cameraName, chunkID)
			// Remove the duplicate physical file
			os.Remove(chunkPath)
			return 0, nil
		}
		log.Printf("[ChunkProcessor] %s: Failed to record chunk in database: %v", cameraName, err)
		// Delete the physical file if database recording failed
		os.Remove(chunkPath)
		return 0, err
	}

	// Update the last processed segment time to the end of this chunk
	err = cp.updateLastProcessedSegmentTime(cameraName, targetChunkEnd)
	if err != nil {
		log.Printf("[ChunkProcessor] Warning: Failed to update last processed time for %s: %v", cameraName, err)
	}

	log.Printf("[ChunkProcessor] ✅ %s: Created chunk %s with %d segments", 
		cameraName, filepath.Base(chunkPath), len(segments))

	return 1, nil
}

// scanSegmentsInTimeRange scans for segment files within a specific time range
func (cp *ChunkProcessor) scanSegmentsInTimeRange(hlsPath string, startTime, endTime time.Time) ([]SegmentFile, error) {
	// Pattern to match HLS segment files: segment_20250811_234500.ts
	segmentPattern := regexp.MustCompile(`^segment_(\d{8})_(\d{6})\.ts$`)
	// Additional pattern for numeric segments: HHMMSS.ts (e.g., 112003.ts)
	numericPattern := regexp.MustCompile(`^(\d{6})\.ts$`)
	
	var segments []SegmentFile

	// Read directory contents
	files, err := os.ReadDir(hlsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %v", hlsPath, err)
	}

	log.Printf("[ChunkProcessor] Scanning %d files in %s for segments between %s and %s", 
		len(files), hlsPath, startTime.Format("15:04:05"), endTime.Format("15:04:05"))

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		var segmentTime time.Time
		var parseErr error

		// Try to match segment_YYYYMMDD_HHMMSS.ts pattern first
		matches := segmentPattern.FindStringSubmatch(file.Name())
		if len(matches) == 3 {
			// Parse timestamp from filename
			segmentTime, parseErr = parseSegmentTimestamp(matches[1], matches[2])
		} else {
			// Try numeric pattern HHMMSS.ts
			numMatches := numericPattern.FindStringSubmatch(file.Name())
			if len(numMatches) == 2 {
				// Parse HHMMSS format - assume it's for today's date
				hourStr := numMatches[1][:2]
				minuteStr := numMatches[1][2:4]
				secondStr := numMatches[1][4:6]
				
				hour, _ := strconv.Atoi(hourStr)
				minute, _ := strconv.Atoi(minuteStr)
				second, _ := strconv.Atoi(secondStr)
				
				// Use the date from startTime for these numeric segments
				segmentTime = time.Date(startTime.Year(), startTime.Month(), startTime.Day(),
					hour, minute, second, 0, startTime.Location())
				
				log.Printf("[ChunkProcessor] Parsed numeric segment %s as %s", 
					file.Name(), segmentTime.Format("15:04:05"))
			} else {
				continue // Not a recognized segment file
			}
		}

		if parseErr != nil {
			log.Printf("[ChunkProcessor] Warning: Could not parse timestamp for %s: %v", file.Name(), parseErr)
			continue
		}

		// Only include segments within the time range
		if segmentTime.Before(startTime) || segmentTime.After(endTime) || segmentTime.Equal(endTime) {
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
		log.Printf("[ChunkProcessor] Found segment: %s at %s", file.Name(), segmentTime.Format("15:04:05"))
	}

	// Sort segments by timestamp
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Timestamp.Before(segments[j].Timestamp)
	})

	log.Printf("[ChunkProcessor] Found %d segments in time range", len(segments))

	return segments, nil
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

// groupSegmentsIntoChunks groups segments into chunks of specified duration
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

// roundDownToChunkBoundary rounds time down to chunk boundary (e.g., 10-minute intervals)
func (cp *ChunkProcessor) roundDownToChunkBoundary(t time.Time, chunkDuration time.Duration) time.Time {
	minutes := int(chunkDuration.Minutes())
	roundedMinute := (t.Minute() / minutes) * minutes
	
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), roundedMinute, 0, 0, t.Location())
}

// createPhysicalChunk creates an actual TS file from segments
func (cp *ChunkProcessor) createPhysicalChunk(ctx context.Context, cameraName string, group ChunkGroup, chunksPath string) (string, error) {
	// Generate chunk filename
	chunkFilename := fmt.Sprintf("%s_%s_chunk.ts", cameraName, group.StartTime.Format("20060102_1504"))
	chunkPath := filepath.Join(chunksPath, chunkFilename)
	
	// Check if chunk already exists
	if _, err := os.Stat(chunkPath); err == nil {
		log.Printf("[ChunkProcessor] Chunk file already exists: %s", chunkFilename)
		// Verify it has content
		info, _ := os.Stat(chunkPath)
		if info.Size() > 0 {
			return chunkPath, nil
		}
		// Remove empty file
		os.Remove(chunkPath)
	}

	// Create segment list file for FFmpeg concatenation
	segmentListPath := filepath.Join(chunksPath, fmt.Sprintf("%s_segments.txt", chunkFilename[:len(chunkFilename)-3]))
	
	// Write segment list
	segmentListFile, err := os.Create(segmentListPath)
	if err != nil {
		return "", fmt.Errorf("failed to create segment list file: %v", err)
	}

	// Write segment paths for FFmpeg (they should now be absolute)
	log.Printf("[ChunkProcessor] Writing %d segments to list file", len(group.Segments))
	if len(group.Segments) > 0 {
		log.Printf("[ChunkProcessor] First segment path: %s", group.Segments[0].FilePath)
		log.Printf("[ChunkProcessor] Last segment path: %s", group.Segments[len(group.Segments)-1].FilePath)
	}
	
	for _, segment := range group.Segments {
		fmt.Fprintf(segmentListFile, "file '%s'\n", segment.FilePath)
	}
	segmentListFile.Close()

	// Ensure cleanup of segment list file
	defer os.Remove(segmentListPath)

	// Use FFmpeg directly to concatenate TS segments
	log.Printf("[ChunkProcessor] Creating chunk %s from %d segments...", chunkFilename, len(group.Segments))
	
	// Build FFmpeg command to concatenate TS files
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", segmentListPath,
		"-c", "copy", // Copy streams without re-encoding
		"-y", // Overwrite output file if exists
		chunkPath,
	)
	
	// Execute FFmpeg
	log.Printf("[ChunkProcessor] Running FFmpeg with segment list: %s", segmentListPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Debug: Show segment list content when FFmpeg fails
		if content, readErr := os.ReadFile(segmentListPath); readErr == nil {
			log.Printf("[ChunkProcessor] FFmpeg failed. Segment list content (first 10 lines):")
			lines := strings.Split(string(content), "\n")
			for i, line := range lines {
				if i >= 10 {
					log.Printf("[ChunkProcessor] ... and %d more lines", len(lines)-10)
					break
				}
				if line != "" {
					log.Printf("[ChunkProcessor] %d: %s", i+1, line)
				}
			}
		} else {
			log.Printf("[ChunkProcessor] Could not read segment list file: %v", readErr)
		}
		
		// Also check if the first segment file actually exists
		if len(group.Segments) > 0 {
			if _, statErr := os.Stat(group.Segments[0].FilePath); statErr != nil {
				log.Printf("[ChunkProcessor] First segment file does not exist: %s", group.Segments[0].FilePath)
			} else {
				log.Printf("[ChunkProcessor] First segment file exists: %s", group.Segments[0].FilePath)
			}
		}
		
		return "", fmt.Errorf("failed to concatenate segments: %v, output: %s", err, string(output))
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
	// First check system config for last processed time
	configKey := fmt.Sprintf("last_processed_segment_%s", cameraName)
	config, err := cp.db.GetSystemConfig(configKey)
	if err == nil && config.Value != "" {
		lastTime, err := time.Parse(time.RFC3339, config.Value)
		if err == nil {
			return lastTime, nil
		}
	}

	// Fallback: Get the latest chunk for this camera
	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour) // Look back 30 days
	
	chunks, err := cp.db.FindChunksInTimeRange(cameraName, startTime, endTime)
	if err != nil {
		return time.Time{}, err
	}

	if len(chunks) == 0 {
		// No chunks found, start from 1 hour ago to avoid processing huge backlog
		return time.Now().Add(-1 * time.Hour), nil
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

	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.Local), nil
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
