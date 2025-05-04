package database

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestSQLiteDB tests SQLite database operations
func TestSQLiteDB(t *testing.T) {
	// Create temporary directory for test database
	tempDir, err := os.MkdirTemp("", "rtsp-recorder-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := NewSQLiteDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create SQLite database: %v", err)
	}
	defer db.Close()

	// Test CreateVideo
	testCreateAndGetVideo(t, db)

	// Test ListVideos
	testListVideos(t, db)

	// Test GetVideosByStatus
	testGetVideosByStatus(t, db)

	// Test UpdateVideoStatus
	testUpdateVideoStatus(t, db)

	// Test UpdateVideoR2Paths
	testUpdateVideoR2Paths(t, db)

	// Test UpdateVideoR2URLs
	testUpdateVideoR2URLs(t, db)

	// Test DeleteVideo
	testDeleteVideo(t, db)
}

// testCreateAndGetVideo tests creating and retrieving a video
func testCreateAndGetVideo(t *testing.T, db *SQLiteDB) {
	// Create test metadata
	now := time.Now()
	metadata := VideoMetadata{
		ID:         "test-video-1",
		CreatedAt:  now,
		Status:     StatusProcessing,
		Duration:   0,
		Size:       1024,
		LocalPath:  "/path/to/video.mp4",
		HLSPath:    "/path/to/hls",
		DASHPath:   "/path/to/dash",
		HLSURL:     "http://example.com/hls/test",
		DASHURL:    "http://example.com/dash/test",
		CameraName:   "camera-1",
		R2HLSPath:  "",
		R2DASHPath: "",
		R2HLSURL:   "",
		R2DASHURL:  "",
	}

	// Insert metadata
	err := db.CreateVideo(metadata)
	if err != nil {
		t.Fatalf("Failed to create video: %v", err)
	}

	// Retrieve metadata
	retrieved, err := db.GetVideo("test-video-1")
	if err != nil {
		t.Fatalf("Failed to get video: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected to retrieve video, got nil")
	}

	// Verify retrieved metadata
	if retrieved.ID != metadata.ID {
		t.Errorf("Expected ID %s, got %s", metadata.ID, retrieved.ID)
	}
	if retrieved.Status != metadata.Status {
		t.Errorf("Expected status %s, got %s", metadata.Status, retrieved.Status)
	}
	if retrieved.CameraName != metadata.CameraName {
		t.Errorf("Expected camera name %s, got %s", metadata.CameraName, retrieved.CameraName)
	}
	if retrieved.LocalPath != metadata.LocalPath {
		t.Errorf("Expected local path %s, got %s", metadata.LocalPath, retrieved.LocalPath)
	}

	// Test getting non-existent video
	nonExistent, err := db.GetVideo("non-existent")
	if err != nil {
		t.Fatalf("Expected no error for non-existent video, got: %v", err)
	}
	if nonExistent != nil {
		t.Errorf("Expected nil for non-existent video, got: %v", nonExistent)
	}

	// Test updating video
	metadata.Status = StatusReady
	finished := time.Now()
	metadata.FinishedAt = &finished
	metadata.Duration = 30.5
	metadata.HLSURL = "http://example.com/hls/test-updated"

	err = db.UpdateVideo(metadata)
	if err != nil {
		t.Fatalf("Failed to update video: %v", err)
	}

	// Verify update
	updated, err := db.GetVideo("test-video-1")
	if err != nil {
		t.Fatalf("Failed to get updated video: %v", err)
	}
	if updated.Status != StatusReady {
		t.Errorf("Expected updated status %s, got %s", StatusReady, updated.Status)
	}
	if updated.Duration != 30.5 {
		t.Errorf("Expected updated duration %f, got %f", 30.5, updated.Duration)
	}
	if updated.HLSURL != "http://example.com/hls/test-updated" {
		t.Errorf("Expected updated HLS URL %s, got %s", "http://example.com/hls/test-updated", updated.HLSURL)
	}
	if updated.FinishedAt == nil {
		t.Error("Expected FinishedAt to be set, got nil")
	}
}

// testListVideos tests listing videos with pagination
func testListVideos(t *testing.T, db *SQLiteDB) {
	// Create additional test videos
	for i := 2; i <= 5; i++ {
		metadata := VideoMetadata{
			ID:        "test-video-" + strconv.Itoa(i),
			CreatedAt: time.Now().Add(time.Duration(-i) * time.Hour), // Older videos
			Status:    StatusReady,
			Duration:  float64(i * 10),
			Size:      int64(i * 1024),
			LocalPath: "/path/to/video" + strconv.Itoa(i+'0') + ".mp4",
			HLSPath:   "/path/to/hls" + strconv.Itoa(i+'0'),
			DASHPath:  "/path/to/dash" + strconv.Itoa(i+'0'),
			HLSURL:    "http://example.com/hls/test" + strconv.Itoa(i+'0'),
			DASHURL:   "http://example.com/dash/test" + strconv.Itoa(i+'0'),
			CameraName:  "camera-1",
		}

		err := db.CreateVideo(metadata)
		if err != nil {
			t.Fatalf("Failed to create additional video: %v", err)
		}
	}

	// Test listing with limit
	videos, err := db.ListVideos(3, 0)
	if err != nil {
		t.Fatalf("Failed to list videos: %v", err)
	}
	if len(videos) != 3 {
		t.Errorf("Expected 3 videos, got %d", len(videos))
	}

	// Test listing with offset
	moreVideos, err := db.ListVideos(3, 3)
	if err != nil {
		t.Fatalf("Failed to list videos with offset: %v", err)
	}

	totalVideos := len(videos) + len(moreVideos)
	if totalVideos < 5 { // We should have at least 5 videos in total
		t.Errorf("Expected at least 5 videos in total, got %d", totalVideos)
	}

	// Check for duplicates between the two queries
	idMap := make(map[string]bool)
	for _, v := range videos {
		idMap[v.ID] = true
	}
	for _, v := range moreVideos {
		if idMap[v.ID] {
			t.Errorf("Found duplicate video ID %s in paginated results", v.ID)
		}
	}
}

// testGetVideosByStatus tests retrieving videos by status
func testGetVideosByStatus(t *testing.T, db *SQLiteDB) {
	// Create videos with different statuses
	statuses := []VideoStatus{StatusProcessing, StatusUploading, StatusFailed}
	for i, status := range statuses {
		metadata := VideoMetadata{
			ID:        "status-test-" + strconv.Itoa(i+'0'),
			CreatedAt: time.Now(),
			Status:    status,
			CameraName:  "camera-1",
		}

		err := db.CreateVideo(metadata)
		if err != nil {
			t.Fatalf("Failed to create status test video: %v", err)
		}
	}

	// Test getting videos by each status
	for _, status := range statuses {
		videos, err := db.GetVideosByStatus(status, 10, 0)
		if err != nil {
			t.Fatalf("Failed to get videos by status %s: %v", status, err)
		}

		// Verify that all returned videos have the requested status
		for _, v := range videos {
			if v.Status != status {
				t.Errorf("Expected all videos to have status %s, found video with status %s", status, v.Status)
			}
		}

		// Check that we found at least one video with this status
		if len(videos) == 0 {
			t.Errorf("Expected to find at least one video with status %s", status)
		}
	}
}

// testUpdateVideoStatus tests updating video status
func testUpdateVideoStatus(t *testing.T, db *SQLiteDB) {
	// Create a test video
	metadata := VideoMetadata{
		ID:        "status-update-test",
		CreatedAt: time.Now(),
		Status:    StatusProcessing,
		CameraName:  "camera-1",
	}

	err := db.CreateVideo(metadata)
	if err != nil {
		t.Fatalf("Failed to create status update test video: %v", err)
	}

	// Update the status
	err = db.UpdateVideoStatus("status-update-test", StatusReady, "")
	if err != nil {
		t.Fatalf("Failed to update video status: %v", err)
	}

	// Verify the update
	video, err := db.GetVideo("status-update-test")
	if err != nil {
		t.Fatalf("Failed to get video after status update: %v", err)
	}
	if video.Status != StatusReady {
		t.Errorf("Expected status %s, got %s", StatusReady, video.Status)
	}

	// Test finished timestamp for completed status
	if video.FinishedAt == nil {
		t.Error("Expected FinishedAt to be set for completed status, got nil")
	}

	// Test updating with error message
	err = db.UpdateVideoStatus("status-update-test", StatusFailed, "Test error message")
	if err != nil {
		t.Fatalf("Failed to update video status with error message: %v", err)
	}

	// Verify error message
	video, err = db.GetVideo("status-update-test")
	if err != nil {
		t.Fatalf("Failed to get video after status update with error: %v", err)
	}
	if video.Status != StatusFailed {
		t.Errorf("Expected status %s, got %s", StatusFailed, video.Status)
	}
	if video.ErrorMessage != "Test error message" {
		t.Errorf("Expected error message 'Test error message', got '%s'", video.ErrorMessage)
	}
}

// testUpdateVideoR2Paths tests updating R2 paths
func testUpdateVideoR2Paths(t *testing.T, db *SQLiteDB) {
	// Create a test video
	metadata := VideoMetadata{
		ID:        "r2-path-test",
		CreatedAt: time.Now(),
		Status:    StatusReady,
		CameraName:  "camera-1",
	}

	err := db.CreateVideo(metadata)
	if err != nil {
		t.Fatalf("Failed to create R2 path test video: %v", err)
	}

	// Update R2 paths
	hlsPath := "hls/r2-path-test"
	dashPath := "dash/r2-path-test"
	err = db.UpdateVideoR2Paths("r2-path-test", hlsPath, dashPath)
	if err != nil {
		t.Fatalf("Failed to update R2 paths: %v", err)
	}

	// Verify the update
	video, err := db.GetVideo("r2-path-test")
	if err != nil {
		t.Fatalf("Failed to get video after R2 path update: %v", err)
	}
	if video.R2HLSPath != hlsPath {
		t.Errorf("Expected R2 HLS path %s, got %s", hlsPath, video.R2HLSPath)
	}
	if video.R2DASHPath != dashPath {
		t.Errorf("Expected R2 DASH path %s, got %s", dashPath, video.R2DASHPath)
	}
}

// testUpdateVideoR2URLs tests updating R2 URLs
func testUpdateVideoR2URLs(t *testing.T, db *SQLiteDB) {
	// Create a test video
	metadata := VideoMetadata{
		ID:        "r2-url-test",
		CreatedAt: time.Now(),
		Status:    StatusReady,
		CameraName:  "camera-1",
	}

	err := db.CreateVideo(metadata)
	if err != nil {
		t.Fatalf("Failed to create R2 URL test video: %v", err)
	}

	// Update R2 URLs
	hlsURL := "https://example.r2.dev/hls/r2-url-test/playlist.m3u8"
	dashURL := "https://example.r2.dev/dash/r2-url-test/manifest.mpd"
	err = db.UpdateVideoR2URLs("r2-url-test", hlsURL, dashURL)
	if err != nil {
		t.Fatalf("Failed to update R2 URLs: %v", err)
	}

	// Verify the update
	video, err := db.GetVideo("r2-url-test")
	if err != nil {
		t.Fatalf("Failed to get video after R2 URL update: %v", err)
	}
	if video.R2HLSURL != hlsURL {
		t.Errorf("Expected R2 HLS URL %s, got %s", hlsURL, video.R2HLSURL)
	}
	if video.R2DASHURL != dashURL {
		t.Errorf("Expected R2 DASH URL %s, got %s", dashURL, video.R2DASHURL)
	}
}

// testDeleteVideo tests deleting a video
func testDeleteVideo(t *testing.T, db *SQLiteDB) {
	// Create a test video
	metadata := VideoMetadata{
		ID:        "delete-test",
		CreatedAt: time.Now(),
		Status:    StatusReady,
		CameraName:  "camera-1",
	}

	err := db.CreateVideo(metadata)
	if err != nil {
		t.Fatalf("Failed to create delete test video: %v", err)
	}

	// Verify the video exists
	video, err := db.GetVideo("delete-test")
	if err != nil {
		t.Fatalf("Failed to get video before deletion: %v", err)
	}
	if video == nil {
		t.Fatal("Expected video to exist before deletion, got nil")
	}

	// Delete the video
	err = db.DeleteVideo("delete-test")
	if err != nil {
		t.Fatalf("Failed to delete video: %v", err)
	}

	// Verify the video no longer exists
	video, err = db.GetVideo("delete-test")
	if err != nil {
		t.Fatalf("Failed to get video after deletion: %v", err)
	}
	if video != nil {
		t.Errorf("Expected video to be deleted, but it still exists: %v", video)
	}
}
