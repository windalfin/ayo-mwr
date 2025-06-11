package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"ayo-mwr/database"
)

func main() {
	// Load database path from environment or use default
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "./data/videos.db"
	}

	// Initialize database connection
	db, err := database.NewSQLiteDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Get storage path from environment or use default
	storagePath := os.Getenv("STORAGE_PATH")
	if storagePath == "" {
		cwd, err := os.Getwd()
		if err == nil {
			storagePath = filepath.Join(cwd, "videos")
		} else {
			storagePath = "/videos"
		}
	}

	// Set up camera names
	cameraNames := []string{"test_camera_1", "test_camera_2"}
	
	// Create 5 test video entries with different dates
	// 1. 12 days ago (should be cleaned up)
	// 2. 8 days ago (should be cleaned up)
	// 3. 7 days ago (edge case - exactly at threshold)
	// 4. 5 days ago (should not be cleaned up)
	// 5. 2 days ago (should not be cleaned up)
	daysAgo := []int{12, 8, 7, 5, 2}

	for i, days := range daysAgo {
		// Alternate between cameras
		cameraName := cameraNames[i%len(cameraNames)]
		
		// Create a test video entry
		createdAt := time.Now().AddDate(0, 0, -days)
		dateStr := createdAt.Format("20060102")

		// Set up paths
		baseDir := storagePath
		cameraDir := filepath.Join(baseDir, "recordings", cameraName)
		hlsDir := filepath.Join(cameraDir, "hls")
		mp4Dir := filepath.Join(cameraDir, "mp4")

		// Create directories if they don't exist
		for _, dir := range []string{baseDir, cameraDir, hlsDir, mp4Dir} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Fatalf("Failed to create directory %s: %v", dir, err)
			}
		}

		// Create MP4 file path
		mp4FileName := fmt.Sprintf("%s_%s_test.mp4", cameraName, dateStr)
		mp4FilePath := filepath.Join(mp4Dir, mp4FileName)

		// Create empty MP4 file
		if _, err := os.Create(mp4FilePath); err != nil {
			log.Printf("Warning: Failed to create test MP4 file: %v", err)
		} else {
			log.Printf("Created test MP4 file: %s", mp4FilePath)
		}

		// Create empty HLS segment file
		hlsSegmentPath := filepath.Join(hlsDir, fmt.Sprintf("segment_%s_000.ts", dateStr))
		if _, err := os.Create(hlsSegmentPath); err != nil {
			log.Printf("Warning: Failed to create test HLS segment: %v", err)
		} else {
			log.Printf("Created test HLS segment: %s", hlsSegmentPath)
		}

		// Create a unique ID for the video with timestamp to ensure uniqueness
		timestamp := time.Now().Unix()
		uniqueId := fmt.Sprintf("test_video_%s_%d_%d", dateStr, i+1, timestamp)
		
		// Create video metadata
		videoMetadata := database.VideoMetadata{
			ID:         fmt.Sprintf("test_%s_%d_%d", dateStr, i+1, timestamp),
			CreatedAt:  createdAt,
			Status:     database.StatusReady, // Not unavailable, so it should be picked up by cleanup
			LocalPath:  mp4FilePath,
			HLSPath:    hlsDir,
			CameraName: cameraName,
			UniqueID:   uniqueId,
			Duration:   60.0 * float64(i+1), // Different durations
			Size:       1024 * 1024 * int64(i+1), // Different sizes
		}

		// Insert the video metadata into the database
		if err := db.CreateVideo(videoMetadata); err != nil {
			log.Fatalf("Failed to insert video metadata: %v", err)
		}

		log.Printf("Successfully inserted test video %d/%d: ID=%s, Camera=%s, Date=%s, Age=%d days", 
			i+1, len(daysAgo), videoMetadata.ID, cameraName, createdAt.Format("2006-01-02"), days)
	}

	log.Printf("Successfully inserted %d test videos", len(daysAgo))
}
