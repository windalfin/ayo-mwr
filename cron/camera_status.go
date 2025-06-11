package cron

import (
	"fmt"
	"log"
	"time"

	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/recording"
)

// StartCameraStatusCron initializes a cron job that runs every 5 minutes to check camera status
// and report to the AYO API using SaveCameraStatus
func StartCameraStatusCron(configManager *config.ConfigManager) {

	go func() {
		// Initialize AYO API client
		ayoClient, err := api.NewAyoIndoClient()
		if err != nil {
			log.Printf("Error initializing AYO API client: %v", err)
			return
		}

		// Initial delay before first run (10 seconds)
		time.Sleep(10 * time.Second)

		// Run immediately once at startup
		checkAndReportCameraStatus(configManager, ayoClient)

		// Then set up ticker for recurring checks every 5 minutes
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			checkAndReportCameraStatus(configManager, ayoClient)
		}
	}()
	log.Println("Camera status cron job started - will check camera connections every 5 minutes")
}

// checkAndReportCameraStatus checks the status of all cameras and reports it to the AYO API
func checkAndReportCameraStatus(configManager *config.ConfigManager, ayoClient *api.AyoIndoClient) {
	// Get current config from manager
	cfg := configManager.GetConfig()

	log.Println("Running camera status check...")

	for _, cam := range cfg.Cameras {
		if !cam.Enabled {
			log.Printf("Camera %s is disabled, skipping check", cam.Name)
			continue
		}

		// Construct full RTSP URL
		fullURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s", 
			cam.Username, cam.Password, cam.IP, cam.Port, cam.Path)
		
		// Test RTSP connection
		isOnline, err := recording.TestRTSPConnection(cam.Name, fullURL)
		if err != nil {
			log.Printf("Error testing connection for camera %s: %v", cam.Name, err)
		}

		// Determine camera status for logging
		statusText := "offline"
		if isOnline {
			statusText = "online"
		}
		log.Printf("Camera %s is %s", cam.Name, statusText)

		// Report status to AYO API
		result, err := ayoClient.SaveCameraStatus(cam.Name, isOnline)
		if err != nil {
			log.Printf("Error saving camera status for %s: %v", cam.Name, err)
			continue
		}
		
		log.Printf("Successfully reported camera %s status to AYO API: %v", cam.Name, result)
	}

	log.Println("Camera status check completed")
}
