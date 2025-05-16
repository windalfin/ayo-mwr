package recording

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// get Watermark for that video from AyoIndonesia API
// the watermark are based on venue, so we will need to submit venue_code
// We will store the watermark image in specific folder do we don't have to download it every time
// We will store the watermark image in ./watermark/{venue_code} folder
// The API will return 3 watermark with 3 different size for different video quality
func GetWatermark(venueCode string) (string, error) {
	ayoindoAPIBase := os.Getenv("AYOINDO_API_BASE_ENDPOINT")
	if ayoindoAPIBase == "" {
		ayoindoAPIBase = "http://iot-api.ayodev.xyz:6060/api/v1"
	}
	ayoindoAPIToken := os.Getenv("AYOINDO_API_TOKEN")
	folder := filepath.Join("..", "recording", "watermark", venueCode)
	if err := os.MkdirAll(folder, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", folder, err)
	}
	// Define watermark sizes and filenames
	sizes := map[string]string{
		"360":  "watermark_360.png",
		"480":  "watermark_480.png",
		"720":  "watermark_720.png",
		"1080": "watermark_1080.png",
	}
	wanted := map[string]bool{"360": true, "480": true, "720": true, "1080": true}

	// Check if all files exist
	cwd, _ := os.Getwd()
	log.Printf("Current working directory: %s", cwd)
	allExist := true
	for res, fname := range sizes {
		if _, err := os.Stat(filepath.Join(folder, fname)); os.IsNotExist(err) && wanted[res] {
			allExist = false
		}
	}
	log.Printf("Checking watermark files in folder: %s", folder)
	log.Printf("Does watermark files exist: %t", allExist)

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
	TopLeft WatermarkPosition = iota
	TopRight
	BottomLeft
	BottomRight
)

// ParseWatermarkPosition parses the env value to WatermarkPosition
func ParseWatermarkPosition(env string) WatermarkPosition {
	switch strings.ToLower(env) {
	case "top_left":
		return TopLeft
	case "top_right":
		return TopRight
	case "bottom_left":
		return BottomLeft
	case "bottom_right":
		return BottomRight
	default:
		return TopRight // fallback default
	}
}

// GetWatermarkSettings fetches watermark position, margin, and opacity from env
func GetWatermarkSettings() (WatermarkPosition, int, float64) {
	pos := ParseWatermarkPosition(os.Getenv("WATERMARK_POSITION"))
	margin := 10
	if m := os.Getenv("WATERMARK_MARGIN"); m != "" {
		if val, err := strconv.Atoi(m); err == nil {
			margin = val
		}
	}
	opacity := 0.6
	if o := os.Getenv("WATERMARK_OPACITY"); o != "" {
		if val, err := strconv.ParseFloat(o, 64); err == nil {
			opacity = val
		}
	}
	return pos, margin, opacity
}

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
// This implementation preserves the original video quality as much as possible.
func AddWatermarkWithPosition(inputVideo, watermarkImg, outputVideo string, position WatermarkPosition, margin int, opacity float64) error {
	if opacity < 0.0 {
		opacity = 0.0
	} else if opacity > 1.0 {
		opacity = 1.0
	}
	var overlayExpr string
	switch position {
	case TopLeft:
		overlayExpr = fmt.Sprintf("overlay=%d:%d", margin, margin)
	case TopRight:
		overlayExpr = fmt.Sprintf("overlay=main_w-overlay_w-%d:%d", margin, margin)
	case BottomLeft:
		overlayExpr = fmt.Sprintf("overlay=%d:main_h-overlay_h-%d", margin, margin)
	case BottomRight:
		overlayExpr = fmt.Sprintf("overlay=main_w-overlay_w-%d:main_h-overlay_h-%d", margin, margin)
	default:
		overlayExpr = fmt.Sprintf("overlay=%d:%d", margin, margin)
	}

	filter := fmt.Sprintf("[1]format=rgba,colorchannelmixer=aa=%.2f[wm];[0][wm]%s", opacity, overlayExpr)
	
	// Komando ffmpeg dengan parameter yang menjaga kualitas video tetap original
	cmd := exec.Command(
		"ffmpeg", "-y",
		"-i", inputVideo,
		"-i", watermarkImg,
		"-filter_complex", filter,
		"-c:v", "libx264",           // Codec video H.264
		"-crf", "17",                // CRF sangat rendah untuk kualitas mendekati lossless (0-51, semakin rendah semakin bagus)
		"-preset", "slow",           // Preset encoding yang mementingkan kualitas
		"-profile:v", "high",        // Profil high untuk kualitas maksimal
		"-pix_fmt", "yuv420p",       // Format pixel standar untuk kompatibilitas maksimal
		"-c:a", "copy",              // Copy audio tanpa mengubah kualitas
		"-map_metadata", "0",        // Pertahankan metadata dari video asli
		"-movflags", "+faststart",   // Optimasi file untuk streaming
		outputVideo,
	)
	
	log.Printf("Executing ffmpeg with high quality settings for watermark overlay")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg error: %v, output: %s", err, string(output))
	}
	return nil
}
