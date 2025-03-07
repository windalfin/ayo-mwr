package storage

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

// TestNewR2Storage tests the creation of a new R2Storage instance
func TestNewR2Storage(t *testing.T) {
	// Test with full config
	config := R2Config{
		AccessKey: "test-access-key",
		SecretKey: "test-secret-key",
		AccountID: "test-account-id",
		Bucket:    "test-bucket",
		Region:    "auto",
	}

	r2, err := NewR2Storage(config)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if r2 == nil {
		t.Fatal("Expected R2Storage instance, got nil")
	}
	if r2.config.Endpoint != "https://test-account-id.r2.cloudflarestorage.com" {
		t.Errorf("Expected endpoint to be set, got: %s", r2.config.Endpoint)
	}

	// Test with custom endpoint
	config.Endpoint = "https://custom.endpoint.com"
	r2, err = NewR2Storage(config)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if r2.config.Endpoint != "https://custom.endpoint.com" {
		t.Errorf("Expected custom endpoint, got: %s", r2.config.Endpoint)
	}

	// Test with missing required fields
	badConfig := R2Config{
		// Missing credentials
		Bucket: "test-bucket",
	}
	_, err = NewR2Storage(badConfig)
	// Should not error as AWS SDK validates credentials when used, not when created
	if err != nil {
		t.Errorf("Expected no error for empty credentials (AWS SDK handles this), got: %v", err)
	}
}

// setupTestFiles creates temporary test files and directories
func setupTestFiles(t *testing.T) (string, func()) {
	// Create temp directory
	tempDir, err := ioutil.TempDir("", "r2-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Create a test MP4 file
	mp4Path := filepath.Join(tempDir, "test.mp4")
	err = ioutil.WriteFile(mp4Path, []byte("fake mp4 content"), 0644)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create test MP4 file: %v", err)
	}

	// Create a test HLS directory structure
	hlsDir := filepath.Join(tempDir, "hls", "test-video")
	err = os.MkdirAll(hlsDir, 0755)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create HLS directory: %v", err)
	}

	// Create playlist.m3u8
	playlistPath := filepath.Join(hlsDir, "playlist.m3u8")
	playlistContent := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:4.000000,
segment_000.ts
#EXTINF:4.000000,
segment_001.ts
#EXT-X-ENDLIST`
	err = ioutil.WriteFile(playlistPath, []byte(playlistContent), 0644)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create playlist file: %v", err)
	}

	// Create segment files
	for i := 0; i < 2; i++ {
		segmentPath := filepath.Join(hlsDir, "segment_%03d.ts")
		segmentPath = filepath.Join(hlsDir, filepath.Base(fmt.Sprintf(segmentPath, i)))
		err = ioutil.WriteFile(segmentPath, []byte("fake ts content"), 0644)
		if err != nil {
			os.RemoveAll(tempDir)
			t.Fatalf("Failed to create segment file: %v", err)
		}
	}

	// Create a test DASH directory structure
	dashDir := filepath.Join(tempDir, "dash", "test-video")
	err = os.MkdirAll(dashDir, 0755)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create DASH directory: %v", err)
	}

	// Create manifest.mpd
	manifestPath := filepath.Join(dashDir, "manifest.mpd")
	manifestContent := `<?xml version="1.0" encoding="utf-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011">
  <!-- Sample MPD content -->
</MPD>`
	err = ioutil.WriteFile(manifestPath, []byte(manifestContent), 0644)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create manifest file: %v", err)
	}

	// Create segment files
	for i := 0; i < 2; i++ {
		initSegPath := filepath.Join(dashDir, "init-stream1.m4s")
		err = ioutil.WriteFile(initSegPath, []byte("fake init segment content"), 0644)
		if err != nil {
			os.RemoveAll(tempDir)
			t.Fatalf("Failed to create init segment file: %v", err)
		}

		segmentPath := filepath.Join(dashDir, "chunk-stream1-%d.m4s")
		segmentPath = filepath.Join(dashDir, filepath.Base(fmt.Sprintf(segmentPath, i+1)))
		err = ioutil.WriteFile(segmentPath, []byte("fake dash segment content"), 0644)
		if err != nil {
			os.RemoveAll(tempDir)
			t.Fatalf("Failed to create dash segment file: %v", err)
		}
	}

	// Return the temp directory and a cleanup function
	return tempDir, func() {
		os.RemoveAll(tempDir)
	}
}

// mockR2Storage creates a mock R2Storage for testing
// This mock implementation allows testing without actual AWS connectivity
type mockR2Storage struct {
	uploadedFiles map[string][]byte
	config        R2Config
}

func newMockR2Storage(config R2Config) *mockR2Storage {
	return &mockR2Storage{
		uploadedFiles: make(map[string][]byte),
		config:        config,
	}
}

func (m *mockR2Storage) UploadFile(localPath, remotePath string) (string, error) {
	data, err := ioutil.ReadFile(localPath)
	if err != nil {
		return "", err
	}
	m.uploadedFiles[remotePath] = data
	return m.config.Endpoint + "/" + remotePath, nil
}

func (m *mockR2Storage) UploadDirectory(localDir, remotePrefix string) ([]string, error) {
	var uploadedLocations []string

	err := filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}

		remotePath := filepath.Join(remotePrefix, relPath)
		remotePath = filepath.ToSlash(remotePath)

		location, err := m.UploadFile(path, remotePath)
		if err != nil {
			return err
		}

		uploadedLocations = append(uploadedLocations, location)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return uploadedLocations, nil
}

func (m *mockR2Storage) UploadHLSStream(hlsDir, videoID string) (string, error) {
	remotePrefix := "hls/" + videoID
	_, err := m.UploadDirectory(hlsDir, remotePrefix)
	if err != nil {
		return "", err
	}
	return m.config.Endpoint + "/" + remotePrefix + "/playlist.m3u8", nil
}

func (m *mockR2Storage) UploadDASHStream(dashDir, videoID string) (string, error) {
	remotePrefix := "dash/" + videoID
	_, err := m.UploadDirectory(dashDir, remotePrefix)
	if err != nil {
		return "", err
	}
	return m.config.Endpoint + "/" + remotePrefix + "/manifest.mpd", nil
}

// TestUploadHLSAndDASH tests the upload of HLS and DASH streams
func TestUploadHLSAndDASH(t *testing.T) {
	// Set up test files
	tempDir, cleanup := setupTestFiles(t)
	defer cleanup()

	// Create mock R2 storage
	config := R2Config{
		AccessKey: "test-access-key",
		SecretKey: "test-secret-key",
		AccountID: "test-account-id",
		Bucket:    "test-bucket",
		Endpoint:  "https://test.cloudflare.com",
		Region:    "auto",
	}
	mock := newMockR2Storage(config)

	// Test HLS upload
	hlsDir := filepath.Join(tempDir, "hls", "test-video")
	hlsURL, err := mock.UploadHLSStream(hlsDir, "test-video")
	if err != nil {
		t.Fatalf("Failed to upload HLS stream: %v", err)
	}

	expectedHLSURL := "https://test.cloudflare.com/hls/test-video/playlist.m3u8"
	if hlsURL != expectedHLSURL {
		t.Errorf("Expected HLS URL %s, got %s", expectedHLSURL, hlsURL)
	}

	// Check that HLS files were uploaded
	if _, exists := mock.uploadedFiles["hls/test-video/playlist.m3u8"]; !exists {
		t.Error("Expected playlist.m3u8 to be uploaded, but it wasn't")
	}
	for i := 0; i < 2; i++ {
		segmentKey := filepath.ToSlash(filepath.Join("hls/test-video", fmt.Sprintf("segment_%03d.ts", i)))
		if _, exists := mock.uploadedFiles[segmentKey]; !exists {
			t.Errorf("Expected %s to be uploaded, but it wasn't", segmentKey)
		}
	}

	// Test DASH upload
	dashDir := filepath.Join(tempDir, "dash", "test-video")
	dashURL, err := mock.UploadDASHStream(dashDir, "test-video")
	if err != nil {
		t.Fatalf("Failed to upload DASH stream: %v", err)
	}

	expectedDASHURL := "https://test.cloudflare.com/dash/test-video/manifest.mpd"
	if dashURL != expectedDASHURL {
		t.Errorf("Expected DASH URL %s, got %s", expectedDASHURL, dashURL)
	}

	// Check that DASH files were uploaded
	if _, exists := mock.uploadedFiles["dash/test-video/manifest.mpd"]; !exists {
		t.Error("Expected manifest.mpd to be uploaded, but it wasn't")
	}
	if _, exists := mock.uploadedFiles["dash/test-video/init-stream1.m4s"]; !exists {
		t.Error("Expected init segment to be uploaded, but it wasn't")
	}
	for i := 0; i < 2; i++ {
		segmentKey := filepath.ToSlash(filepath.Join("dash/test-video", fmt.Sprintf("chunk-stream1-%d.m4s", i+1)))
		if _, exists := mock.uploadedFiles[segmentKey]; !exists {
			t.Errorf("Expected %s to be uploaded, but it wasn't", segmentKey)
		}
	}
}
