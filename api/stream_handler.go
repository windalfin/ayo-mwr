package api

import (
	"fmt"
	"net/http"
	"path/filepath"

	"ayo-mwr/config"
	"ayo-mwr/streaming"

	"github.com/gin-gonic/gin"
)

var streamManager *streaming.StreamManager

func init() {
	streamManager = streaming.NewStreamManager()
}

// StartLiveStream starts a live stream for a camera
func (s *Server) startLiveStream(c *gin.Context) {
	cameraName := c.Param("camera")

	// Find camera in config
	var camera *config.CameraConfig
	for _, cam := range s.config.Cameras {
		if cam.Name == cameraName {
			camera = &cam
			break
		}
	}

	if camera == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Camera not found"})
		return
	}

	// Build RTSP URL
	rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
		camera.Username,
		camera.Password,
		camera.IP,
		camera.Port,
		camera.Path)

	// Start the stream
	err := streamManager.AddStream(cameraName, rtspURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to start stream: %v", err)})
		return
	}

	// Return HLS URL with the correct path
	hlsURL := fmt.Sprintf("/videos/recordings/%s/hls/stream.m3u8", cameraName)
	c.JSON(http.StatusOK, gin.H{
		"status": "streaming",
		"hlsUrl": hlsURL,
	})
}

// StopLiveStream stops a live stream
func (s *Server) stopLiveStream(c *gin.Context) {
	cameraName := c.Param("camera")
	streamManager.RemoveStream(cameraName)
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// ServeLiveStream serves HLS stream content
func (s *Server) serveLiveStream(c *gin.Context) {
	cameraName := c.Param("camera")
	path := c.Param("path")

	stream, ok := streamManager.GetStream(cameraName)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Stream not found"})
		return
	}

	if path == "/stream.m3u8" {
		c.Header("Content-Type", "application/vnd.apple.mpegurl")
		c.String(http.StatusOK, stream.HLSMuxer.GenerateM3U8())
		return
	}

	// Serve segment files
	http.ServeFile(c.Writer, c.Request, filepath.Join("/Users/windalfinculmen/Projects/ayo-mwr/videos/recordings", cameraName, "hls", filepath.Base(path)))
}

// RegisterStreamHandlers registers the stream-related routes
func (s *Server) RegisterStreamHandlers(router *gin.Engine) {
	router.POST("/api/stream/:camera/start", s.startLiveStream)
	router.POST("/api/stream/:camera/stop", s.stopLiveStream)
	router.GET("/api/stream/:camera/*path", s.serveLiveStream)
}
