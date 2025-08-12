package api

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/offline"
	"ayo-mwr/service"
	"ayo-mwr/storage"

	"github.com/gin-gonic/gin"
)

// Request struct for the process booking video endpoint
type ProcessBookingVideoRequest struct {
	FieldID    int    `json:"field_id" binding:"required"`
	CameraName string `json:"camera_name,omitempty"` // optional camera name override
}

// Response struct for the API
type ApiResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// getBookingJSON converts a booking map to JSON string
// getBookingJSON function removed - now using RawJSON from database

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

// BookingVideoRequestHandler handles booking video processing requests
type BookingVideoRequestHandler struct {
	config           *config.Config
	db               database.Database
	r2Storage        *storage.R2Storage
	uploadService    *service.UploadService
	queueManager     *offline.QueueManager
	hybridProcessor  *service.HybridVideoProcessor
	storageManager   *storage.DiskManager

	// Rate limiting untuk mencegah klik berkali-kali dalam jangka waktu tertentu
	lastRequestMutex sync.RWMutex
	lastRequestTime  map[int]time.Time // Menyimpan waktu permintaan terakhir untuk setiap field_id
	rateLimit        time.Duration     // Durasi minimum antara permintaan untuk field_id yang sama
}

// NewBookingVideoRequestHandler creates a new booking video request handler instance
func NewBookingVideoRequestHandler(cfg *config.Config, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService, storageManager *storage.DiskManager) *BookingVideoRequestHandler {
	log.Printf("📦 OFFLINE QUEUE: Initializing offline queue system...")

	// Initialize AYO client for queue manager
	ayoClient, err := NewAyoIndoClient()
	if err != nil {
		log.Printf("❌ ERROR: Failed to initialize AYO client for queue manager: %v", err)
		// Use a dummy client or handle this error appropriately
	}

	// Initialize queue manager
	queueManager := offline.NewQueueManager(db, uploadService, r2Storage, ayoClient, cfg)
	queueManager.Start()

	// Initialize hybrid video processor for chunk optimization
	hybridProcessor := service.NewHybridVideoProcessor(db, cfg, storageManager)

	log.Printf("📦 OFFLINE QUEUE: ✅ Offline queue system started successfully")
	log.Printf("🌐 CONNECTIVITY: System will automatically handle online/offline transitions")
	log.Printf("🚀 HYBRID PROCESSOR: ✅ Chunk optimization system initialized")

	return &BookingVideoRequestHandler{
		config:          cfg,
		db:              db,
		r2Storage:       r2Storage,
		uploadService:   uploadService,
		queueManager:    queueManager,
		hybridProcessor: hybridProcessor,
		storageManager:  storageManager,
		lastRequestTime: make(map[int]time.Time),
		rateLimit:       30 * time.Second, // Rate limit 30 detik
	}
}

// ProcessBookingVideo handles the POST /api/request-booking-video endpoint
// It processes booking videos for a specific field_id for the last minute
func (h *BookingVideoRequestHandler) ProcessBookingVideo(c *gin.Context) {
	var request ProcessBookingVideoRequest

	// Parse request body
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Success: false,
			Message: "Invalid request: " + err.Error(),
		})
		return
	}

	// Get field_id from request
	fieldID := request.FieldID

	// Check rate limiting untuk mencegah klik berkali-kali dalam jangka waktu tertentu
	h.lastRequestMutex.RLock()
	lastRequest, exists := h.lastRequestTime[fieldID]
	h.lastRequestMutex.RUnlock()

	if exists {
		timeElapsed := time.Since(lastRequest)
		if timeElapsed < h.rateLimit {
			timeRemaining := h.rateLimit - timeElapsed
			c.JSON(http.StatusTooManyRequests, ApiResponse{
				Success: false,
				Message: fmt.Sprintf("Harap tunggu %d detik sebelum meminta video lagi", int(timeRemaining.Seconds())),
				Data: map[string]interface{}{
					"wait_time_seconds": int(timeRemaining.Seconds()),
					"field_id":          fieldID,
				},
			})
			return
		}
	}

	// Update waktu permintaan terakhir untuk field_id ini
	h.lastRequestMutex.Lock()
	h.lastRequestTime[fieldID] = time.Now()
	h.lastRequestMutex.Unlock()

	log.Printf("Processing video for field_id: %d", fieldID)

	// Determine target camera. Prefer camera_name when provided for precise mapping.
	var targetCamera *config.CameraConfig

	if request.CameraName != "" {
		for i := range h.config.Cameras {
			if h.config.Cameras[i].Name == request.CameraName && h.config.Cameras[i].Enabled {
				targetCamera = &h.config.Cameras[i]
				log.Printf("📹 CAMERA: Selected by name: %s", request.CameraName)
				break
			}
		}
		if targetCamera == nil {
			log.Printf("⚠️ WARNING: camera_name '%s' not found, falling back to field_id mapping", request.CameraName)
		}
	}

	// If still not found, fall back to field_id based lookup
	log.Printf("Looking for camera with field_id: %d, found %d cameras in config", fieldID, len(h.config.Cameras))

	// --- Step 1: try name (already attempted above) ---

	// --- Step 2: if still nil, try field_id mapping ---
	if targetCamera == nil {
		for i, camera := range h.config.Cameras {
			// Log camera details for debugging
			log.Printf("Camera %d: Name=%s, Field=%s, Enabled=%v", i, camera.Name, camera.Field, camera.Enabled)
			cameraField, err := strconv.Atoi(camera.Field)
			if err != nil {
				log.Printf("Error converting field value '%s' to int: %v", camera.Field, err)
				continue
			}
			if cameraField == fieldID && camera.Enabled {
				targetCamera = &h.config.Cameras[i]
				log.Printf("📹 CAMERA: Selected by field_id mapping: %s", camera.Name)
				break
			}
		}
	}

	// --- Step 3: final fallback to first enabled camera ---
	if targetCamera == nil {
		for i := range h.config.Cameras {
			if h.config.Cameras[i].Enabled {
				targetCamera = &h.config.Cameras[i]
				log.Printf("📹 CAMERA: Fallback to first enabled: %s", targetCamera.Name)
				break
			}
		}
	}

	// Check if camera was found
	if targetCamera == nil {
		c.JSON(http.StatusNotFound, ApiResponse{
			Success: false,
			Message: fmt.Sprintf("No enabled camera found for field_id: %d", fieldID),
		})
		return
	}

	// Initialize required services
	ayoClient, err := NewAyoIndoClient()
	if err != nil {
		log.Printf("Error initializing AYO API client: %v", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Success: false,
			Message: "Error initializing API client",
		})
		return
	}

	// Initialize R2 storage client with database configuration
	r2Config := storage.R2Config{
		AccessKey: h.config.R2AccessKey,
		SecretKey: h.config.R2SecretKey,
		AccountID: h.config.R2AccountID,
		Bucket:    h.config.R2Bucket,
		Endpoint:  h.config.R2Endpoint,
		Region:    h.config.R2Region,
		BaseURL:   h.config.R2BaseURL,
	}

	// Fallback to database if config values are empty
	if r2Config.AccessKey == "" {
		if config, err := h.db.GetSystemConfig(database.ConfigR2AccessKey); err == nil {
			r2Config.AccessKey = config.Value
		}
	}
	if r2Config.SecretKey == "" {
		if config, err := h.db.GetSystemConfig(database.ConfigR2SecretKey); err == nil {
			r2Config.SecretKey = config.Value
		}
	}
	if r2Config.AccountID == "" {
		if config, err := h.db.GetSystemConfig(database.ConfigR2AccountID); err == nil {
			r2Config.AccountID = config.Value
		}
	}
	if r2Config.Bucket == "" {
		if config, err := h.db.GetSystemConfig(database.ConfigR2Bucket); err == nil {
			r2Config.Bucket = config.Value
		}
	}
	if r2Config.Endpoint == "" {
		if config, err := h.db.GetSystemConfig(database.ConfigR2Endpoint); err == nil {
			r2Config.Endpoint = config.Value
		}
	}
	if r2Config.Region == "" {
		if config, err := h.db.GetSystemConfig(database.ConfigR2Region); err == nil {
			r2Config.Region = config.Value
		}
	}
	if r2Config.BaseURL == "" {
		if config, err := h.db.GetSystemConfig(database.ConfigR2BaseURL); err == nil {
			r2Config.BaseURL = config.Value
		}
	}

	r2Client, err := storage.NewR2Storage(r2Config)
	if err != nil {
		log.Printf("Error initializing R2 storage client: %v", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Success: false,
			Message: "Error initializing storage client",
		})
		return
	}

	// Initialize booking video service
	bookingVideoService := service.NewBookingVideoService(h.db, ayoClient, r2Client, h.config)
	// localNow := time.Now()
	// _, localOffset := localNow.Zone()
	// localOffsetHours := time.Duration(localOffset) * time.Second

	// Get current time in UTC and add the local timezone offset
	endTime := time.Now()
	startTime := endTime.Add(-1 * time.Minute)

	// Get today's date for booking lookup
	today := endTime.Format("2006-01-02")
	// today := "2025-04-30"

	// Get bookings from database instead of API
	bookingsData, err := h.db.GetBookingsByDate(today)
	if err != nil {
		log.Printf("Error fetching bookings from database: %v", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Success: false,
			Message: "Error fetching bookings from database: " + err.Error(),
		})
		return
	}

	if len(bookingsData) == 0 {
		c.JSON(http.StatusNotFound, ApiResponse{
			Success: false,
			Message: "No bookings found for today in database",
		})
		return
	}

	log.Printf("📅 DATABASE: Found %d bookings for date %s", len(bookingsData), today)

	// Find matching booking for the current time and field_id
	var matchingBooking *database.BookingData
	var orderDetailID string
	var bookingID string

	for _, booking := range bookingsData {
		bookingID = booking.BookingID
		status := strings.ToLower(booking.Status) // convert to lowercase

		if status == "cancelled" || status == "canceled" {
			log.Printf("⏭️ SKIP: Booking %s is cancelled", bookingID)
			continue
		} else if status != "success" {
			log.Printf("⏭️ SKIP: Booking %s status is not success (status: %s)", bookingID, status)
			continue
		}

		bookingDate, err := time.ParseInLocation("2006-01-02", booking.Date, time.Local)
		log.Printf("Booking Date: %v", bookingDate)
		if err != nil {
			log.Printf("Error parsing date: %v", err)
			continue
		}

		// Combine date and time strings into time.Time objects
		parseTime := func(timeStr string) (time.Time, error) {
			parts := strings.Split(timeStr, ":")
			if len(parts) != 3 {
				return time.Time{}, fmt.Errorf("invalid time format: %s", timeStr)
			}
			hour, _ := strconv.Atoi(parts[0])
			minute, _ := strconv.Atoi(parts[1])
			second, _ := strconv.Atoi(parts[2])
			return time.Date(bookingDate.Year(), bookingDate.Month(), bookingDate.Day(), hour, minute, second, 0, time.Local), nil
		}

		bookingStartTime, err := parseTime(booking.StartTime)
		if err != nil {
			log.Printf("Error parsing start time: %v", err)
			continue
		}

		bookingEndTime, err := parseTime(booking.EndTime)
		if err != nil {
			log.Printf("Error parsing end time: %v", err)
			continue
		}

		bookingStartTime = bookingStartTime
		bookingEndTime = bookingEndTime
		log.Printf("Debug Booking Start Time: %v", bookingStartTime)
		log.Printf("Debug Booking End Time: %v", bookingEndTime)
		log.Printf("Debug End Time: %v", endTime)
		log.Printf("Debug Start Time: %v", startTime)

		// Compare field_id and check if current time is within booking time range
		if booking.FieldID == fieldID && endTime.After(bookingStartTime) && startTime.Before(bookingEndTime) {
			log.Printf("🎯 BOOKING: Found matching booking %s (field: %d, time: %s-%s)",
				bookingID, fieldID, booking.StartTime, booking.EndTime)
			matchingBooking = &booking
			orderDetailID = strconv.Itoa(booking.OrderDetailID)
			break
		}
	}

	// Check if matching booking was found
	if matchingBooking == nil {
		c.JSON(http.StatusNotFound, ApiResponse{
			Success: false,
			Message: fmt.Sprintf("No active booking found for field_id: %d at current time", fieldID),
		})
		return
	}
	// Check if video content is available using hybrid chunk/segment discovery
	log.Printf("🔍 DISCOVERY: Checking video availability for camera %s from %s to %s", 
		targetCamera.Name, startTime.Format("15:04:05"), endTime.Format("15:04:05"))
	
	// Use hybrid discovery to check if we have chunks or segments for this time range
	hasContent, err := h.hybridProcessor.CheckVideoAvailability(targetCamera.Name, startTime, endTime)
	if err != nil {
		log.Printf("❌ ERROR: Failed to check video availability for camera %s: %v", targetCamera.Name, err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to check video availability for camera %s", targetCamera.Name),
		})
		return
	}
	
	if !hasContent {
		c.JSON(http.StatusNotFound, ApiResponse{
			Success: false,
			Message: fmt.Sprintf("No video content found for camera %s in the requested time range", targetCamera.Name),
		})
		return
	}

	log.Printf("✅ DISCOVERY: Video content is available for camera %s (will use chunks + segments optimization)", targetCamera.Name)

	// Generate a unique ID for this processing job
	taskID := fmt.Sprintf("task_%s_%d", bookingID, time.Now().Unix())

	// Start processing in background goroutine
	go func() {
		log.Printf("🚀 TASK: Starting background processing for %s (field: %d, booking: %s)", taskID, fieldID, bookingID)
		time.Sleep(30 * time.Second)
		videoType := "clip"

		// Step 1: Process video using optimized hybrid chunks + segments approach (60-70% faster)
		var uniqueID string
		err := cleanRetryWithBackoff(func() error {
			var err error
			uniqueID, err = h.hybridProcessor.ProcessVideoSegmentsOptimized(
				*targetCamera,
				bookingID,
				orderDetailID,
				startTime,
				endTime,
				matchingBooking.RawJSON, // Use RawJSON from database
				videoType,
			)
			return err
		}, 3, "Optimized Video Processing")

		if err != nil {
			log.Printf("❌ ERROR: Video processing failed for task %s after retries: %v", taskID, err)
			return
		}

		log.Printf("🎬 SUCCESS: Video processing completed for task %s (ID: %s)", taskID, uniqueID)

		// Get paths to processed files
		baseDir := filepath.Join(h.config.StoragePath, "recordings", targetCamera.Name)
		watermarkedVideoPath := filepath.Join(baseDir, "tmp", "watermark", uniqueID+".ts")
		previewPath := filepath.Join(baseDir, "tmp", "preview", uniqueID+".mp4")
		thumbnailPath := filepath.Join(baseDir, "tmp", "thumbnail", uniqueID+".png")

		// Step 2: Try direct upload with internet connectivity check
		connectivityChecker := offline.NewConnectivityChecker()

		if connectivityChecker.IsOnline() {
			log.Printf("🌐 CONNECTIVITY: Online - mencoba upload langsung...")

			// Try direct upload with retry
			var previewURL, thumbnailURL string
			err = cleanRetryWithBackoff(func() error {
				var err error
				previewURL, thumbnailURL, err = bookingVideoService.UploadProcessedVideo(
					uniqueID,
					watermarkedVideoPath,
					bookingID,
					targetCamera.Name,
				)
				return err
			}, 5, "File Upload")

			if err != nil {
				log.Printf("⚠️ WARNING: Direct upload failed for task %s: %v", taskID, err)
				log.Printf("📦 QUEUE: Menambahkan task upload ke offline queue...")

				// Add to offline queue
				err = h.queueManager.EnqueueR2Upload(
					uniqueID,
					watermarkedVideoPath,
					previewPath,
					thumbnailPath,
					fmt.Sprintf("mp4/%s.ts", uniqueID),
					fmt.Sprintf("preview/%s.mp4", uniqueID),
					fmt.Sprintf("thumbnail/%s.png", uniqueID),
				)

				if err != nil {
					log.Printf("❌ ERROR: Failed to add upload task to queue: %v", err)
					h.db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("Upload failed and queue error: %v", err))
					return
				}

				// Update status to uploading (will be processed by queue)
				h.db.UpdateVideoStatus(uniqueID, database.StatusUploading, "")
				log.Printf("📦 QUEUE: Upload task queued for video %s", uniqueID)
				return
			}

			log.Printf("📤 SUCCESS: Direct upload completed for task %s", taskID)

			// Step 3: Try direct API notification
			// Get video to calculate duration
			video, err := h.db.GetVideo(uniqueID)
			var duration float64 = 60.0 // Default 60 seconds
			if err == nil && video != nil {
				duration = video.Duration
			}

			err = cleanRetryWithBackoff(func() error {
				return h.uploadService.NotifyAyoAPI(
					uniqueID,
					"", // mp4URL will be filled by queue manager
					previewURL,
					thumbnailURL,
					duration,
				)
			}, 3, "API Notification")

			if err != nil {
				log.Printf("⚠️ WARNING: Direct API notification failed for task %s: %v", taskID, err)
				log.Printf("📦 QUEUE: Menambahkan task notifikasi API ke offline queue...")

				// Add API notification to queue
				err = h.queueManager.EnqueueAyoAPINotify(
					uniqueID,
					uniqueID,
					"", // MP4 URL will be updated when available
					previewURL,
					thumbnailURL,
					duration,
				)

				if err != nil {
					log.Printf("❌ ERROR: Failed to add API notification task to queue: %v", err)
				} else {
					log.Printf("📦 QUEUE: API notification task queued for video %s", uniqueID)
				}
			} else {
				log.Printf("🔔 SUCCESS: Direct API notification sent for task %s", taskID)
			}
		} else {
			log.Printf("🌐 CONNECTIVITY: Offline - menambahkan semua task ke queue...")

			// Add both upload and API notification to queue since we're offline
			err = h.queueManager.EnqueueR2Upload(
				uniqueID,
				watermarkedVideoPath,
				previewPath,
				thumbnailPath,
				fmt.Sprintf("mp4/%s.ts", uniqueID),
				fmt.Sprintf("preview/%s.mp4", uniqueID),
				fmt.Sprintf("thumbnail/%s.png", uniqueID),
			)

			if err != nil {
				log.Printf("❌ ERROR: Failed to add upload task to queue: %v", err)
				h.db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("Offline queue error: %v", err))
				return
			}

			// Get video duration
			video, err := h.db.GetVideo(uniqueID)
			var duration float64 = 60.0 // Default 60 seconds
			if err == nil && video != nil {
				duration = video.Duration
			}

			err = h.queueManager.EnqueueAyoAPINotify(
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
			h.db.UpdateVideoStatus(uniqueID, database.StatusUploading, "")
			log.Printf("📦 QUEUE: Tasks queued untuk video %s - akan diproses saat online", uniqueID)
		}

		log.Printf("🎉 COMPLETED: Background processing finished for task %s (video: %s)", taskID, uniqueID)
	}()

	// Return immediate success response
	c.JSON(http.StatusOK, ApiResponse{
		Success: true,
		Message: "Video processing started in background",
		Data: map[string]interface{}{
			"task_id":    taskID,
			"booking_id": bookingID,
			"start_time": startTime.Format(time.RFC3339),
			"end_time":   endTime.Format(time.RFC3339),
			"camera":     targetCamera.Name,
			"status":     "processing",
		},
	})
}

// GetQueueStatus returns the current status of the offline queue
func (h *BookingVideoRequestHandler) GetQueueStatus(c *gin.Context) {
	queueStats, err := h.queueManager.GetQueueStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Success: false,
			Message: "Failed to get queue statistics: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Success: true,
		Message: "Queue status retrieved successfully (async processing)",
		Data:    queueStats,
	})
}

func getConnectivityStatusString(isOnline bool) string {
	if isOnline {
		return "ONLINE ✅"
	}
	return "OFFLINE ❌"
}
