package signaling

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FindClosestVideo finds the video file closest to the given timestamp for a camera
func FindClosestVideo(storagePath string, cameraName string, targetTime time.Time) (string, error) {
	// Convert relative path to absolute if needed
	absStoragePath, err := filepath.Abs(storagePath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	// Find the video file closest to the timestamp
	videoDir := filepath.Join(absStoragePath, "recordings", cameraName, "mp4")
	targetDate := targetTime.Format("20060102")

	// Ensure the directory exists
	if _, err := os.Stat(videoDir); os.IsNotExist(err) {
		return "", fmt.Errorf("video directory does not exist: %s", videoDir)
	}

	log.Printf("Looking for videos in %s matching date %s", videoDir, targetDate)

	// Look for videos within a 5-minute window
	pattern := fmt.Sprintf("%s_%s_*.mp4", cameraName, targetDate)
	files, err := filepath.Glob(filepath.Join(videoDir, pattern))
	if err != nil {
		return "", fmt.Errorf("failed to find videos: %v", err)
	}

	log.Printf("Found %d potential video files", len(files))

	var closestFile string
	var minDiff int64 = 1<<63 - 1

	for _, file := range files {
		// Extract timestamp from filename (format: camera_1_20250320_115851.mp4)
		base := filepath.Base(file)
		parts := strings.Split(base, "_")
		if len(parts) != 4 {
			log.Printf("Warning: Invalid filename format: %s (expected 4 parts)", base)
			continue
		}
		timeStr := strings.TrimSuffix(parts[3], ".mp4")
		fileTime, err := time.ParseInLocation("150405", timeStr, time.Local)
		if err != nil {
			log.Printf("Warning: Could not parse time from filename %s: %v", base, err)
			continue
		}

		// Adjust fileTime to match the target date
		fileTime = time.Date(
			targetTime.Year(), targetTime.Month(), targetTime.Day(),
			fileTime.Hour(), fileTime.Minute(), fileTime.Second(),
			0, targetTime.Location(),
		)

		// Calculate time difference
		diff := abs(fileTime.Unix() - targetTime.Unix())
		if diff < minDiff {
			minDiff = diff
			closestFile = file
		}
	}

	if closestFile == "" {
		return "", fmt.Errorf("no video files found for camera %s at timestamp %v", cameraName, targetTime)
	}

	log.Printf("Found closest video file: %s", closestFile)
	return closestFile, nil
}

// CallProcessBookingVideoAPI sends a POST request to the booking video process API.
func CallProcessBookingVideoAPI(fieldID string) error {
	fmt.Printf("[ARDUINO] CallProcessBookingVideoAPI called with field_id: %s\n", fieldID)

	// Prepare data for the API call
	// convert fieldID to integer
	fieldIDInt, err := strconv.Atoi(fieldID)
	if err != nil {
		fmt.Printf("[ARDUINO] Error converting field_id to integer: %v\n", err)
		return fmt.Errorf("error converting field_id to integer: %w", err)
	}
	requestBodyMap := map[string]int{"field_id": fieldIDInt}
	requestBodyBytes, err := json.Marshal(requestBodyMap)
	if err != nil {
		fmt.Printf("[ARDUINO] Error marshaling JSON for API call: %v\n", err)
		return fmt.Errorf("error marshaling JSON: %w", err)
	}

	// Make the API call
	apiURL := "http://localhost:3000/api/request-booking-video"
	fmt.Printf("[ARDUINO] Attempting API call to: %s with body: %s\n", apiURL, string(requestBodyBytes))

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(requestBodyBytes))
	if err != nil {
		fmt.Printf("[ARDUINO] Error creating API request: %v\n", err)
		return fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	fmt.Printf("[ARDUINO] Sending HTTP request to %s\n", apiURL)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[ARDUINO] Error sending API request: %v\n", err)
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[ARDUINO] API call failed with status %s\n", resp.Status)

		// Read and log the response body for more details
		responseBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[ARDUINO] API error response body: %s\n", string(responseBody))

		return fmt.Errorf("API call failed with status %s", resp.Status)
	}

	// Read and log the successful response
	responseBody, _ := io.ReadAll(resp.Body)
	fmt.Printf("[ARDUINO] API success response body: %s\n", string(responseBody))

	fmt.Printf("[ARDUINO] Successfully called API to process booking video for field_id: %s, Status: %s\n", fieldID, resp.Status)
	return nil
}

// abs returns the absolute value of x
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
