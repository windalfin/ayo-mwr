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

// FindSegmentsInRange returns a sorted list of video files (.mp4, .ts, .mkv, etc.) in inputPath whose timestamps fall within [startTime, endTime].
func FindSegmentsInRange(inputPath string, startTime, endTime time.Time) ([]string, error) {
	var matches []struct {
		path string
		ts   time.Time
	}

	entries, err := os.ReadDir(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	// Supported video extensions for auto-detection
	videoExtensions := []string{".mp4", ".ts", ".mkv", ".avi", ".mov", ".wmv", ".webm", ".m4v"}
	
	// Helper function to check if file has supported video extension
	isVideoFile := func(filename string) bool {
		for _, ext := range videoExtensions {
			if strings.HasSuffix(strings.ToLower(filename), ext) {
				return true
			}
		}
		return false
	}

	// Helper function to get file extension
	getFileExtension := func(filename string) string {
		for _, ext := range videoExtensions {
			if strings.HasSuffix(strings.ToLower(filename), ext) {
				return ext
			}
		}
		return ""
	}

	// Loop through files to find the segments in the time range
	for _, entry := range entries {
		if entry.IsDir() || !isVideoFile(entry.Name()) {
			continue
		}
		
		base := entry.Name()
		fileExt := getFileExtension(base)
		var ts time.Time
		
		if fileExt == ".ts" {
			// Parse .ts filename format: segment_YYYYMMDD_HHMMSS.ts
			if !strings.HasPrefix(base, "segment_") {
				continue // skip non-segment files
			}
			
			// Remove prefix and suffix
			timeStr := strings.TrimPrefix(base, "segment_")
			timeStr = strings.TrimSuffix(timeStr, fileExt)
			
			// Parse timestamp
			ts, err = time.ParseInLocation("20060102_150405", timeStr, time.Local)
			if err != nil {
				continue // skip invalid timestamp
			}
		} else {
			// Parse MP4/other formats: camera_name_YYYYMMDD_HHMMSS.ext
			parts := strings.Split(base, "_")
			if len(parts) < 3 {
				continue // not a valid segment filename
			}
			dateStr := parts[len(parts)-2]
			timeStr := strings.TrimSuffix(parts[len(parts)-1], fileExt)
			ts, err = time.ParseInLocation("20060102_150405", dateStr+"_"+timeStr, time.Local)
			if err != nil {
				continue // skip invalid timestamp
			}
		}

		// Check if segment is within time range
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

// parseTimestampFromFilename extracts timestamp from video filename (any extension)
func parseTimestampFromFilename(filename string) (time.Time, error) {
	// Supported video extensions
	videoExtensions := []string{".mp4", ".ts", ".mkv", ".avi", ".mov", ".wmv", ".webm", ".m4v"}
	
	// Helper function to get file extension
	getFileExtension := func(filename string) string {
		for _, ext := range videoExtensions {
			if strings.HasSuffix(strings.ToLower(filename), ext) {
				return ext
			}
		}
		return ""
	}
	
	fileExt := getFileExtension(filename)
	if fileExt == "" {
		return time.Time{}, fmt.Errorf("unsupported file extension: %s", filename)
	}
	
	if fileExt == ".ts" {
		// Parse .ts filename format: segment_YYYYMMDD_HHMMSS.ts
		if !strings.HasPrefix(filename, "segment_") {
			return time.Time{}, fmt.Errorf("invalid .ts filename format: %s", filename)
		}
		
		// Remove prefix and suffix
		timeStr := strings.TrimPrefix(filename, "segment_")
		timeStr = strings.TrimSuffix(timeStr, fileExt)
		
		return time.ParseInLocation("20060102_150405", timeStr, time.Local)
	} else {
		// Parse MP4/other formats: camera_name_YYYYMMDD_HHMMSS.ext
		parts := strings.Split(filename, "_")
		if len(parts) < 3 {
			return time.Time{}, fmt.Errorf("invalid filename format: %s", filename)
		}

		dateStr := parts[len(parts)-2]
		timeStr := strings.TrimSuffix(parts[len(parts)-1], fileExt)
		timestampStr := dateStr + "_" + timeStr

		return time.ParseInLocation("20060102_150405", timestampStr, time.Local)
	}
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
			if !entry.IsDir() {
				// Check if it's a video file with supported extension
				videoExtensions := []string{".mp4", ".ts", ".mkv", ".avi", ".mov", ".wmv", ".webm", ".m4v"}
				for _, ext := range videoExtensions {
					if strings.HasSuffix(strings.ToLower(entry.Name()), ext) {
						totalSegments++
						break
					}
				}
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
