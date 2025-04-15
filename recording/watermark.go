package recording

import (
	"fmt"
	"os/exec"
)

// AddWatermark overlays a PNG watermark at (x, y) on the input video and writes to outputVideo.
// Returns error if the operation fails.
func AddWatermark(inputVideo, watermarkImg, outputVideo string, x, y int) error {
	// ffmpeg overlay filter: https://ffmpeg.org/ffmpeg-filters.html#overlay
	// Example: ffmpeg -i input.mp4 -i watermark.png -filter_complex "overlay=100:100" output.mp4
	cmd := exec.Command(
		"ffmpeg", "-y",
		"-i", inputVideo,
		"-i", watermarkImg,
		"-filter_complex", fmt.Sprintf("overlay=%d:%d", x, y),
		outputVideo,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg error: %v, output: %s", err, string(output))
	}
	return nil
}
