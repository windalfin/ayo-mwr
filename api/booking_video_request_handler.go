package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/recording"
	"ayo-mwr/service"
	"ayo-mwr/storage"

	"github.com/gin-gonic/gin"
)

// Request struct for the process booking video endpoint
type ProcessBookingVideoRequest struct {
	FieldID int `json:"field_id" binding:"required"`
}

// Response struct for the API
type ApiResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// getBookingJSON converts a booking map to JSON string
func getBookingJSON(booking map[string]interface{}) string {
	jsonBytes, err := json.Marshal(booking)
	if err != nil {
		log.Printf("Error marshaling booking to JSON: %v", err)
		return ""
	}
	return string(jsonBytes)
}

// BookingVideoRequestHandler handles booking video processing requests
type BookingVideoRequestHandler struct {
	config        *config.Config
	db            database.Database
	r2Storage     *storage.R2Storage
	uploadService *service.UploadService
}

// NewBookingVideoRequestHandler creates a new booking video request handler instance
func NewBookingVideoRequestHandler(cfg *config.Config, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService) *BookingVideoRequestHandler {
	return &BookingVideoRequestHandler{
		config:        cfg,
		db:            db,
		r2Storage:     r2Storage,
		uploadService: uploadService,
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
	log.Printf("Processing video for field_id: %d", fieldID)

	// Find camera with matching field_id in config
	var targetCamera *config.CameraConfig
	log.Printf("Looking for camera with field_id: %d, found %d cameras in config", fieldID, len(h.config.Cameras))

	// Since we only have one enabled camera as per the logs, let's simply use it
	// This is a temporary workaround
	if len(h.config.Cameras) > 0 && h.config.Cameras[0].Enabled {
		targetCamera = &h.config.Cameras[0]
		log.Printf("Using the first enabled camera as a fallback: %s", targetCamera.Name)
	} else {
		// Original logic as fallback
		for i, camera := range h.config.Cameras {
			// Log camera details for debugging
			log.Printf("Camera %d: Name=%s, Field=%s, Enabled=%v", i, camera.Name, camera.Field, camera.Enabled)
			
			// Convert camera field to int for comparison
			cameraField, err := strconv.Atoi(camera.Field)
			if err != nil {
				log.Printf("Error converting field value '%s' to int: %v", camera.Field, err)
				continue
			}

			log.Printf("Comparing field_id %d with camera field %d", fieldID, cameraField)
			if cameraField == fieldID && camera.Enabled {
				targetCamera = &h.config.Cameras[i]
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
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Success: false,
			Message: "Error initializing storage client",
		})
		return
	}

	// Initialize booking video service
	bookingVideoService := service.NewBookingVideoService(h.db, ayoClient, r2Client, h.config)
	localNow := time.Now()
	_, localOffset := localNow.Zone()
	localOffsetHours := time.Duration(localOffset) * time.Second
	
	// Get current time in UTC and add the local timezone offset
	endTime := time.Now().UTC().Add(localOffsetHours)
	startTime := endTime.Add(-1 * time.Minute)

	// Get today's date for booking lookup
	today := endTime.Format("2006-01-02")
	// today := "2025-04-30"
	// Get bookings from AYO API
	response, err := ayoClient.GetBookings(today)
	if err != nil {
		log.Printf("Error fetching bookings from API: %v", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Success: false,
			Message: "Error fetching bookings: " + err.Error(),
		})
		return
	}
	// log.Printf("Bookings: %v", response)
	// Extract bookings from response
	data, ok := response["data"].([]interface{})
	if !ok || len(data) == 0 {
		c.JSON(http.StatusNotFound, ApiResponse{
			Success: false,
			Message: "No bookings found for today",
		})
		return
	}

	// Find matching booking for the current time and field_id
	var matchingBooking map[string]interface{}
	var orderDetailID string
	var bookingID string
	
	for _, item := range data {
		booking, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		
		// Extract booking details
		orderDetailIDFloat, _ := booking["order_detail_id"].(float64)
		bookingFieldIDFloat, _ := booking["field_id"].(float64)
		bookingID, _ = booking["booking_id"].(string)
		date, _ := booking["date"].(string)
		startTimeStr, _ := booking["start_time"].(string)
		endTimeStr, _ := booking["end_time"].(string)
		
		// Parse date and times
		log.Printf("Date: %v", date)
		
		bookingDate, err := time.Parse("2006-01-02", date)
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
		
		bookingStartTime, err := parseTime(startTimeStr)
		if err != nil {
			log.Printf("Error parsing start time: %v", err)
			continue
		}
		
		bookingEndTime, err := parseTime(endTimeStr)
		if err != nil {
			log.Printf("Error parsing end time: %v", err)
			continue
		}

		bookingStartTime = bookingStartTime.UTC().Add(localOffsetHours)
		bookingEndTime = bookingEndTime.UTC().Add(localOffsetHours)
		log.Printf("Debug Booking Start Time: %v", bookingStartTime)
		log.Printf("Debug Booking End Time: %v", bookingEndTime)
		log.Printf("Debug End Time: %v", endTime)
		log.Printf("Debug Start Time: %v", startTime)	
		// Compare field_id and check if current time is within booking time range
		if int(bookingFieldIDFloat) == fieldID && endTime.After(bookingStartTime) && startTime.Before(bookingEndTime) {
			log.Printf("Found matching booking for field_id: %d at current time", fieldID)
			matchingBooking = booking
			orderDetailID = strconv.Itoa(int(orderDetailIDFloat))
			break
		}
		// matchingBooking = booking
		// orderDetailID = strconv.Itoa(int(orderDetailIDFloat))
	}
	
	// Check if matching booking was found
	if matchingBooking == nil {
		c.JSON(http.StatusNotFound, ApiResponse{
			Success: false,
			Message: fmt.Sprintf("No active booking found for field_id: %d at current time", fieldID),
		})
		return
	}
	
	// Find video directory for this camera
	videoDirectory := filepath.Join(h.config.StoragePath, "recordings", targetCamera.Name, "mp4")
	
	// Check if directory exists
	if _, err := os.Stat(videoDirectory); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, ApiResponse{
			Success: false,
			Message: fmt.Sprintf("No video directory found for camera %s", targetCamera.Name),
		})
		return
	}
	
	// Find segments for this camera in the time range
	segments, err := recording.FindSegmentsInRange(videoDirectory, startTime, endTime)
	if err != nil || len(segments) == 0 {
		c.JSON(http.StatusNotFound, ApiResponse{
			Success: false,
			Message: fmt.Sprintf("No video segments found for camera %s in the requested time range", targetCamera.Name),
		})
		return
	}
	
	log.Printf("Found %d video segments for camera %s in the time range", len(segments), targetCamera.Name)
	
	// Generate a unique ID for this processing job
	taskID := fmt.Sprintf("task_%s_%d", bookingID, time.Now().Unix())

	// Start processing in background goroutine
	go func() {
		log.Printf("Starting background processing for task: %s, field_id: %d, booking_id: %s", taskID, fieldID, bookingID)
		time.Sleep(30 * time.Second)

		// Process video segments using service
		uniqueID, err := bookingVideoService.ProcessVideoSegments(
			*targetCamera,
			bookingID,
			orderDetailID,
			segments,
			startTime,
			endTime,
			getBookingJSON(matchingBooking),
		)

		if err != nil {
			log.Printf("Error processing video segments for task %s: %v", taskID, err)
			return
		}

		// Get path to watermarked video
		watermarkedVideoPath := filepath.Join(h.config.StoragePath, "tmp", "watermark", uniqueID+".mp4")

		// Upload processed video
		previewURL, thumbnailURL, err := bookingVideoService.UploadProcessedVideo(
			uniqueID,
			watermarkedVideoPath,
			bookingID,
			targetCamera.Name,
		)

		if err != nil {
			log.Printf("Error uploading processed video for task %s: %v", taskID, err)
			// Update status to failed
			h.db.UpdateVideoStatus(uniqueID, database.StatusFailed, fmt.Sprintf("Upload failed: %v", err))
			return
		}

		// Notify AYO API of successful upload
		err = bookingVideoService.NotifyAyoAPI(
			bookingID,
			uniqueID,
			previewURL,
			thumbnailURL,
			startTime,
			endTime,
		)

		if err != nil {
			log.Printf("Error notifying AYO API of successful upload for task %s: %v", taskID, err)
			// Continue anyway since the video was processed and uploaded successfully
		}

		log.Printf("Completed background processing for task: %s, unique_id: %s", taskID, uniqueID)
	}()

	// Return immediate success response
	c.JSON(http.StatusOK, ApiResponse{
		Success: true,
		Message: "Video processing started in background",
		Data: map[string]interface{}{
			"task_id": taskID,
			"booking_id": bookingID,
			"start_time": startTime.Format(time.RFC3339),
			"end_time": endTime.Format(time.RFC3339),
			"camera": targetCamera.Name,
			"status": "processing",
		},
	})
}
