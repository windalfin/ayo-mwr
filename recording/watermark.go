package recording

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

// get Watermark for that video from AyoIndonesia API
// the watermark are based on venue, so we will need to submit venue_code
// We will store the watermark image in specific folder do we don't have to download it every time
// We will store the watermark image in ./watermark/{venue_code} folder
// The API will return 3 watermark with 3 different size for different video quality
func getWatermark(venueCode string) (string, error) {
	ayoindoAPIBase := os.Getenv("AYOINDO_API_BASE_ENDPOINT")
	if ayoindoAPIBase == "" {
		ayoindoAPIBase = "http://iot-api.ayodev.xyz:6060/api/v1"
	}
	ayoindoAPIToken := os.Getenv("AYOINDO_API_TOKEN")
	folder := filepath.Join(".", "watermark", venueCode)
	os.MkdirAll(folder, 0755)

	// Define watermark sizes and filenames
	sizes := map[string]string{
		"360":  "watermark_360.png",
		"480":  "watermark_480.png",
		"720":  "watermark_720.png",
		"1080": "watermark_1080.png",
	}
	wanted := map[string]bool{"360": true, "480": true, "720": true, "1080": true}

	// Check if all files exist
	allExist := true
	for res, fname := range sizes {
		if _, err := os.Stat(filepath.Join(folder, fname)); os.IsNotExist(err) && wanted[res] {
			allExist = false
		}
	}

	if !allExist {
		// Download metadata JSON from API
		url := fmt.Sprintf("%s/watermark?venue_code=%s&token=%s", ayoindoAPIBase, venueCode, ayoindoAPIToken)
		resp, err := http.Get(url)
		if err != nil {
			return "", fmt.Errorf("failed to fetch watermark metadata: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("failed to fetch watermark metadata: status %d", resp.StatusCode)
		}
		var apiResp struct {
			Error      bool `json:"error"`
			StatusCode int  `json:"status_code"`
			Data       []struct {
				Resolution string `json:"resolution"`
				Path       string `json:"path"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return "", fmt.Errorf("failed to parse watermark API response: %w", err)
		}
		if apiResp.Error || apiResp.StatusCode != 200 {
			return "", fmt.Errorf("API error or bad status: %v, %d", apiResp.Error, apiResp.StatusCode)
		}

		// Download images for required resolutions
		for _, entry := range apiResp.Data {
			if wanted[entry.Resolution] {
				fname, ok := sizes[entry.Resolution]
				if !ok {
					continue
				}
				path := filepath.Join(folder, fname)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					imgResp, err := http.Get(entry.Path)
					if err != nil {
						return "", fmt.Errorf("failed to download watermark image %s: %w", entry.Resolution, err)
					}
					defer imgResp.Body.Close()
					if imgResp.StatusCode != 200 {
						return "", fmt.Errorf("failed to download watermark image %s: status %d", entry.Resolution, imgResp.StatusCode)
					}
					f, err := os.Create(path)
					if err != nil {
						return "", fmt.Errorf("failed to create file %s: %w", path, err)
					}
					_, err = io.Copy(f, imgResp.Body)
					f.Close()
					if err != nil {
						return "", fmt.Errorf("failed to save watermark image %s: %w", entry.Resolution, err)
					}
				}
			}
		}
	}

	// Return 360p if available, else fallback to 480/720/1080
	for _, res := range []string{"360", "480", "720", "1080"} {
		fname := sizes[res]
		path := filepath.Join(folder, fname)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no watermark image found after download")
}

// WatermarkPosition defines standard positions for watermark overlay
// Margin is the distance from the video edge in pixels
// Position options: TopLeft, TopRight, BottomLeft, BottomRight
// Example usage: AddWatermarkWithPosition(..., PositionTopRight, 10)
type WatermarkPosition int

const (
	PositionTopLeft WatermarkPosition = iota
	PositionTopRight
	PositionBottomLeft
	PositionBottomRight
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

// AddWatermarkWithPosition overlays a PNG watermark at a relative position with margin and opacity
// on the input video and writes to outputVideo.
// Opacity should be between 0.0 (fully transparent) and 1.0 (fully opaque).
func AddWatermarkWithPosition(inputVideo, watermarkImg, outputVideo string, position WatermarkPosition, margin int, opacity float64) error {
	if opacity < 0.0 {
		opacity = 0.0
	} else if opacity > 1.0 {
		opacity = 1.0
	}
	var overlayExpr string
	switch position {
	case PositionTopLeft:
		overlayExpr = fmt.Sprintf("overlay=%d:%d", margin, margin)
	case PositionTopRight:
		overlayExpr = fmt.Sprintf("overlay=main_w-overlay_w-%d:%d", margin, margin)
	case PositionBottomLeft:
		overlayExpr = fmt.Sprintf("overlay=%d:main_h-overlay_h-%d", margin, margin)
	case PositionBottomRight:
		overlayExpr = fmt.Sprintf("overlay=main_w-overlay_w-%d:main_h-overlay_h-%d", margin, margin)
	default:
		overlayExpr = fmt.Sprintf("overlay=%d:%d", margin, margin)
	}

	filter := fmt.Sprintf("[1]format=rgba,colorchannelmixer=aa=%.2f[wm];[0][wm]%s", opacity, overlayExpr)
	cmd := exec.Command(
		"ffmpeg", "-y",
		"-i", inputVideo,
		"-i", watermarkImg,
		"-filter_complex", filter,
		outputVideo,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg error: %v, output: %s", err, string(output))
	}
	return nil
}
