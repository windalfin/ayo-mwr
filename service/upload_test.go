package service

import (
	"os"
	"path/filepath"
	"testing"

	"ayo-mwr/database"
)

// Simple test for queue operations
func TestQueueOperations(t *testing.T) {
	// Create a minimal upload service that we can test
	service := &UploadService{
		uploadQueue: make([]QueuedVideo, 0),
		maxRetries:  5,
	}

	// Test adding a video to the queue
	service.QueueVideo("test1")

	// Check if the video is in the queue
	service.queueMutex.Lock()
	if len(service.uploadQueue) != 1 || service.uploadQueue[0].VideoID != "test1" {
		t.Errorf("Expected queue to contain test1, got %v", service.uploadQueue)
	}
	service.queueMutex.Unlock()

	// Test adding the same video again (should not duplicate)
	service.QueueVideo("test1")

	service.queueMutex.Lock()
	if len(service.uploadQueue) != 1 {
		t.Errorf("Expected queue to still have 1 item, got %d", len(service.uploadQueue))
	}
	service.queueMutex.Unlock()

	// Test adding a second video
	service.QueueVideo("test2")

	service.queueMutex.Lock()
	if len(service.uploadQueue) != 2 {
		t.Errorf("Expected queue to have 2 items, got %d", len(service.uploadQueue))
	}
	service.queueMutex.Unlock()

	// Test removing a video
	service.removeFromQueue("test1")

	service.queueMutex.Lock()
	if len(service.uploadQueue) != 1 || service.uploadQueue[0].VideoID != "test2" {
		t.Errorf("Expected queue to contain only test2, got %v", service.uploadQueue)
	}
	service.queueMutex.Unlock()

	// Test updating a video
	service.updateQueuedVideo("test2", "test error")

	service.queueMutex.Lock()
	if service.uploadQueue[0].RetryCount != 1 {
		t.Errorf("Expected retry count to be 1, got %d", service.uploadQueue[0].RetryCount)
	}
	if service.uploadQueue[0].FailReason != "test error" {
		t.Errorf("Expected fail reason to be 'test error', got '%s'", service.uploadQueue[0].FailReason)
	}
	service.queueMutex.Unlock()
}

// Test removeLocalFiles function
func TestRemoveLocalFiles(t *testing.T) {
	// Create temporary test files
	tempDir := t.TempDir()

	// Create a test video file
	videoPath := filepath.Join(tempDir, "test.mp4")
	err := os.WriteFile(videoPath, []byte("test video data"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test video file: %v", err)
	}

	// Create HLS directory
	hlsPath := filepath.Join(tempDir, "hls")
	err = os.MkdirAll(hlsPath, 0755)
	if err != nil {
		t.Fatalf("Failed to create HLS directory: %v", err)
	}

	// Create a test HLS file
	hlsFile := filepath.Join(hlsPath, "playlist.m3u8")
	err = os.WriteFile(hlsFile, []byte("test HLS data"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test HLS file: %v", err)
	}

	// Create DASH directory
	dashPath := filepath.Join(tempDir, "dash")
	err = os.MkdirAll(dashPath, 0755)
	if err != nil {
		t.Fatalf("Failed to create DASH directory: %v", err)
	}

	// Create a test DASH file
	dashFile := filepath.Join(dashPath, "manifest.mpd")
	err = os.WriteFile(dashFile, []byte("test DASH data"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test DASH file: %v", err)
	}

	// Create test video metadata
	video := database.VideoMetadata{
		ID:        "test",
		LocalPath: videoPath,
		HLSPath:   hlsPath,
		MP4Path:   filepath.Join(tempDir, "test.mp4"),
	}

	// Test removing local files
	removeLocalFiles(video)

	// Check if files were removed
	_, err = os.Stat(videoPath)
	if !os.IsNotExist(err) {
		t.Errorf("Expected video file to be removed, but it still exists")
	}

	_, err = os.Stat(hlsPath)
	if !os.IsNotExist(err) {
		t.Errorf("Expected HLS directory to be removed, but it still exists")
	}

	// DASH directory removal is no longer part of removeLocalFiles function
	// _, err = os.Stat(dashPath)
	// if !os.IsNotExist(err) {
	// 	t.Errorf("Expected DASH directory to be removed, but it still exists")
	// }
}

// TestWithRealVideo tests with an existing video file if available
func TestWithRealVideo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test that requires real video file")
	}

	// Look for a real video file from the videos folder
	// Try a few different paths to accommodate different working directories
	possiblePaths := []string{
		"test/videos/uploads/camera_A_20250304_120503.mp4",
		"videos/uploads/camera_A_20250304_120503.mp4",
		"./videos/uploads/camera_A_20250304_120503.mp4",
		"../videos/uploads/camera_A_20250304_120503.mp4",
		"d:/Projects/ayo-mwr/videos/uploads/camera_A_20250304_120503.mp4",
	}

	var videoPath string
	var pathExists bool

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			videoPath = path
			pathExists = true
			break
		}
	}

	if !pathExists {
		t.Skip("Could not find test video file in any of the expected locations")
		return
	}

	t.Logf("Found test video file: %s", videoPath)

	// Extract video ID
	videoID := filepath.Base(videoPath)
	videoID = videoID[:len(videoID)-len(filepath.Ext(videoID))]

	// Try different paths for HLS and DASH directories too
	possibleHLSRoots := []string{
		"videos/hls",
		"./videos/hls",
		"../videos/hls",
		"d:/Projects/ayo-mwr/videos/hls",
	}

	possibleDASHRoots := []string{
		"videos/dash",
		"./videos/dash",
		"../videos/dash",
		"d:/Projects/ayo-mwr/videos/dash",
	}

	var hlsPath, dashPath string
	var hlsExists, dashExists bool

	for _, root := range possibleHLSRoots {
		path := filepath.Join(root, videoID)
		if _, err := os.Stat(path); err == nil {
			hlsPath = path
			hlsExists = true
			break
		}
	}

	for _, root := range possibleDASHRoots {
		path := filepath.Join(root, videoID)
		if _, err := os.Stat(path); err == nil {
			dashPath = path
			dashExists = true
			break
		}
	}

	t.Logf("Video ID: %s", videoID)
	t.Logf("HLS path exists: %v (%s)", hlsExists, hlsPath)
	t.Logf("DASH path exists: %v (%s)", dashExists, dashPath)

	if !hlsExists || !dashExists {
		t.Logf("Working directory: %s", getWorkingDir())
		t.Skip("Could not locate HLS or DASH directories")
		return
	}

	// If we get here, we've verified that:
	// 1. The video file exists
	// 2. The HLS directory exists
	// 3. The DASH directory exists

	// We've verified the files exist which is a good test on its own
	t.Logf("Successfully verified files for video: %s", videoID)
	t.Logf("Video: %s", videoPath)
	t.Logf("HLS: %s", hlsPath)
	t.Logf("DASH: %s", dashPath)

	// Now we create a minimal service just to test the queue
	service := &UploadService{
		uploadQueue: make([]QueuedVideo, 0),
		maxRetries:  5,
	}

	// Test that we can queue the video
	service.QueueVideo(videoID)

	// Verify it's in the queue
	service.queueMutex.Lock()
	found := false
	for _, v := range service.uploadQueue {
		if v.VideoID == videoID {
			found = true
			break
		}
	}
	service.queueMutex.Unlock()

	if !found {
		t.Errorf("Video should have been added to the upload queue")
	} else {
		t.Logf("Successfully added video to queue")
	}
}

// Helper function to get working directory
func getWorkingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return dir
}

// TestInternetConnectivity checks the internet connectivity function
func TestInternetConnectivity(t *testing.T) {
	// Create a minimal upload service that we can test
	service := &UploadService{}

	// Just test that the function runs without errors
	result := service.checkInternetConnectivity()
	t.Logf("Internet connectivity check result: %v", result)
}

// TestIntegrationPlaceholder is a placeholder for a full integration test
// You'll need to customize this based on your specific implementation
func TestIntegrationPlaceholder(t *testing.T) {
	t.Skip("This is a placeholder for a future integration test. " +
		"To use it, you'll need to customize it with your specific database and storage constructors.")

	// Example of what the integration test might look like once customized:
	/*
		if testing.Short() {
			t.Skip("Skipping integration test in short mode")
		}

		// Create a temporary database
		dbPath := filepath.Join(t.TempDir(), "test.db")
		db, err := database.YourDatabaseConstructor(dbPath)
		if err != nil {
			t.Fatalf("Could not create database: %v", err)
		}
		defer db.Close()

		// Create a test R2Storage
		cfg := config.Config{
			StoragePath: t.TempDir(),
			// Add other required config fields
		}

		r2Storage, err := storage.YourR2StorageConstructor(cfg)
		if err != nil {
			t.Fatalf("Could not create R2Storage: %v", err)
		}

		// Create the service
		service := NewUploadService(db, r2Storage, cfg)

		// Test basic operations
		service.QueueVideo("test_integration")

		// Verify queue state
		service.queueMutex.Lock()
		queueSize := len(service.uploadQueue)
		service.queueMutex.Unlock()

		if queueSize != 1 {
			t.Errorf("Expected queue size to be 1, got %d", queueSize)
		}
	*/
}
