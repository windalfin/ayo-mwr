package recording

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ayo-mwr/database"
)

// MockDatabase implements database.Database interface for testing
type MockDatabase struct {
	roots []database.DayCameraRoot
}

func (m *MockDatabase) CreateDayCameraRoot(root database.DayCameraRoot) error {
	m.roots = append(m.roots, root)
	return nil
}

func (m *MockDatabase) GetDayCameraRoots(cameraName, date string) ([]database.DayCameraRoot, error) {
	var result []database.DayCameraRoot
	for _, root := range m.roots {
		if root.CameraName == cameraName && root.Date == date {
			result = append(result, root)
		}
	}
	return result, nil
}

func (m *MockDatabase) UpdateDayCameraRootLastSeen(cameraName, date, diskID string, lastSeenTime time.Time) error {
	for i := range m.roots {
		if m.roots[i].CameraName == cameraName && m.roots[i].Date == date && m.roots[i].DiskID == diskID {
			m.roots[i].LastSeenTime = lastSeenTime
			return nil
		}
	}
	return nil
}

func (m *MockDatabase) GetAllDayCameraRoots() ([]database.DayCameraRoot, error) {
	return m.roots, nil
}

// Add stub implementations for other required interface methods
func (m *MockDatabase) CreateVideo(metadata database.VideoMetadata) error { return nil }
func (m *MockDatabase) GetVideo(id string) (*database.VideoMetadata, error) { return nil, nil }
func (m *MockDatabase) UpdateVideo(metadata database.VideoMetadata) error { return nil }
func (m *MockDatabase) UpdateLocalPathVideo(metadata database.VideoMetadata) error { return nil }
func (m *MockDatabase) ListVideos(limit, offset int) ([]database.VideoMetadata, error) { return nil, nil }
func (m *MockDatabase) DeleteVideo(id string) error { return nil }
func (m *MockDatabase) GetVideosByStatus(status database.VideoStatus, limit, offset int) ([]database.VideoMetadata, error) { return nil, nil }
func (m *MockDatabase) UpdateVideoStatus(id string, status database.VideoStatus, errorMsg string) error { return nil }
func (m *MockDatabase) UpdateLastCheckFile(id string, lastCheckTime time.Time) error { return nil }
func (m *MockDatabase) CleanupStuckVideosOnStartup() error { return nil }
func (m *MockDatabase) GetVideosByBookingID(bookingID string) ([]database.VideoMetadata, error) { return nil, nil }
func (m *MockDatabase) GetVideoByUniqueID(uniqueID string) (*database.VideoMetadata, error) { return nil, nil }
func (m *MockDatabase) GetCameras() ([]database.CameraConfig, error) { return nil, nil }
func (m *MockDatabase) InsertCameras(cameras []database.CameraConfig) error { return nil }
func (m *MockDatabase) UpdateCameraConfig(cameraName string, frameRate int, autoDelete int) error { return nil }
func (m *MockDatabase) CreateStorageDisk(disk database.StorageDisk) error { return nil }
func (m *MockDatabase) GetStorageDisks() ([]database.StorageDisk, error) { return nil, nil }
func (m *MockDatabase) GetActiveDisk() (*database.StorageDisk, error) { return nil, nil }
func (m *MockDatabase) UpdateDiskSpace(id string, totalGB, availableGB int64) error { return nil }
func (m *MockDatabase) UpdateDiskPriority(id string, priority int) error { return nil }
func (m *MockDatabase) SetActiveDisk(id string) error { return nil }
func (m *MockDatabase) GetStorageDisk(id string) (*database.StorageDisk, error) { return nil, nil }
func (m *MockDatabase) CreateRecordingSegment(segment database.RecordingSegment) error { return nil }
func (m *MockDatabase) GetRecordingSegments(cameraName string, start, end time.Time) ([]database.RecordingSegment, error) { return nil, nil }
func (m *MockDatabase) DeleteRecordingSegment(id string) error { return nil }
func (m *MockDatabase) GetRecordingSegmentsByDisk(diskID string) ([]database.RecordingSegment, error) { return nil, nil }
func (m *MockDatabase) UpdateVideoR2Paths(id, hlsPath, mp4Path string) error { return nil }
func (m *MockDatabase) UpdateVideoR2URLs(id, hlsURL, mp4URL string) error { return nil }
func (m *MockDatabase) UpdateVideoRequestID(id, requestId string, remove bool) error { return nil }
func (m *MockDatabase) CreatePendingTask(task database.PendingTask) error { return nil }
func (m *MockDatabase) GetPendingTasks(limit int) ([]database.PendingTask, error) { return nil, nil }
func (m *MockDatabase) UpdateTaskStatus(taskID int, status string, errorMsg string) error { return nil }
func (m *MockDatabase) UpdateTaskNextRetry(taskID int, nextRetryAt time.Time, attempts int) error { return nil }
func (m *MockDatabase) DeleteCompletedTasks(olderThan time.Time) error { return nil }
func (m *MockDatabase) GetTaskByID(taskID int) (*database.PendingTask, error) { return nil, nil }
func (m *MockDatabase) CreateOrUpdateBooking(booking database.BookingData) error { return nil }
func (m *MockDatabase) GetBookingByID(bookingID string) (*database.BookingData, error) { return nil, nil }
func (m *MockDatabase) GetBookingsByDate(date string) ([]database.BookingData, error) { return nil, nil }
func (m *MockDatabase) GetBookingsByStatus(status string) ([]database.BookingData, error) { return nil, nil }
func (m *MockDatabase) UpdateBookingStatus(bookingID string, status string) error { return nil }
func (m *MockDatabase) DeleteOldBookings(olderThan time.Time) error { return nil }
func (m *MockDatabase) GetSystemConfig(key string) (*database.SystemConfig, error) { return nil, nil }
func (m *MockDatabase) SetSystemConfig(config database.SystemConfig) error { return nil }
func (m *MockDatabase) GetAllSystemConfigs() ([]database.SystemConfig, error) { return nil, nil }
func (m *MockDatabase) DeleteSystemConfig(key string) error { return nil }
func (m *MockDatabase) CreateUser(username, passwordHash string) error { return nil }
func (m *MockDatabase) GetUserByUsername(username string) (*database.User, error) { return nil, nil }
func (m *MockDatabase) HasUsers() (bool, error) { return false, nil }
func (m *MockDatabase) DeleteOldDayCameraRoots(olderThan time.Time) error { return nil }
func (m *MockDatabase) Close() error { return nil }

func TestFindSegmentsInRangeMultiRoot(t *testing.T) {
	// Create temporary test directory structure
	tmpDir, err := os.MkdirTemp("", "multi_root_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create mock database with multiple roots for the same camera/date
	mockDB := &MockDatabase{}
	
	// Create test directory structure for multiple roots
	date := "20240108"
	camera := "test_camera"
	
	// Root 1 - Disk A
	root1Path := filepath.Join(tmpDir, "diskA", "recordings", date, camera, "hls")
	err = os.MkdirAll(root1Path, 0755)
	if err != nil {
		t.Fatalf("Failed to create root1 directory: %v", err)
	}
	
	// Root 2 - Disk B
	root2Path := filepath.Join(tmpDir, "diskB", "recordings", date, camera, "hls")
	err = os.MkdirAll(root2Path, 0755)
	if err != nil {
		t.Fatalf("Failed to create root2 directory: %v", err)
	}
	
	// Add roots to mock database
	root1 := database.DayCameraRoot{
		CameraName:   camera,
		Date:         date,
		DiskID:       "diskA",
		BasePath:     root1Path,
		LastSeenTime: time.Now(),
		CreatedAt:    time.Now().Add(-2 * time.Hour),
	}
	root2 := database.DayCameraRoot{
		CameraName:   camera,
		Date:         date,
		DiskID:       "diskB",
		BasePath:     root2Path,
		LastSeenTime: time.Now(),
		CreatedAt:    time.Now().Add(-1 * time.Hour),
	}
	
	mockDB.CreateDayCameraRoot(root1)
	mockDB.CreateDayCameraRoot(root2)
	
	// Create test segment files with different timestamps
	// Segments in root1 (older disk)
	segment1Path := filepath.Join(root1Path, "segment_20240108_100000.ts")
	segment2Path := filepath.Join(root1Path, "segment_20240108_100004.ts")
	
	// Segments in root2 (newer disk, after disk switch)
	segment3Path := filepath.Join(root2Path, "segment_20240108_100008.ts")
	segment4Path := filepath.Join(root2Path, "segment_20240108_100012.ts")
	
	// Create the segment files
	for _, segPath := range []string{segment1Path, segment2Path, segment3Path, segment4Path} {
		file, err := os.Create(segPath)
		if err != nil {
			t.Fatalf("Failed to create segment file %s: %v", segPath, err)
		}
		file.Close()
	}
	
	// Test multi-root segment lookup
	startTime, _ := time.Parse("20060102_150405", "20240108_095900")
	endTime, _ := time.Parse("20060102_150405", "20240108_100100")
	
	segments, err := FindSegmentsInRangeMultiRoot(mockDB, camera, startTime, endTime)
	if err != nil {
		t.Fatalf("FindSegmentsInRangeMultiRoot failed: %v", err)
	}
	
	// Should find segments from both roots
	if len(segments) != 4 {
		t.Errorf("Expected 4 segments, got %d", len(segments))
	}
	
	// Verify segments are from both disks
	foundDiskA := false
	foundDiskB := false
	for _, segment := range segments {
		if filepath.Dir(segment) == root1Path {
			foundDiskA = true
		}
		if filepath.Dir(segment) == root2Path {
			foundDiskB = true
		}
	}
	
	if !foundDiskA {
		t.Error("No segments found from diskA")
	}
	if !foundDiskB {
		t.Error("No segments found from diskB")
	}
	
	t.Logf("✅ Multi-root test passed: Found %d segments across 2 roots", len(segments))
}

func TestGetMultiRootStats(t *testing.T) {
	mockDB := &MockDatabase{}
	
	// Add test roots with different scenarios
	now := time.Now()
	testRoots := []database.DayCameraRoot{
		{
			CameraName:   "camera1",
			Date:         "20240108",
			DiskID:       "diskA",
			BasePath:     "/diskA/recordings/20240108/camera1/hls",
			LastSeenTime: now,
			CreatedAt:    now.Add(-3 * time.Hour),
		},
		{
			CameraName:   "camera1",
			Date:         "20240108",
			DiskID:       "diskB",
			BasePath:     "/diskB/recordings/20240108/camera1/hls",
			LastSeenTime: now,
			CreatedAt:    now.Add(-1 * time.Hour), // Disk switch event
		},
		{
			CameraName:   "camera2",
			Date:         "20240108",
			DiskID:       "diskA",
			BasePath:     "/diskA/recordings/20240108/camera2/hls",
			LastSeenTime: now,
			CreatedAt:    now.Add(-2 * time.Hour),
		},
	}
	
	for _, root := range testRoots {
		mockDB.CreateDayCameraRoot(root)
	}
	
	stats, err := GetMultiRootStats(mockDB)
	if err != nil {
		t.Fatalf("GetMultiRootStats failed: %v", err)
	}
	
	// Verify basic statistics
	if stats["total_roots"] != 3 {
		t.Errorf("Expected 3 total roots, got %v", stats["total_roots"])
	}
	
	if stats["unique_cameras"] != 2 {
		t.Errorf("Expected 2 unique cameras, got %v", stats["unique_cameras"])
	}
	
	if stats["unique_disks"] != 2 {
		t.Errorf("Expected 2 unique disks, got %v", stats["unique_disks"])
	}
	
	// Verify disk switch detection
	if stats["disk_switch_events"] != 1 {
		t.Errorf("Expected 1 disk switch event, got %v", stats["disk_switch_events"])
	}
	
	// Verify multi-root dates detection
	multiRootDates := stats["multi_root_dates"].([]map[string]interface{})
	if len(multiRootDates) != 1 {
		t.Errorf("Expected 1 multi-root date scenario, got %d", len(multiRootDates))
	}
	
	if len(multiRootDates) > 0 {
		event := multiRootDates[0]
		if event["camera"] != "camera1" {
			t.Errorf("Expected multi-root event for camera1, got %v", event["camera"])
		}
		if event["root_count"] != 2 {
			t.Errorf("Expected 2 roots for multi-root event, got %v", event["root_count"])
		}
	}
	
	t.Log("✅ Multi-root statistics test passed")
}