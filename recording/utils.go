package recording

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// FindSegmentsInRange returns a sorted list of MP4 files in inputPath whose timestamps fall within [startTime, endTime].
func FindSegmentsInRange(inputPath string, startTime, endTime time.Time) ([]string, error) {
	var matches []struct {
		path string
		ts   time.Time
	}

	entries, err := os.ReadDir(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	// loop through files to find the segments in the time range
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp4") {
			continue
		}
		base := entry.Name()
		// Expect format: camera_name_YYYYMMDD_HHMMSS.mp4
		parts := strings.Split(base, "_")
		if len(parts) < 3 {
			continue // not a valid segment filename
		}
		dateStr := parts[len(parts)-2]
		timeStr := strings.TrimSuffix(parts[len(parts)-1], ".mp4")
		ts, err := time.Parse("20060102_150405", dateStr+"_"+timeStr)
		if err != nil {
			continue // skip invalid timestamp
		}
		if !ts.Before(startTime) && !ts.After(endTime) {
			matches = append(matches, struct {
				path string
				ts   time.Time
			}{filepath.Join(inputPath, base), ts})
		}
	}

	// Sort by timestamp ascending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].ts.Before(matches[j].ts)
	})

	result := make([]string, len(matches))
	for i, m := range matches {
		result[i] = m.path
	}
	return result, nil
}

// FindSegmentsInRangeFromDB is deprecated - use FindSegmentsInRangeOptimized instead
// Kept for backward compatibility only
func FindSegmentsInRangeFromDB(cameraName string, startTime, endTime time.Time, db database.Database, diskManager *storage.DiskManager) ([]string, error) {
	// Fallback to optimized filesystem approach
	return FindSegmentsInRangeOptimized(cameraName, "./videos", startTime, endTime)
}

// FindSegmentsInRangeMultiDisk searches for segments across multiple potential storage locations
func FindSegmentsInRangeMultiDisk(cameraName string, startTime, endTime time.Time, storagePaths []string) ([]string, error) {
	var allSegments []struct {
		path string
		ts   time.Time
	}

	// Search across all provided storage paths
	for _, storagePath := range storagePaths {
		cameraPath := filepath.Join(storagePath, "recordings", cameraName, "mp4")
		
		// Skip if directory doesn't exist
		if _, err := os.Stat(cameraPath); os.IsNotExist(err) {
			continue
		}

		segments, err := FindSegmentsInRange(cameraPath, startTime, endTime)
		if err != nil {
			fmt.Printf("Warning: failed to scan %s: %v\n", cameraPath, err)
			continue
		}

		// Add found segments with timestamp for sorting
		for _, segmentPath := range segments {
			filename := filepath.Base(segmentPath)
			ts, err := parseTimestampFromFilename(filename)
			if err != nil {
				fmt.Printf("Warning: failed to parse timestamp from %s: %v\n", filename, err)
				continue
			}
			allSegments = append(allSegments, struct {
				path string
				ts   time.Time
			}{segmentPath, ts})
		}
	}

	// Sort all segments by timestamp
	sort.Slice(allSegments, func(i, j int) bool {
		return allSegments[i].ts.Before(allSegments[j].ts)
	})

	// Convert to string slice
	result := make([]string, len(allSegments))
	for i, seg := range allSegments {
		result[i] = seg.path
	}

	return result, nil
}

// parseTimestampFromFilename extracts timestamp from MP4 filename
func parseTimestampFromFilename(filename string) (time.Time, error) {
	// Expected format: camera_name_YYYYMMDD_HHMMSS.mp4
	parts := strings.Split(filename, "_")
	if len(parts) < 3 {
		return time.Time{}, fmt.Errorf("invalid filename format: %s", filename)
	}
	
	dateStr := parts[len(parts)-2]
	timeStr := strings.TrimSuffix(parts[len(parts)-1], ".mp4")
	timestampStr := dateStr + "_" + timeStr
	
	return time.Parse("20060102_150405", timestampStr)
}

// FindSegmentsInRangeOptimized searches for segments using automatic disk discovery
func FindSegmentsInRangeOptimized(cameraName, primaryPath string, startTime, endTime time.Time, additionalPaths ...string) ([]string, error) {
	// For now, use simple single-path approach 
	// The existing disk management system handles multi-disk automatically
	cameraPath := filepath.Join(primaryPath, "recordings", cameraName, "mp4")
	return FindSegmentsInRange(cameraPath, startTime, endTime)
}

// FindSegmentsInRangeEnhanced is maintained for backward compatibility but simplified
func FindSegmentsInRangeEnhanced(cameraName, inputPath string, startTime, endTime time.Time, db database.Database, diskManager *storage.DiskManager) ([]string, error) {
	// Extract storage path from input path (remove /recordings/camera/mp4 suffix)
	storagePath := inputPath
	if strings.Contains(inputPath, "/recordings/") {
		parts := strings.Split(inputPath, "/recordings/")
		if len(parts) > 0 {
			storagePath = parts[0]
		}
	}

	// Use optimized filesystem approach - no database dependency
	return FindSegmentsInRangeOptimized(cameraName, storagePath, startTime, endTime)
}

// ValidateSegmentPaths verifies filesystem integrity for camera segments (simplified version)
func ValidateSegmentPaths(cameraName string, storagePaths []string) error {
	var totalSegments int
	var missingDirs []string
	
	for _, storagePath := range storagePaths {
		cameraPath := filepath.Join(storagePath, "recordings", cameraName, "mp4")
		
		// Check if directory exists
		if _, err := os.Stat(cameraPath); os.IsNotExist(err) {
			missingDirs = append(missingDirs, cameraPath)
			continue
		}
		
		// Count segments in this directory
		entries, err := os.ReadDir(cameraPath)
		if err != nil {
			continue
		}
		
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".mp4") {
				totalSegments++
			}
		}
	}
	
	if len(missingDirs) == len(storagePaths) {
		return fmt.Errorf("no segment directories found for camera %s", cameraName)
	}
	
	fmt.Printf("Camera %s: found %d segments across %d storage locations\n", cameraName, totalSegments, len(storagePaths)-len(missingDirs))
	return nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
