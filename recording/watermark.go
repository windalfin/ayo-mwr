package recording

import (
	"context"
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
	"time"

	"ayo-mwr/database"
)

// get Watermark for that video from AyoIndonesia API
// the watermark are based on venue, so we will need to submit venue_code
// We will store the watermark image in specific folder do we don't have to download it every time
// We will store the watermark image in ./watermark/{venue_code} folder
// The API will return 3 watermark with 3 different size for different video quality
func GetWatermark(venueCode string) (string, error) {
	// Load configuration from database
	db, err := database.NewSQLiteDB("./data/videos.db")
	if err != nil {
		log.Printf("Failed to connect to database, using fallback values: %v", err)
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()

	ayoindoAPIBase := "http://iot-api.ayodev.xyz:6060/api/v1"
	ayoindoAPIToken := ""
	storagePath := "./videos"

	if db != nil {
		if baseURL, err := db.GetSystemConfig(database.ConfigAyoindoAPIBaseEndpoint); err == nil && baseURL.Value != "" {
			ayoindoAPIBase = baseURL.Value
		}
		if token, err := db.GetSystemConfig(database.ConfigAyoindoAPIToken); err == nil && token.Value != "" {
			ayoindoAPIToken = token.Value
		}
		if path, err := db.GetSystemConfig(database.ConfigStoragePath); err == nil && path.Value != "" {
			storagePath = path.Value
		}
	}

	// Use default values if database values are empty
	if ayoindoAPIBase == "" {
		ayoindoAPIBase = "http://iot-api.ayodev.xyz:6060/api/v1"
	}
	if storagePath == "" {
		storagePath = "./videos"
	}
	folder := filepath.Join(storagePath, "watermark", venueCode)
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

	// Check if any files exist first - prioritize using existing files
	cwd, _ := os.Getwd()
	log.Printf("Current working directory: %s", cwd)
	log.Printf("Checking watermark files in folder: %s", folder)
	
	// First try to return any existing watermark file (prioritize 1080 > 720 > 480 > 360)
	for _, res := range []string{"1080", "720", "480", "360"} {
		fname := sizes[res]
		path := filepath.Join(folder, fname)
		if _, err := os.Stat(path); err == nil {
			log.Printf("Found existing watermark: %s", path)
			return path, nil
		}
	}
	
	// No existing files found, try to download
	allExist := true
	for res, fname := range sizes {
		if _, err := os.Stat(filepath.Join(folder, fname)); os.IsNotExist(err) && wanted[res] {
			allExist = false
		}
	}
	log.Printf("No existing watermark files found, attempting download...")
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

	// Try again to return any downloaded watermark file (prioritize 1080 > 720 > 480 > 360)
	for _, res := range []string{"1080", "720", "480", "360"} {
		fname := sizes[res]
		path := filepath.Join(folder, fname)
		if _, err := os.Stat(path); err == nil {
			log.Printf("Using downloaded watermark: %s", path)
			return path, nil
		}
	}
	return "", fmt.Errorf("no watermark image found after download attempt")
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

// GetWatermarkSettings fetches watermark position, margin, and opacity from database
func GetWatermarkSettings() (WatermarkPosition, int, float64) {
	// Load configuration from database
	db, err := database.NewSQLiteDB("./data/videos.db")
	if err != nil {
		log.Printf("Failed to connect to database, using fallback values: %v", err)
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()

	pos := TopRight // default
	margin := 10
	opacity := 0.6

	if db != nil {
		if position, err := db.GetSystemConfig(database.ConfigWatermarkPosition); err == nil && position.Value != "" {
			pos = ParseWatermarkPosition(position.Value)
		}
		if marginStr, err := db.GetSystemConfig(database.ConfigWatermarkMargin); err == nil && marginStr.Value != "" {
			if val, err := strconv.Atoi(marginStr.Value); err == nil {
				margin = val
			}
		}
		if opacityStr, err := db.GetSystemConfig(database.ConfigWatermarkOpacity); err == nil && opacityStr.Value != "" {
			if val, err := strconv.ParseFloat(opacityStr.Value, 64); err == nil {
				opacity = val
			}
		}
	}

	// Use default values if database connection failed
	// pos, margin, and opacity already have default values set above

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
func AddWatermarkWithPosition(inputVideo, watermarkImg, outputVideo string, position WatermarkPosition, margin int, opacity float64, resolution string) error {
	log.Printf("AddWatermarkWithPosition : Adding watermark to video: %s", inputVideo)
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

	// Use a simplified but more reliable filter chain
	filter := fmt.Sprintf("overlay=%s:enable='between(t,0,999999)'", overlayExpr[8:])
	log.Printf("AddWatermarkWithPosition : Filter: %s", filter)
	// Verify watermark file exists and is readable
	if _, err := os.Stat(watermarkImg); err != nil {
		log.Printf("AddWatermarkWithPosition : WARNING - Watermark file not found or not accessible: %s", watermarkImg)
		return fmt.Errorf("watermark file not found or not accessible: %v", err)
	}
	
	// Read first few bytes of watermark file to verify it's actually an image
	f, err := os.Open(watermarkImg)
	if err == nil {
		header := make([]byte, 8)
		_, err = f.Read(header)
		f.Close()
		
		// Check for PNG signature
		if err == nil && string(header[:4]) != "\x89PNG" {
			log.Printf("AddWatermarkWithPosition : WARNING - File doesn't appear to be a valid PNG image: %s", watermarkImg)
		}
	}
	
	log.Printf("AddWatermarkWithPosition : Using watermark: %s with opacity %.2f", watermarkImg, opacity)
	
	// Komando ffmpeg dengan parameter optimized untuk kecepatan
	args := []string{
		"-y",
		"-v", "warning", // Show more detailed output
		"-i", inputVideo,
		"-i", watermarkImg,
		"-filter_complex", filter,
		"-c:v", "libx264",           // Codec video H.264
		"-crf", "28",                // Higher CRF for much faster encoding (slightly lower quality but much faster)
		"-preset", "ultrafast",      // Ultra fast preset for maximum encoding speed
		"-profile:v", "high",        // Profil high untuk kualitas maksimal
		"-pix_fmt", "yuv420p",       // Format pixel standar untuk kompatibilitas maksimal
		"-c:a", "copy",              // Copy audio tanpa mengubah kualitas
		"-map_metadata", "0",        // Pertahankan metadata dari video asli
		"-movflags", "+faststart",   // Optimasi file untuk streaming
		"-threads", "0",             // Use all available CPU threads for faster encoding
		outputVideo,
	}
	
	// Create context with timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	
	log.Printf("AddWatermarkWithPosition : Executing ffmpeg with optimized settings for watermark overlay (5min timeout)")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("AddWatermarkWithPosition : ffmpeg error: %v, output: %s", err, string(output))
		return fmt.Errorf("AddWatermarkWithPosition : ffmpeg error: %v, output: %s", err, string(output))
	}
	log.Printf("AddWatermarkWithPosition : Watermark added successfully")
	return nil
}