package cron

import (
	"log"
	"net/http"
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
func StartVideoRequestCron(configManager *config.ConfigManager) {
	// Get current config from manager
	cfg := configManager.GetConfig()
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
	videoRequestIDs := []string{}
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
			// videoRequestIDs = append(videoRequestIDs, videoRequestID)
			continue
		}

		if matchingVideo == nil {
			log.Printf("No matching video found for unique_id: %s", uniqueID)
			videoRequestIDs = append(videoRequestIDs, videoRequestID)
			continue
		}

		// Check if video is ready
		if matchingVideo.Status != database.StatusReady {
			log.Printf("Video for unique_id %s is not ready yet (status: %s)", uniqueID, matchingVideo.Status)
			videoRequestIDs = append(videoRequestIDs, videoRequestID)
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
				Resolution:   matchingVideo.Resolution,
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
			videoRequestIDs = append(videoRequestIDs, videoRequestID)
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

		// Check if the video URLs are accessible
		urlsAccessible := true

		// Function to check if a URL is accessible
		checkURL := func(url string) bool {
			if url == "" {
				return false
			}
			
			client := &http.Client{
				Timeout: 5 * time.Second,
			}
			
			// Use HEAD request to avoid downloading the entire file
			req, err := http.NewRequest("HEAD", url, nil)
			if err != nil {
				log.Printf("Error creating request for URL %s: %v", url, err)
				return false
			}
			
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Error accessing URL %s: %v", url, err)
				return false
			}
			defer resp.Body.Close()
			
			// Check if status code indicates success (2xx range)
			return resp.StatusCode >= 200 && resp.StatusCode < 300
		}
		
		// Check stream URL
		if !checkURL(videoData[0].StreamPath) {
			log.Printf("Stream URL not accessible: %s", videoData[0].StreamPath)
			urlsAccessible = false
		}
		
		// Check download URL
		if !checkURL(videoData[0].DownloadPath) {
			log.Printf("Download URL not accessible: %s", videoData[0].DownloadPath)
			urlsAccessible = false
		}
		
		// If either URL is not accessible, add to invalid requests and skip
		if !urlsAccessible {
			log.Printf("Video URLs not accessible for request ID: %s", videoRequestID)
			videoRequestIDs = append(videoRequestIDs, videoRequestID)
			continue
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

	// Mark invalid video requests if any were found
	if len(videoRequestIDs) > 0 {
		log.Printf("Marking %d video requests as invalid: %v", len(videoRequestIDs), videoRequestIDs)
		
		// Process in batches of 10 if needed (API limit)
		for i := 0; i < len(videoRequestIDs); i += 10 {
			end := i + 10
			if end > len(videoRequestIDs) {
				end = len(videoRequestIDs)
			}
			
			batch := videoRequestIDs[i:end]
			result, err := ayoClient.MarkVideoRequestsInvalid(batch)
			if err != nil {
				log.Printf("Error marking video requests as invalid: %v", err)
			} else {
				log.Printf("Successfully marked batch of video requests as invalid: %v", result)
			}
		}
	}
}
