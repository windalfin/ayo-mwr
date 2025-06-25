package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/recording"
	"ayo-mwr/service"
	"ayo-mwr/storage"

	"github.com/robfig/cron/v3"
	"golang.org/x/sync/semaphore"
)

// Semua helper function telah dipindahkan ke BookingVideoService

// getBookingJSON mengkonversi map ke string JSON
func getBookingJSON(booking map[string]interface{}) string {
	jsonBytes, err := json.Marshal(booking)
	if err != nil {
		log.Printf("processBookings : Error marshaling booking to JSON: %v", err)
		return ""
	}
	return string(jsonBytes)
}

// StartBookingVideoCron initializes a cron job that runs every 30 minutes to:
// 1. Get bookings from AYO API
// 2. Generate videos for bookings from all cameras
// 3. Add watermarks
// 4. Upload to R2 storage
// 5. Save to database
func StartBookingVideoCron(cfg *config.Config) {
	go func() {
		// Initialize database
		dbPath := cfg.DatabasePath
		db, err := database.NewSQLiteDB(dbPath)
		if err != nil {
			log.Printf("processBookings : Error initializing database: %v", err)
			return
		}
		// Removed defer db.Close() so database remains open for scheduled cron jobs

		// Initialize AYO API client
		ayoClient, err := api.NewAyoIndoClient()
		if err != nil {
			log.Printf("processBookings : Error initializing AYO API client: %v", err)
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
			log.Printf("processBookings : Error initializing R2 storage client: %v", err)
			return
		}

		// Initialize booking video service
		bookingVideoService := service.NewBookingVideoService(db, ayoClient, r2Client, cfg)

		// Initial delay before first run (10 seconds)
		time.Sleep(10 * time.Second)

		// Run immediately once at startup
		processBookings(cfg, db, ayoClient, r2Client, bookingVideoService)

		// Start the booking video cron
		schedule := cron.New()

		// Schedule the task every minute for testing
		// In production, you'd use a more reasonable interval like "@every 30m"
		_, err = schedule.AddFunc("@every 2m", func() {
			processBookings(cfg, db, ayoClient, r2Client, bookingVideoService)
		})
		if err != nil {
			log.Fatalf("Error scheduling booking video cron: %v", err)
		}

		schedule.Start()
		log.Println("processBookings : Booking video processing cron job started - will run every 1 minute (testing mode)")
	}()
}

// processBookings handles fetching bookings and processing them
func processBookings(cfg *config.Config, db database.Database, ayoClient *api.AyoIndoClient, r2Client *storage.R2Storage, bookingService *service.BookingVideoService) {
	log.Println("processBookings : Running booking video processing task...")

	// Use fixed date for testing
	today := time.Now().Format("2006-01-02")
	// today := "2025-04-30" // Fixed date for testing purposes

	// Get bookings from AYO API
	response, err := ayoClient.GetBookings(today)
	if err != nil {
		log.Printf("processBookings : Error fetching bookings from API: %v", err)
		return
	}

	// Extract data from response
	data, ok := response["data"].([]interface{})
	if !ok || len(data) == 0 {
		log.Println("processBookings : No bookings found for today or invalid response format")
		return
	}

	log.Printf("processBookings : Found %d bookings for today", len(data))

	// Process bookings concurrently with a limit on parallelism
	const maxConcurrent = 5 // Maximum number of concurrent booking processing
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(maxConcurrent)

	// Process each booking
	for _, item := range data {
		// Extract booking details from map

		booking, ok := item.(map[string]interface{})
		if !ok {
			log.Printf("processBookings : Invalid booking format: %v", item)
			continue
		}

		// Extract fields from booking map
		orderDetailID, _ := booking["order_detail_id"].(float64)
		bookingID, _ := booking["booking_id"].(string)
		date, _ := booking["date"].(string)
		startTimeStr, _ := booking["start_time"].(string)
		endTimeStr, _ := booking["end_time"].(string)
		statusVal, _ := booking["status"].(string)
		status := strings.ToLower(statusVal) // convert to lowercase
		field_id, _ := booking["field_id"].(float64)

		// date := "2025-05-05T00:00:00Z"
		// endTimeStr := "06:00:00"
		// startTimeStr := "05:00:00"

		log.Printf("processBookings : Processing booking %s (Order Detail ID: %d)", bookingID, int(orderDetailID))

		// akan menggunakan kode parsing date di bawah untuk menghindari duplikasi

		// 2. Check if there's already a video with status 'ready' for this booking
		existingVideos, err := db.GetVideosByBookingID(bookingID)
		if err != nil {
			log.Printf("processBookings : Error checking existing videos for booking %s: %v", bookingID, err)
		} else {
			hasReadyVideo := false
			for _, video := range existingVideos {
				log.Printf("video.Status %s", video.Status)

				if (video.Status == database.StatusReady || video.Status == database.StatusUploading || video.Status == database.StatusInitial) && video.VideoType == "full" {
					hasReadyVideo = true
					break
				}
			}
			if hasReadyVideo {
				log.Printf("processBookings : Skipping booking %s: already has a video with 'ready' status", bookingID)
				if status == "cancelled" {
					// update status to cancelled
					db.UpdateVideoStatus(existingVideos[0].ID, database.StatusCancelled, "Cancel from api")
					log.Printf("processBookings : Booking %s is cancelled, updating status to 'cancelled'", bookingID)
				}
				continue
			}
		}
		if status != "success" {
			log.Printf("processBookings : Booking %s is not success, skipping processing", bookingID)
			continue
		}

		// Convert date and time strings to time.Time objects
		// Try parsing as ISO format first (2006-01-02T15:04:05Z)
		bookingDate, err := time.Parse(time.RFC3339, date)
		if err != nil {
			// Fall back to simple date format
			bookingDate, err = time.Parse("2006-01-02", date)
			if err != nil {
				log.Printf("processBookings : Invalid date format %s for booking %s: %v", date, bookingID, err)
				continue
			}
		}

		// Combine date and time using service
		startTime, err := bookingService.CombineDateTime(bookingDate, startTimeStr)
		if err != nil {
			log.Printf("processBookings : Error parsing start time for booking %s: %v", bookingID, err)
			continue
		}

		endTime, err := bookingService.CombineDateTime(bookingDate, endTimeStr)
		if err != nil {
			log.Printf("processBookings : Error parsing end time for booking %s: %v", bookingID, err)
			continue
		}

		// 1. Check if current time is after booking end time
		// Calculate timezone offset dynamically
		localNow := time.Now()
		_, localOffset := localNow.Zone()
		localOffsetHours := time.Duration(localOffset) * time.Second

		// Get current time in UTC and add the local timezone offset
		now := time.Now().UTC().Add(localOffsetHours)

		// Print raw times with zones for debugging
		log.Printf("processBookings : DEBUG - Comparing times - Now: %s (%s) vs EndTime: %s (%s)",
			now.Format("2006-01-02 15:04:05"), now.Location(),
			endTime.Format("2006-01-02 15:04:05"), endTime.Location())

		// Direct comparison without conversion
		if now.After(endTime) {
			log.Printf("processBookings : Booking %s end time (%s) is in the past, proceeding with processing. Current time: %s",
				bookingID, endTime.Format("2006-01-02 15:04:05 -0700"), now.Format("2006-01-02 15:04:05 -0700"))
		} else {
			// Skip bookings that haven't ended yet
			log.Printf("processBookings : Skipping booking %s: booking end time (%s) is in the future, because now is %s",
				bookingID, endTime.Format("2006-01-02 15:04:05 -0700"), now.Format("2006-01-02 15:04:05 -0700"))
			continue
		}

		// The log message was moved to the condition above

		// Venue code tidak digunakan lagi karena sudah diakses melalui service

		// Loop through all cameras for this booking
		log.Printf("processBookings : Processing booking %s in timeframe %s to %s for all cameras",
			bookingID, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

		// Acquire semaphore before processing this booking
		if err := sem.Acquire(context.Background(), 1); err != nil {
			log.Printf("processBookings : Error acquiring semaphore for booking %s: %v", bookingID, err)
			continue
		}

		// Process this booking in a separate goroutine
		wg.Add(1)
		go func(booking map[string]interface{}, bookingID string, startTime, endTime time.Time,
			orderDetailID float64, localOffsetHours time.Duration, field_id float64) {
			defer wg.Done()
			defer sem.Release(1) // Release semaphore when done

			// Track successful camera count
			camerasWithVideo := 0
			videoType := "full"
			// Process each camera
			for _, camera := range cfg.Cameras {
				// Skip disabled cameras
				// if !camera.Enabled {
				// 	log.Printf("processBookings : Skipping disabled camera: %s", camera.Name)
				// 	continue
				// }
				// log.Printf(camera)
				cameraField, err := strconv.Atoi(camera.Field)
				if err != nil || cameraField != int(field_id) {
					log.Printf("processBookings : Skipping camera %s for booking %s", camera.Name, bookingID)
					log.Println("camera.Field", camera.Field)
					log.Println("field_id", strconv.Itoa(int(field_id)))
					continue
				}

				log.Printf("processBookings : Checking camera %s for booking %s", camera.Name, bookingID)
				BaseDir := filepath.Join(cfg.StoragePath, "recordings", camera.Name)
				// Find video segments directory for this camera
				videoDirectory := filepath.Join(BaseDir, "mp4")

				// Check if directory exists
				if _, err := os.Stat(videoDirectory); os.IsNotExist(err) {
					log.Printf("processBookings : No video directory found for camera %s", camera.Name)
					continue
				}

				// Find segments for this camera in the time range
				segments, err := recording.FindSegmentsInRange(videoDirectory, startTime, endTime)
				if err != nil || len(segments) == 0 {
					log.Printf("processBookings : No video segments found for booking %s on camera %s: %v", bookingID, camera.Name, err)
					continue
				}

				log.Printf("processBookings : Found %d video segments for booking %s on camera %s", len(segments), bookingID, camera.Name)

				// Convert orderDetailID to string
				orderDetailIDStr := strconv.Itoa(int(orderDetailID))

				// Process video segments using service
				log.Printf("processBookings : orderDetailIDStr %s", orderDetailIDStr)
				uniqueID, err := bookingService.ProcessVideoSegments(
					camera,
					bookingID,
					orderDetailIDStr,
					segments,
					startTime,
					endTime,
					getBookingJSON(booking), // rawJSON - contains the full booking JSON
					videoType,
				)

				if err != nil {
					log.Printf("processBookings : Error processing video segments for booking %s on camera %s: %v", bookingID, camera.Name, err)
					continue
				}
				log.Printf("processBookings : uniqueID %s", uniqueID)

				// Ambil path file watermarked yang akan digunakan
				watermarkedVideoPath := filepath.Join(BaseDir, "tmp", "watermark", uniqueID+".mp4")
				log.Printf("processBookings : watermarkedVideoPath %s", watermarkedVideoPath)
				// Upload processed video
				// hlsPath dan hlsURL tidak dikirim ke API tapi tetap disimpan di database
				previewURL, thumbnailURL, err := bookingService.UploadProcessedVideo(
					uniqueID,
					watermarkedVideoPath,
					bookingID,
					camera.Name,
				)

				if err != nil {
					log.Printf("processBookings : Error uploading processed video for booking %s on camera %s: %v", bookingID, camera.Name, err)
					// Update status to failed
					db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("Upload failed: %v", err))
					continue
				}
				log.Printf("processBookings : previewURL %s", previewURL)
				log.Printf("processBookings : thumbnailURL %s", thumbnailURL)
				// Notify AYO API of successful upload
				var startTimeBooking = startTime.Add(localOffsetHours * -1)
				var endTimeBooking = endTime.Add(localOffsetHours * -1)
				err = bookingService.NotifyAyoAPI(
					bookingID,
					uniqueID,
					previewURL,
					thumbnailURL,
					startTimeBooking,
					endTimeBooking,
					videoType,
				)

				if err != nil {
					db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("Notify AYO API failed: %v", err))
					log.Printf("processBookings : Error notifying AYO API of successful upload for booking %s on camera %s: %v", bookingID, camera.Name, err)
				}
				log.Printf("processBookings : Notify AYO API of successful upload for booking %s on camera %s", bookingID, camera.Name)
				// Cleanup temporary files after successful processing
				// bookingService.CleanupTemporaryFiles(
				// 	mergedVideoPath,
				// 	watermarkedVideoPath,
				// 	previewVideoPath,
				// 	thumbnailPath,
				// )

				// Increment counter for successful camera processing
				camerasWithVideo++

				log.Printf("processBookings : Successfully processed and uploaded video for booking %s on camera %s (ID: %s)", bookingID, camera.Name, uniqueID)
			}

			// Log summary of camera processing
			if camerasWithVideo > 0 {
				log.Printf("processBookings : Successfully processed %d cameras for booking %s", camerasWithVideo, bookingID)
			} else {
				log.Printf("processBookings : No camera videos found for booking %s in the specified time range", bookingID)
			}
		}(booking, bookingID, startTime, endTime, orderDetailID, localOffsetHours, field_id) // Pass variables to goroutine
	}

	// Wait for all booking processing goroutines to complete
	wg.Wait()
	log.Println("processBookings : Booking video processing task completed")
}

// Semua fungsi helper sudah dipindahkan ke BookingVideoService di service/booking_video.go
