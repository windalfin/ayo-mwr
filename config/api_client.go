package config

import (
	"fmt"
	"log"

	"ayo-mwr/database"
)

// APIClient adalah interface untuk mendapatkan konfigurasi dari API
type APIClient interface {
	GetVideoConfiguration() (map[string]interface{}, error)
}

// calculateDimensions menghitung width dan height berdasarkan resolution
func calculateDimensions(resolution string) (int, int) {
	switch resolution {
	case "360":
		return 640, 360
	case "480":
		return 854, 480
	case "720":
		return 1280, 720
	case "1080":
		return 1920, 1080
	case "1440":
		return 2560, 1440
	case "2160", "4k":
		return 3840, 2160
	default:
		// Default ke 720p jika resolution tidak dikenali
		log.Printf("⚠️ WARNING: Unknown resolution '%s', defaulting to 720p", resolution)
		return 1280, 720
	}
}

// UpdateConfigFromAPIResponse memperbarui konfigurasi dari response API
func UpdateConfigFromAPIResponse(cfg *Config, data map[string]interface{}, db database.Database) {
	// Extract the required fields from the API response
	if clipDuration, ok := data["clip_duration"].(float64); ok {
		cfg.ClipDuration = int(clipDuration)
		log.Printf("Set ClipDuration from API: %d", cfg.ClipDuration)
	}

	if videoQuality, ok := data["video_quality"].(float64); ok {
		cfg.Resolution = fmt.Sprintf("%d", int(videoQuality))
		log.Printf("Set Resolution from API: %s", cfg.Resolution)
	}

	if frameRate, ok := data["frame_rate"].(float64); ok {
		cfg.FrameRate = int(frameRate)
		log.Printf("Set FrameRate from API: %d", cfg.FrameRate)
	}

	if autoDelete, ok := data["auto_delete"].(float64); ok {
		cfg.AutoDelete = int(autoDelete)
		log.Printf("Set AutoDelete from API: %d", cfg.AutoDelete)
	}

	// Update camera configurations with the new values
	for i := range cfg.Cameras {
		originalResolution := cfg.Cameras[i].Resolution
		originalFrameRate := cfg.Cameras[i].FrameRate
		originalAutoDelete := cfg.Cameras[i].AutoDelete

		if cfg.Resolution != "" {
			cfg.Cameras[i].Resolution = cfg.Resolution
		}
		if cfg.FrameRate > 0 {
			cfg.Cameras[i].FrameRate = cfg.FrameRate
		}
		if cfg.AutoDelete > 0 {
			cfg.Cameras[i].AutoDelete = cfg.AutoDelete
		}

		// Check if any values changed
		if originalResolution != cfg.Cameras[i].Resolution ||
			originalFrameRate != cfg.Cameras[i].FrameRate ||
			originalAutoDelete != cfg.Cameras[i].AutoDelete {
			
			// Calculate new dimensions based on resolution
			width, height := calculateDimensions(cfg.Cameras[i].Resolution)
			cfg.Cameras[i].Width = width
			cfg.Cameras[i].Height = height
			
			log.Printf("📹 CAMERA: Updated config for %s - Resolution: %s (%dx%d), FrameRate: %d, AutoDelete: %d",
				cfg.Cameras[i].Name, cfg.Cameras[i].Resolution, width, height, cfg.Cameras[i].FrameRate, cfg.Cameras[i].AutoDelete)
			
			// Save updated camera configuration to database if database is available
			if db != nil {
				if err := db.UpdateCameraConfig(
					cfg.Cameras[i].Name,
					cfg.Cameras[i].Resolution,
					cfg.Cameras[i].FrameRate,
					cfg.Cameras[i].AutoDelete,
					width,
					height,
				); err != nil {
					log.Printf("❌ ERROR: Failed to update camera config for %s: %v", cfg.Cameras[i].Name, err)
				} else {
					log.Printf("✅ SUCCESS: Updated camera config for %s in database", cfg.Cameras[i].Name)
				}
			}
		}
	}
}
