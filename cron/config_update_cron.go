package cron

import (
	"log"
	"time"

	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/database"
)

// StartConfigUpdateCron initializes a cron job that runs every 24 hours to:
// 1. Get video configuration from AYO API
// 2. Update the application configuration with values from the API
// 3. Apply the updated configuration to all cameras
func StartConfigUpdateCron(cfg *config.Config, db database.Database) {
	go func() {
		// Initialize AYO API client
		ayoClient, err := api.NewAyoIndoClient()
		if err != nil {
			log.Printf("Error initializing AYO API client for config update cron: %v", err)
			return
		}

		// Initial delay before first run (10 seconds)
		time.Sleep(10 * time.Second)

		// Run immediately once at startup
		updateConfigFromAPI(cfg, ayoClient, db)

		// Then set up ticker for recurring updates every 24 hours
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			updateConfigFromAPI(cfg, ayoClient, db)
		}
	}()
	log.Println("Config update cron job started - will update configuration from API every 24 hours")
}

// updateConfigFromAPI handles fetching configuration from API and updating the application config
func updateConfigFromAPI(cfg *config.Config, ayoClient *api.AyoIndoClient, db database.Database) {
	// Create config API client wrapper
	configClient := api.NewConfigAPIClient(ayoClient)
	log.Println("Running configuration update task...")

	// Get video configuration from AYO API
	response, err := configClient.GetVideoConfiguration()
	if err != nil {
		log.Printf("Error fetching configuration from API: %v", err)
		return
	}

	// Extract data from response
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		log.Println("No configuration found or invalid response format")
		return
	}

	log.Printf("Received configuration from API: %+v", data)

	// Previous state for change detection
	prevClipDuration := cfg.ClipDuration
	prevResolution := cfg.Resolution
	prevFrameRate := cfg.FrameRate
	prevAutoDelete := cfg.AutoDelete

	// Update config using helper function
	config.UpdateConfigFromAPIResponse(cfg, data, db)

	// Log changes
	updated := false
	if prevClipDuration != cfg.ClipDuration {
		log.Printf("Updated ClipDuration to: %d seconds", cfg.ClipDuration)
		updated = true
	}
	if prevResolution != cfg.Resolution {
		log.Printf("Updated Resolution to: %s", cfg.Resolution)
		updated = true
	}
	if prevFrameRate != cfg.FrameRate {
		log.Printf("Updated FrameRate to: %d", cfg.FrameRate)
		updated = true
	}
	if prevAutoDelete != cfg.AutoDelete {
		log.Printf("Updated AutoDelete to: %d days", cfg.AutoDelete)
		updated = true
	}

	if updated {
		log.Printf("Updated configuration for %d cameras", len(cfg.Cameras))
	} else {
		log.Println("No configuration values were updated from API")
	}

	log.Println("Configuration update task completed")
}
