package cron

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"ayo-mwr/database"
)

// HLSCleanupCron handles cleanup of deprecated HLS files
type HLSCleanupCron struct {
	db      database.Database
	running bool
}

// NewHLSCleanupCron creates a new HLS cleanup cron instance
func NewHLSCleanupCron(db database.Database) *HLSCleanupCron {
	return &HLSCleanupCron{
		db:      db,
		running: false,
	}
}

// Start begins the HLS cleanup cron job
func (hcc *HLSCleanupCron) Start() {
	if hcc.running {
		log.Println("HLS cleanup cron is already running")
		return
	}

	hcc.running = true
	log.Println("Starting HLS cleanup cron job")

	go func() {
		// Run initial cleanup after a short delay
		time.Sleep(5 * time.Minute)
		hcc.runHLSCleanup()

		// Then run daily at 3 AM (after disk scan at 2 AM)
		for hcc.running {
			now := time.Now()
			next3AM := time.Date(now.Year(), now.Month(), now.Day()+1, 3, 0, 0, 0, now.Location())
			duration := next3AM.Sub(now)

			log.Printf("HLS cleanup cron: next run scheduled in %v at %v", duration, next3AM)

			select {
			case <-time.After(duration):
				if hcc.running {
					hcc.runHLSCleanup()
				}
			}
		}
	}()
}

// Stop stops the HLS cleanup cron job
func (hcc *HLSCleanupCron) Stop() {
	log.Println("Stopping HLS cleanup cron job")
	hcc.running = false
}

// runHLSCleanup performs the daily HLS cleanup process
func (hcc *HLSCleanupCron) runHLSCleanup() {
	log.Println("=== Starting HLS cleanup process ===")
	startTime := time.Now()

	cleanupStats := struct {
		videosProcessed   int
		hlsFilesDeleted   int
		hlsDirsDeleted    int
		spaceFreedMB      int64
		errors            int
	}{}

	// Get all videos that have HLS files but are not marked as deprecated
	videos, err := hcc.getVideosWithHLS()
	if err != nil {
		log.Printf("ERROR: Failed to get videos with HLS: %v", err)
		return
	}

	log.Printf("Found %d videos with HLS files to process", len(videos))

	for _, video := range videos {
		cleanupStats.videosProcessed++
		
		// Verify MP4 file exists before cleaning HLS
		if !hcc.verifyMP4Exists(video) {
			log.Printf("WARNING: Skipping HLS cleanup for video %s - MP4 file not found", video.ID)
			cleanupStats.errors++
			continue
		}

		// Clean up HLS files for this video
		filesDeleted, dirsDeleted, spaceFreed, err := hcc.cleanupVideoHLS(video)
		if err != nil {
			log.Printf("ERROR: Failed to cleanup HLS for video %s: %v", video.ID, err)
			cleanupStats.errors++
			continue
		}

		cleanupStats.hlsFilesDeleted += filesDeleted
		cleanupStats.hlsDirsDeleted += dirsDeleted
		cleanupStats.spaceFreedMB += spaceFreed

		// Mark video as HLS deprecated in database
		err = hcc.markVideoHLSDeprecated(video.ID)
		if err != nil {
			log.Printf("WARNING: Failed to mark video %s as HLS deprecated: %v", video.ID, err)
		}

		// Small delay to avoid overwhelming the system
		time.Sleep(100 * time.Millisecond)
	}

	duration := time.Since(startTime)
	log.Printf("=== HLS cleanup completed in %v ===", duration)
	log.Printf("Cleanup Statistics:")
	log.Printf("  Videos processed: %d", cleanupStats.videosProcessed)
	log.Printf("  HLS files deleted: %d", cleanupStats.hlsFilesDeleted)
	log.Printf("  HLS directories deleted: %d", cleanupStats.hlsDirsDeleted)
	log.Printf("  Space freed: %d MB", cleanupStats.spaceFreedMB)
	log.Printf("  Errors: %d", cleanupStats.errors)
}

// getVideosWithHLS retrieves videos that have HLS files but are not deprecated
func (hcc *HLSCleanupCron) getVideosWithHLS() ([]database.VideoMetadata, error) {
	// Get all videos (we'll filter those with HLS paths)
	allVideos, err := hcc.db.ListVideos(10000, 0) // Large limit to get all videos
	if err != nil {
		return nil, err
	}

	var videosWithHLS []database.VideoMetadata
	for _, video := range allVideos {
		// Check if video has HLS path and is not already marked as deprecated
		if video.HLSPath != "" && !video.DeprecatedHLS {
			videosWithHLS = append(videosWithHLS, video)
		}
	}

	return videosWithHLS, nil
}

// verifyMP4Exists checks if the MP4 file exists for the video
func (hcc *HLSCleanupCron) verifyMP4Exists(video database.VideoMetadata) bool {
	// Check both local MP4 path and new full path
	pathsToCheck := []string{}
	
	if video.LocalPath != "" {
		pathsToCheck = append(pathsToCheck, video.LocalPath)
	}
	
	if video.MP4FullPath != "" {
		pathsToCheck = append(pathsToCheck, video.MP4FullPath)
	}

	// Also check if this video has corresponding recording segments
	segments, err := hcc.db.GetRecordingSegments(video.CameraName, video.CreatedAt, video.CreatedAt.Add(24*time.Hour))
	if err == nil && len(segments) > 0 {
		return true // Has segments in database, MP4 exists in new system
	}

	// Check file system paths
	for _, path := range pathsToCheck {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	return false
}

// cleanupVideoHLS removes HLS files for a specific video
func (hcc *HLSCleanupCron) cleanupVideoHLS(video database.VideoMetadata) (filesDeleted, dirsDeleted int, spaceFreedMB int64, err error) {
	if video.HLSPath == "" {
		return 0, 0, 0, nil
	}

	// Check if HLS directory exists
	if _, err := os.Stat(video.HLSPath); os.IsNotExist(err) {
		log.Printf("HLS directory already removed for video %s: %s", video.ID, video.HLSPath)
		return 0, 0, 0, nil
	}

	// Calculate space usage before deletion
	spaceUsed, err := hcc.calculateDirectorySize(video.HLSPath)
	if err != nil {
		log.Printf("WARNING: Failed to calculate HLS directory size for %s: %v", video.ID, err)
		spaceUsed = 0
	}

	// Count files before deletion
	fileCount, err := hcc.countFilesInDirectory(video.HLSPath)
	if err != nil {
		log.Printf("WARNING: Failed to count files in HLS directory for %s: %v", video.ID, err)
		fileCount = 0
	}

	// Remove the entire HLS directory
	err = os.RemoveAll(video.HLSPath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to remove HLS directory %s: %v", video.HLSPath, err)
	}

	log.Printf("Cleaned up HLS for video %s: removed %d files, freed %d MB", 
		video.ID, fileCount, spaceUsed/(1024*1024))

	return fileCount, 1, spaceUsed / (1024 * 1024), nil
}

// calculateDirectorySize calculates the total size of files in a directory
func (hcc *HLSCleanupCron) calculateDirectorySize(dirPath string) (int64, error) {
	var size int64

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})

	return size, err
}

// countFilesInDirectory counts the number of files in a directory
func (hcc *HLSCleanupCron) countFilesInDirectory(dirPath string) (int, error) {
	var count int

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})

	return count, err
}

// markVideoHLSDeprecated marks a video as having deprecated HLS in the database
func (hcc *HLSCleanupCron) markVideoHLSDeprecated(videoID string) error {
	// Get current video data
	video, err := hcc.db.GetVideo(videoID)
	if err != nil {
		return err
	}

	if video == nil {
		return fmt.Errorf("video not found: %s", videoID)
	}

	// Update the deprecated_hls flag
	video.DeprecatedHLS = true

	// Update the video in database
	return hcc.db.UpdateVideo(*video)
}

// RunManualCleanup triggers a manual HLS cleanup (useful for testing or on-demand cleanup)
func (hcc *HLSCleanupCron) RunManualCleanup() error {
	log.Println("Running manual HLS cleanup...")
	hcc.runHLSCleanup()
	log.Println("Manual HLS cleanup completed")
	return nil
}

// CleanupSpecificVideo cleans up HLS files for a specific video ID
func (hcc *HLSCleanupCron) CleanupSpecificVideo(videoID string) error {
	video, err := hcc.db.GetVideo(videoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %v", err)
	}

	if video == nil {
		return fmt.Errorf("video not found: %s", videoID)
	}

	if video.HLSPath == "" {
		return fmt.Errorf("video has no HLS path: %s", videoID)
	}

	if !hcc.verifyMP4Exists(*video) {
		return fmt.Errorf("MP4 file not found for video %s, cannot cleanup HLS safely", videoID)
	}

	filesDeleted, dirsDeleted, spaceFreed, err := hcc.cleanupVideoHLS(*video)
	if err != nil {
		return fmt.Errorf("cleanup failed: %v", err)
	}

	err = hcc.markVideoHLSDeprecated(videoID)
	if err != nil {
		log.Printf("WARNING: Failed to mark video as HLS deprecated: %v", err)
	}

	log.Printf("Manual cleanup completed for video %s: %d files deleted, %d dirs deleted, %d MB freed",
		videoID, filesDeleted, dirsDeleted, spaceFreed)

	return nil
}

// IsRunning returns whether the cron job is currently running
func (hcc *HLSCleanupCron) IsRunning() bool {
	return hcc.running
}

// GetCleanupStats returns statistics about HLS cleanup
func (hcc *HLSCleanupCron) GetCleanupStats() (map[string]interface{}, error) {
	// Get count of videos with HLS vs deprecated HLS
	allVideos, err := hcc.db.ListVideos(10000, 0)
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"total_videos":           len(allVideos),
		"videos_with_hls":       0,
		"videos_hls_deprecated": 0,
		"videos_no_hls":         0,
	}

	for _, video := range allVideos {
		if video.HLSPath == "" {
			stats["videos_no_hls"] = stats["videos_no_hls"].(int) + 1
		} else if video.DeprecatedHLS {
			stats["videos_hls_deprecated"] = stats["videos_hls_deprecated"].(int) + 1
		} else {
			stats["videos_with_hls"] = stats["videos_with_hls"].(int) + 1
		}
	}

	return stats, nil
}