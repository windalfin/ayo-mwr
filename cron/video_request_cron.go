package cron

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
	"ayo-mwr/transcode"

	"github.com/robfig/cron/v3"
)

// StartVideoRequestCron initializes a cron job that runs every 30 minutes to:
// 1. Get pending video requests from AYO API
// 2. Check if video exists in database by unique_id
// 3. Send video info to AYO API if it exists
func StartVideoRequestCron(cfg *config.Config) {
	go func() {
		// Initialize database
		dbPath := cfg.DatabasePath
		db, err := database.NewSQLiteDB(dbPath)
		if err != nil {
			log.Printf("Error initializing database: %v", err)
			return
		}
		// defer db.Close()

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
		_, err = schedule.AddFunc("@every 2m", func() {
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
func processVideoRequests(cfg *config.Config, db database.Database, ayoClient *api.AyoIndoClient, r2Client *storage.R2Storage) {
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
			videoRequestIDs = append(videoRequestIDs, videoRequestID)
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

		// Upload video files to R2 if they haven't been uploaded yet
		var r2HlsURL, r2MP4URL string
		
		// Get the video path
		videoPath := matchingVideo.LocalPath
		if videoPath == "" {
			log.Printf("No local video path found for unique_id: %s", uniqueID)
			videoRequestIDs = append(videoRequestIDs, videoRequestID)
			continue
		}

		// Check if file exists
		if _, err := os.Stat(videoPath); os.IsNotExist(err) {
			log.Printf("Video file does not exist at path: %s", videoPath)
			videoRequestIDs = append(videoRequestIDs, videoRequestID)
			continue
		}

		// Buat direktori HLS untuk video ini di folder hls
		hlsParentDir := filepath.Join(cfg.StoragePath, "hls")
		os.MkdirAll(hlsParentDir, 0755)
		hlsDir := filepath.Join(hlsParentDir, uniqueID)
		hlsURL := ""
		r2HLSPath := fmt.Sprintf("hls/%s", uniqueID) // Path di R2 storage
		
		// Buat HLS stream dari video menggunakan ffmpeg
		log.Printf("Generating HLS stream in: %s", hlsDir)
		if err := transcode.GenerateHLS(videoPath, hlsDir, uniqueID, cfg); err != nil {
			log.Printf("Warning: Failed to create HLS stream: %v", err)
			// Use existing R2 URL if HLS generation fails
			r2HlsURL = matchingVideo.R2HLSURL
		} else {
			// Format HLS URL untuk server lokal yang sudah di-setup di api/server.go
			baseURL := cfg.BaseURL
			if baseURL == "" {
				baseURL = "http://localhost:8080" // Fallback if not configured
			}
			hlsURL = fmt.Sprintf("%s/hls/%s/master.m3u8", baseURL, uniqueID)
			log.Printf("HLS stream created at: %s", hlsDir)
			log.Printf("HLS stream can be accessed at: %s", hlsURL)
			
			// Upload HLS ke R2
			_, r2HlsURLTemp, err := r2Client.UploadHLSStream(hlsDir, uniqueID)
			if err != nil {
				log.Printf("Warning: Failed to upload HLS stream to R2: %v", err)
				// Use existing R2 URL if upload fails
				r2HlsURL = matchingVideo.R2HLSURL
			} else {
				r2HlsURL = r2HlsURLTemp
				log.Printf("HLS stream uploaded to R2: %s", r2HlsURL)
			}
			
			// Update database with HLS path and URL information
			// First update the R2 paths
			err = db.UpdateVideoR2Paths(matchingVideo.ID, r2HLSPath, matchingVideo.R2MP4Path)
			if err != nil {
				log.Printf("Warning: Failed to update HLS R2 paths in database: %v", err)
			}
			
			// Then update the R2 URLs
			err = db.UpdateVideoR2URLs(matchingVideo.ID, r2HlsURL, matchingVideo.R2MP4URL)
			if err != nil {
				log.Printf("Warning: Failed to update HLS R2 URLs in database: %v", err)
			}
			
			// Update the full video metadata to include local HLS path
			matchingVideo.HLSPath = hlsDir
			matchingVideo.HLSURL = hlsURL
			matchingVideo.R2HLSURL = r2HlsURL
			matchingVideo.R2HLSPath = r2HLSPath
			err = db.UpdateVideo(*matchingVideo)
			if err != nil {
				log.Printf("Warning: Failed to update video metadata in database: %v", err)
			}
		}

		// Upload MP4 to R2 if local video exists
		if matchingVideo.LocalPath != "" {
			mp4Path := fmt.Sprintf("mp4/%s.mp4", uniqueID)
			_, err = r2Client.UploadFile(matchingVideo.LocalPath, mp4Path)
			if err != nil {
				log.Printf("Warning: Failed to upload video to R2: %v", err)
				// Use existing R2 URL if upload fails
				r2MP4URL = matchingVideo.R2MP4URL
			} else {
				// Generate URL using custom domain
				r2MP4URL = fmt.Sprintf("%s/%s", r2Client.GetBaseURL(), mp4Path)
				log.Printf("Video uploaded to custom URL: %s", r2MP4URL)
			}
		} else {
			r2MP4URL = matchingVideo.R2MP4URL
		}

		// Update database with R2 URLs if they were uploaded successfully
		if r2HlsURL != matchingVideo.R2HLSURL || r2MP4URL != matchingVideo.R2MP4URL {
			err = db.UpdateVideoR2URLs(matchingVideo.ID, r2HlsURL, r2MP4URL)
			if err != nil {
				log.Printf("Warning: Failed to update video URLs in database: %v", err)
			} else {
				log.Printf("Updated video URLs in database for unique_id: %s", uniqueID)
			}
		}

		// Send video data to AYO API
		result, err := ayoClient.SaveVideo(
			videoRequestID,
			bookingID,
			matchingVideo.VideoType, // Assuming "clip" as video type, adjust if needed
			r2HlsURL,
			r2MP4URL,
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
