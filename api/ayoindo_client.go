package api

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AyoIndoClient handles interactions with the AYO Indonesia API
type AyoIndoClient struct {
	baseURL    string
	apiToken   string
	venueCode  string
	secretKey  string
	httpClient *http.Client
}

// loadEnvFile attempts to load environment variables from .env file
// It tries current directory, parent directory, and any parent directory up to root
func loadEnvFile() error {
	// Try current directory first
	curDir, err := os.Getwd()
	if err != nil {
		return err
	}
	fmt.Printf("[DEBUG] Current working directory: %s\n", curDir)

	// Try to find .env file in current directory or parents
	dir := curDir
	for {
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			// Found .env file, read it
			fmt.Printf("[DEBUG] Found .env file at %s\n", envPath)
			envFile, err := os.Open(envPath)
			if err != nil {
				return err
			}
			defer envFile.Close()

			// Parse and set environment variables
			scanner := bufio.NewScanner(envFile)
			for scanner.Scan() {
				line := scanner.Text()

				// Skip empty lines or comments
				if len(line) == 0 || line[0] == '#' {
					continue
				}

				// Split by first = sign
				parts := strings.SplitN(line, "=", 2)
				if len(parts) != 2 {
					continue
				}

				// Extract key and value
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				// Remove quotes if present
				if len(value) > 1 && (value[0] == '"' || value[0] == '\'') && value[0] == value[len(value)-1] {
					value = value[1 : len(value)-1]
				}

				// Set environment variable if not already set
				if os.Getenv(key) == "" {
					os.Setenv(key, value)
					fmt.Printf("[DEBUG] %s: %s\n", key, value)
				}
			}

			if err := scanner.Err(); err != nil {
				return err
			}

			return nil
		}

		// Try parent directory
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// Reached root directory
			fmt.Printf("[DEBUG] No .env file found in current dir, trying parent...\n")
			break
		}
		dir = parentDir
	}

	return nil
}

// defaultAyoIndoClient holds the singleton instance
var (
	defaultAyoIndoClient *AyoIndoClient
	clientInitOnce       sync.Once
)

// NewAyoIndoClient returns a singleton AyoIndoClient instance. Subsequent calls
// return the same instance to avoid repeated environment loading and duplicated
// debug output.
func NewAyoIndoClient() (*AyoIndoClient, error) {
	var err error
	clientInitOnce.Do(func() {
		defaultAyoIndoClient, err = newAyoIndoClientInternal()
	})
	return defaultAyoIndoClient, err
}

// newAyoIndoClientInternal performs the actual construction logic that used to
// be in NewAyoIndoClient. It is only executed once by the sync.Once wrapper
// above.
func newAyoIndoClientInternal() (*AyoIndoClient, error) {
	// Try to explicitly load .env file first
	_ = loadEnvFile()

	baseURL := os.Getenv("AYOINDO_API_BASE_ENDPOINT")
	apiToken := os.Getenv("AYOINDO_API_TOKEN")
	venueCode := os.Getenv("VENUE_CODE")
	secretKey := os.Getenv("VENUE_SECRET_KEY")

	// Fix baseURL format - should end without slash and point to base domain without /api prefix
	baseURL = strings.TrimSuffix(baseURL, "/")
	// If baseURL ends with '/api/v1', remove it
	if strings.HasSuffix(baseURL, "/api/v1") {
		baseURL = strings.TrimSuffix(baseURL, "/api/v1")
	}

	fmt.Printf("[DEBUG] Loading configuration from env:\n")
	fmt.Printf("[DEBUG] Base URL: %s\n", baseURL)
	fmt.Printf("[DEBUG] API Token: %s\n", apiToken)
	fmt.Printf("[DEBUG] Venue Code: %s\n", venueCode)
	fmt.Printf("[DEBUG] Secret Key: %s\n", secretKey)

	if baseURL == "" || apiToken == "" || venueCode == "" || secretKey == "" {
		return nil, fmt.Errorf("missing required environment variables for AYO API client")
	}

	return &AyoIndoClient{
		baseURL:    baseURL,
		apiToken:   apiToken,
		venueCode:  venueCode,
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// GenerateSignature creates an HMAC-SHA512 signature as specified in AYO API documentation
func (c *AyoIndoClient) GenerateSignature(params map[string]interface{}) (string, error) {
	// 1. Sort the data by key in ascending order
	fmt.Println("Params:", params)
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 2. Convert the sorted data to a query string
	values := url.Values{}
	for _, k := range keys {
		switch v := params[k].(type) {
		case string:
			values.Add(k, v)
		case int:
			values.Add(k, fmt.Sprintf("%d", v))
		case float64:
			values.Add(k, fmt.Sprintf("%f", v))
		case bool:
			values.Add(k, fmt.Sprintf("%t", v))
		default:
			jsonVal, err := json.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("could not marshal parameter %s: %w", k, err)
			}
			values.Add(k, string(jsonVal))
		}
	}
	// var queryString string
	// if values.Get("video") != "" {
	// 	queryString = "booking_id=BK%2F0003%2F250106%2F0000003&end_timestamp=2025-05-08T11%3A46%3A04%2B07%3A00&start_timestamp=2025-05-08T11%3A45%3A18%2B07%3A00&token=RtYNF7Abg6xFpYJLqdJy&type=clip&venue_code=eohcbaWbVH&video=download_path%3Ahttps%3A%2F%2Fmedia.beligem.com%2Fmp4%2FBK-0003-250106-0000001_camera_3_20250508114511.mp4%2Cresolution%3A%2Cstream_path%3Ahttps%3A%2F%2Fmedia.beligem.com%2Fhls%2FBK-0003-250106-0000001_camera_3_20250508114511%2Fplaylist.m3u8&video_request_id=sample-request-video-id-1"
	// } else {
	// 	queryString = values.Encode()
	// }
	queryString := values.Encode()
	fmt.Println("QueryString:", queryString)
	// 3. Generate HMAC-SHA512 signature
	h := hmac.New(sha512.New, []byte(c.secretKey))
	h.Write([]byte(queryString))
	signature := hex.EncodeToString(h.Sum(nil))

	return signature, nil
}

// GetWatermarkMetadata retrieves watermark information from AYO API
func (c *AyoIndoClient) GetWatermarkMetadata() (map[string]interface{}, error) {
	// Prepare the parameters
	params := map[string]interface{}{
		"token":      c.apiToken,
		"venue_code": c.venueCode,
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature

	// Build the URL with query parameters
	endpoint := fmt.Sprintf("%s/api/v1/watermark", c.baseURL)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, fmt.Sprintf("%v", v))
	}
	req.URL.RawQuery = q.Encode()

	// Print full URL for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL: %s\n", req.URL.String())

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// GetBookings retrieves booking information for a specific date
func (c *AyoIndoClient) GetBookings(date string) (map[string]interface{}, error) {
	// Validate date format (YYYY-MM-DD)
	if _, err := time.ParseInLocation("2006-01-02", date, time.Local); err != nil {
		return nil, fmt.Errorf("invalid date format, should be YYYY-MM-DD: %w", err)
	}

	// Prepare the parameters
	params := map[string]interface{}{
		"token":      c.apiToken,
		"venue_code": c.venueCode,
		"date":       date,
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature

	// Build the URL with query parameters
	endpoint := fmt.Sprintf("%s/api/v1/bookings", c.baseURL)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, fmt.Sprintf("%v", v))
	}
	req.URL.RawQuery = q.Encode()

	// Print full URL for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL: %s\n", req.URL.String())

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// SaveVideoAvailable notifies AYO API that a video is available
func (c *AyoIndoClient) SaveVideoAvailable(bookingID, videoType, previewPath, imagePath, uniqueID string, startTime, endTime time.Time) (map[string]interface{}, error) {
	// Prepare the parameters
	params := map[string]interface{}{
		"token":           c.apiToken,
		"venue_code":      c.venueCode,
		"booking_id":      bookingID,
		"type":            videoType,
		"preview_path":    previewPath,
		"image_path":      imagePath,
		"unique_id":       uniqueID,
		"start_timestamp": startTime.Format(time.RFC3339),
		"end_timestamp":   endTime.Format(time.RFC3339),
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature

	// Convert parameters to JSON
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build the URL
	endpoint := fmt.Sprintf("%s/api/v1/save-video-available", c.baseURL)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(payload))

	// Print full URL and payload for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL (POST): %s\n", endpoint)
	fmt.Printf("[DEBUG] API Request Body: %s\n", string(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// GetVideoRequests retrieves video requests from AYO API
func (c *AyoIndoClient) GetVideoRequests(date string) (map[string]interface{}, error) {
	// Prepare the parameters
	params := map[string]interface{}{
		"token":      c.apiToken,
		"venue_code": c.venueCode,
	}

	// If date is provided, add it to parameters
	// if date != "" {
	// 	params["date"] = date
	// } else {
	// 	// If date is empty, use today's date as default
	// 	params["date"] = time.Now().Format("2006-01-02")
	// }

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature

	// Build the URL with query parameters
	endpoint := fmt.Sprintf("%s/api/v1/video-requests", c.baseURL)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, fmt.Sprintf("%v", v))
	}
	req.URL.RawQuery = q.Encode()

	// Print full URL for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL: %s\n", req.URL.String())

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// SaveVideo saves video path information to AYO API
func (c *AyoIndoClient) SaveVideo(videoRequestID, bookingID, videoType, streamPath, downloadPath string, startTime, endTime time.Time) (map[string]interface{}, error) {
	// Prepare video object according to documentation

	// value videoObj as string = "download_path:https://media.beligem.com/mp4/BK-0003-250106-0000001_camera_3_20250508114511.mp4," +
	//             "resolution:," +
	//             "stream_path:https://media.beligem.com/hls/BK-0003-250106-0000001_camera_3_20250508114511/playlist.m3u8",
	videoObj := "download_path:" + downloadPath + "," + "resolution:," + "stream_path:" + streamPath

	// Prepare the parameters
	params := map[string]interface{}{
		"token":            c.apiToken,
		"venue_code":       c.venueCode,
		"video_request_id": videoRequestID,
		"booking_id":       bookingID,
		"type":             videoType,
		"video":            videoObj, // Menggunakan "video" (bukan "videos") dan format single objek
		"start_timestamp":  startTime.Format(time.RFC3339),
		"end_timestamp":    endTime.Format(time.RFC3339),
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}
	videoObjPost := map[string]interface{}{
		"stream_path":   streamPath,
		"download_path": downloadPath,
		"resolution":    "", // Assuming 1080p, adjust if needed
	}
	// Add signature to parameters
	params["signature"] = signature
	params["video"] = []map[string]interface{}{videoObjPost}
	// Convert parameters to JSON
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build the URL
	endpoint := fmt.Sprintf("%s/api/v1/save-video", c.baseURL)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(payload))

	// Print full URL and payload for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL (POST): %s\n", endpoint)
	fmt.Printf("[DEBUG] API Request Body: %s\n", string(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// HealthCheck performs a health check request to the AYO API
func (c *AyoIndoClient) HealthCheck() (map[string]interface{}, error) {
	// Prepare the parameters
	params := map[string]interface{}{
		"venue_code": c.venueCode,
		"token":      c.apiToken,
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature

	// Convert parameters to JSON
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build the URL
	endpoint := fmt.Sprintf("%s/api/v1/health-check", c.baseURL)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(payload))

	// Print full URL and payload for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL (POST): %s\n", endpoint)
	fmt.Printf("[DEBUG] API Request Body: %s\n", string(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// GetWatermark retrieves the watermark image path for the current venue
func (c *AyoIndoClient) GetWatermark(resolution string) (string, error) {
	// Create watermark directory if it doesn't exist
	venueCode := c.venueCode
	// Use storage path from environment (updated by active disk selection)
	storagePath := os.Getenv("STORAGE_PATH")
	if storagePath == "" {
		storagePath = "./videos"
	}
	folder := filepath.Join(storagePath, "watermark", venueCode)
	if err := os.MkdirAll(folder, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", folder, err)
	}

	// Test watermark URL for development/testing purposes
	testWatermarkURL := "https://www.pngall.com/wp-content/uploads/8/Sample-PNG-Image.png"

	// Define watermark sizes and filenames
	sizes := map[string]string{
		"1080": "watermark_1080.png",
		"720":  "watermark_720.png",
		"480":  "watermark_480.png",
		"360":  "watermark_360.png",
	}

	// Use specified resolution or fallback to 1080
	if resolution == "" || sizes[resolution] == "" {
		resolution = "1080" // Default to 1080p if no valid resolution specified
	}

	// Check if watermark file for the specified resolution exists
	specificPath := filepath.Join(folder, sizes[resolution])
	log.Printf("GetWatermark : Watermark path: %s", specificPath)

	// Check file age to determine if we need to update from API
	needsUpdate := false
	if stat, err := os.Stat(specificPath); err == nil {
		// Check if file is older than 24 hours
		if time.Since(stat.ModTime()) > 24*time.Hour {
			needsUpdate = true
			log.Printf("GetWatermark : Watermark exists but is older than 24 hours, checking for updates")
		} else {
			log.Printf("GetWatermark : Watermark found for resolution %s", resolution)
			return specificPath, nil
		}
	} else {
		needsUpdate = true
		log.Printf("GetWatermark : Watermark not found for resolution %s", resolution)
	}

	// Only proceed with API check if we need to update
	if !needsUpdate {
		return specificPath, nil
	}

	// Download metadata JSON from API
	response, err := c.GetWatermarkMetadata()
	if err != nil {
		log.Printf("Warning: Failed to get watermark metadata: %v", err)
		log.Printf("Using test watermark instead")

		// Simpan watermark dari URL test ke file
		fallbackPath := filepath.Join(folder, "watermark_test.png")
		resp, err := http.Get(testWatermarkURL)
		if err != nil {
			return "", fmt.Errorf("failed to download test watermark: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to download test watermark, status code: %d", resp.StatusCode)
		}

		f, err := os.Create(fallbackPath)
		if err != nil {
			return "", fmt.Errorf("failed to create test watermark file: %v", err)
		}

		_, err = io.Copy(f, resp.Body)
		f.Close()
		if err != nil {
			return "", fmt.Errorf("failed to save test watermark file: %v", err)
		}

		return fallbackPath, nil
	}

	// Process the API response
	data, ok := response["data"].([]interface{})
	if !ok {
		return "", fmt.Errorf("invalid response format from API")
	}
	// Download images for required resolutions
	for _, entry := range data {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}

		resAPI, _ := entryMap["resolution"].(string)
		pathValue, _ := entryMap["path"].(string)

		// Ensure path has proper URL format with https:// prefix
		watermarkURL := pathValue
		if watermarkURL != "" && !strings.HasPrefix(watermarkURL, "http://") && !strings.HasPrefix(watermarkURL, "https://") {
			watermarkURL = "https://" + watermarkURL
		}

		if resAPI == resolution && watermarkURL != "" {
			fname, ok := sizes[resolution]
			if !ok {
				continue
			}

			path := filepath.Join(folder, fname)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				log.Printf("Downloading watermark from: %s", watermarkURL)
				// Download watermark image
				resp, err := http.Get(watermarkURL)
				if err != nil {
					log.Printf("Error downloading watermark: %v", err)
					continue // try next resolution
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					log.Printf("Error downloading watermark, status code: %d", resp.StatusCode)
					continue // try next resolution
				}

				f, err := os.Create(path)
				if err != nil {
					log.Printf("Error creating watermark file: %v", err)
					continue // try next resolution
				}

				_, err = io.Copy(f, resp.Body)
				f.Close()
				if err == nil {
					// Successfully downloaded and saved this watermark
					log.Printf("Successfully downloaded watermark for resolution %s", resAPI)
					return path, nil
				} else {
					log.Printf("Error saving watermark file: %v", err)
				}
			} else {
				// File already exists
				log.Printf("Using existing watermark file for resolution %s", resAPI)
				return path, nil
			}
		}
	}

	// Jika tidak ada watermark yang ditemukan, gunakan test watermark URL
	log.Printf("No watermark found from API, using test watermark URL")

	// Simpan watermark dari URL test ke file
	fallbackPath := filepath.Join(folder, "watermark_test.png")
	resp, err := http.Get(testWatermarkURL)
	if err != nil {
		return "", fmt.Errorf("failed to download test watermark: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download test watermark, status code: %d", resp.StatusCode)
	}

	f, err := os.Create(fallbackPath)
	if err != nil {
		return "", fmt.Errorf("failed to create test watermark file: %v", err)
	}

	_, err = io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		return "", fmt.Errorf("failed to save test watermark file: %v", err)
	}

	return fallbackPath, nil
}

// GetVideoConfiguration retrieves video configuration from AYO API
func (c *AyoIndoClient) GetVideoConfiguration() (map[string]interface{}, error) {
	// Prepare the parameters
	params := map[string]interface{}{
		"token":      c.apiToken,
		"venue_code": c.venueCode,
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature

	// Build the URL with query parameters
	endpoint := fmt.Sprintf("%s/api/v1/video-configuration", c.baseURL)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, fmt.Sprintf("%v", v))
	}
	req.URL.RawQuery = q.Encode()

	// Print full URL for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL: %s\n", req.URL.String())

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// MarkVideoRequestsInvalid marks multiple video requests as invalid
func (c *AyoIndoClient) MarkVideoRequestsInvalid(videoRequestIDs []string) (map[string]interface{}, error) {
	// Validate input
	if len(videoRequestIDs) == 0 {
		return nil, fmt.Errorf("at least one video request ID must be provided")
	}
	if len(videoRequestIDs) > 10 {
		return nil, fmt.Errorf("maximum 10 video request IDs are allowed, got %d", len(videoRequestIDs))
	}

	// Prepare the parameters
	params := map[string]interface{}{
		"token":      c.apiToken,
		"venue_code": c.venueCode,
		// Convert array to comma-separated string
		"video_request_ids": strings.Join(videoRequestIDs, ","),
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature
	params["video_request_ids"] = videoRequestIDs

	// Prepare the request body
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Build the URL
	endpoint := fmt.Sprintf("%s/api/v1/video-request-invalid", c.baseURL)

	// Print full URL for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL (POST): %s\n", endpoint)
	fmt.Printf("[DEBUG] API Request Body: %s\n", string(body))

	// Create the request
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// MarkVideosUnavailable marks multiple videos as unavailable
func (c *AyoIndoClient) MarkVideosUnavailable(uniqueIDs []string) (map[string]interface{}, error) {
	// Validate input
	if len(uniqueIDs) == 0 {
		return nil, fmt.Errorf("at least one unique ID must be provided")
	}
	if len(uniqueIDs) > 10 {
		return nil, fmt.Errorf("maximum 10 unique IDs are allowed, got %d", len(uniqueIDs))
	}

	// Prepare the parameters
	params := map[string]interface{}{
		"token":      c.apiToken,
		"venue_code": c.venueCode,
		"unique_ids": uniqueIDs,
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature

	// Prepare the request body
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Build the URL
	endpoint := fmt.Sprintf("%s/api/v1/video-unavailable", c.baseURL)

	// Print full URL for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL (POST): %s\n", endpoint)
	fmt.Printf("[DEBUG] API Request Body: %s\n", string(body))

	// Create the request
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// SaveCameraStatus updates camera status to AYO API
func (c *AyoIndoClient) SaveCameraStatus(cameraID string, isOnline bool) (map[string]interface{}, error) {
	// Prepare the parameters
	strIsonline := "INACTIVE"
	if isOnline {
		strIsonline = "ACTIVE"
	}
	params := map[string]interface{}{
		"token":      c.apiToken,
		"venue_code": c.venueCode,
		"camera_id":  cameraID,
		"status":     strIsonline,
	}

	// Generate signature
	signature, err := c.GenerateSignature(params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	// Add signature to parameters
	params["signature"] = signature

	// Convert parameters to JSON
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build the URL
	endpoint := fmt.Sprintf("%s/api/v1/update-camera-status", c.baseURL)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(payload))

	// Print full URL and payload for debugging/Postman testing
	fmt.Printf("[DEBUG] API Request URL (POST): %s\n", endpoint)
	fmt.Printf("[DEBUG] API Request Body: %s\n", string(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}
