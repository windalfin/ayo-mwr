package service

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
	"ayo-mwr/transcode"
)

// QueuedVideo represents a video in the upload queue
type QueuedVideo struct {
	VideoID     string
	RetryCount  int
	LastAttempt time.Time
	FailReason  string
}

// Note: AyoAPIClient interface is already defined in booking_video.go

// UploadService handles uploading videos to R2 storage
type UploadService struct {
	db           database.Database
	r2Storage    *storage.R2Storage
	config       *config.Config
	ayoClient    AyoAPIClient
	uploadQueue  []QueuedVideo
	queueMutex   sync.Mutex
	maxRetries   int
	retryBackoff time.Duration
	semaphore    chan struct{} // Semaphore to limit concurrent uploads
}

// NewUploadService creates a new upload service
// ayoClient can be nil if AYO API is not available
func NewUploadService(db database.Database, r2Storage *storage.R2Storage, cfg *config.Config, ayoClient AyoAPIClient) *UploadService {
	if ayoClient == nil {
		log.Printf("⚠️ WARNING: UploadService initialized without AYO API client - API notifications will be disabled")
	}

	return &UploadService{
		db:           db,
		r2Storage:    r2Storage,
		config:       cfg,
		ayoClient:    ayoClient,
		uploadQueue:  make([]QueuedVideo, 0),
		maxRetries:   5,               // Maximum number of retry attempts
		retryBackoff: 5 * time.Minute, // Time to wait between retries
		semaphore:    make(chan struct{}, cfg.UploadWorkerConcurrency),
	}
}

// QueueVideo adds a video to the upload queue
func (s *UploadService) QueueVideo(videoID string) {
	s.queueMutex.Lock()
	defer s.queueMutex.Unlock()

	// Check if video is already in queue
	for _, v := range s.uploadQueue {
		if v.VideoID == videoID {
			return
		}
	}

	s.uploadQueue = append(s.uploadQueue, QueuedVideo{
		VideoID:     videoID,
		RetryCount:  0,
		LastAttempt: time.Time{},
	})
	log.Printf("Added video %s to upload queue", videoID)
}

// removeFromQueue removes a video from the queue
func (s *UploadService) removeFromQueue(videoID string) {
	s.queueMutex.Lock()
	defer s.queueMutex.Unlock()

	for i, v := range s.uploadQueue {
		if v.VideoID == videoID {
			s.uploadQueue = append(s.uploadQueue[:i], s.uploadQueue[i+1:]...)
			return
		}
	}
}

// updateQueuedVideo updates the retry information for a queued video
func (s *UploadService) updateQueuedVideo(videoID string, failReason string) {
	s.queueMutex.Lock()
	defer s.queueMutex.Unlock()

	for i, v := range s.uploadQueue {
		if v.VideoID == videoID {
			s.uploadQueue[i].RetryCount++
			s.uploadQueue[i].LastAttempt = time.Now()
			s.uploadQueue[i].FailReason = failReason
			return
		}
	}
}

// StartUploadWorker starts concurrent workers to upload videos to R2 storage
func (s *UploadService) StartUploadWorker() {
	go func() {
		log.Printf("Starting R2 upload worker with %d concurrent workers", s.config.UploadWorkerConcurrency)
		isOffline := false

		for {
			// Check internet connectivity
			if !s.checkInternetConnectivity() {
				if !isOffline {
					log.Println("Internet connection lost. Upload worker entering offline mode")
					isOffline = true
				}
				time.Sleep(30 * time.Second)
				continue
			}

			// If we were offline and now we're online, log the recovery
			if isOffline {
				log.Println("Internet connection restored. Resuming uploads")
				isOffline = false
			}

			// Get videos that are ready but not yet uploaded to R2
			videos, err := s.db.GetVideosByStatus(database.StatusReady, 10, 0)
			if err != nil {
				log.Printf("Error fetching videos for upload: %v", err)
				time.Sleep(30 * time.Second)
				continue
			}

			// Add ready videos to queue if not already there
			for _, video := range videos {
				if video.R2HLSURL == "" || video.R2MP4URL == "" {
					s.QueueVideo(video.ID)
				}
			}

			// Process queue concurrently
			s.queueMutex.Lock()
			queueLength := len(s.uploadQueue)
			
			if queueLength == 0 {
				s.queueMutex.Unlock()
				time.Sleep(10 * time.Second)
				continue
			}

			// Process multiple queue items concurrently
			processedCount := 0
			for i := 0; i < queueLength && processedCount < s.config.UploadWorkerConcurrency; i++ {
				queuedVideo := s.uploadQueue[i]
				
				// Check if we should retry yet
				if !queuedVideo.LastAttempt.IsZero() && time.Since(queuedVideo.LastAttempt) < s.retryBackoff {
					continue
				}

				// Check retry count
				if queuedVideo.RetryCount >= s.maxRetries {
					log.Printf("Video %s exceeded maximum retry attempts (%d). Last error: %s",
						queuedVideo.VideoID, s.maxRetries, queuedVideo.FailReason)
					s.db.UpdateVideoStatus(queuedVideo.VideoID, database.StatusFailed,
						fmt.Sprintf("Exceeded maximum retry attempts. Last error: %s", queuedVideo.FailReason))
					s.removeFromQueueUnsafe(queuedVideo.VideoID)
					continue
				}

				// Try to acquire semaphore (non-blocking)
				select {
				case s.semaphore <- struct{}{}:
					// Got semaphore, start concurrent upload
					processedCount++
					go func(qv QueuedVideo) {
						defer func() { <-s.semaphore }() // Release semaphore
						s.processUpload(qv)
					}(queuedVideo)
				default:
					// Semaphore full, skip this video for now
					break
				}
			}
			
			s.queueMutex.Unlock()
			
			if processedCount == 0 {
				time.Sleep(5 * time.Second)
			} else {
				time.Sleep(1 * time.Second) // Short sleep to allow processing
			}
		}
	}()
}

// removeFromQueueUnsafe removes a video from the queue without acquiring mutex (caller must hold mutex)
func (s *UploadService) removeFromQueueUnsafe(videoID string) {
	for i, v := range s.uploadQueue {
		if v.VideoID == videoID {
			s.uploadQueue = append(s.uploadQueue[:i], s.uploadQueue[i+1:]...)
			return
		}
	}
}

// processUpload handles the upload process for a single video
func (s *UploadService) processUpload(queuedVideo QueuedVideo) {
	video, err := s.db.GetVideo(queuedVideo.VideoID)
	if err != nil {
		log.Printf("Error fetching video %s from database: %v", queuedVideo.VideoID, err)
		s.updateQueuedVideo(queuedVideo.VideoID, fmt.Sprintf("Database error: %v", err))
		return
	}

	// Start upload process
	log.Printf("Attempting upload for video %s (attempt %d/%d)",
		video.ID, queuedVideo.RetryCount+1, s.maxRetries)

	err = s.db.UpdateVideoStatus(video.ID, database.StatusUploading, "")
	if err != nil {
		log.Printf("Error updating video status to uploading: %v", err)
		s.updateQueuedVideo(queuedVideo.VideoID, fmt.Sprintf("Status update error: %v", err))
		return
	}

	// Upload HLS stream - sekarang mengembalikan r2Path, r2URL, error
	_, hlsURL, err := s.r2Storage.UploadHLSStream(video.HLSPath, video.ID)
	if err != nil {
		log.Printf("Error uploading HLS stream for video %s: %v", video.ID, err)
		s.updateQueuedVideo(queuedVideo.VideoID, fmt.Sprintf("HLS upload error: %v", err))
		s.db.UpdateVideoStatus(video.ID, database.StatusReady, fmt.Sprintf("HLS upload error: %v", err))
		return
	}

	// Upload MP4 file
	mp4URL, err := s.r2Storage.UploadMP4(video.LocalPath, video.ID)
	if err != nil {
		log.Printf("Error uploading MP4 for video %s: %v", video.ID, err)
		s.updateQueuedVideo(queuedVideo.VideoID, fmt.Sprintf("MP4 upload error: %v", err))
		s.db.UpdateVideoStatus(video.ID, database.StatusReady, fmt.Sprintf("MP4 upload error: %v", err))
		return
	}

	// Update R2 paths and URLs in database
	err = s.db.UpdateVideoR2Paths(video.ID, fmt.Sprintf("hls/%s", video.ID), fmt.Sprintf("mp4/%s", video.ID))
	if err != nil {
		log.Printf("Error updating R2 paths for video %s: %v", video.ID, err)
	}

	err = s.db.UpdateVideoR2URLs(video.ID, hlsURL, mp4URL)
	if err != nil {
		log.Printf("Error updating R2 URLs for video %s: %v", video.ID, err)
	}

	// Verifikasi dan pastikan field-field penting tidak kosong
	if video.CameraName == "" || video.LocalPath == "" || video.UniqueID == "" ||
		video.OrderDetailID == "" || video.BookingID == "" {
		// Ambil data lengkap dari database
		fullVideo, err := s.db.GetVideo(video.ID)
		if err != nil {
			log.Printf("Error fetching complete data for video %s: %v", video.ID, err)
		} else if fullVideo != nil {
			// Update field yang kosong
			updateNeeded := false

			if video.CameraName == "" && fullVideo.CameraName != "" {
				video.CameraName = fullVideo.CameraName
				updateNeeded = true
			}
			if video.LocalPath == "" && fullVideo.LocalPath != "" {
				video.LocalPath = fullVideo.LocalPath
				updateNeeded = true
			}
			if video.UniqueID == "" && fullVideo.UniqueID != "" {
				video.UniqueID = fullVideo.UniqueID
				updateNeeded = true
			}
			if video.OrderDetailID == "" && fullVideo.OrderDetailID != "" {
				video.OrderDetailID = fullVideo.OrderDetailID
				updateNeeded = true
			}
			if video.BookingID == "" && fullVideo.BookingID != "" {
				video.BookingID = fullVideo.BookingID
				updateNeeded = true
			}

			// Update jika ada field yang perlu diperbarui
			if updateNeeded {
				// Gunakan dereferensi pointer (*video) untuk mengakses nilai
				err = s.db.UpdateVideo(*video)
				if err != nil {
					log.Printf("Error updating missing fields for video %s: %v", video.ID, err)
				} else {
					log.Printf("Successfully updated missing fields for video %s", video.ID)
				}
			}
		}
	}

	// Persiapkan data untuk update status
	now := time.Now()

	// Ambil data video lengkap
	fullVideoData, err := s.db.GetVideo(video.ID)
	if err != nil {
		log.Printf("Error getting complete video data: %v", err)

		// Fallback ke UpdateVideoStatus standar jika gagal mendapatkan data lengkap
		err = s.db.UpdateVideoStatus(video.ID, database.StatusReady, "")
		if err != nil {
			log.Printf("Error updating video status back to ready: %v", err)
		}
	} else {
		// Set status dan timestamp
		fullVideoData.Status = database.StatusReady
		fullVideoData.ErrorMessage = ""
		fullVideoData.FinishedAt = &now

		// Pastikan UploadedAt juga diset
		if fullVideoData.UploadedAt == nil {
			fullVideoData.UploadedAt = &now
		}

		// Update video dengan semua data
		err = s.db.UpdateVideo(*fullVideoData)
		if err != nil {
			log.Printf("Error updating video to ready status with full data: %v", err)
		} else {
			log.Printf("Successfully updated video %s to ready status with complete data", video.ID)
		}
	}

	log.Printf("Video %s successfully uploaded to R2 storage at %s", video.ID, now.Format(time.RFC3339))
	s.removeFromQueue(video.ID)
}

// CleanupLocalStorage removes local files that have been successfully uploaded to R2
// and are older than the specified retention period
func (s *UploadService) CleanupLocalStorage(retentionHours int) {
	go func() {
		log.Printf("Starting local storage cleanup worker (retention: %d hours)", retentionHours)
		for {
			// Wait for an hour between cleanup attempts
			time.Sleep(1 * time.Hour)

			// Get videos that are ready and have R2 URLs
			videos, err := s.db.GetVideosByStatus(database.StatusReady, 100, 0)
			if err != nil {
				log.Printf("Error fetching videos for cleanup: %v", err)
				continue
			}

			now := time.Now()
			for _, video := range videos {
				// Skip if no R2 URLs (not uploaded yet)
				if video.R2HLSURL == "" {
					continue
				}

				// Skip if no finished timestamp or too recent
				if video.FinishedAt == nil {
					continue
				}

				hoursAgo := now.Sub(*video.FinishedAt).Hours()
				if hoursAgo < float64(retentionHours) {
					// Video is still within retention period
					continue
				}

				log.Printf("Cleaning up local storage for video %s (%.1f hours old)", video.ID, hoursAgo)

				// Remove local files but keep database record
				removeLocalFiles(video)
			}
		}
	}()
}

// removeLocalFiles removes the local files for a video
func removeLocalFiles(video database.VideoMetadata) {
	// Remove original file
	if video.LocalPath != "" {
		if err := os.Remove(video.LocalPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Error removing original file %s: %v", video.LocalPath, err)
		}
	}

	// Remove HLS directory
	if video.HLSPath != "" {
		if err := os.RemoveAll(video.HLSPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Error removing HLS directory %s: %v", video.HLSPath, err)
		}
	}
}

// UploadVideo manually uploads a specific video to R2
func (s *UploadService) UploadVideo(videoID string) error {
	video, err := s.db.GetVideo(videoID)
	if err != nil {
		return fmt.Errorf("error fetching video %s: %v", videoID, err)
	}

	if video == nil {
		return fmt.Errorf("video %s not found", videoID)
	}

	// Update status to uploading
	err = s.db.UpdateVideoStatus(video.ID, database.StatusUploading, "")
	if err != nil {
		return fmt.Errorf("error updating video status to uploading: %v", err)
	}

	// Check if HLS directory exists
	if _, err := os.Stat(video.HLSPath); os.IsNotExist(err) {
		return fmt.Errorf("HLS directory %s does not exist", video.HLSPath)
	}

	// Upload HLS stream - sekarang dengan 3 nilai return (r2Path, r2URL, error)
	r2HLSPath, r2HLSURL, err := s.r2Storage.UploadHLSStream(video.HLSPath, video.ID)
	if err != nil {
		s.db.UpdateVideoStatus(video.ID, database.StatusFailed, fmt.Sprintf("HLS upload error: %v", err))
		return fmt.Errorf("error uploading HLS stream: %v", err)
	}

	// Update video metadata with R2 information
	video.R2HLSURL = r2HLSURL
	video.R2HLSPath = r2HLSPath
	video.Status = database.StatusReady

	// Update the complete video metadata
	if err := s.db.UpdateVideo(*video); err != nil {
		return fmt.Errorf("error updating video metadata: %v", err)
	}

	log.Printf("Successfully uploaded video %s to R2 storage", video.ID)
	return nil
}

// ProcessVideoFile processes a video file and uploads it
func (s *UploadService) ProcessVideoFile(filePath string) error {
	// Extract video ID from filename
	videoID := filepath.Base(filePath)
	videoID = videoID[:len(videoID)-len(filepath.Ext(videoID))]

	log.Printf("Processing video file: %s with ID: %s", filePath, videoID)

	// Create metadata record in database
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("error getting file info for %s: %v", filePath, err)
	}

	// Create metadata
	metadata := database.VideoMetadata{
		ID:         videoID,
		CreatedAt:  time.Now(),
		Status:     database.StatusProcessing,
		Size:       fileInfo.Size(),
		LocalPath:  filePath,
		CameraName: "camera_A", // Could be parameterized
	}

	// Add to database
	if err := s.db.CreateVideo(metadata); err != nil {
		log.Printf("Error creating video record in database: %v", err)
	}

	// Generate HLS streams
	hlsPath := filepath.Join(s.config.StoragePath, "hls", videoID)

	// Use the transcode package
	urls, _, err := transcode.TranscodeVideo(filePath, videoID, "hls", s.config)
	if err != nil {
		log.Printf("Error transcoding video %s: %v", videoID, err)
		s.db.UpdateVideoStatus(videoID, database.StatusFailed, fmt.Sprintf("Transcoding error: %v", err))
		return err
	}

	// Update metadata with successful transcoding
	metadata.Status = database.StatusReady
	metadata.HLSPath = hlsPath
	metadata.HLSURL = urls["hls"]
	now := time.Now()
	metadata.FinishedAt = &now

	if err := s.db.UpdateVideo(metadata); err != nil {
		log.Printf("Error updating video record in database: %v", err)
	}

	return nil
}

// checkInternetConnectivity checks if there is an active internet connection
func (s *UploadService) checkInternetConnectivity() bool {
	client := http.Client{
		Timeout: 5 * time.Second,
	}

	// Try to connect to Cloudflare's reliable DNS service
	_, err := client.Get("https://1.1.1.1")
	return err == nil
}

// NotifyAyoAPI sends notification to AYO API using actual implementation
// This method retrieves video data from database and calls the real AYO API
func (s *UploadService) NotifyAyoAPI(uniqueID, mp4URL, previewURL, thumbnailURL string, duration float64) error {
	log.Printf("📡 AYO API: Starting notification for video %s", uniqueID)

	// Get video data from database to retrieve booking information
	video, err := s.db.GetVideo(uniqueID)
	if err != nil {
		return fmt.Errorf("failed to get video data from database: %v", err)
	}

	if video == nil {
		return fmt.Errorf("video not found in database: %s", uniqueID)
	}

	// Check if we have required booking information
	if video.BookingID == "" {
		return fmt.Errorf("no booking ID found for video: %s", uniqueID)
	}

	// Use actual start and end times from the clip booking (stored in database)
	var startTime, endTime time.Time

	if video.StartTime != nil && video.EndTime != nil {
		// Use actual booking clip times
		startTime = *video.StartTime
		endTime = *video.EndTime
		log.Printf("📡 AYO API: Using actual clip times from database")
	} else {
		// Fallback to calculated times if clip times not available
		startTime = video.CreatedAt
		endTime = startTime.Add(time.Duration(duration) * time.Second)
		log.Printf("📡 AYO API: ⚠️ Using fallback calculated times (no clip times in database)")

		// If we have finished time, use that for more accurate end time
		if video.FinishedAt != nil {
			endTime = *video.FinishedAt
		}
	}

	// Determine video type (default to "clip" if not specified)
	videoType := video.VideoType
	if videoType == "" {
		videoType = "clip"
	}

	log.Printf("📡 AYO API: Calling SaveVideoAvailable...")
	log.Printf("📡 AYO API: - Booking ID: %s", video.BookingID)
	log.Printf("📡 AYO API: - Video Type: %s", videoType)
	log.Printf("📡 AYO API: - Preview URL: %s", previewURL)
	log.Printf("📡 AYO API: - Thumbnail URL: %s", thumbnailURL)
	log.Printf("📡 AYO API: - Unique ID: %s", uniqueID)
	log.Printf("📡 AYO API: - Start Time: %s", startTime.Format(time.RFC3339))
	log.Printf("📡 AYO API: - End Time: %s", endTime.Format(time.RFC3339))

	// Check if AYO client is available
	if s.ayoClient == nil {
		return fmt.Errorf("AYO API client not initialized")
	}

	// Call the actual AYO API
	_, err = s.ayoClient.SaveVideoAvailable(
		video.BookingID, // bookingID
		videoType,       // videoType
		previewURL,      // previewPath
		thumbnailURL,    // imagePath
		uniqueID,        // uniqueID
		startTime,       // startTime
		endTime,         // endTime
		int(duration),   // duration
	)

	if err != nil {
		return fmt.Errorf("AYO API call failed: %v", err)
	}

	log.Printf("📡 AYO API: ✅ Successfully notified AYO API for video %s", uniqueID)
	return nil
}
