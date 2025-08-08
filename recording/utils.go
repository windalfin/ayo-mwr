package recording

import (
	"fmt"
	"log"
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
	// endtime kasih toleransi 1 menit
	endTime = endTime.Add(1 * time.Minute)
	var matches []struct {
		path string
		ts   time.Time
	}

	entries, err := os.ReadDir(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	// Loop through files to find the segments in the time range
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		base := entry.Name()
		baseLower := strings.ToLower(base)

		// Single pass extension detection - optimized for performance
		var fileExt string
		var isVideo bool

		// Optimized extension checking - check most common first
		switch {
		case strings.HasSuffix(baseLower, ".mp4"):
			fileExt = ".mp4"
			isVideo = true
		case strings.HasSuffix(baseLower, ".ts"):
			fileExt = ".ts"
			isVideo = true
		case strings.HasSuffix(baseLower, ".mkv"):
			fileExt = ".mkv"
			isVideo = true
		case strings.HasSuffix(baseLower, ".avi"):
			fileExt = ".avi"
			isVideo = true
		case strings.HasSuffix(baseLower, ".mov"):
			fileExt = ".mov"
			isVideo = true
		case strings.HasSuffix(baseLower, ".wmv"):
			fileExt = ".wmv"
			isVideo = true
		case strings.HasSuffix(baseLower, ".webm"):
			fileExt = ".webm"
			isVideo = true
		case strings.HasSuffix(baseLower, ".m4v"):
			fileExt = ".m4v"
			isVideo = true
		}

		if !isVideo {
			continue
		}

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
	filenameLower := strings.ToLower(filename)

	// Single pass extension detection - optimized for performance
	var fileExt string

	// Optimized extension checking - check most common first
	switch {
	case strings.HasSuffix(filenameLower, ".mp4"):
		fileExt = ".mp4"
	case strings.HasSuffix(filenameLower, ".ts"):
		fileExt = ".ts"
	case strings.HasSuffix(filenameLower, ".mkv"):
		fileExt = ".mkv"
	case strings.HasSuffix(filenameLower, ".avi"):
		fileExt = ".avi"
	case strings.HasSuffix(filenameLower, ".mov"):
		fileExt = ".mov"
	case strings.HasSuffix(filenameLower, ".wmv"):
		fileExt = ".wmv"
	case strings.HasSuffix(filenameLower, ".webm"):
		fileExt = ".webm"
	case strings.HasSuffix(filenameLower, ".m4v"):
		fileExt = ".m4v"
	default:
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
				// Optimized extension checking for video files
				nameLower := strings.ToLower(entry.Name())
				switch {
				case strings.HasSuffix(nameLower, ".mp4"),
					strings.HasSuffix(nameLower, ".ts"),
					strings.HasSuffix(nameLower, ".mkv"),
					strings.HasSuffix(nameLower, ".avi"),
					strings.HasSuffix(nameLower, ".mov"),
					strings.HasSuffix(nameLower, ".wmv"),
					strings.HasSuffix(nameLower, ".webm"),
					strings.HasSuffix(nameLower, ".m4v"):
					totalSegments++
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

// FindSegmentsInRangeMultiRoot searches for HLS segments across multiple day camera roots for booking processing
func FindSegmentsInRangeMultiRoot(db database.Database, cameraName string, startTime, endTime time.Time) ([]string, error) {
	// Derive date from start time (bookings don't span midnight)
	date := startTime.Format("20060102")
	
	// Get all day camera roots for this camera and date
	roots, err := db.GetDayCameraRoots(cameraName, date)
	if err != nil {
		return nil, fmt.Errorf("failed to get day camera roots for camera %s, date %s: %v", cameraName, date, err)
	}
	
	if len(roots) == 0 {
		log.Printf("[FindSegmentsInRangeMultiRoot] No roots found for camera %s, date %s", cameraName, date)
		return nil, fmt.Errorf("no storage roots found for camera %s on date %s", cameraName, date)
	}
	
	log.Printf("[FindSegmentsInRangeMultiRoot] Searching %d roots for camera %s, date %s", len(roots), cameraName, date)
	
	var allSegments []struct {
		path string
		ts   time.Time
	}
	
	// Search across all roots for this camera/date combination
	for _, root := range roots {
		// Check if root directory exists
		if _, err := os.Stat(root.BasePath); os.IsNotExist(err) {
			log.Printf("[FindSegmentsInRangeMultiRoot] Root path does not exist: %s", root.BasePath)
			continue
		}
		
		// Find segments in this root using existing logic
		segments, err := FindSegmentsInRange(root.BasePath, startTime, endTime)
		if err != nil {
			log.Printf("[FindSegmentsInRangeMultiRoot] Warning: failed to scan root %s: %v", root.BasePath, err)
			continue
		}
		
		log.Printf("[FindSegmentsInRangeMultiRoot] Found %d segments in root %s (disk %s)", len(segments), root.BasePath, root.DiskID)
		
		// Add found segments with timestamp for sorting
		for _, segmentPath := range segments {
			filename := filepath.Base(segmentPath)
			ts, err := parseSegmentTimeFromFilename(filename)
			if err != nil {
				log.Printf("[FindSegmentsInRangeMultiRoot] Warning: failed to parse timestamp from %s: %v", filename, err)
				continue
			}
			allSegments = append(allSegments, struct {
				path string
				ts   time.Time
			}{segmentPath, ts})
		}
		
		// Update last seen time for this root
		db.UpdateDayCameraRootLastSeen(cameraName, date, root.DiskID, time.Now())
	}
	
	if len(allSegments) == 0 {
		return nil, fmt.Errorf("no segments found across %d roots for camera %s on date %s", len(roots), cameraName, date)
	}
	
	// Sort segments by timestamp
	sort.Slice(allSegments, func(i, j int) bool {
		return allSegments[i].ts.Before(allSegments[j].ts)
	})
	
	// Extract just the paths
	result := make([]string, len(allSegments))
	for i, seg := range allSegments {
		result[i] = seg.path
	}
	
	log.Printf("[FindSegmentsInRangeMultiRoot] ✅ SUCCESS: %d segments found across %d roots for camera %s", len(result), len(roots), cameraName)
	
	// Log detailed multi-root lookup statistics for monitoring
	logMultiRootLookupStats(cameraName, date, roots, len(result), startTime, endTime)
	
	return result, nil
}

// logMultiRootLookupStats provides detailed observability for multi-root operations
func logMultiRootLookupStats(cameraName, date string, roots []database.DayCameraRoot, totalSegments int, startTime, endTime time.Time) {
	log.Printf("📊 [MULTI-ROOT STATS] Camera: %s | Date: %s | Roots: %d | Segments: %d | Window: %v to %v", 
		cameraName, date, len(roots), totalSegments, startTime.Format("15:04:05"), endTime.Format("15:04:05"))
	
	// Log per-root statistics
	for i, root := range roots {
		age := time.Since(root.CreatedAt)
		lastSeen := time.Since(root.LastSeenTime)
		log.Printf("📁 [ROOT %d] Disk: %s | Path: %s | Age: %v | Last seen: %v ago", 
			i+1, root.DiskID, root.BasePath, age.Round(time.Minute), lastSeen.Round(time.Second))
	}
	
	// Monitor for potential issues
	if len(roots) > 3 {
		log.Printf("⚠️ [MULTI-ROOT WARNING] Camera %s has %d roots for date %s - possible excessive disk switching", 
			cameraName, len(roots), date)
	}
	
	if totalSegments == 0 {
		log.Printf("❌ [MULTI-ROOT ALERT] No segments found across %d roots for camera %s on date %s", 
			len(roots), cameraName, date)
	}
}

// GetMultiRootStats returns comprehensive statistics about multi-root operations
func GetMultiRootStats(db database.Database) (map[string]interface{}, error) {
	allRoots, err := db.GetAllDayCameraRoots()
	if err != nil {
		return nil, fmt.Errorf("failed to get all day camera roots: %v", err)
	}
	
	// Aggregate statistics
	stats := map[string]interface{}{
		"total_roots":          len(allRoots),
		"cameras_with_roots":   make(map[string]int),
		"dates_with_roots":     make(map[string]int),
		"disks_with_roots":     make(map[string]int),
		"multi_root_dates":     make([]map[string]interface{}, 0),
		"disk_switch_events":   0,
		"oldest_root":          time.Time{},
		"newest_root":          time.Time{},
	}
	
	cameraStats := make(map[string]int)
	dateStats := make(map[string]int)
	diskStats := make(map[string]int)
	dateRootCount := make(map[string]map[string]int) // date -> camera -> root count
	
	var oldestRoot, newestRoot time.Time
	for i, root := range allRoots {
		// Camera statistics
		cameraStats[root.CameraName]++
		
		// Date statistics
		dateKey := root.Date
		dateStats[dateKey]++
		
		// Disk statistics
		diskStats[root.DiskID]++
		
		// Track roots per date per camera for multi-root detection
		if dateRootCount[dateKey] == nil {
			dateRootCount[dateKey] = make(map[string]int)
		}
		dateRootCount[dateKey][root.CameraName]++
		
		// Track oldest and newest roots
		if i == 0 || root.CreatedAt.Before(oldestRoot) {
			oldestRoot = root.CreatedAt
		}
		if i == 0 || root.CreatedAt.After(newestRoot) {
			newestRoot = root.CreatedAt
		}
	}
	
	// Detect multi-root scenarios (disk switches)
	multiRootEvents := make([]map[string]interface{}, 0)
	diskSwitchCount := 0
	
	for date, cameraRoots := range dateRootCount {
		for camera, rootCount := range cameraRoots {
			if rootCount > 1 {
				diskSwitchCount += rootCount - 1 // Each additional root represents a disk switch
				multiRootEvents = append(multiRootEvents, map[string]interface{}{
					"date":       date,
					"camera":     camera,
					"root_count": rootCount,
					"switches":   rootCount - 1,
				})
			}
		}
	}
	
	// Update statistics
	stats["cameras_with_roots"] = cameraStats
	stats["dates_with_roots"] = dateStats
	stats["disks_with_roots"] = diskStats
	stats["multi_root_dates"] = multiRootEvents
	stats["disk_switch_events"] = diskSwitchCount
	stats["unique_cameras"] = len(cameraStats)
	stats["unique_dates"] = len(dateStats)
	stats["unique_disks"] = len(diskStats)
	
	if !oldestRoot.IsZero() {
		stats["oldest_root"] = oldestRoot.Format("2006-01-02 15:04:05")
		stats["system_age_days"] = int(time.Since(oldestRoot).Hours() / 24)
	}
	if !newestRoot.IsZero() {
		stats["newest_root"] = newestRoot.Format("2006-01-02 15:04:05")
	}
	
	return stats, nil
}
