package service

import (
	"fmt"
	"log"
	"os"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// UploadService handles uploading videos to R2 storage
type UploadService struct {
	db        database.Database
	r2Storage *storage.R2Storage
	config    config.Config
}

// NewUploadService creates a new upload service
func NewUploadService(db database.Database, r2Storage *storage.R2Storage, cfg config.Config) *UploadService {
	return &UploadService{
		db:        db,
		r2Storage: r2Storage,
		config:    cfg,
	}
}

// StartUploadWorker starts a worker to upload videos to R2 storage
func (s *UploadService) StartUploadWorker() {
	go func() {
		log.Println("Starting R2 upload worker")
		for {
			// Get videos that are ready but not yet uploaded to R2
			videos, err := s.db.GetVideosByStatus(database.StatusReady, 10, 0)
			if err != nil {
				log.Printf("Error fetching videos for upload: %v", err)
				time.Sleep(30 * time.Second)
				continue
			}

			if len(videos) == 0 {
				// No videos to upload, sleep and check again
				time.Sleep(10 * time.Second)
				continue
			}

			for _, video := range videos {
				// Skip if R2 URLs are already set
				if video.R2HLSURL != "" && video.R2DASHURL != "" {
					continue
				}

				log.Printf("Uploading video %s to R2 storage", video.ID)

				// Update status to uploading
				err = s.db.UpdateVideoStatus(video.ID, database.StatusUploading, "")
				if err != nil {
					log.Printf("Error updating video status to uploading: %v", err)
					continue
				}

				// Upload HLS stream
				hlsURL, err := s.r2Storage.UploadHLSStream(video.HLSPath, video.ID)
				if err != nil {
					log.Printf("Error uploading HLS stream for video %s: %v", video.ID, err)
					s.db.UpdateVideoStatus(video.ID, database.StatusFailed, fmt.Sprintf("HLS upload error: %v", err))
					continue
				}

				// Upload DASH stream
				dashURL, err := s.r2Storage.UploadDASHStream(video.DASHPath, video.ID)
				if err != nil {
					log.Printf("Error uploading DASH stream for video %s: %v", video.ID, err)
					s.db.UpdateVideoStatus(video.ID, database.StatusFailed, fmt.Sprintf("DASH upload error: %v", err))
					continue
				}

				// Update R2 paths and URLs in database
				err = s.db.UpdateVideoR2Paths(video.ID, fmt.Sprintf("hls/%s", video.ID), fmt.Sprintf("dash/%s", video.ID))
				if err != nil {
					log.Printf("Error updating R2 paths for video %s: %v", video.ID, err)
				}

				err = s.db.UpdateVideoR2URLs(video.ID, hlsURL, dashURL)
				if err != nil {
					log.Printf("Error updating R2 URLs for video %s: %v", video.ID, err)
				}

				// Update status back to ready
				err = s.db.UpdateVideoStatus(video.ID, database.StatusReady, "")
				if err != nil {
					log.Printf("Error updating video status back to ready: %v", err)
				}

				log.Printf("Successfully uploaded video %s to R2 storage", video.ID)
			}
		}
	}()
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
				if video.R2HLSURL == "" || video.R2DASHURL == "" {
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

	// Remove DASH directory
	if video.DASHPath != "" {
		if err := os.RemoveAll(video.DASHPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Error removing DASH directory %s: %v", video.DASHPath, err)
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

	// Check if HLS and DASH directories exist
	if _, err := os.Stat(video.HLSPath); os.IsNotExist(err) {
		return fmt.Errorf("HLS directory %s does not exist", video.HLSPath)
	}

	if _, err := os.Stat(video.DASHPath); os.IsNotExist(err) {
		return fmt.Errorf("DASH directory %s does not exist", video.DASHPath)
	}

	// Upload HLS stream
	hlsURL, err := s.r2Storage.UploadHLSStream(video.HLSPath, video.ID)
	if err != nil {
		s.db.UpdateVideoStatus(video.ID, database.StatusFailed, fmt.Sprintf("HLS upload error: %v", err))
		return fmt.Errorf("error uploading HLS stream: %v", err)
	}

	// Upload DASH stream
	dashURL, err := s.r2Storage.UploadDASHStream(video.DASHPath, video.ID)
	if err != nil {
		s.db.UpdateVideoStatus(video.ID, database.StatusFailed, fmt.Sprintf("DASH upload error: %v", err))
		return fmt.Errorf("error uploading DASH stream: %v", err)
	}

	// Update video metadata with R2 information
	video.R2HLSURL = hlsURL
	video.R2DASHURL = dashURL
	video.R2HLSPath = fmt.Sprintf("hls/%s", video.ID)
	video.R2DASHPath = fmt.Sprintf("dash/%s", video.ID)
	video.Status = database.StatusReady

	// Update the complete video metadata
	if err := s.db.UpdateVideo(*video); err != nil {
		return fmt.Errorf("error updating video metadata: %v", err)
	}

	log.Printf("Successfully uploaded video %s to R2 storage", video.ID)
	return nil
}