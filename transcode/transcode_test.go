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

	// This will fail because we're not actually transcoding, but we can check if directories were created
	_, _, err = TranscodeVideo(inputPath, videoID, "hls", &cfg)
	
	// The transcoding will fail, but the directories should be created
	if _, err := os.Stat(hlsPath); os.IsNotExist(err) {
		t.Errorf("HLS directory was not created")
	}
}

func TestIsTSFile(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		expected bool
	}{
		{"TS file with .ts extension", "video.ts", true},
		{"TS file with .TS extension", "video.TS", true},
		{"MP4 file", "video.mp4", false},
		{"MP4 file with .MP4 extension", "video.MP4", false},
		{"No extension", "video", false},
		{"Empty path", "", false},
		{"Path with directory", "/path/to/video.ts", true},
		{"Path with directory and .TS", "/path/to/video.TS", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTSFile(tt.filePath)
			if result != tt.expected {
				t.Errorf("IsTSFile(%s) = %v, expected %v", tt.filePath, result, tt.expected)
			}
		})
	}
}

func TestIsMP4File(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		expected bool
	}{
		{"MP4 file with .mp4 extension", "video.mp4", true},
		{"MP4 file with .MP4 extension", "video.MP4", true},
		{"TS file", "video.ts", false},
		{"TS file with .TS extension", "video.TS", false},
		{"No extension", "video", false},
		{"Empty path", "", false},
		{"Path with directory", "/path/to/video.mp4", true},
		{"Path with directory and .MP4", "/path/to/video.MP4", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsMP4File(tt.filePath)
			if result != tt.expected {
				t.Errorf("IsMP4File(%s) = %v, expected %v", tt.filePath, result, tt.expected)
			}
		})
	}
}

func TestConvertTSToMP4_FileNotExists(t *testing.T) {
	// Test with non-existent file
	err := ConvertTSToMP4("nonexistent.ts", "output.mp4")
	if err == nil {
		t.Error("Expected error for non-existent file, but got nil")
	}
}

func TestConvertTSToMP4_CreateOutputDirectory(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	
	// Test that the function creates output directory
	outputPath := filepath.Join(tempDir, "subdir", "output.mp4")
	
	// This should fail because input file doesn't exist, but it should create the output directory
	_ = ConvertTSToMP4("nonexistent.ts", outputPath)
	
	// Check if output directory was created
	if _, err := os.Stat(filepath.Dir(outputPath)); os.IsNotExist(err) {
		// The directory creation happens before the file existence check, so this should pass
		t.Error("Expected output directory to be created, but it doesn't exist")
	}
}
