package config

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"ayo-mwr/database"
)

// APIClient adalah interface untuk mendapatkan konfigurasi dari API
type APIClient interface {
	GetVideoConfiguration() (map[string]interface{}, error)
}


// UpdateConfigFromAPIResponse memperbarui konfigurasi dari response API
func UpdateConfigFromAPIResponse(cfg *Config, data map[string]interface{}, db database.Database) {
	// Extract the required fields from the API response
	if clipDuration, ok := data["clip_duration"].(float64); ok {
		cfg.ClipDuration = int(clipDuration)
		log.Printf("Set ClipDuration from API: %d", cfg.ClipDuration)
	}

	if videoQuality, ok := data["video_quality"].(string); ok {
		// Parse video_quality JSON array string to slice
		// Try parsing as array of numbers first, then as array of strings
		var qualities []string
		
		// Try parsing as array of numbers
		var numericQualities []int
		if err := json.Unmarshal([]byte(videoQuality), &numericQualities); err == nil {
			// Convert numbers to strings
			for _, quality := range numericQualities {
				qualities = append(qualities, fmt.Sprintf("%d", quality))
			}
		} else {
			// Try parsing as array of strings
			if err := json.Unmarshal([]byte(videoQuality), &qualities); err != nil {
				log.Printf("❌ ERROR: Failed to parse video_quality JSON: %v", err)
				return
			}
		}
		
		if len(qualities) > 0 {
			// Convert numeric qualities to format with 'p' suffix
			var formattedQualities []string
			for _, quality := range qualities {
				formattedQualities = append(formattedQualities, quality+"p")
			}
			
			// Save to database as enabled_qualities
			if db != nil {
				config := database.SystemConfig{
					Key:       database.ConfigEnabledQualities,
					Value:     strings.Join(formattedQualities, ","),
					Type:      "string",
					UpdatedBy: "api_sync",
				}
				if err := db.SetSystemConfig(config); err != nil {
					log.Printf("❌ ERROR: Failed to set enabled qualities: %v", err)
				} else {
					log.Printf("✅ SUCCESS: Set enabled qualities from API: %s", strings.Join(formattedQualities, ","))
				}
			} else {
				log.Printf("⚠️ WARNING: Database not available, cannot save enabled qualities")
			}
		}
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
		originalFrameRate := cfg.Cameras[i].FrameRate
		originalAutoDelete := cfg.Cameras[i].AutoDelete

		if cfg.FrameRate > 0 {
			cfg.Cameras[i].FrameRate = cfg.FrameRate
		}
		if cfg.AutoDelete > 0 {
			cfg.Cameras[i].AutoDelete = cfg.AutoDelete
		}

		// Check if any values changed
		if originalFrameRate != cfg.Cameras[i].FrameRate ||
			originalAutoDelete != cfg.Cameras[i].AutoDelete {

			log.Printf("📹 CAMERA: Updated config for %s - FrameRate: %d, AutoDelete: %d",
				cfg.Cameras[i].Name, cfg.Cameras[i].FrameRate, cfg.Cameras[i].AutoDelete)

			// Save updated camera configuration to database if database is available
			if db != nil {
				if err := db.UpdateCameraConfig(
					cfg.Cameras[i].Name,
					cfg.Cameras[i].FrameRate,
					cfg.Cameras[i].AutoDelete,
				); err != nil {
					log.Printf("❌ ERROR: Failed to update camera config for %s: %v", cfg.Cameras[i].Name, err)
				} else {
					log.Printf("✅ SUCCESS: Updated camera config for %s in database", cfg.Cameras[i].Name)
				}
			}
		}
	}
}
