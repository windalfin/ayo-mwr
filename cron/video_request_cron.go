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
	"ayo-mwr/recording"
	"ayo-mwr/storage"
	"ayo-mwr/transcode"

	"github.com/robfig/cron/v3"
	"golang.org/x/sync/semaphore"
)

// =================== GLOBAL CONCURRENCY CONTROL SYSTEM ===================
// Global variables untuk tracking proses video request
var (
	videoRequestProcessingMutex sync.Mutex
	activeVideoRequestProcesses int
	videoRequestCronCounter     int
)

// Global variables untuk dynamic semaphore management
var (
	videoRequestCronMutex            sync.RWMutex
	currentVideoRequestMaxConcurrent int
	currentVideoRequestSemaphore     *semaphore.Weighted
)

// updateVideoRequestConcurrency updates the semaphore with new concurrency value
func updateVideoRequestConcurrency(newMaxConcurrent int) {
	videoRequestCronMutex.Lock()
	defer videoRequestCronMutex.Unlock()

	if currentVideoRequestMaxConcurrent != newMaxConcurrent {
		log.Printf("🔄 VIDEO-REQUEST-CRON: Updating concurrency from %d to %d", currentVideoRequestMaxConcurrent, newMaxConcurrent)
		currentVideoRequestMaxConcurrent = newMaxConcurrent
		currentVideoRequestSemaphore = semaphore.NewWeighted(int64(newMaxConcurrent))
		log.Printf("✅ VIDEO-REQUEST-CRON: Concurrency updated successfully to %d", newMaxConcurrent)
	}
}

// getVideoRequestConcurrencySettings returns current semaphore and max concurrent value
func getVideoRequestConcurrencySettings() (*semaphore.Weighted, int) {
	videoRequestCronMutex.RLock()
	defer videoRequestCronMutex.RUnlock()
	return currentVideoRequestSemaphore, currentVideoRequestMaxConcurrent
}

// getActiveVideoRequestProcessesCount mengembalikan jumlah proses yang sedang aktif
func getActiveVideoRequestProcessesCount() int {
	videoRequestProcessingMutex.Lock()
	defer videoRequestProcessingMutex.Unlock()
	return activeVideoRequestProcesses
}

// logVideoRequestProcessingStatus menampilkan status proses yang sedang berjalan
func logVideoRequestProcessingStatus(cronID int, action string, maxConcurrent int) {
	videoRequestProcessingMutex.Lock()
	defer videoRequestProcessingMutex.Unlock()
	log.Printf("📊 VIDEO-REQUEST-CRON-%d: %s - Proses aktif: %d/%d", cronID, action, activeVideoRequestProcesses, maxConcurrent)
}

// =================== CONCURRENCY CONTROL SYSTEM ===================
// Sistem ini memastikan hanya maksimal 2 proses video request yang berjalan bersamaan
// bahkan jika ada multiple cron jobs yang dipicu:
//
// 1. Global Semaphore: Membatasi maksimal 2 proses bersamaan secara global
// 2. Process Tracking: Melacak jumlah proses yang sedang aktif
// 3. Cron Run Tracking: Setiap cron run memiliki ID unik untuk logging
// 4. Queueing System: Cron baru akan menunggu jika sudah ada 2 proses berjalan
//
// Contoh skenario:
// - VIDEO-REQUEST-CRON-1 mulai dengan 2 request -> Proses 1 & 2 berjalan (2/2 slot terisi)
// - VIDEO-REQUEST-CRON-2 mulai dengan 1 request -> Menunggu slot kosong
// - Proses 1 selesai -> Slot kosong (1/2) -> Proses 3 dari VIDEO-REQUEST-CRON-2 mulai
// - dst...
// ====================================================================

// isVideoRequestRetryableError menentukan apakah error layak untuk di-retry
func isVideoRequestRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// RETRY SEMUA ERROR - karena mayoritas error bisa temporary
	// Hanya skip retry untuk error yang benar-benar nil
	log.Printf("🔄 VIDEO-REQUEST-RETRY: Will retry error: %v", err)
	return true
}

// cleanVideoRequestRetryWithBackoff melakukan retry dengan logging yang bersih dan emoji
func cleanVideoRequestRetryWithBackoff(operation func() error, maxRetries int, operationName string) error {
	var lastErr error
	var retryCount int

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := operation()
		if err == nil {
			// Hanya log jika ada retry sebelumnya
			if retryCount > 0 {
				log.Printf("🔄 VIDEO-REQUEST-RETRY: %s ✅ Berhasil setelah %d kali retry", operationName, retryCount)
			}
			return nil
		}

		lastErr = err

		// Log retry attempt - semua error akan di-retry
		isVideoRequestRetryableError(err) // Ini akan log error yang akan di-retry

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
	log.Printf("🔄 VIDEO-REQUEST-RETRY: %s ❌ Gagal setelah %d percobaan: %v", operationName, maxRetries, lastErr)
	return fmt.Errorf("%s gagal setelah %d percobaan: %v", operationName, maxRetries, lastErr)
}

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

		// Initialize R2 storage client with database configuration
		r2Config := storage.R2Config{
			AccessKey: cfg.R2AccessKey,
			SecretKey: cfg.R2SecretKey,
			AccountID: cfg.R2AccountID,
			Bucket:    cfg.R2Bucket,
			Endpoint:  cfg.R2Endpoint,
			Region:    cfg.R2Region,
			BaseURL:   cfg.R2BaseURL,
		}

		// Load R2 configuration from database if config values are empty
		if r2Config.AccessKey == "" {
			if accessKey, err := db.GetSystemConfig(database.ConfigR2AccessKey); err == nil && accessKey.Value != "" {
				r2Config.AccessKey = accessKey.Value
			}
		}
		if r2Config.SecretKey == "" {
			if secretKey, err := db.GetSystemConfig(database.ConfigR2SecretKey); err == nil && secretKey.Value != "" {
				r2Config.SecretKey = secretKey.Value
			}
		}
		if r2Config.AccountID == "" {
			if accountID, err := db.GetSystemConfig(database.ConfigR2AccountID); err == nil && accountID.Value != "" {
				r2Config.AccountID = accountID.Value
			}
		}
		if r2Config.Bucket == "" {
			if bucket, err := db.GetSystemConfig(database.ConfigR2Bucket); err == nil && bucket.Value != "" {
				r2Config.Bucket = bucket.Value
			}
		}
		if r2Config.Endpoint == "" {
			if endpoint, err := db.GetSystemConfig(database.ConfigR2Endpoint); err == nil && endpoint.Value != "" {
				r2Config.Endpoint = endpoint.Value
			}
		}
		if r2Config.Region == "" {
			if region, err := db.GetSystemConfig(database.ConfigR2Region); err == nil && region.Value != "" {
				r2Config.Region = region.Value
			}
		}
		if r2Config.BaseURL == "" {
			if baseURL, err := db.GetSystemConfig(database.ConfigR2BaseURL); err == nil && baseURL.Value != "" {
				r2Config.BaseURL = baseURL.Value
			}
		}

		r2Client, err := storage.NewR2Storage(r2Config)
		if err != nil {
			log.Printf("Error initializing R2 storage client: %v", err)
			return
		}

		// Initialize semaphore dengan konfigurasi dinamis
		updateVideoRequestConcurrency(cfg.VideoRequestWorkerConcurrency)
		log.Printf("📊 VIDEO-REQUEST-CRON: Sistem antrian dimulai - maksimal %d proses video request bersamaan", cfg.VideoRequestWorkerConcurrency)

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
		log.Println("🚀 VIDEO-REQUEST-CRON: Video request processing cron job started - will run every 2 minutes")
		// Get current concurrency settings for logging
		_, currentMax := getVideoRequestConcurrencySettings()
		log.Printf("📊 VIDEO-REQUEST-CONCURRENCY: Slot processing tersedia: %d/%d", currentMax-getActiveVideoRequestProcessesCount(), currentMax)
	}()
}

// processVideoRequests handles fetching and processing video requests
func processVideoRequests(cfg *config.Config, db database.Database, ayoClient *api.AyoIndoClient, r2Client *storage.R2Storage) {
	// Reload configuration from database before processing
	// This ensures we have the latest venue code and secret key
	if err := ayoClient.ReloadConfigFromDatabase(); err != nil {
		log.Printf("Warning: Failed to reload config from database: %v", err)
	}

	// Load latest configuration and update semaphore if needed
	sysConfigService := config.NewSystemConfigService(db)
	if err := sysConfigService.LoadSystemConfigToConfig(cfg); err != nil {
		log.Printf("Warning: Failed to reload system config: %v", err)
	}
	updateVideoRequestConcurrency(cfg.VideoRequestWorkerConcurrency)

	// Get current semaphore and max concurrent settings
	globalVideoRequestSemaphore, maxConcurrent := getVideoRequestConcurrencySettings()

	// Get cron run ID untuk tracking
	videoRequestProcessingMutex.Lock()
	videoRequestCronCounter++
	currentCronID := videoRequestCronCounter
	videoRequestProcessingMutex.Unlock()

	log.Printf("🔄 VIDEO-REQUEST-CRON-%d: Starting video request processing task...", currentCronID)
	logVideoRequestProcessingStatus(currentCronID, "CRON START", maxConcurrent)

	// Get video requests from AYO API
	response, err := ayoClient.GetVideoRequests("")
	if err != nil {
		log.Printf("❌ VIDEO-REQUEST-CRON-%d: Error fetching video requests from API: %v", currentCronID, err)
		return
	}

	// Extract data from response
	data, ok := response["data"].([]interface{})
	if !ok {
		log.Printf("ℹ️ VIDEO-REQUEST-CRON-%d: No video requests found or invalid response format", currentCronID)
		return
	}

	log.Printf("📋 VIDEO-REQUEST-CRON-%d: Found %d video requests", currentCronID, len(data))
	videoRequestIDs := []string{}
	videoRequestIDsIncomplete := []string{}

	// Use a mutex to protect videoRequestIDs during concurrent access
	var mutex sync.Mutex

	// Setup for concurrent processing - menggunakan global semaphore
	var wg sync.WaitGroup

	// Count valid video requests yang akan diproses
	validVideoRequests := 0
	for _, item := range data {
		request, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		status, _ := request["status"].(string)
		if status == "PENDING" {
			validVideoRequests++
		}
	}

	log.Printf("📊 VIDEO-REQUEST-CRON-%d: %d dari %d video requests memenuhi syarat untuk diproses", currentCronID, validVideoRequests, len(data))

	// Process each video request
	for _, item := range data {
		request, ok := item.(map[string]interface{})
		if !ok {
			log.Printf("❌ VIDEO-REQUEST-CRON-%d: Invalid video request format: %v", currentCronID, item)
			continue
		}

		// Extract fields from request
		videoRequestID, _ := request["video_request_id"].(string)
		uniqueID, _ := request["unique_id"].(string)
		bookingID, _ := request["booking_id"].(string)
		status, _ := request["status"].(string)

		log.Printf("📋 VIDEO-REQUEST-CRON-%d: Processing video request: %s (Status: %s)", currentCronID, videoRequestID, status)

		// Process this request in a separate goroutine
		wg.Add(1)
		go func(videoRequestID, uniqueID, bookingID, status string, cronID int, maxConcurrent int) {
			defer wg.Done()

			// Acquire global semaphore before processing this request
			videoRequestProcessingMutex.Lock()
			currentActive := activeVideoRequestProcesses
			videoRequestProcessingMutex.Unlock()

			log.Printf("⏳ VIDEO-REQUEST-CRON-%d: Request %s menunggu slot processing (aktif: %d/%d)...", cronID, videoRequestID, currentActive, maxConcurrent)

			// Record start time untuk menghitung waktu tunggu
			waitStartTime := time.Now()

			if err := globalVideoRequestSemaphore.Acquire(context.Background(), 1); err != nil {
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: Error acquiring global semaphore for video request %s: %v", cronID, videoRequestID, err)
				return
			}

			// Update active processes counter
			videoRequestProcessingMutex.Lock()
			activeVideoRequestProcesses++
			currentActive = activeVideoRequestProcesses
			videoRequestProcessingMutex.Unlock()

			defer func() {
				// Release semaphore dan update counter
				globalVideoRequestSemaphore.Release(1)
				videoRequestProcessingMutex.Lock()
				activeVideoRequestProcesses--
				newActive := activeVideoRequestProcesses
				videoRequestProcessingMutex.Unlock()
				log.Printf("✅ VIDEO-REQUEST-CRON-%d: Request %s processing selesai (aktif: %d/%d)", cronID, videoRequestID, newActive, maxConcurrent)
			}()

			// Calculate wait time
			waitDuration := time.Since(waitStartTime)
			if waitDuration > 100*time.Millisecond {
				log.Printf("🚀 VIDEO-REQUEST-CRON-%d: Request %s mulai processing setelah menunggu %v (aktif: %d/%d)", cronID, videoRequestID, waitDuration.Round(time.Millisecond), currentActive, maxConcurrent)
			} else {
				log.Printf("🚀 VIDEO-REQUEST-CRON-%d: Request %s mulai processing langsung (aktif: %d/%d)", cronID, videoRequestID, currentActive, maxConcurrent)
			}

			// Skip if not pending
			if status != "PENDING" {
				log.Printf("⏭️ VIDEO-REQUEST-CRON-%d: Skipping video request %s with status %s", cronID, videoRequestID, status)
				return
			}

			log.Printf("📋 VIDEO-REQUEST-CRON-%d: Processing pending video request: %s, unique_id: %s", cronID, videoRequestID, uniqueID)

			// Check if video exists in database using direct uniqueID lookup
			matchingVideo, err := db.GetVideoByUniqueID(uniqueID)
			if err != nil {
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: Error checking database for unique ID %s: %v", cronID, uniqueID, err)
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
			}

			if matchingVideo == nil {
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: No matching video found for unique_id: %s", cronID, uniqueID)
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
			}
			// matchingVideo.request_id ilike videoRequestID
			if strings.Contains(matchingVideo.RequestID, videoRequestID) {
				log.Printf("✅ VIDEO-REQUEST-CRON-%d: matchingVideo.request_id ilike videoRequestID %s found in %s", cronID, videoRequestID, matchingVideo.RequestID)
				// videoRequestIDs = append(videoRequestIDs, videoRequestID)
				return
			}

			// Check if video is ready
			if matchingVideo.Status != database.StatusReady {
				log.Printf("⏳ VIDEO-REQUEST-CRON-%d: Video for unique_id %s is not ready yet (status: %s)", cronID, uniqueID, matchingVideo.Status)
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
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: No local video path found for unique_id: %s", cronID, uniqueID)
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
			}

			// Check if file exists
			if _, err := os.Stat(videoPath); os.IsNotExist(err) {
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: Video file does not exist at path: %s", cronID, videoPath)
				mutex.Lock()
				videoRequestIDs = append(videoRequestIDs, videoRequestID)
				mutex.Unlock()
				return
			}

			// Check if video duration validation is enabled
			enableVideoDurationCheck := true // default to enabled
			if config, err := sysConfigService.GetConfig(database.ConfigEnableVideoDurationCheck); err == nil {
				if config.Value == "false" {
					enableVideoDurationCheck = false
				}
			}

			if enableVideoDurationCheck {
				// Check video duration
				videoDuration, err := transcode.GetVideoDuration(videoPath)
				if err != nil {
					log.Printf("❌ VIDEO-REQUEST-CRON-%d: Failed to get video duration for %s: %v", cronID, videoPath, err)
					mutex.Lock()
					videoRequestIDs = append(videoRequestIDs, videoRequestID)
					mutex.Unlock()
					return
				}
				log.Printf("✅ VIDEO-REQUEST-CRON-%d: Video duration validation passed: %.2fs for %s", cronID, videoDuration, videoPath)
				// Check plan duration vs actual duration
				if matchingVideo.StartTime != nil && matchingVideo.EndTime != nil {
					planDuration := matchingVideo.EndTime.Sub(*matchingVideo.StartTime).Seconds()
					if videoDuration < planDuration {
						log.Printf("❌ VIDEO-REQUEST-CRON-%d: Actual duration %.2fs is less than plan duration %.2fs for %s", cronID, videoDuration, planDuration, videoPath)
						mutex.Lock()
						videoRequestIDsIncomplete = append(videoRequestIDsIncomplete, videoRequestID)
						mutex.Unlock()
						return
					}
					log.Printf("✅ VIDEO-REQUEST-CRON-%d: Plan duration validation passed: actual %.2fs >= plan %.2fs for %s", cronID, videoDuration, planDuration, videoPath)
				} else {
					log.Printf("⚠️ VIDEO-REQUEST-CRON-%d: StartTime or EndTime is nil, skipping plan duration check for %s", cronID, videoPath)
				}
			} else {
				log.Printf("⚠️ VIDEO-REQUEST-CRON-%d: Video duration validation is disabled, skipping duration checks for %s", cronID, videoPath)
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
			log.Printf("📹 VIDEO-REQUEST-CRON-%d: Generating HLS stream in: %s", cronID, hlsDir)
			if err := transcode.GenerateHLS(videoPath, hlsDir, uniqueID, cfg); err != nil {
				log.Printf("⚠️ VIDEO-REQUEST-CRON-%d: Warning: Failed to create HLS stream: %v", cronID, err)
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
				log.Printf("✅ VIDEO-REQUEST-CRON-%d: HLS stream created at: %s", cronID, hlsDir)
				log.Printf("✅ VIDEO-REQUEST-CRON-%d: HLS stream can be accessed at: %s", cronID, hlsURL)

				// Upload HLS ke R2
				_, r2HlsURLTemp, err := r2Client.UploadHLSStream(hlsDir, uniqueID)
				if err != nil {
					log.Printf("❌ VIDEO-REQUEST-CRON-%d: Warning: Failed to upload HLS stream to R2: %v", cronID, err)
					// Use existing R2 URL if upload fails
					// r2HlsURL = matchingVideo.R2HLSURL
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				} else {
					r2HlsURL = r2HlsURLTemp
					log.Printf("✅ VIDEO-REQUEST-CRON-%d: HLS stream uploaded to R2: %s", cronID, r2HlsURL)
				}

				// Update database with HLS path and URL information
				// First update the R2 paths
				err = db.UpdateVideoR2Paths(matchingVideo.ID, r2HLSPath, matchingVideo.R2MP4Path)
				if err != nil {
					log.Printf("⚠️ VIDEO-REQUEST-CRON-%d: Warning: Failed to update HLS R2 paths in database: %v", cronID, err)
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				}

				// Then update the R2 URLs
				err = db.UpdateVideoR2URLs(matchingVideo.ID, r2HlsURL, matchingVideo.R2MP4URL)
				if err != nil {
					log.Printf("⚠️ VIDEO-REQUEST-CRON-%d: Warning: Failed to update HLS R2 URLs in database: %v", cronID, err)
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
					log.Printf("⚠️ VIDEO-REQUEST-CRON-%d: Warning: Failed to update video metadata in database: %v", cronID, err)
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

				// Get watermark settings from database using existing recording package functions
				var watermarkPath string
				position, margin, opacity := recording.GetWatermarkSettings()

				// Get venue code for watermark
				venueCode := ""
				if venueConfig, err := db.GetSystemConfig(database.ConfigVenueCode); err == nil && venueConfig.Value != "" {
					venueCode = venueConfig.Value
					log.Printf("📋 VIDEO-REQUEST-CRON-%d: Found venue code: %s", cronID, venueCode)
				}

				if venueCode != "" {
					// Get watermark using recording package
					var err error
					watermarkPath, err = recording.GetWatermark(venueCode)
					if err != nil {
						log.Printf("⚠️ VIDEO-REQUEST-CRON-%d: Failed to get watermark: %v", cronID, err)
						// Continue without watermark
						watermarkPath = ""
					} else {
						log.Printf("✅ VIDEO-REQUEST-CRON-%d: Got watermark path: %s", cronID, watermarkPath)
					}
				}

				// Get watermark margin
				if marginConfig, err := db.GetSystemConfig(database.ConfigWatermarkMargin); err == nil && marginConfig.Value != "" {
					if val, err := strconv.Atoi(marginConfig.Value); err == nil {
						margin = val
					}
				}

				// Get watermark opacity
				if opacityConfig, err := db.GetSystemConfig(database.ConfigWatermarkOpacity); err == nil && opacityConfig.Value != "" {
					if val, err := strconv.ParseFloat(opacityConfig.Value, 64); err == nil {
						opacity = val
					}
				}

				log.Printf("🎨 VIDEO-REQUEST-CRON-%d: Watermark settings - Position: %d, Margin: %d, Opacity: %.2f", cronID, position, margin, opacity)

				if transcode.IsTSFile(matchingVideo.LocalPath) {
					log.Printf("📹 TS file detected: %s, converting to MP4...", matchingVideo.LocalPath)

					// Create temporary MP4 file path for conversion
					convertedMP4Path = filepath.Join(filepath.Dir(matchingVideo.LocalPath), fmt.Sprintf("%s_converted.mp4", uniqueID))

					// Convert TS to MP4 first (remux operation)
					if err := transcode.ConvertTSToMP4(matchingVideo.LocalPath, convertedMP4Path); err != nil {
						log.Printf("❌ ERROR: Failed to convert TS to MP4: %v", err)
						db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
						return
					}
					log.Printf("✅ VIDEO-REQUEST-CRON-%d: TS to MP4 conversion successful", cronID)

					// Apply watermark if available
					if watermarkPath != "" {
						// Create watermarked version
						watermarkedPath := filepath.Join(filepath.Dir(convertedMP4Path), fmt.Sprintf("%s_watermarked.mp4", uniqueID))
						if err := recording.AddWatermarkWithPosition(convertedMP4Path, watermarkPath, watermarkedPath, position, margin, opacity, "1080"); err != nil {
							log.Printf("⚠️ WARNING: Failed to add watermark: %v", err)
							log.Printf("✅ VIDEO-REQUEST-CRON-%d: Using video without watermark", cronID)
							// Continue with non-watermarked version
						} else {
							log.Printf("✅ VIDEO-REQUEST-CRON-%d: Watermark applied successfully", cronID)
							// Replace convertedMP4Path with watermarked version
							if removeErr := os.Remove(convertedMP4Path); removeErr != nil {
								log.Printf("⚠️ WARNING: Failed to remove original converted file: %v", removeErr)
							}
							convertedMP4Path = watermarkedPath
						}
					} else {
						log.Printf("✅ VIDEO-REQUEST-CRON-%d: No watermark applied (no watermark path)", cronID)
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
					log.Printf("⚠️ VIDEO-REQUEST-CRON-%d: Warning: Failed to update video URLs in database: %v", cronID, err)
					db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
					return
				} else {
					log.Printf("✅ VIDEO-REQUEST-CRON-%d: Updated video URLs in database for unique_id: %s", cronID, uniqueID)
				}
			}
			// Check if r2MP4URL is corrupted or not accessible
			log.Printf("🔍 VIDEO-REQUEST-CRON-%d: VALIDATION: Checking R2 MP4 URL integrity for %s", cronID, uniqueID)
			if err := validateR2MP4URL(r2MP4URL); err != nil {
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: ERROR: R2 MP4 URL validation failed for %s: %v", cronID, uniqueID, err)

				// Set database status to failed
				err = db.UpdateVideoStatus(matchingVideo.ID, database.StatusFailed,
					fmt.Sprintf("R2 MP4 URL validation failed: %v", err))
				if err != nil {
					log.Printf("❌ VIDEO-REQUEST-CRON-%d: Error updating video status to failed: %v", cronID, err)
				}

				// Mark video request as invalid and return
				db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
				return
			}
			log.Printf("✅ VIDEO-REQUEST-CRON-%d: VALIDATION: R2 MP4 URL validation passed for %s", cronID, uniqueID)

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
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: ERROR: Failed to send video data to AYO API for %s: %v", cronID, uniqueID, err)

				// Set database status to failed when API call fails
				// updateErr := db.UpdateVideoStatus(matchingVideo.ID, database.StatusFailed,
				// 	fmt.Sprintf("AYO API call failed: %v", err))
				// if updateErr != nil {
				// 	log.Printf("❌ VIDEO-REQUEST-CRON-%d: Error updating video status to failed: %v", cronID, updateErr)
				// }

				db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
				return
			}

			// Check API response
			statusCode, _ := result["status_code"].(float64)
			message, _ := result["message"].(string)

			if statusCode == 200 {
				log.Printf("✅ VIDEO-REQUEST-CRON-%d: SUCCESS: Successfully sent video to API for request %s: %s", cronID, videoRequestID, message)
			} else {
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: ERROR: API returned error for video request %s (status: %.0f): %s", cronID, videoRequestID, statusCode, message)

				// Set database status to failed when API returns error
				// updateErr := db.UpdateVideoStatus(matchingVideo.ID, database.StatusFailed,
				// 	fmt.Sprintf("AYO API error (status: %.0f): %s", statusCode, message))
				// if updateErr != nil {
				// 	log.Printf("❌ VIDEO-REQUEST-CRON-%d: Error updating video status to failed: %v", cronID, updateErr)
				// }

				db.UpdateVideoRequestID(uniqueID, videoRequestID, true)
				return
			}
		}(videoRequestID, uniqueID, bookingID, status, currentCronID, maxConcurrent) // End of goroutine
	}

	// Wait for all request processing goroutines to complete
	wg.Wait()
	log.Printf("🎉 VIDEO-REQUEST-CRON-%d: Video request processing task completed", currentCronID)
	logVideoRequestProcessingStatus(currentCronID, "CRON END", maxConcurrent)

	// Mark invalid video requests if any were found
	if len(videoRequestIDs) > 0 {
		log.Printf("📝 VIDEO-REQUEST-CRON-%d: Marking %d video requests as invalid: %v", currentCronID, len(videoRequestIDs), videoRequestIDs)

		// Process in batches of 10 if needed (API limit)
		for i := 0; i < len(videoRequestIDs); i += 10 {
			end := i + 10
			if end > len(videoRequestIDs) {
				end = len(videoRequestIDs)
			}

			batch := videoRequestIDs[i:end]
			result, err := ayoClient.MarkVideoRequestsInvalid(batch, false)
			if err != nil {
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: Error marking video requests as invalid: %v", currentCronID, err)
			} else {
				log.Printf("✅ VIDEO-REQUEST-CRON-%d: Successfully marked batch of video requests as invalid: %v", currentCronID, result)
			}
		}
	}
	if len(videoRequestIDsIncomplete) > 0 {
		log.Printf("📝 VIDEO-REQUEST-CRON-%d: Marking %d video requests as incomplete: %v", currentCronID, len(videoRequestIDsIncomplete), videoRequestIDsIncomplete)

		// Process in batches of 10 if needed (API limit)
		for i := 0; i < len(videoRequestIDsIncomplete); i += 10 {
			end := i + 10
			if end > len(videoRequestIDsIncomplete) {
				end = len(videoRequestIDsIncomplete)
			}

			batch := videoRequestIDsIncomplete[i:end]
			result, err := ayoClient.MarkVideoRequestsInvalid(batch, true)
			if err != nil {
				log.Printf("❌ VIDEO-REQUEST-CRON-%d: Error marking video requests as incomplete: %v", currentCronID, err)
			} else {
				log.Printf("✅ VIDEO-REQUEST-CRON-%d: Successfully marked batch of video requests as incomplete: %v", currentCronID, result)
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
