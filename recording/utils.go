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

// FindSegmentsInRangeFromDB returns a sorted list of MP4 files from the database whose timestamps fall within [startTime, endTime].
// This is the enhanced version that uses the database instead of filesystem scanning.
func FindSegmentsInRangeFromDB(cameraName string, startTime, endTime time.Time, db database.Database, diskManager *storage.DiskManager) ([]string, error) {
	// Get recording segments from database
	segments, err := db.GetRecordingSegments(cameraName, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to get recording segments from database: %w", err)
	}

	if len(segments) == 0 {
		return []string{}, nil
	}

	var matches []struct {
		path string
		ts   time.Time
	}

	// Convert database segments to full file paths
	for _, segment := range segments {
		// Get the storage disk for this segment
		disk, err := db.GetStorageDisk(segment.StorageDiskID)
		if err != nil {
			fmt.Printf("Warning: failed to get storage disk %s for segment %s: %v\n", segment.StorageDiskID, segment.ID, err)
			continue
		}

		if disk == nil {
			fmt.Printf("Warning: storage disk %s not found for segment %s\n", segment.StorageDiskID, segment.ID)
			continue
		}

		// Construct full path to the MP4 file
		fullPath := filepath.Join(disk.Path, segment.MP4Path)

		// Verify the file exists
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			fmt.Printf("Warning: segment file does not exist: %s\n", fullPath)
			continue
		}

		// Check if segment overlaps with requested time range
		if segment.SegmentEnd.Before(startTime) || segment.SegmentStart.After(endTime) {
			continue // No overlap
		}

		matches = append(matches, struct {
			path string
			ts   time.Time
		}{fullPath, segment.SegmentStart})
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

// FindSegmentsInRangeEnhanced is a hybrid approach that tries database first, then falls back to filesystem
func FindSegmentsInRangeEnhanced(cameraName, inputPath string, startTime, endTime time.Time, db database.Database, diskManager *storage.DiskManager) ([]string, error) {
	// Try database approach first (preferred for new system)
	if db != nil && diskManager != nil {
		segments, err := FindSegmentsInRangeFromDB(cameraName, startTime, endTime, db, diskManager)
		if err == nil && len(segments) > 0 {
			return segments, nil
		}
		
		// Log if database approach failed but continue with fallback
		if err != nil {
			fmt.Printf("Database segment discovery failed, falling back to filesystem: %v\n", err)
		}
	}

	// Fallback to original filesystem approach for compatibility
	return FindSegmentsInRange(inputPath, startTime, endTime)
}

// ValidateSegmentPaths verifies that all segment files in the database actually exist on disk
func ValidateSegmentPaths(cameraName string, db database.Database) error {
	// Get all segments for this camera (use a wide time range)
	past := time.Now().AddDate(-1, 0, 0) // 1 year ago
	future := time.Now().AddDate(1, 0, 0) // 1 year from now
	
	segments, err := db.GetRecordingSegments(cameraName, past, future)
	if err != nil {
		return fmt.Errorf("failed to get segments: %w", err)
	}

	missingFiles := []string{}
	
	for _, segment := range segments {
		// Get storage disk
		disk, err := db.GetStorageDisk(segment.StorageDiskID)
		if err != nil || disk == nil {
			missingFiles = append(missingFiles, fmt.Sprintf("Disk not found: %s for segment %s", segment.StorageDiskID, segment.ID))
			continue
		}

		// Check if file exists
		fullPath := filepath.Join(disk.Path, segment.MP4Path)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			missingFiles = append(missingFiles, fullPath)
		}
	}

	if len(missingFiles) > 0 {
		return fmt.Errorf("found %d missing segment files: %v", len(missingFiles), missingFiles[:min(5, len(missingFiles))])
	}

	return nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
