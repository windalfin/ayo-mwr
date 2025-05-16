package recording

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Dummy signature for the function to be tested (to be implemented later)
// func MergeSessionVideos(inputPath string, startTime, endTime time.Time, outputPath string) error

func TestFindSegmentsInRange(t *testing.T) {
	inputPath := filepath.Join("..", "test", "videos", "uploads")

	// Prepare test window to include only the 20250414 segments
	startTime, _ := time.Parse("20060102_150405", "20250414_120500")
	endTime, _ := time.Parse("20060102_150405", "20250414_120540")

	files, err := FindSegmentsInRange(inputPath, startTime, endTime)
	t.Logf("Looking for files in range: %s to %s", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("FindSegmentsInRange failed: %v", err)
	}
	t.Logf("Found %d segment(s):", len(files))
	for _, f := range files {
		t.Logf("  %s", f)
	}
	if len(files) != 4 {
		t.Fatalf("Expected 4 segments, got %d", len(files))
	}

	// Check that returned files exist and are within the time window
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("File does not exist: %s", f)
		}
	}
}

func TestMergeSessionVideos(t *testing.T) {
	// Prepare test input
	inputPath := filepath.Join("..", "test", "videos", "uploads")
	// for testing we will create a temp folder called "merged_videos"
	outputPath := filepath.Join("..", "test", "videos", "merged_videos", "merged_test_output.mp4")
	defer func() {
		if err := os.Remove(outputPath); err != nil {
			t.Logf("Failed to remove temporary file %s: %v", outputPath, err)
		}
	}()
	// Define a time window that should include the test video
	startTime, _ := time.Parse("20060102_150405", "20250414_120500")
	endTime, _ := time.Parse("20060102_150405", "20250414_120540")

	// Call the function under test (to be implemented)
	err := MergeSessionVideos(inputPath, startTime, endTime, outputPath)
	if err != nil {
		t.Fatalf("MergeSessionVideos failed: %v", err)
	}

	// Check that the output file exists and is not empty
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Output file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("Output file is empty")
	}
}
