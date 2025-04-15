package recording

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAddWatermark(t *testing.T) {
	inputVideo := filepath.Join("..", "test", "videos", "uploads", "camera_A_20250304_120503.mp4")
	watermarkImg := filepath.Join("..", "test", "watermark", "ayologo.png")
	outputVideo := filepath.Join("..", "test", "videos", "uploads", "output_watermarked.mp4")
	frameOriginal := filepath.Join("..", "test", "videos", "uploads", "frame_original.png")
	frameWatermarked := filepath.Join("..", "test", "videos", "uploads", "frame_watermarked.png")

	// Clean up output files before/after
	defer os.Remove(outputVideo)
	defer os.Remove(frameOriginal)
	defer os.Remove(frameWatermarked)

	// Call the function under test (to be implemented)
	err := AddWatermark(inputVideo, watermarkImg, outputVideo, 100, 100) // position (x, y) example
	if err != nil {
		t.Fatalf("AddWatermark failed: %v", err)
	}

	// Extract frame at 1 second from both videos
	extractFrame := func(video, out string) error {
		cmd := exec.Command("ffmpeg", "-y", "-ss", "1", "-i", video, "-vframes", "1", out)
		return cmd.Run()
	}
	if err := extractFrame(inputVideo, frameOriginal); err != nil {
		t.Fatalf("Failed to extract original frame: %v", err)
	}
	if err := extractFrame(outputVideo, frameWatermarked); err != nil {
		t.Fatalf("Failed to extract watermarked frame: %v", err)
	}

	// Open both frames
	origF, err := os.Open(frameOriginal)
	if err != nil {
		t.Fatalf("Open original frame: %v", err)
	}
	defer origF.Close()
	origImg, err := png.Decode(origF)
	if err != nil {
		t.Fatalf("Decode original frame: %v", err)
	}

	wmF, err := os.Open(frameWatermarked)
	if err != nil {
		t.Fatalf("Open watermarked frame: %v", err)
	}
	defer wmF.Close()
	wmImg, err := png.Decode(wmF)
	if err != nil {
		t.Fatalf("Decode watermarked frame: %v", err)
	}

	// Compare region where watermark should be
	// (100,100) is the top-left of the watermark, adjust width/height as needed
	w, h := 50, 50 // Example watermark size, adjust as needed
	diff := false
	for x := 100; x < 100+w; x++ {
		for y := 100; y < 100+h; y++ {
			if origImg.At(x, y) != wmImg.At(x, y) {
				diff = true
				break
			}
		}
	}
	if !diff {
		t.Error("No difference detected in watermark region; watermark may not have been applied")
	}
}
