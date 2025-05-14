package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func setupTestEnv() func() {
	// Save original env vars
	origBaseURL := os.Getenv("AYOINDO_API_BASE_ENDPOINT")
	origToken := os.Getenv("AYOINDO_API_TOKEN")
	origVenueCode := os.Getenv("VENUE_CODE")
	origSecretKey := os.Getenv("VENUE_SECRET_KEY")

	// Set test env vars
	os.Setenv("AYOINDO_API_BASE_ENDPOINT", "http://test-api")
	os.Setenv("AYOINDO_API_TOKEN", "test-token")
	os.Setenv("VENUE_CODE", "TEST123456")
	os.Setenv("VENUE_SECRET_KEY", "test-secret-key")

	// Return cleanup function
	return func() {
		os.Setenv("AYOINDO_API_BASE_ENDPOINT", origBaseURL)
		os.Setenv("AYOINDO_API_TOKEN", origToken)
		os.Setenv("VENUE_CODE", origVenueCode)
		os.Setenv("VENUE_SECRET_KEY", origSecretKey)
	}
}

func TestNewAyoIndoClient(t *testing.T) {
	cleanup := setupTestEnv()
	defer cleanup()

	client, err := NewAyoIndoClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if client.baseURL != "http://test-api" {
		t.Errorf("Expected baseURL to be 'http://test-api', got %s", client.baseURL)
	}
	if client.apiToken != "test-token" {
		t.Errorf("Expected apiToken to be 'test-token', got %s", client.apiToken)
	}
	if client.venueCode != "TEST123456" {
		t.Errorf("Expected venueCode to be 'TEST123456', got %s", client.venueCode)
	}
	if client.secretKey != "test-secret-key" {
		t.Errorf("Expected secretKey to be 'test-secret-key', got %s", client.secretKey)
	}
}

func TestGenerateSignature(t *testing.T) {
	cleanup := setupTestEnv()
	defer cleanup()

	client, err := NewAyoIndoClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	params := map[string]interface{}{
		"token":      "test-token",
		"venue_code": "TEST123456",
		"booking_id": "BK/0042/230228/0000118",
		"date":       "2023-03-04",
	}

	signature, err := client.GenerateSignature(params)
	if err != nil {
		t.Fatalf("Failed to generate signature: %v", err)
	}

	// We can't easily verify the exact signature since it depends on the secret key,
	// but we can at least verify it's not empty and has the expected format (hex string)
	if len(signature) == 0 {
		t.Error("Expected non-empty signature")
	}
	if len(signature) != 128 {  // SHA-512 produces a 64-byte (128 hex chars) hash
		t.Errorf("Expected signature length to be 128, got %d", len(signature))
	}
}

func TestGetWatermark(t *testing.T) {
	cleanup := setupTestEnv()
	defer cleanup()

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check request method
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		// Check path
		if r.URL.Path != "/watermark" {
			t.Errorf("Expected path to be '/watermark', got %s", r.URL.Path)
		}

		// Check query parameters
		query := r.URL.Query()
		if query.Get("token") != "test-token" {
			t.Errorf("Expected token to be 'test-token', got %s", query.Get("token"))
		}
		if query.Get("venue_code") != "TEST123456" {
			t.Errorf("Expected venue_code to be 'TEST123456', got %s", query.Get("venue_code"))
		}
		if query.Get("signature") == "" {
			t.Error("Expected signature to be present")
		}

		// Check headers
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Expected Accept header to be 'application/json', got %s", r.Header.Get("Accept"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type header to be 'application/json', got %s", r.Header.Get("Content-Type"))
		}

		// Send response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"error": false,
			"status_code": 200,
			"data": [
				{
					"resolution": "480",
					"path": "https://asset.ayo.co.id/venue-a-watermark-480.png"
				},
				{
					"resolution": "720",
					"path": "https://asset.ayo.co.id/venue-a-watermark-720.png"
				},
				{
					"resolution": "1080",
					"path": "https://asset.ayo.co.id/venue-a-watermark-1080.png"
				}
			]
		}`)
	}))
	defer server.Close()

	// Save original API base URL
	originalBaseURL := os.Getenv("AYOINDO_API_BASE_ENDPOINT")
	// Point client to test server
	os.Setenv("AYOINDO_API_BASE_ENDPOINT", server.URL)
	defer os.Setenv("AYOINDO_API_BASE_ENDPOINT", originalBaseURL)

	// Create client with updated baseURL
	client, err := NewAyoIndoClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Call the API
	result, err := client.GetWatermarkMetadata()
	if err != nil {
		t.Fatalf("GetWatermarkMetadata failed: %v", err)
	}

	// Check response
	errorValue, ok := result["error"]
	if !ok {
		t.Errorf("Response missing 'error' field")
	} else if errorValue != false {
		t.Errorf("Expected error to be false, got %v", errorValue)
	}
	
	statusCode, ok := result["status_code"]
	if !ok {
		t.Errorf("Response missing 'status_code' field")
	} else if statusCode != float64(200) {
		t.Errorf("Expected status_code to be 200, got %v", statusCode)
	}

	// Check data
	dataValue, exists := result["data"]
	if !exists {
		t.Fatalf("Response missing 'data' field")
	}
	
	data, ok := dataValue.([]interface{})
	if !ok {
		t.Fatalf("Expected data to be an array, got %T", dataValue)
	}
	if len(data) != 3 {
		t.Errorf("Expected 3 items in data, got %d", len(data))
	}

	// Check first item in data
	item, ok := data[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data item to be an object")
	}
	if item["resolution"] != "480" {
		t.Errorf("Expected resolution to be '480', got %v", item["resolution"])
	}
	if item["path"] != "https://asset.ayo.co.id/venue-a-watermark-480.png" {
		t.Errorf("Expected path to be 'https://asset.ayo.co.id/venue-a-watermark-480.png', got %v", item["path"])
	}
}

func TestGetBookings(t *testing.T) {
	cleanup := setupTestEnv()
	defer cleanup()

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check request method
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		// Check path
		if r.URL.Path != "/bookings" {
			t.Errorf("Expected path to be '/bookings', got %s", r.URL.Path)
		}

		// Check query parameters
		query := r.URL.Query()
		if query.Get("token") != "test-token" {
			t.Errorf("Expected token to be 'test-token', got %s", query.Get("token"))
		}
		if query.Get("venue_code") != "TEST123456" {
			t.Errorf("Expected venue_code to be 'TEST123456', got %s", query.Get("venue_code"))
		}
		if query.Get("date") != "2023-03-04" {
			t.Errorf("Expected date to be '2023-03-04', got %s", query.Get("date"))
		}
		if query.Get("signature") == "" {
			t.Error("Expected signature to be present")
		}

		// Check headers
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Expected Accept header to be 'application/json', got %s", r.Header.Get("Accept"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type header to be 'application/json', got %s", r.Header.Get("Content-Type"))
		}

		// Send response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"error": false,
			"status_code": 200,
			"data": [
				{
					"booking_id": "BK/0042/230304/0000001",
					"start_time": "10:00:00",
					"end_time": "11:00:00",
					"court_name": "Court A",
					"date": "2023-03-04"
				},
				{
					"booking_id": "BK/0042/230304/0000002",
					"start_time": "11:00:00",
					"end_time": "12:00:00",
					"court_name": "Court B",
					"date": "2023-03-04"
				}
			]
		}`)
	}))
	defer server.Close()

	// Save original API base URL
	originalBaseURL := os.Getenv("AYOINDO_API_BASE_ENDPOINT")
	// Point client to test server
	os.Setenv("AYOINDO_API_BASE_ENDPOINT", server.URL)
	defer os.Setenv("AYOINDO_API_BASE_ENDPOINT", originalBaseURL)

	// Create client with updated baseURL
	client, err := NewAyoIndoClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Call the API
	result, err := client.GetBookings("2023-03-04")
	if err != nil {
		t.Fatalf("GetBookings failed: %v", err)
	}

	// Check response
	errorValue, ok := result["error"]
	if !ok {
		t.Errorf("Response missing 'error' field")
	} else if errorValue != false {
		t.Errorf("Expected error to be false, got %v", errorValue)
	}
	
	statusCode, ok := result["status_code"]
	if !ok {
		t.Errorf("Response missing 'status_code' field")
	} else if statusCode != float64(200) {
		t.Errorf("Expected status_code to be 200, got %v", statusCode)
	}

	// Check data
	dataValue, exists := result["data"]
	if !exists {
		t.Fatalf("Response missing 'data' field")
	}
	
	data, ok := dataValue.([]interface{})
	if !ok {
		t.Fatalf("Expected data to be an array, got %T", dataValue)
	}
	if len(data) != 2 {
		t.Errorf("Expected 2 items in data, got %d", len(data))
	}

	// Check first booking
	booking, ok := data[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected booking to be an object")
	}
	if booking["booking_id"] != "BK/0042/230304/0000001" {
		t.Errorf("Expected booking_id to be 'BK/0042/230304/0000001', got %v", booking["booking_id"])
	}
}

func TestSaveVideoAvailable(t *testing.T) {
	cleanup := setupTestEnv()
	defer cleanup()

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check request method
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		// Check path
		if r.URL.Path != "/save-video-available" {
			t.Errorf("Expected path to be '/save-video-available', got %s", r.URL.Path)
		}

		// Check headers
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Expected Accept header to be 'application/json', got %s", r.Header.Get("Accept"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type header to be 'application/json', got %s", r.Header.Get("Content-Type"))
		}

		// Read and check request body
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("Failed to parse request body: %v", err)
		}
		if body["token"] != "test-token" {
			t.Errorf("Expected token to be 'test-token', got %v", body["token"])
		}
		if body["venue_code"] != "TEST123456" {
			t.Errorf("Expected venue_code to be 'TEST123456', got %v", body["venue_code"])
		}
		if body["booking_id"] != "BK/0042/230304/0000001" {
			t.Errorf("Expected booking_id to be 'BK/0042/230304/0000001', got %v", body["booking_id"])
		}
		if body["signature"] == nil {
			t.Error("Expected signature to be present")
		}

		// Send response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"error": false,
			"status_code": 200,
			"data": {
				"id": 1,
				"booking_id": "BK/0042/230304/0000001",
				"preview_path": "https://asset.ayo.co.id/preview-123456.mp4",
				"image_path": "https://asset.ayo.co.id/image-123456.png"
			}
		}`)
	}))
	defer server.Close()

	// Save original API base URL
	originalBaseURL := os.Getenv("AYOINDO_API_BASE_ENDPOINT")
	// Point client to test server
	os.Setenv("AYOINDO_API_BASE_ENDPOINT", server.URL)
	defer os.Setenv("AYOINDO_API_BASE_ENDPOINT", originalBaseURL)

	// Create client with updated baseURL
	client, err := NewAyoIndoClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Call the API
	startTime := time.Date(2023, 3, 4, 10, 0, 0, 0, time.UTC)
	endTime := time.Date(2023, 3, 4, 11, 0, 0, 0, time.UTC)
	result, err := client.SaveVideoAvailable(
		"BK/0042/230304/0000001",
		"clip",
		"https://asset.ayo.co.id/preview-123456.mp4",
		"https://asset.ayo.co.id/image-123456.png",
		"TEST_UNIQUE_ID",
		startTime,
		endTime,
	)
	if err != nil {
		t.Fatalf("SaveVideoAvailable failed: %v", err)
	}

	// Check response
	errorValue, ok := result["error"]
	if !ok {
		t.Errorf("Response missing 'error' field")
	} else if errorValue != false {
		t.Errorf("Expected error to be false, got %v", errorValue)
	}
}

// Test with real credentials from .env file (use for integration testing)
func TestAyoIndoClientWithRealCredentials(t *testing.T) {
	// Skip this test when running regular unit tests
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Set explicit credentials for testing
	os.Setenv("AYOINDO_API_BASE_ENDPOINT", "http://iot-api.ayodev.xyz:6060")
	os.Setenv("AYOINDO_API_TOKEN", "RtYNF7Abg6xFpYJLqdJy")
	os.Setenv("VENUE_CODE", "eohcbaWbVH")
	os.Setenv("VENUE_SECRET_KEY", "JXP72RM48B6rBxpzgMHvfqfUV4LAzzu4A9qLswrM") 

	// Create client
	client, err := NewAyoIndoClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Display the configuration for debugging
	t.Logf("Testing with real configuration:")
	t.Logf("Base URL: %s", client.baseURL)
	t.Logf("API Token: %s", client.apiToken)
	t.Logf("Venue Code: %s", client.venueCode)
	t.Logf("Secret Key: %s", client.secretKey)

	// Test GetWatermark
	t.Run("GetWatermark", func(t *testing.T) {
		watermarkPath, err := client.GetWatermark()
		if err != nil {
			t.Fatalf("GetWatermark failed: %v", err)
		}
		t.Logf("GetWatermark path: %s", watermarkPath)

		// Basic validation of response
		if watermarkPath == "" {
			t.Errorf("Expected non-empty watermark path")
		}
	})

	// Test GetBookings dengan tanggal yang bisa dipilih
	// Gunakan tanggal spesifik "2025-02-02" jika USE_SPECIFIC_DATE=true, jika tidak gunakan hari ini
	useSpecificDate := true // Ubah ke false untuk menggunakan tanggal hari ini

	t.Run("GetBookings", func(t *testing.T) {
		var bookingDate string
		if useSpecificDate {
			bookingDate = "2025-04-30" // Tanggal spesifik untuk pengujian
		} else {
			bookingDate = time.Now().Format("2006-01-02") // Tanggal hari ini
		}
		
		t.Logf("Testing GetBookings for date: %s", bookingDate)

		result, err := client.GetBookings(bookingDate)
		if err != nil {
			t.Fatalf("GetBookings failed: %v", err)
		}
		t.Logf("GetBookings response: %+v", result)

		// Basic validation of response
		errorValue, ok := result["error"]
		if !ok {
			t.Errorf("Response missing 'error' field")
		} else if errorValue != false {
			t.Errorf("Expected 'error' to be false, got %v", errorValue)
		}
	})

	// Test GetVideoRequests
	t.Run("GetVideoRequests", func(t *testing.T) {
		// Use a specific date that works with the API
		result, err := client.GetVideoRequests("")
		if err != nil {
			t.Fatalf("GetVideoRequests failed: %v", err)
		}
		t.Logf("GetVideoRequests response: %+v", result)

		// Basic validation of response
		errorValue, ok := result["error"]
		if !ok {
			t.Errorf("Response missing 'error' field")
		} else if errorValue != false {
			t.Errorf("Expected 'error' to be false, got %v", errorValue)
		}
	})

	// Test SaveVideoAvailable 
	t.Run("SaveVideoAvailable", func(t *testing.T) {
		// Test parameters
		bookingID := "BX/20230406/001"
		videoType := "clip"
		previewPath := "https://asset.ayo.co.id/preview-123456.mp4"
		imagePath := "https://asset.ayo.co.id/image-123456.png"
		uniqueID := "TEST_VIDEO_UNIQUE_ID_" + time.Now().Format("20060102150405")
		startTime := time.Now()
		endTime := startTime.Add(10 * time.Minute)

		result, err := client.SaveVideoAvailable(bookingID, videoType, previewPath, imagePath, uniqueID, startTime, endTime)
		// Error is expected without actual video files, but URL and body should be printed
		t.Logf("SaveVideoAvailable attempted, got: %v, err: %v", result, err)
	})

	// Test SaveVideo
	t.Run("SaveVideo", func(t *testing.T) {
		videoRequestID := "sample-request-video-id"
		bookingID := "BX/20230406/001"
		uniqueID := "TEST_UNIQUE_ID_" + time.Now().Format("20060102150405")
		videoType := "clip"
		streamPath := "https://asset.ayo.co.id/stream-123456.m3u8"
		downloadPath := "https://asset.ayo.co.id/download-123456.mp4"
		startTime := time.Now()
		endTime := startTime.Add(10 * time.Minute)

		result, err := client.SaveVideo(videoRequestID, bookingID, uniqueID, videoType, streamPath, downloadPath, startTime, endTime)
		// Error is expected without actual video files, but URL and body should be printed
		t.Logf("SaveVideo attempted, got: %v, err: %v", result, err)
	})

	// Test SaveCameraStatus
	t.Run("SaveCameraStatus", func(t *testing.T) {
		cameraID := "CAMERA_001"
		isOnline := true

		result, err := client.SaveCameraStatus(cameraID, isOnline)
		// Error is expected without actual camera, but URL and body should be printed
		t.Logf("SaveCameraStatus attempted, got: %v, err: %v", result, err)
	})
}

// This is a simple command-line tool to test the AYO API integration
// It can be used for quick testing from the command line with real credentials
func ExampleAyoIndoClient() {
	// To run this example, you need to have the following environment variables set:
	// - AYOINDO_API_BASE_ENDPOINT
	// - AYOINDO_API_TOKEN
	// - VENUE_CODE
	// - VENUE_SECRET_KEY

	client, err := NewAyoIndoClient()
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		return
	}

	// Print configuration for verification
	fmt.Printf("Configuration:\n")
	fmt.Printf("Base URL: %s\n", client.baseURL)
	fmt.Printf("API Token: %s\n", client.apiToken)
	fmt.Printf("Venue Code: %s\n", client.venueCode)
	fmt.Printf("Secret Key: %s\n\n", client.secretKey)

	// Get watermark
	fmt.Println("Testing GetWatermark...")
	result, err := client.GetWatermark()
	if err != nil {
		fmt.Printf("Error getting watermark: %v\n", err)
	} else {
		prettyPrint(result)
	}
	fmt.Println()

	// Get bookings for today
	today := time.Now().Format("2006-01-02")
	fmt.Printf("Testing GetBookings for date %s...\n", today)
	bookings, err := client.GetBookings(today)
	if err != nil {
		fmt.Printf("Error getting bookings: %v\n", err)
	} else {
		prettyPrint(bookings)
	}
	fmt.Println()

	// Get video requests
	fmt.Println("Testing GetVideoRequests...")
	videoRequests, err := client.GetVideoRequests("2025-04-28")
	if err != nil {
		fmt.Printf("Error getting video requests: %v\n", err)
	} else {
		prettyPrint(videoRequests)
	}

	// Output depends on the API response, so we don't check it here
}

// prettyPrint outputs a JSON object with indentation
func prettyPrint(data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Printf("Error formatting JSON: %v\n", err)
		return
	}
	fmt.Println(string(jsonData))
}
