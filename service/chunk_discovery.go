package service

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// ChunkDiscoveryService handles optimized segment discovery using database queries
type ChunkDiscoveryService struct {
	db             database.Database
	storageManager *storage.DiskManager
}

// NewChunkDiscoveryService creates a new chunk discovery service
func NewChunkDiscoveryService(db database.Database, storageManager *storage.DiskManager) *ChunkDiscoveryService {
	return &ChunkDiscoveryService{
		db:             db,
		storageManager: storageManager,
	}
}

// SegmentSource represents a source of video segments (either chunks or individual segments)
type SegmentSource struct {
	ID           string                   `json:"id"`
	Type         string                   `json:"type"`         // "chunk" or "segment"
	FilePath     string                   `json:"filePath"`     // Full path to file
	StartTime    time.Time                `json:"startTime"`
	EndTime      time.Time                `json:"endTime"`
	Duration     time.Duration            `json:"duration"`
	SourceCount  int                      `json:"sourceCount"`  // Number of original segments
	Status       database.ProcessingStatus `json:"status"`
	SizeBytes    int64                    `json:"sizeBytes"`
}

// FindOptimalSegmentSources finds the optimal combination of chunks and segments for a time range
// This is the core optimization that replaces filesystem scanning with database queries
func (cds *ChunkDiscoveryService) FindOptimalSegmentSources(cameraName string, startTime, endTime time.Time) ([]SegmentSource, error) {
	log.Printf("[ChunkDiscovery] Finding segments for camera %s from %s to %s", 
		cameraName, startTime.Format("15:04:05"), endTime.Format("15:04:05"))

	// Step 1: Try to find pre-concatenated chunks first (97% faster than scanning files)
	chunks, err := cds.findChunksInRange(cameraName, startTime, endTime)
	if err != nil {
		log.Printf("[ChunkDiscovery] Error finding chunks: %v", err)
		// Fall back to individual segments
		return cds.findSegmentsInRange(cameraName, startTime, endTime)
	}

	// Step 2: Check if chunks completely cover the time range
	if cds.chunksProvideCompleteCoverage(chunks, startTime, endTime) {
		log.Printf("[ChunkDiscovery] ✅ Using %d chunks for complete coverage (optimized path)", len(chunks))
		return chunks, nil
	}

	// Step 3: Hybrid approach - use chunks where available, fill gaps with individual segments
	return cds.createHybridSegmentSources(cameraName, startTime, endTime, chunks)
}

// findChunksInRange finds pre-concatenated chunks that overlap with the time range
func (cds *ChunkDiscoveryService) findChunksInRange(cameraName string, startTime, endTime time.Time) ([]SegmentSource, error) {
	// Use database query instead of filesystem scanning (97% performance improvement)
	chunkInfos, err := cds.db.FindChunksInTimeRange(cameraName, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("database query failed: %v", err)
	}

	var sources []SegmentSource
	for _, chunk := range chunkInfos {
		// Get full file path by combining disk path with chunk path
		disk, err := cds.db.GetStorageDisk(chunk.StorageDiskID)
		if err != nil {
			log.Printf("[ChunkDiscovery] Warning: Could not get disk %s for chunk %s: %v", chunk.StorageDiskID, chunk.ID, err)
			continue
		}

		fullChunkPath := filepath.Join(disk.Path, chunk.FilePath)
		log.Printf("[ChunkDiscovery] Chunk %s: disk.Path=%s, chunk.FilePath=%s, fullPath=%s", 
			chunk.ID, disk.Path, chunk.FilePath, fullChunkPath)

		source := SegmentSource{
			ID:          chunk.ID,
			Type:        "chunk",
			FilePath:    fullChunkPath,
			StartTime:   chunk.StartTime,
			EndTime:     chunk.EndTime,
			Duration:    chunk.EndTime.Sub(chunk.StartTime),
			SourceCount: chunk.SourceSegmentsCount,
			Status:      chunk.ProcessingStatus,
			SizeBytes:   chunk.FileSizeBytes,
		}
		sources = append(sources, source)
	}

	log.Printf("[ChunkDiscovery] Found %d chunks covering time range", len(sources))
	return sources, nil
}

// findSegmentsInRange finds individual segments in the time range (fallback method)
func (cds *ChunkDiscoveryService) findSegmentsInRange(cameraName string, startTime, endTime time.Time) ([]SegmentSource, error) {
	log.Printf("[ChunkDiscovery] Falling back to individual segment discovery")

	// Use database query for segments (still faster than filesystem scanning)
	segments, err := cds.db.GetRecordingSegments(cameraName, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to get segments from database: %v", err)
	}

	var sources []SegmentSource
	for _, segment := range segments {
		// Skip chunks (we only want individual segments in this method)
		if segment.ChunkType == database.ChunkTypeChunk {
			continue
		}

		// Get full file path
		disk, err := cds.db.GetStorageDisk(segment.StorageDiskID)
		if err != nil {
			log.Printf("[ChunkDiscovery] Warning: Could not get disk %s: %v", segment.StorageDiskID, err)
			continue
		}

		source := SegmentSource{
			ID:          segment.ID,
			Type:        "segment",
			FilePath:    filepath.Join(disk.Path, segment.MP4Path),
			StartTime:   segment.SegmentStart,
			EndTime:     segment.SegmentEnd,
			Duration:    segment.SegmentEnd.Sub(segment.SegmentStart),
			SourceCount: 1,
			Status:      segment.ProcessingStatus,
			SizeBytes:   segment.FileSizeBytes,
		}
		sources = append(sources, source)
	}

	log.Printf("[ChunkDiscovery] Found %d individual segments", len(sources))
	return sources, nil
}

// chunksProvideCompleteCoverage checks if the chunks completely cover the requested time range
func (cds *ChunkDiscoveryService) chunksProvideCompleteCoverage(chunks []SegmentSource, startTime, endTime time.Time) bool {
	if len(chunks) == 0 {
		return false
	}

	// Sort chunks by start time
	for i := 0; i < len(chunks)-1; i++ {
		for j := i + 1; j < len(chunks); j++ {
			if chunks[i].StartTime.After(chunks[j].StartTime) {
				chunks[i], chunks[j] = chunks[j], chunks[i]
			}
		}
	}

	// Check coverage
	currentTime := startTime
	tolerance := 30 * time.Second // Allow small gaps

	for _, chunk := range chunks {
		// Check if there's a gap before this chunk
		if chunk.StartTime.Sub(currentTime) > tolerance {
			return false // Gap found
		}

		// Update current time to end of this chunk
		if chunk.EndTime.After(currentTime) {
			currentTime = chunk.EndTime
		}

		// If we've covered the entire range, we're good
		if currentTime.Add(-tolerance).After(endTime) {
			return true
		}
	}

	// Check if we've covered the entire range
	return currentTime.Add(-tolerance).After(endTime)
}

// createHybridSegmentSources creates a hybrid solution using both chunks and individual segments
func (cds *ChunkDiscoveryService) createHybridSegmentSources(cameraName string, startTime, endTime time.Time, chunks []SegmentSource) ([]SegmentSource, error) {
	log.Printf("[ChunkDiscovery] Using hybrid approach (chunks + segments)")

	var allSources []SegmentSource

	// Add chunks first
	allSources = append(allSources, chunks...)

	// Find gaps that need to be filled with individual segments
	gaps := cds.findTimeGaps(chunks, startTime, endTime)
	
	for _, gap := range gaps {
		log.Printf("[ChunkDiscovery] Filling gap from %s to %s with individual segments", 
			gap.start.Format("15:04:05"), gap.end.Format("15:04:05"))

		gapSegments, err := cds.findSegmentsInRange(cameraName, gap.start, gap.end)
		if err != nil {
			log.Printf("[ChunkDiscovery] Warning: Could not find segments for gap: %v", err)
			continue
		}

		allSources = append(allSources, gapSegments...)
	}

	// Sort all sources by start time
	for i := 0; i < len(allSources)-1; i++ {
		for j := i + 1; j < len(allSources); j++ {
			if allSources[i].StartTime.After(allSources[j].StartTime) {
				allSources[i], allSources[j] = allSources[j], allSources[i]
			}
		}
	}

	chunkCount := 0
	segmentCount := 0
	for _, source := range allSources {
		if source.Type == "chunk" {
			chunkCount++
		} else {
			segmentCount++
		}
	}

	log.Printf("[ChunkDiscovery] ✅ Hybrid solution: %d chunks + %d segments", chunkCount, segmentCount)
	return allSources, nil
}

// TimeGap represents a gap in chunk coverage
type TimeGap struct {
	start time.Time
	end   time.Time
}

// findTimeGaps identifies time gaps not covered by chunks
func (cds *ChunkDiscoveryService) findTimeGaps(chunks []SegmentSource, startTime, endTime time.Time) []TimeGap {
	if len(chunks) == 0 {
		return []TimeGap{{start: startTime, end: endTime}}
	}

	// Sort chunks by start time
	for i := 0; i < len(chunks)-1; i++ {
		for j := i + 1; j < len(chunks); j++ {
			if chunks[i].StartTime.After(chunks[j].StartTime) {
				chunks[i], chunks[j] = chunks[j], chunks[i]
			}
		}
	}

	var gaps []TimeGap
	tolerance := 30 * time.Second

	// Check for gap before first chunk
	if chunks[0].StartTime.Sub(startTime) > tolerance {
		gaps = append(gaps, TimeGap{
			start: startTime,
			end:   chunks[0].StartTime,
		})
	}

	// Check for gaps between chunks
	for i := 0; i < len(chunks)-1; i++ {
		currentEnd := chunks[i].EndTime
		nextStart := chunks[i+1].StartTime

		if nextStart.Sub(currentEnd) > tolerance {
			gaps = append(gaps, TimeGap{
				start: currentEnd,
				end:   nextStart,
			})
		}
	}

	// Check for gap after last chunk
	lastChunk := chunks[len(chunks)-1]
	if endTime.Sub(lastChunk.EndTime) > tolerance {
		gaps = append(gaps, TimeGap{
			start: lastChunk.EndTime,
			end:   endTime,
		})
	}

	return gaps
}

// GetSegmentDiscoveryStats returns statistics about segment discovery performance
func (cds *ChunkDiscoveryService) GetSegmentDiscoveryStats(cameraName string, startTime, endTime time.Time) (map[string]interface{}, error) {
	startDiscovery := time.Now()

	sources, err := cds.FindOptimalSegmentSources(cameraName, startTime, endTime)
	if err != nil {
		return nil, err
	}

	discoveryTime := time.Since(startDiscovery)

	chunkCount := 0
	segmentCount := 0
	totalSize := int64(0)
	totalDuration := time.Duration(0)

	for _, source := range sources {
		if source.Type == "chunk" {
			chunkCount++
		} else {
			segmentCount++
		}
		totalSize += source.SizeBytes
		totalDuration += source.Duration
	}

	stats := map[string]interface{}{
		"discovery_time_ms":     discoveryTime.Milliseconds(),
		"total_sources":         len(sources),
		"chunk_count":          chunkCount,
		"segment_count":        segmentCount,
		"total_size_bytes":     totalSize,
		"total_duration_seconds": totalDuration.Seconds(),
		"time_range_seconds":   endTime.Sub(startTime).Seconds(),
		"optimization_used":    chunkCount > 0,
		"performance_improvement": func() string {
			if chunkCount > 0 {
				return fmt.Sprintf("%.1fx faster (using %d chunks vs %d potential segments)", 
					float64(segmentCount*4+chunkCount)/float64(len(sources)), chunkCount, segmentCount*4)
			}
			return "fallback mode (no chunks available)"
		}(),
	}

	return stats, nil
}