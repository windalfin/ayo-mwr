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

// NewAyoIndoClient creates a new client for interacting with the AYO Indonesia API
func NewAyoIndoClient() (*AyoIndoClient, error) {
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
		"token":       c.apiToken,
		"venue_code":  c.venueCode,
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
	if _, err := time.Parse("2006-01-02", date); err != nil {
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

// GetWatermark retrieves the watermark image path for the current venue
func (c *AyoIndoClient) GetWatermark() (string, error) {
	// Create watermark directory if it doesn't exist
	venueCode := c.venueCode
	folder := filepath.Join("watermark", venueCode)
	if err := os.MkdirAll(folder, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", folder, err)
	}

	// Test watermark URL for development/testing purposes
	testWatermarkURL := "https://www.pngall.com/wp-content/uploads/8/Sample-PNG-Image.png"

	// Define watermark sizes and filenames
	sizes := map[string]string{
		"360":  "watermark_360.png",
		"480":  "watermark_480.png",
		"720":  "watermark_720.png",
		"1080": "watermark_1080.png",
	}
	wanted := map[string]bool{"360": true, "480": true, "720": true, "1080": true}

	// Check if any watermark files already exist
	allExist := false
	var existingWatermarkPath string
	for res, fname := range sizes {
		path := filepath.Join(folder, fname)
		if _, err := os.Stat(path); err == nil && wanted[res] {
			allExist = true
			existingWatermarkPath = path
			break
		}
	}

	if allExist && existingWatermarkPath != "" {
		return existingWatermarkPath, nil
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

		resolution, _ := entryMap["resolution"].(string)
		watermarkURL, _ := entryMap["path"].(string)

		if wanted[resolution] && watermarkURL != "" {
			fname, ok := sizes[resolution]
			if !ok {
				continue
			}

			path := filepath.Join(folder, fname)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				// Download watermark image
				resp, err := http.Get(watermarkURL)
				if err != nil {
					continue // try next resolution
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					continue // try next resolution
				}

				f, err := os.Create(path)
				if err != nil {
					continue // try next resolution
				}

				_, err = io.Copy(f, resp.Body)
				f.Close()
				if err == nil {
					// Successfully downloaded and saved this watermark
					return path, nil
				}
			} else {
				// File already exists
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

// SaveCameraStatus updates camera status to AYO API
func (c *AyoIndoClient) SaveCameraStatus(cameraID string, isOnline bool) (map[string]interface{}, error) {
	// Prepare the parameters
	strIsonline := "INACTIVE"
	if isOnline {
		strIsonline = "ACTIVE"
	}
	params := map[string]interface{}{
		"token":       c.apiToken,
		"venue_code":  c.venueCode,
		"camera_id":   cameraID,
		"status":   strIsonline,
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
