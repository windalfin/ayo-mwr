package cron

import (
	"context"
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
	"ayo-mwr/offline"
	"ayo-mwr/recording"
	"ayo-mwr/service"
	"ayo-mwr/storage"

	"github.com/robfig/cron/v3"
	"golang.org/x/sync/semaphore"
)

// Semua helper function telah dipindahkan ke BookingVideoService

// isRetryableError menentukan apakah error layak untuk di-retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// RETRY SEMUA ERROR - karena mayoritas error bisa temporary
	// Hanya skip retry untuk error yang benar-benar nil
	log.Printf("🔄 RETRY: Will retry error: %v", err)
	return true
}

// cleanRetryWithBackoff melakukan retry dengan logging yang bersih dan emoji
func cleanRetryWithBackoff(operation func() error, maxRetries int, operationName string) error {
	var lastErr error
	var retryCount int

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := operation()
		if err == nil {
			// Hanya log jika ada retry sebelumnya
			if retryCount > 0 {
				log.Printf("🔄 RETRY: %s ✅ Berhasil setelah %d kali retry", operationName, retryCount)
			}
			return nil
		}

		lastErr = err

		// Log retry attempt - semua error akan di-retry
		isRetryableError(err) // Ini akan log error yang akan di-retry

		// Increment retry count untuk semua attempts setelah yang pertama
		if attempt > 1 {
			retryCount++
		}

		// Jika masih ada percobaan lagi, tunggu tanpa log noise
		if attempt < maxRetries {
			waitTime := time.Duration(3*attempt) * time.Second
			time.Sleep(waitTime)
		}
	}

	// Log final failure dengan summary
	log.Printf("🔄 RETRY: %s ❌ Gagal setelah %d percobaan: %v", operationName, maxRetries, lastErr)
	return fmt.Errorf("%s gagal setelah %d percobaan: %v", operationName, maxRetries, lastErr)
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

		// Initialize upload service
		uploadService := service.NewUploadService(db, r2Client, cfg, ayoClient)

		// Initialize queue manager for offline capabilities
		log.Printf("📦 OFFLINE QUEUE: Initializing offline queue system for booking video cron...")
		queueManager := offline.NewQueueManager(db, uploadService, r2Client, ayoClient, cfg)
		queueManager.Start()
		log.Printf("📦 OFFLINE QUEUE: ✅ Offline queue system started successfully for cron")

		// Initialize booking video service
		bookingVideoService := service.NewBookingVideoService(db, ayoClient, r2Client, cfg)

		// Initial delay before first run (10 seconds)
		time.Sleep(10 * time.Second)

		// Run immediately once at startup
		processBookings(cfg, db, ayoClient, r2Client, bookingVideoService, queueManager, uploadService)

		// Start the booking video cron
		schedule := cron.New()

		// Schedule the task every minute for testing
		// In production, you'd use a more reasonable interval like "@every 30m"
		_, err = schedule.AddFunc("@every 2m", func() {
			processBookings(cfg, db, ayoClient, r2Client, bookingVideoService, queueManager, uploadService)
		})
		if err != nil {
			log.Fatalf("Error scheduling booking video cron: %v", err)
		}

		schedule.Start()
		log.Println("processBookings : Booking video processing cron job started - will run every 1 minute (testing mode)")
	}()
}

// processBookings handles fetching bookings from database and processing them
func processBookings(cfg *config.Config, db database.Database, ayoClient *api.AyoIndoClient, r2Client *storage.R2Storage, bookingService *service.BookingVideoService, queueManager *offline.QueueManager, uploadService *service.UploadService) {
	log.Println("processBookings : Running booking video processing task...")

	// Use fixed date for testing
	today := time.Now().Format("2006-01-02")
	// today := "2025-04-30" // Fixed date for testing purposes

	// Get bookings from database by date
	bookingsData, err := db.GetBookingsByDate(today)
	if err != nil {
		log.Printf("processBookings : Error fetching bookings from database: %v", err)
		return
	}

	if len(bookingsData) == 0 {
		log.Println("processBookings : No bookings found for today in database")
		return
	}

	log.Printf("processBookings : Found %d bookings for today in database", len(bookingsData))

	// Process bookings concurrently with a limit on parallelism
	const maxConcurrent = 2 // Maximum number of concurrent booking processing
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(maxConcurrent)

	// Process each booking from database
	for _, bookingItem := range bookingsData {
		// Extract booking details from database struct
		orderDetailID := float64(bookingItem.OrderDetailID)
		bookingID := bookingItem.BookingID
		date := bookingItem.Date
		startTimeStr := bookingItem.StartTime
		endTimeStr := bookingItem.EndTime
		status := strings.ToLower(bookingItem.Status) // convert to lowercase
		field_id := float64(bookingItem.FieldID)
		bookingSource := bookingItem.BookingSource

		log.Printf("processBookings : Processing booking from DB: %s (Status: %s, Source: %s)", bookingID, status, bookingSource)

		// 2. THEN: Continue with video processing logic
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
		// Handle cancelled bookings - update video status if exists
		if status == "cancelled" || status == "canceled" {
			existingVideos, err := db.GetVideosByBookingID(bookingID)
			if err != nil {
				log.Printf("processBookings : Error checking existing videos for cancelled booking %s: %v", bookingID, err)
			} else if len(existingVideos) > 0 {
				// Update all videos for this booking to cancelled status
				for _, video := range existingVideos {
					if video.Status != database.StatusCancelled {
						err := db.UpdateVideoStatus(video.ID, database.StatusCancelled, "Booking cancelled via API")
						if err != nil {
							log.Printf("processBookings : Error updating video status to cancelled for booking %s: %v", bookingID, err)
						} else {
							log.Printf("📅 BOOKING: Updated video %s to cancelled status for booking %s", video.ID, bookingID)
						}
					}
				}
			}
			log.Printf("processBookings : Booking %s is cancelled, skipping video processing", bookingID)
			continue
		}

		if status != "success" {
			log.Printf("processBookings : Booking %s status is '%s', skipping video processing", bookingID, status)
			continue
		}

		// Convert date and time strings to time.Time objects
		// Try parsing as ISO format first (2006-01-02T15:04:05Z)
		bookingDate, err := time.ParseInLocation(time.RFC3339, date, time.Local)
		if err != nil {
			// Fall back to simple date format
			bookingDate, err = time.ParseInLocation("2006-01-02", date, time.Local)
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
		// Add a 3-minute tolerance to endTime for processing
		tolerantEndTime := endTime.Add(3 * time.Minute)
		if now.After(tolerantEndTime) {
			log.Printf("processBookings : Booking %s end time (%s) with 3-min tolerance (%s) is in the past, proceeding with processing. Current time: %s",
				bookingID, endTime.Format("2006-01-02 15:04:05 -0700"), tolerantEndTime.Format("2006-01-02 15:04:05 -0700"), now.Format("2006-01-02 15:04:05 -0700"))
		} else {
			// Skip bookings that haven't ended yet
			log.Printf("processBookings : Skipping booking %s: booking end time (%s) with 3-min tolerance (%s) is in the future, because now is %s",
				bookingID, endTime.Format("2006-01-02 15:04:05 -0700"), tolerantEndTime.Format("2006-01-02 15:04:05 -0700"), now.Format("2006-01-02 15:04:05 -0700"))
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
		go func(bookingData database.BookingData, bookingID string, startTime, endTime time.Time,
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
				videoDirectory := filepath.Join(BaseDir, "hls")

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

				// Process video segments using service with retry logic
				log.Printf("processBookings : orderDetailIDStr %s", orderDetailIDStr)

				var uniqueID string
				err = cleanRetryWithBackoff(func() error {
					var err error
					uniqueID, err = bookingService.ProcessVideoSegments(
						camera,
						bookingID,
						orderDetailIDStr,
						segments,
						startTime,
						endTime,
						bookingData.RawJSON, // rawJSON from database
						videoType,
					)
					return err
				}, 3, fmt.Sprintf("Video Processing for %s-%s", bookingID, camera.Name))

				if err != nil {
					log.Printf("processBookings : Error processing video segments for booking %s on camera %s after retries: %v", bookingID, camera.Name, err)
					// Update status to failed
					db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("Video processing failed: %v", err))
					continue
				}
				log.Printf("processBookings : uniqueID %s", uniqueID)

				// Ambil path file watermarked yang akan digunakan
				watermarkedVideoPath := filepath.Join(BaseDir, "tmp", "watermark", uniqueID+".mp4")
				log.Printf("processBookings : watermarkedVideoPath %s", watermarkedVideoPath)

				// Get paths to processed files
				previewPath := filepath.Join(BaseDir, "tmp", "preview", uniqueID+".mp4")
				thumbnailPath := filepath.Join(BaseDir, "tmp", "thumbnail", uniqueID+".png")

				// Check internet connectivity
				connectivityChecker := offline.NewConnectivityChecker()

				var previewURL, thumbnailURL string

				if connectivityChecker.IsOnline() {
					log.Printf("🌐 CONNECTIVITY: Online - mencoba upload langsung untuk %s-%s...", bookingID, camera.Name)

					// Upload processed video with retry logic
					// hlsPath dan hlsURL tidak dikirim ke API tapi tetap disimpan di database
					err = cleanRetryWithBackoff(func() error {
						var err error
						previewURL, thumbnailURL, err = bookingService.UploadProcessedVideo(
							uniqueID,
							watermarkedVideoPath,
							bookingID,
							camera.Name,
						)
						return err
					}, 5, fmt.Sprintf("File Upload for %s-%s", bookingID, camera.Name))

					if err != nil {
						log.Printf("⚠️ WARNING: Direct upload failed for %s-%s: %v", bookingID, camera.Name, err)
						log.Printf("📦 QUEUE: Menambahkan task upload ke offline queue...")

						// Add to offline queue
						err = queueManager.EnqueueR2Upload(
							uniqueID,
							watermarkedVideoPath,
							previewPath,
							thumbnailPath,
							fmt.Sprintf("mp4/%s.mp4", uniqueID),
							fmt.Sprintf("preview/%s.mp4", uniqueID),
							fmt.Sprintf("thumbnail/%s.png", uniqueID),
						)

						if err != nil {
							log.Printf("❌ ERROR: Failed to add upload task to queue: %v", err)
							db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("Upload failed and queue error: %v", err))
							continue
						}

						// Update status to uploading (will be processed by queue)
						db.UpdateVideoStatus(uniqueID, database.StatusUploading, "")
						log.Printf("📦 QUEUE: Upload task queued for video %s", uniqueID)
						continue
					}

					log.Printf("📤 SUCCESS: Direct upload completed for %s-%s", bookingID, camera.Name)
				} else {
					log.Printf("🌐 CONNECTIVITY: Offline - menambahkan upload task ke queue untuk %s-%s...", bookingID, camera.Name)

					// Add to offline queue since we're offline
					err = queueManager.EnqueueR2Upload(
						uniqueID,
						watermarkedVideoPath,
						previewPath,
						thumbnailPath,
						fmt.Sprintf("mp4/%s.mp4", uniqueID),
						fmt.Sprintf("preview/%s.mp4", uniqueID),
						fmt.Sprintf("thumbnail/%s.png", uniqueID),
					)

					if err != nil {
						log.Printf("❌ ERROR: Failed to add upload task to queue: %v", err)
						db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("Offline queue error: %v", err))
						continue
					}

					// Get video to calculate duration for future notification
					video, err := db.GetVideo(uniqueID)
					var duration float64 = 60.0 // Default 60 seconds
					if err == nil && video != nil {
						duration = video.Duration
					}

					// Add API notification to queue as well (will be processed after upload completes)
					err = queueManager.EnqueueAyoAPINotify(
						uniqueID,
						uniqueID,
						"", // MP4 URL will be updated when upload completes
						"", // Preview URL will be updated when upload completes
						"", // Thumbnail URL will be updated when upload completes
						duration,
					)

					if err != nil {
						log.Printf("❌ ERROR: Failed to add API notification task to queue: %v", err)
					} else {
						log.Printf("📦 QUEUE: API notification task queued untuk video %s", uniqueID)
					}

					// Update status to uploading (will be processed by queue when online)
					db.UpdateVideoStatus(uniqueID, database.StatusUploading, "")
					log.Printf("📦 QUEUE: Upload task queued untuk video %s - akan diproses saat online", uniqueID)
					continue
				}
				log.Printf("processBookings : previewURL %s", previewURL)
				log.Printf("processBookings : thumbnailURL %s", thumbnailURL)

				// Notify AYO API of successful upload with retry logic
				// Get video to calculate duration
				video, err := db.GetVideo(uniqueID)
				var duration float64 = 60.0 // Default 60 seconds
				if err == nil && video != nil {
					duration = video.Duration
				}

				err = cleanRetryWithBackoff(func() error {
					return uploadService.NotifyAyoAPI(
						uniqueID,
						"", // mp4URL will be filled by queue manager if needed
						previewURL,
						thumbnailURL,
						duration,
					)
				}, 3, fmt.Sprintf("API Notification for %s-%s", bookingID, camera.Name))

				if err != nil {
					log.Printf("⚠️ WARNING: Direct API notification failed for %s-%s: %v", bookingID, camera.Name, err)
					log.Printf("📦 QUEUE: Menambahkan task notifikasi API ke offline queue...")

					// Add API notification to queue
					err = queueManager.EnqueueAyoAPINotify(
						uniqueID,
						uniqueID,
						"", // MP4 URL will be updated when available
						previewURL,
						thumbnailURL,
						duration,
					)

					if err != nil {
						log.Printf("❌ ERROR: Failed to add API notification task to queue: %v", err)
						db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("API notification failed and queue error: %v", err))
					} else {
						log.Printf("📦 QUEUE: API notification task queued for video %s", uniqueID)
					}
				} else {
					log.Printf("🔔 SUCCESS: Direct API notification sent for %s-%s", bookingID, camera.Name)
				}
				// Cleanup temporary files after successful processing
				// bookingService.CleanupTemporaryFiles(
				// 	mergedVideoPath,
				// 	watermarkedVideoPath,
				// 	previewVideoPath,
				// 	thumbnailPath,
				// )

				// Increment counter for successful camera processing
				camerasWithVideo++

				log.Printf("🎉 SUCCESS: Completed processing for booking %s on camera %s (ID: %s)", bookingID, camera.Name, uniqueID)
			}

			// Log summary of camera processing
			if camerasWithVideo > 0 {
				log.Printf("processBookings : Successfully processed %d cameras for booking %s", camerasWithVideo, bookingID)
			} else {
				log.Printf("processBookings : No camera videos found for booking %s in the specified time range", bookingID)
			}
		}(bookingItem, bookingID, startTime, endTime, orderDetailID, localOffsetHours, field_id) // Pass variables to goroutine
	}

	// Wait for all booking processing goroutines to complete
	wg.Wait()
	log.Println("processBookings : Booking video processing task completed")
}

// Semua fungsi helper sudah dipindahkan ke BookingVideoService di service/booking_video.go
