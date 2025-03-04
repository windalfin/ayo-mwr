package transcode

import (
	"ayo-mwr/config"
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

	// Create a dummy input file
	inputPath := filepath.Join(tempDir, "test.mp4")
	f, err := os.Create(inputPath)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	// Test that directories are created
	videoID := "test123"
	hlsPath := filepath.Join(tempDir, "hls", videoID)
	dashPath := filepath.Join(tempDir, "dash", videoID)

	// This will fail because we're not actually transcoding, but we can check if directories were created
	_, _, err = TranscodeVideo(inputPath, videoID, cfg)
	
	// The transcoding will fail, but the directories should be created
	if _, err := os.Stat(hlsPath); os.IsNotExist(err) {
		t.Errorf("HLS directory was not created")
	}
	if _, err := os.Stat(dashPath); os.IsNotExist(err) {
		t.Errorf("DASH directory was not created")
	}
}
