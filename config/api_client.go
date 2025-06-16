package config

import (
	"fmt"
	"log"
)

// APIClient adalah interface untuk mendapatkan konfigurasi dari API
type APIClient interface {
	GetVideoConfiguration() (map[string]interface{}, error)
}

// UpdateConfigFromAPIResponse memperbarui konfigurasi dari response API
func UpdateConfigFromAPIResponse(cfg *Config, data map[string]interface{}) {
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
		if cfg.Resolution != "" {
			cfg.Cameras[i].Resolution = cfg.Resolution
		}
		if cfg.FrameRate > 0 {
			cfg.Cameras[i].FrameRate = cfg.FrameRate
		}
		if cfg.AutoDelete > 0 {
			cfg.Cameras[i].AutoDelete = cfg.AutoDelete
		}
	}
}
