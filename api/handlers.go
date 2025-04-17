package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"ayo-mwr/recording"
	"ayo-mwr/signaling"
	"ayo-mwr/transcode"

	"github.com/gin-gonic/gin"
)

func (s *Server) listStreams(c *gin.Context) {
	limit := 20
	offset := 0
	videos, err := s.db.ListVideos(limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to list streams: %v", err)})
		return
	}

	streams := make([]gin.H, 0)
	for _, video := range videos {
		stream := gin.H{
			"id":        video.ID,
			"status":    video.Status,
			"createdAt": video.CreatedAt,
			"size":      video.Size,
		}

		if video.R2HLSURL != "" {
			stream["hlsUrl"] = video.R2HLSURL
			stream["usingCloud"] = true
		} else {
			stream["hlsUrl"] = video.HLSURL
			stream["usingCloud"] = false
		}

		streams = append(streams, stream)
	}

	c.JSON(http.StatusOK, gin.H{"streams": streams})
}

func (s *Server) getStream(c *gin.Context) {
	id := c.Param("id")
	video, err := s.db.GetVideo(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get stream: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         video.ID,
		"status":     video.Status,
		"createdAt":  video.CreatedAt,
		"size":       video.Size,
		"usingCloud": video.R2HLSURL != "",
		"hlsUrl":     video.R2HLSURL,
	})
}

// TranscodeRequest represents an HTTP request for transcoding
type TranscodeRequest struct {
	Timestamp  time.Time `json:"timestamp"`
	CameraName string    `json:"cameraName"` // Camera identifier
}

func (s *Server) handleUpload(c *gin.Context) {
	var req TranscodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid request: %v", err)})
		return
	}

	// TODO: Prevent concurrent requests for the same video/camera (locking)

	// Find the closest video file
	inputPath, err := signaling.FindClosestVideo(s.config.StoragePath, req.CameraName, req.Timestamp)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Failed to find video: %v", err)})
		return
	}

	// Extract video ID from the file path
	videoID := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))

	// getWatermark for that venue from config or env
	venueCode := req.CameraName
	recordingName := fmt.Sprintf("wm_%s.mp4", videoID)

	// MP4 output: /recordings/[camera]/[recordingName]
	mp4Path := filepath.Join(s.config.StoragePath, "recordings", req.CameraName, "mp4", recordingName)
	// HLS output dir: /recordings/[camera]/hls/[videoId]
	hlsDir := filepath.Join(s.config.StoragePath, "recordings", req.CameraName, "hls", videoID)

	watermarkPath, err := recording.GetWatermark(venueCode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get watermark: %v", err)})
		return
	}

	// Add watermark to the video
	err = recording.AddWatermarkWithPosition(inputPath, watermarkPath, mp4Path, recording.TopRight, 10, 0.6)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to add watermark: %v", err)})
		return
	}

	// Transcode the watermarked video
	urls, timings, err := transcode.TranscodeVideo(mp4Path, videoID, req.CameraName, s.config)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Transcoding failed: %v", err)})
		return
	}

	// Return the URLs and timings
	c.JSON(http.StatusOK, gin.H{
		"urls":     urls,
		"timings":  timings,
		"videoId":  videoID,
		"filename": filepath.Base(mp4Path),
	})

	// --- R2 Upload Integration ---
	// After successful transcoding, upload HLS and MP4 to R2
	if s.r2Storage != nil {

		// Upload HLS
		hlsURL, err := s.r2Storage.UploadHLSStream(hlsDir, videoID)
		if err != nil {
			fmt.Printf("[R2] Failed to upload HLS: %v\n", err)
		} else {
			fmt.Printf("[R2] HLS uploaded: %s\n", hlsURL)
		}

		// Upload MP4
		mp4URL, err := s.r2Storage.UploadMP4(mp4Path, videoID)
		if err != nil {
			fmt.Printf("[R2] Failed to upload MP4: %v\n", err)
		} else {
			fmt.Printf("[R2] MP4 uploaded: %s\n", mp4URL)
		}
	}
}
