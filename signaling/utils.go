package signaling

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FindClosestVideo finds the video file closest to the given timestamp for a camera
func FindClosestVideo(storagePath string, cameraName string, targetTime time.Time) (string, error) {
	// Convert relative path to absolute if needed
	absStoragePath, err := filepath.Abs(storagePath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	// Find the video file closest to the timestamp
	videoDir := filepath.Join(absStoragePath, "recordings", cameraName)
	targetDate := targetTime.Format("20060102")

	// Ensure the directory exists
	if _, err := os.Stat(videoDir); os.IsNotExist(err) {
		return "", fmt.Errorf("video directory does not exist: %s", videoDir)
	}

	log.Printf("Looking for videos in %s matching date %s", videoDir, targetDate)

	// Look for videos within a 5-minute window
	pattern := fmt.Sprintf("%s_%s_*.mp4", cameraName, targetDate)
	files, err := filepath.Glob(filepath.Join(videoDir, pattern))
	if err != nil {
		return "", fmt.Errorf("failed to find videos: %v", err)
	}

	log.Printf("Found %d potential video files", len(files))

	var closestFile string
	var minDiff int64 = 1<<63 - 1

	for _, file := range files {
		// Extract timestamp from filename (format: camera_1_20250320_115851.mp4)
		base := filepath.Base(file)
		parts := strings.Split(base, "_")
		if len(parts) != 4 {
			log.Printf("Warning: Invalid filename format: %s (expected 4 parts)", base)
			continue
		}
		timeStr := strings.TrimSuffix(parts[3], ".mp4")
		fileTime, err := time.ParseInLocation("150405", timeStr, time.Local)
		if err != nil {
			log.Printf("Warning: Could not parse time from filename %s: %v", base, err)
			continue
		}

		// Adjust fileTime to match the target date
		fileTime = time.Date(
			targetTime.Year(), targetTime.Month(), targetTime.Day(),
			fileTime.Hour(), fileTime.Minute(), fileTime.Second(),
			0, targetTime.Location(),
		)

		// Calculate time difference
		diff := abs(fileTime.Unix() - targetTime.Unix())
		if diff < minDiff {
			minDiff = diff
			closestFile = file
		}
	}

	if closestFile == "" {
		return "", fmt.Errorf("no video files found for camera %s at timestamp %v", cameraName, targetTime)
	}

	log.Printf("Found closest video file: %s", closestFile)
	return closestFile, nil
}

// abs returns the absolute value of x
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
