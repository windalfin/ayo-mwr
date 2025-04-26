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

// CameraInfo is the structure returned by /api/cameras
// LastChecked is the last time the camera connection was tested (ISO8601 string)
type CameraInfo struct {
	Name        string `json:"name"`
	IP          string `json:"ip"`
	Port        string `json:"port"`
	Path        string `json:"path"`
	Status      string `json:"status"`
	LastChecked string `json:"last_checked"`
}

// GET /api/cameras
func (s *Server) listCameras(c *gin.Context) {
	var cameras []CameraInfo
	for _, cam := range s.config.Cameras {
		status := "offline"
		lastChecked := ""
		// Attempt to check RTSP connection (non-blocking, short timeout)
		fullURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s", cam.Username, cam.Password, cam.IP, cam.Port, cam.Path)
		ok, _ := recording.TestRTSPConnection(cam.Name, fullURL)
		if ok {
			status = "online"
		}
		lastChecked = time.Now().Format(time.RFC3339)
		cameras = append(cameras, CameraInfo{
			Name:        cam.Name,
			IP:          cam.IP,
			Port:        cam.Port,
			Path:        cam.Path,
			Status:      status,
			LastChecked: lastChecked,
		})
	}
	c.JSON(200, cameras)
}

// VideoInfo is the structure returned by /api/videos
type VideoInfo struct {
	ID        string `json:"id"`
	Camera    string `json:"camera"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	Duration  string `json:"duration"`
	Size      string `json:"size"`
	Cloud     bool   `json:"cloud"`
	Error     string `json:"error"`
	Action    string `json:"action"`
}

// GET /api/videos
func (s *Server) listVideos(c *gin.Context) {
	videos, _ := s.db.ListVideos(100, 0)
	var out []VideoInfo
	for _, v := range videos {
		out = append(out, VideoInfo{
			ID:        v.ID,
			Camera:    v.CameraID,
			Status:    string(v.Status),
			CreatedAt: v.CreatedAt.Format("2006-01-02 15:04"),
			Duration:  fmt.Sprintf("%.0fs", v.Duration),
			Size:      fmt.Sprintf("%.0fMB", float64(v.Size)/1024/1024),
			Cloud:     v.R2HLSURL != "",
			Error:     v.ErrorMessage,
			Action:    getVideoAction(v.Status),
		})
	}
	c.JSON(200, out)
}

func getVideoAction(status interface{}) string {
	switch status {
	case "ready":
		return "view"
	case "failed":
		return "retry"
	default:
		return ""
	}
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
	venueCode := s.config.VenueCode
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

	// Get watermark settings from environment
	position, margin, opacity := recording.GetWatermarkSettings()

	// Add watermark to the video
	err = recording.AddWatermarkWithPosition(inputPath, watermarkPath, mp4Path, position, margin, opacity)
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
		hlsURL, err := s.r2Storage.UploadHLSStream(hlsDir, videoID)
		if err != nil {
			fmt.Printf("[R2] Failed to upload HLS: %v\n", err)
		} else {
			fmt.Printf("[R2] HLS uploaded: %s\n", hlsURL)
		}

		mp4URL, err := s.r2Storage.UploadMP4(mp4Path, videoID)
		if err != nil {
			fmt.Printf("[R2] Failed to upload MP4: %v\n", err)
		} else {
			fmt.Printf("[R2] MP4 uploaded: %s\n", mp4URL)
		}
	} else {
		fmt.Println("R2 storage is nil, skipping upload.")
	}
}
