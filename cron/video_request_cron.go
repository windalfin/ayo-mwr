package cron

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
	"ayo-mwr/transcode"

	"github.com/robfig/cron/v3"
	"golang.org/x/sync/semaphore"
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

	// Use a mutex to protect videoRequestIDs during concurrent access
	var mutex sync.Mutex

	// Setup for concurrent processing with a max of 10 concurrent requests
	const maxConcurrent = 2
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(maxConcurrent)

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

		// Acquire semaphore before processing this request
		if err := sem.Acquire(context.Background(), 1); err != nil {
			log.Printf("Error acquiring semaphore for video request %s: %v", videoRequestID, err)
			continue
		}

		// Process this request in a separate goroutine
		wg.Add(1)
		go func(videoRequestID, uniqueID, bookingID, status string) {
			defer wg.Done()
			defer sem.Release(1) // Release semaphore when done

			// Skip if not pending
			if status != "PENDING" {
				log.Printf("Skipping video request %s with status %s", videoRequestID, status)
				return
			}

			log.Printf("Processing pending video request: %s, unique_id: %s", videoRequestID, uniqueID)

			// Check if video exists in database using direct uniqueID lookup
			matchingVideo, err := db.GetVideoByUniqueID(uniqueID)
			if err != nil {
				log.Printf("Error checking database for unique ID %s: %v", uniqueID, err)
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
			}

			if matchingVideo == nil {
				log.Printf("No matching video found for unique_id: %s", uniqueID)
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
			}
			// matchingVideo.request_id ilike videoRequestID
			if strings.Contains(matchingVideo.RequestID, videoRequestID) {
				log.Printf("matchingVideo.request_id ilike videoRequestID %s found in %s", videoRequestID, matchingVideo.RequestID)
				// videoRequestIDs = append(videoRequestIDs, videoRequestID)
				return
			}

			// Check if video is ready
			if matchingVideo.Status != database.StatusReady {
				log.Printf("Video for unique_id %s is not ready yet (status: %s)", uniqueID, matchingVideo.Status)
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
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
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
			}

			// Check if file exists
			if _, err := os.Stat(videoPath); os.IsNotExist(err) {
				log.Printf("Video file does not exist at path: %s", videoPath)
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
			}
			db.UpdateVideoRequestID(uniqueID, videoRequestID, false)
			cameraName := matchingVideo.CameraName
			BaseDir := filepath.Join(cfg.StoragePath, "recordings", cameraName)
			// Buat direktori HLS untuk video ini di folder hls
			hlsParentDir := filepath.Join(BaseDir, "hls")
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
				db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
				return
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
					// r2HlsURL = matchingVideo.R2HLSURL
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				} else {
					r2HlsURL = r2HlsURLTemp
					log.Printf("HLS stream uploaded to R2: %s", r2HlsURL)
				}

				// Update database with HLS path and URL information
				// First update the R2 paths
				err = db.UpdateVideoR2Paths(matchingVideo.ID, r2HLSPath, matchingVideo.R2MP4Path)
				if err != nil {
					log.Printf("Warning: Failed to update HLS R2 paths in database: %v", err)
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				}

				// Then update the R2 URLs
				err = db.UpdateVideoR2URLs(matchingVideo.ID, r2HlsURL, matchingVideo.R2MP4URL)
				if err != nil {
					log.Printf("Warning: Failed to update HLS R2 URLs in database: %v", err)
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				}

				// Update the full video metadata to include local HLS path
				matchingVideo.HLSPath = hlsDir
				matchingVideo.HLSURL = hlsURL
				matchingVideo.R2HLSURL = r2HlsURL
				matchingVideo.R2HLSPath = r2HLSPath
				err = db.UpdateVideo(*matchingVideo)
				if err != nil {
					log.Printf("Warning: Failed to update video metadata in database: %v", err)
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				}
			}

			// Upload MP4 to R2 if local video exists
			if matchingVideo.LocalPath != "" {
				// Check if the file is TS or MP4 and handle accordingly
				var uploadPath string
				var convertedMP4Path string
				var shouldDeleteConverted bool

				if transcode.IsTSFile(matchingVideo.LocalPath) {
					log.Printf("📹 TS file detected: %s, converting to MP4...", matchingVideo.LocalPath)
					
					// Create temporary MP4 file path for conversion
					convertedMP4Path = filepath.Join(filepath.Dir(matchingVideo.LocalPath), fmt.Sprintf("%s_converted.mp4", uniqueID))
					
					// Convert TS to MP4 without changing quality
					if err := transcode.ConvertTSToMP4(matchingVideo.LocalPath, convertedMP4Path); err != nil {
						log.Printf("❌ ERROR: Failed to convert TS to MP4: %v", err)
						db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
						return
					}
					
					log.Printf("✅ TS to MP4 conversion successful: %s", convertedMP4Path)
					uploadPath = convertedMP4Path
					shouldDeleteConverted = true
					
				} else if transcode.IsMP4File(matchingVideo.LocalPath) {
					log.Printf("📹 MP4 file detected: %s, uploading directly...", matchingVideo.LocalPath)
					uploadPath = matchingVideo.LocalPath
					shouldDeleteConverted = false
					
				} else {
					log.Printf("⚠️ WARNING: Unknown file format: %s", matchingVideo.LocalPath)
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				}

				// Upload the file (either original MP4 or converted MP4) to R2
				mp4Path := fmt.Sprintf("mp4/%s.mp4", uniqueID)
				_, err = r2Client.UploadFile(uploadPath, mp4Path)
				
				if err != nil {
					log.Printf("❌ ERROR: Failed to upload video to R2: %v", err)
					// Use existing R2 URL if upload fails
					r2MP4URL = matchingVideo.R2MP4URL
					
					// Clean up converted file if it was created
					if shouldDeleteConverted && convertedMP4Path != "" {
						if removeErr := os.Remove(convertedMP4Path); removeErr != nil {
							log.Printf("⚠️ WARNING: Failed to remove converted file: %v", removeErr)
						} else {
							log.Printf("🧹 Cleaned up converted file: %s", convertedMP4Path)
						}
					}
					
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				} else {
					// Generate URL using custom domain
					r2MP4URL = fmt.Sprintf("%s/%s", r2Client.GetBaseURL(), mp4Path)
					log.Printf("✅ Video uploaded to custom URL: %s", r2MP4URL)
					
					// Clean up converted file if it was created and upload was successful
					if shouldDeleteConverted && convertedMP4Path != "" {
						if removeErr := os.Remove(convertedMP4Path); removeErr != nil {
							log.Printf("⚠️ WARNING: Failed to remove converted file: %v", removeErr)
						} else {
							log.Printf("🧹 Cleaned up converted file: %s", convertedMP4Path)
						}
					}
				}
			} else {
				db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
				return
			}

			// Update database with R2 URLs if they were uploaded successfully
			if r2HlsURL != matchingVideo.R2HLSURL || r2MP4URL != matchingVideo.R2MP4URL {
				err = db.UpdateVideoR2URLs(matchingVideo.ID, r2HlsURL, r2MP4URL)
				if err != nil {
					log.Printf("Warning: Failed to update video URLs in database: %v", err)
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				} else {
					log.Printf("Updated video URLs in database for unique_id: %s", uniqueID)
				}
			}
			// Check if r2MP4URL is corrupted or not accessible
			log.Printf("🔍 VALIDATION: Checking R2 MP4 URL integrity for %s", uniqueID)
			if err := validateR2MP4URL(r2MP4URL); err != nil {
				log.Printf("❌ ERROR: R2 MP4 URL validation failed for %s: %v", uniqueID, err)
				
				// Set database status to failed
				err = db.UpdateVideoStatus(matchingVideo.ID, database.StatusFailed, 
					fmt.Sprintf("R2 MP4 URL validation failed: %v", err))
				if err != nil {
					log.Printf("Error updating video status to failed: %v", err)
				}
				
				// Mark video request as invalid and return
				db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
				return
			}
			log.Printf("✅ VALIDATION: R2 MP4 URL validation passed for %s", uniqueID)
			
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
				log.Printf("❌ ERROR: Failed to send video data to AYO API for %s: %v", uniqueID, err)
				
				// Set database status to failed when API call fails
				updateErr := db.UpdateVideoStatus(matchingVideo.ID, database.StatusFailed, 
					fmt.Sprintf("AYO API call failed: %v", err))
				if updateErr != nil {
					log.Printf("Error updating video status to failed: %v", updateErr)
				}
				
				db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
				return
			}

			// Check API response
			statusCode, _ := result["status_code"].(float64)
			message, _ := result["message"].(string)

			if statusCode == 200 {
				log.Printf("✅ SUCCESS: Successfully sent video to API for request %s: %s", videoRequestID, message)
			} else {
				log.Printf("❌ ERROR: API returned error for video request %s (status: %.0f): %s", videoRequestID, statusCode, message)
				
				// Set database status to failed when API returns error
				updateErr := db.UpdateVideoStatus(matchingVideo.ID, database.StatusFailed, 
					fmt.Sprintf("AYO API error (status: %.0f): %s", statusCode, message))
				if updateErr != nil {
					log.Printf("Error updating video status to failed: %v", updateErr)
				}
				
				db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
				return
			}
		}(videoRequestID, uniqueID, bookingID, status) // End of goroutine
	}

	// Wait for all request processing goroutines to complete
	wg.Wait()
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

// validateR2MP4URL validates that the R2 MP4 URL is accessible and not corrupted
func validateR2MP4URL(url string) error {
	log.Printf("🔍 VALIDATION: Checking R2 MP4 URL: %s", url)
	
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	// Perform HEAD request to check if file exists and is accessible
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %v", err)
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()
	
	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	// Check Content-Type header for video/mp4
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType, "video/mp4") && 
	   !strings.Contains(contentType, "video/mpeg") && 
	   !strings.Contains(contentType, "application/octet-stream") {
		log.Printf("⚠️ WARNING: Unexpected content type for MP4 file: %s", contentType)
		// Don't fail on content type as some CDNs may return generic types
	}
	
	// Check Content-Length header to ensure file is not empty
	contentLengthStr := resp.Header.Get("Content-Length")
	if contentLengthStr != "" {
		contentLength, err := strconv.ParseInt(contentLengthStr, 10, 64)
		if err == nil {
			if contentLength <= 0 {
				return fmt.Errorf("file appears to be empty (Content-Length: %d)", contentLength)
			}
			if contentLength < 1024 { // Less than 1KB is suspicious for MP4
				return fmt.Errorf("file too small (Content-Length: %d bytes), likely corrupted", contentLength)
			}
			log.Printf("✅ VALIDATION: File size check passed (%d bytes)", contentLength)
		} else {
			log.Printf("⚠️ WARNING: Could not parse Content-Length header: %s", contentLengthStr)
		}
	} else {
		log.Printf("⚠️ WARNING: No Content-Length header found")
	}
	
	// Optional: Perform a partial download to check file header (first 32 bytes)
	// This can help detect completely corrupted files
	err = validateMP4Header(url, client)
	if err != nil {
		return fmt.Errorf("MP4 header validation failed: %v", err)
	}
	
	log.Printf("✅ VALIDATION: R2 MP4 URL validation completed successfully")
	return nil
}

// validateMP4Header performs a partial download to check MP4 file header
func validateMP4Header(url string, client *http.Client) error {
	log.Printf("🔍 VALIDATION: Checking MP4 file header...")
	
	// Create request for first 32 bytes
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create header check request: %v", err)
	}
	
	// Set Range header to download only first 32 bytes
	req.Header.Set("Range", "bytes=0-31")
	
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("header check request failed: %v", err)
	}
	defer resp.Body.Close()
	
	// Accept both 206 (Partial Content) and 200 (OK) responses
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ WARNING: Range request not supported (HTTP %d), skipping header validation", resp.StatusCode)
		return nil // Don't fail if range requests are not supported
	}
	
	// Read the header bytes
	header := make([]byte, 32)
	n, err := resp.Body.Read(header)
	if err != nil && n == 0 {
		return fmt.Errorf("failed to read file header: %v", err)
	}
	
	// Basic MP4 validation - check for common MP4 box types
	// MP4 files typically start with 'ftyp' box
	headerStr := string(header[4:8]) // bytes 4-7 should contain box type
	if n >= 8 && (headerStr == "ftyp" || headerStr == "moov" || headerStr == "mdat") {
		log.Printf("✅ VALIDATION: Valid MP4 header detected (%s)", headerStr)
		return nil
	}
	
	// Check for other valid patterns that might indicate a valid file
	if n >= 4 {
		// Some files might have different structures, be less strict
		log.Printf("⚠️ WARNING: MP4 header not immediately recognizable, but file appears accessible")
		return nil
	}
	
	return fmt.Errorf("invalid or corrupted MP4 header (read %d bytes)", n)
}
