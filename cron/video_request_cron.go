package cron

import (
	"log"
	"os"
	"time"

	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"

	"github.com/robfig/cron/v3"
)

// StartVideoRequestCron initializes a cron job that runs every 30 minutes to:
// 1. Get pending video requests from AYO API
// 2. Check if video exists in database by unique_id
// 3. Send video info to AYO API if it exists
func StartVideoRequestCron(cfg config.Config) {
	go func() {
		// Initialize database
		dbPath := cfg.DatabasePath
		db, err := database.NewSQLiteDB(dbPath)
		if err != nil {
			log.Printf("Error initializing database: %v", err)
			return
		}
		defer db.Close()

		// Initialize AYO API client
		ayoClient, err := api.NewAyoIndoClient()
		if err != nil {
			log.Printf("Error initializing AYO API client: %v", err)
			return
		}

		// Initialize R2 storage client
		r2Config := storage.R2Config{
			AccessKey: os.Getenv("R2_ACCESS_KEY"),
			SecretKey: os.Getenv("R2_SECRET_KEY"),
			AccountID: os.Getenv("R2_ACCOUNT_ID"),
			Bucket:    os.Getenv("R2_BUCKET"),
			Endpoint:  os.Getenv("R2_ENDPOINT"),
			Region:    os.Getenv("R2_REGION"),
			BaseURL:   os.Getenv("R2_BASE_URL"),
		}

		r2Client, err := storage.NewR2Storage(r2Config)
		if err != nil {
			log.Printf("Error initializing R2 storage client: %v", err)
			return
		}

		// Initial delay before first run (5 seconds)
		time.Sleep(5 * time.Second)

		// Run immediately once at startup
		processVideoRequests(cfg, db, ayoClient, r2Client)

		// Start the cron job
		schedule := cron.New()

		// Schedule the task every 30 minutes
		_, err = schedule.AddFunc("@every 30m", func() {
			processVideoRequests(cfg, db, ayoClient, r2Client)
		})
		if err != nil {
			log.Fatalf("Error scheduling video request cron: %v", err)
		}

		schedule.Start()
		log.Println("Video request processing cron job started - will run every 30 minutes")
	}()
}

// processVideoRequests handles fetching and processing video requests
func processVideoRequests(cfg config.Config, db database.Database, ayoClient *api.AyoIndoClient, r2Client *storage.R2Storage) {
	log.Println("Running video request processing task...")

	// Get video requests from AYO API
	response, err := ayoClient.GetVideoRequests("")
	if err != nil {
		log.Printf("Error fetching video requests from API: %v", err)
		return
	}

	// Extract data from response
	data, ok := response["data"].([]interface{})
	if !ok {
		log.Println("No video requests found or invalid response format")
		return
	}

	log.Printf("Found %d video requests", len(data))

	// Process each video request
	for _, item := range data {
		request, ok := item.(map[string]interface{})
		if !ok {
			log.Printf("Invalid video request format: %v", item)
			continue
		}

		// Extract fields from request
		videoRequestID, _ := request["video_request_id"].(string)
		uniqueID, _ := request["unique_id"].(string)
		bookingID, _ := request["booking_id"].(string)
		status, _ := request["status"].(string)

		// Skip if not pending
		if status != "PENDING" {
			log.Printf("Skipping video request %s with status %s", videoRequestID, status)
			continue
		}

		log.Printf("Processing pending video request: %s, unique_id: %s", videoRequestID, uniqueID)

		// Check if video exists in database using direct uniqueID lookup
		matchingVideo, err := db.GetVideoByUniqueID(uniqueID)
		if err != nil {
			log.Printf("Error checking database for unique ID %s: %v", uniqueID, err)
			continue
		}

		if matchingVideo == nil {
			log.Printf("No matching video found for unique_id: %s", uniqueID)
			continue
		}

		// Check if video is ready
		if matchingVideo.Status != database.StatusReady {
			log.Printf("Video for unique_id %s is not ready yet (status: %s)", uniqueID, matchingVideo.Status)
			continue
		}

		// Prepare video data for API
		type videoResolution struct {
			StreamPath   string `json:"stream_path"`
			DownloadPath string `json:"download_path"`
			Resolution   string `json:"resolution"`
		}

		// Create slice of video resolutions
		videoData := []videoResolution{}

		// Add HLS and MP4 streams if available
		if matchingVideo.R2HLSURL != "" && matchingVideo.R2MP4URL != "" {
			videoData = append(videoData, videoResolution{
				StreamPath:   matchingVideo.R2HLSURL,
				DownloadPath: matchingVideo.R2MP4URL,
				Resolution:   "1080", // Assuming 1080p resolution, adjust as needed
			})
		}

		// // Add preview if available
		// if matchingVideo.R2PreviewMP4URL != "" {
		// 	videoData = append(videoData, videoResolution{
		// 		StreamPath:   matchingVideo.R2PreviewMP4URL,
		// 		DownloadPath: matchingVideo.R2PreviewMP4URL,
		// 		Resolution:   "720", // Assuming 720p for preview, adjust as needed
		// 	})
		// }

		if len(videoData) == 0 {
			log.Printf("No video URLs available for unique_id: %s", uniqueID)
			continue
		}

		// Parse start and end timestamps from the metadata if available
		var startTime, endTime time.Time
		if matchingVideo.CreatedAt.IsZero() {
			startTime = time.Now().Add(-1 * time.Hour) // Fallback: 1 hour ago
		} else {
			startTime = matchingVideo.CreatedAt
		}

		if matchingVideo.FinishedAt == nil {
			endTime = time.Now() // Fallback: now
		} else {
			endTime = *matchingVideo.FinishedAt
		}

		// Send video data to AYO API
		result, err := ayoClient.SaveVideo(
			videoRequestID,
			bookingID,
			"clip", // Assuming "clip" as video type, adjust if needed
			videoData[0].StreamPath,
			videoData[0].DownloadPath,
			startTime,
			endTime,
		)

		if err != nil {
			log.Printf("Error sending video to AYO API: %v", err)
			continue
		}

		// Check API response
		statusCode, _ := result["status_code"].(float64)
		message, _ := result["message"].(string)
		
		if statusCode == 200 {
			log.Printf("Successfully sent video to API for request %s: %s", videoRequestID, message)
		} else {
			log.Printf("API error for video request %s: %s", videoRequestID, message)
		}
	}

	log.Println("Video request processing task completed")
}
