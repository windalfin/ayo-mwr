package cron

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/database"

	"github.com/robfig/cron/v3"
)

// StartBookingSyncCron initializes a cron job that runs every 5 minutes to:
// 1. Get bookings from AYO API
// 2. Save/update booking data to database
// This is separate from video processing for better separation of concerns
func StartBookingSyncCron(cfg *config.Config) {
	go func() {
		// Initialize database
		dbPath := cfg.DatabasePath
		db, err := database.NewSQLiteDB(dbPath)
		if err != nil {
			log.Printf("bookingSync : Error initializing database: %v", err)
			return
		}

		// Initialize AYO API client
		ayoClient, err := api.NewAyoIndoClient()
		if err != nil {
			log.Printf("bookingSync : Error initializing AYO API client: %v", err)
			return
		}

		// Initial delay before first run (5 seconds)
		time.Sleep(5 * time.Second)

		// Run immediately once at startup
		syncBookingsFromAPI(db, ayoClient)

		// Start the booking sync cron
		schedule := cron.New()

		// Schedule the task every 5 minutes
		_, err = schedule.AddFunc("@every 5m", func() {
			syncBookingsFromAPI(db, ayoClient)
		})
		if err != nil {
			log.Fatalf("Error scheduling booking sync cron: %v", err)
		}

		schedule.Start()
		log.Println("bookingSync : Booking synchronization cron job started - will run every 5 minutes")
	}()
}

// syncBookingsFromAPI handles fetching bookings from API and saving to database
func syncBookingsFromAPI(db database.Database, ayoClient *api.AyoIndoClient) {
	log.Println("bookingSync : Running booking synchronization task...")

	// Get bookings for today
	today := time.Now().Format("2006-01-02")
	// today := "2025-07-02" // Fixed date for testing purposes

	// Get bookings from AYO API
	response, err := ayoClient.GetBookings(today)
	if err != nil {
		log.Printf("bookingSync : Error fetching bookings from API: %v", err)
		return
	}

	// Extract data from response
	data, ok := response["data"].([]interface{})
	if !ok || len(data) == 0 {
		log.Println("bookingSync : No bookings found for today or invalid response format")
		return
	}

	log.Printf("bookingSync : Found %d bookings from API for date %s", len(data), today)

	successCount := 0
	errorCount := 0

	// Process each booking data and save/update in database
	for _, item := range data {
		booking, ok := item.(map[string]interface{})
		if !ok {
			log.Printf("bookingSync : Invalid booking format: %v", item)
			errorCount++
			continue
		}

		// Extract fields for database storage based on actual API response
		bookingID, _ := booking["booking_id"].(string)
		orderDetailID, _ := booking["order_detail_id"].(float64)
		fieldID, _ := booking["field_id"].(float64)
		date, _ := booking["date"].(string)
		startTimeStr, _ := booking["start_time"].(string)
		endTimeStr, _ := booking["end_time"].(string)
		statusVal, _ := booking["status"].(string)
		bookingSource, _ := booking["booking_source"].(string)

		// Convert entire booking map to JSON string
		rawJSON := getBookingJSON(booking)

		// Create BookingData struct
		bookingData := database.BookingData{
			BookingID:     bookingID,
			OrderDetailID: int(orderDetailID),
			FieldID:       int(fieldID),
			Date:          date,
			StartTime:     startTimeStr,
			EndTime:       endTimeStr,
			BookingSource: bookingSource,
			Status:        strings.ToLower(statusVal), // Normalize to lowercase
			RawJSON:       rawJSON,
		}

		// Save or update booking in database
		err := db.CreateOrUpdateBooking(bookingData)
		if err != nil {
			log.Printf("bookingSync : Error saving booking %s to database: %v", bookingID, err)
			errorCount++
		} else {
			successCount++
		}
	}

	log.Printf("bookingSync : Synchronization completed - Success: %d, Errors: %d", successCount, errorCount)
}

// getBookingJSON mengkonversi map ke string JSON
func getBookingJSON(booking map[string]interface{}) string {
	jsonBytes, err := json.Marshal(booking)
	if err != nil {
		log.Printf("bookingSync : Error marshaling booking to JSON: %v", err)
		return ""
	}
	return string(jsonBytes)
} 