package transcode

import (
	"ayo-mwr/config"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitFFmpegParams(t *testing.T) {
	testCases := []struct {
		hwAccel string
		codec   string
	}{
		{"nvidia", "h264"},
		{"intel", "h264"},
		{"amd", "h264"},
		{"software", "h264"},
		{"", "h264"},
		{"nvidia", "hevc"},
		{"software", "hevc"},
	}

	for _, tc := range testCases {
		inputParams, outputParams := SplitFFmpegParams(tc.hwAccel, tc.codec)
		if tc.hwAccel != "software" && tc.hwAccel != "" && len(inputParams) == 0 {
			t.Errorf("Expected non-empty input params for hwAccel=%s, codec=%s", tc.hwAccel, tc.codec)
		}
		if len(outputParams) == 0 {
			t.Errorf("Expected non-empty output params for hwAccel=%s, codec=%s", tc.hwAccel, tc.codec)
		}
	}
}

func TestTranscodeVideoValidation(t *testing.T) {
	// Skip actual transcoding, just test the validation logic
	tempDir, err := os.MkdirTemp("", "transcode-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test config
	cfg := config.Config{
		StoragePath: tempDir,
		BaseURL:     "http://localhost:8080",
	}

	// Use an existing test video file instead of creating empty file
	existingVideo := "../test/videos/uploads/camera_A_20250304_120503.mp4"
	inputPath := filepath.Join(tempDir, "test.mp4")
	
	// Copy existing test video to temp directory
	if err := copyFile(existingVideo, inputPath); err != nil {
		t.Skipf("Skipping test - could not copy test video: %v", err)
	}

	// Test that directories are created
	videoID := "test123"
	// The actual path structure is: recordings/hls/hls/videoID/quality/
	hlsPath := filepath.Join(tempDir, "recordings", "hls", "hls", videoID)

	// Call the transcode function
	hlsURLs, durations, err := TranscodeVideo(inputPath, videoID, "hls", &cfg)
	if err != nil {
		t.Logf("TranscodeVideo error (expected): %v", err)
	}
	
	t.Logf("HLS URLs: %v", hlsURLs)
	t.Logf("Durations: %v", durations)
	
	// Check that the HLS directory structure was created
	if _, err := os.Stat(hlsPath); os.IsNotExist(err) {
		t.Errorf("HLS directory was not created at %s", hlsPath)
	} else {
		t.Logf("HLS directory created successfully at %s", hlsPath)
	}
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}
