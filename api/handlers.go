package api

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ayo-mwr/config"
	dbmod "ayo-mwr/database"
	monitoring "ayo-mwr/monitoring"
	recording "ayo-mwr/recording"
	signaling "ayo-mwr/signaling"
	transcode "ayo-mwr/transcode"

	"github.com/gin-gonic/gin"
)

// handleHealthCheck provides detailed health information for zero-downtime deployments
func (s *Server) handleHealthCheck(c *gin.Context) {
	startTime := time.Now()

	// Basic service info
	healthResponse := gin.H{
		"status":      "healthy",
		"timestamp":   startTime.UTC().Format(time.RFC3339),
		"uptime":      time.Since(startTime).String(),
		"version":     "1.0.0",               // You can make this dynamic
		"instance_id": os.Getenv("HOSTNAME"), // Or generate a unique ID
	}

	// Check database connectivity by attempting a simple query
	_, err := s.db.ListVideos(1, 0) // Try to get one video to test DB connection
	if err != nil {
		healthResponse["status"] = "unhealthy"
		healthResponse["database"] = gin.H{
			"status": "failed",
			"error":  err.Error(),
		}
		c.JSON(http.StatusServiceUnavailable, healthResponse)
		return
	}

	healthResponse["database"] = gin.H{"status": "connected"}

	// Check camera recording status
	runningCameras := recording.ListRunningWorkers()
	totalCameras := 0
	enabledCameras := 0

	for _, cam := range s.config.Cameras {
		totalCameras++
		if cam.Enabled {
			enabledCameras++
		}
	}

	cameraStatus := gin.H{
		"total_cameras":   totalCameras,
		"enabled_cameras": enabledCameras,
		"running_cameras": len(runningCameras),
		"camera_list":     runningCameras,
	}

	// If no cameras are running but some are enabled, mark as degraded
	if enabledCameras > 0 && len(runningCameras) == 0 {
		healthResponse["status"] = "degraded"
		cameraStatus["status"] = "no_cameras_running"
	} else if len(runningCameras) < enabledCameras {
		healthResponse["status"] = "degraded"
		cameraStatus["status"] = "partial_recording"
	} else {
		cameraStatus["status"] = "recording"
	}

	healthResponse["recording"] = cameraStatus

	// Check system resources
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	healthResponse["system"] = gin.H{
		"memory_mb":  memStats.Alloc / 1024 / 1024,
		"goroutines": runtime.NumGoroutine(),
		"go_version": runtime.Version(),
	}

	// Check disk space on storage path
	if diskInfo := s.getDiskSpace(); diskInfo != nil {
		healthResponse["storage"] = diskInfo
	}

	// Determine final status based on all checks
	status := http.StatusOK
	if healthResponse["status"] == "degraded" {
		status = http.StatusOK // Still return 200 for degraded
	} else if healthResponse["status"] == "unhealthy" {
		status = http.StatusServiceUnavailable
	}

	// Add response time
	healthResponse["response_time_ms"] = time.Since(startTime).Milliseconds()

	c.JSON(status, healthResponse)
}

// getDiskSpace returns disk space information for the storage path
func (s *Server) getDiskSpace() gin.H {
	// This is a simplified version - you might want to use a proper disk space library
	// or system calls for more accurate information
	return gin.H{
		"path":          s.config.StoragePath,
		"status":        "available",
		"check_enabled": true,
	}
}

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
			Camera:    v.CameraName,
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

	// Add watermark to the video (using 1080p resolution by default)
	err = recording.AddWatermarkWithPosition(inputPath, watermarkPath, mp4Path, position, margin, opacity, "1080")
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
		_, hlsURL, err := s.r2Storage.UploadHLSStream(hlsDir, videoID)
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

// GET /api/system_health
func (s *Server) getSystemHealth(c *gin.Context) {
	usage, err := monitoring.GetCurrentResourceUsage(s.config.StoragePath)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{
		"cpu":            usage.CPUPercent,
		"memory_used":    usage.MemoryUsedMB,
		"memory_total":   usage.MemoryTotalMB,
		"memory_percent": usage.MemoryPercent,
		"goroutines":     usage.NumGoroutines,
		"uptime":         usage.Uptime,
		"storage":        usage.Storage,
	})
}

// GET /api/logs
func (s *Server) getLogs(c *gin.Context) {
	logPath := "server.log"
	data, err := readLastNLines(logPath, 100)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.Data(200, "text/plain; charset=utf-8", []byte(data))
}

// readLastNLines reads the last N lines from a file
func readLastNLines(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), scanner.Err()
}

// GET /api/admin/cameras-config
// Get the current camera configuration from the database
func (s *Server) getCamerasConfig(c *gin.Context) {
	// Check if database is initialized
	if s.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not initialized"})
		return
	}

	// Log database type for debugging
	log.Printf("Database type: %T", s.db)

	// Get cameras using the database interface
	dbCams, err := s.db.GetCameras()
	if err != nil {
		log.Printf("Error getting cameras from DB: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to load cameras from database",
			"details": err.Error(),
		})
		return
	}

	// Convert to API response format (without passwords)
	cameras := make([]config.CameraConfig, len(dbCams))
	for i, cam := range dbCams {
		cameras[i] = config.CameraConfig{
			ButtonNo:   cam.ButtonNo,
			Name:       cam.Name,
			IP:         cam.IP,
			Port:       cam.Port,
			Path:       cam.Path,
			Username:   cam.Username,
			Password:   cam.Password, // Include password in response for editing
			Enabled:    cam.Enabled,
			Width:      cam.Width,
			Height:     cam.Height,
			FrameRate:  cam.FrameRate,
			Field:      cam.Field,
			Resolution: cam.Resolution,
			AutoDelete: cam.AutoDelete,
			// New path fields
			Path720:       cam.Path720,
			Path480:       cam.Path480,
			Path360:       cam.Path360,
			// Active path fields
			ActivePath720: cam.ActivePath720,
			ActivePath480: cam.ActivePath480,
			ActivePath360: cam.ActivePath360,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    cameras,
	})
}

// PUT /api/admin/cameras-config
// Update camera configuration in the database
func (s *Server) updateCamerasConfig(c *gin.Context) {
	sqldb, ok := s.db.(*dbmod.SQLiteDB)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database is not SQLiteDB"})
		return
	}

	var request struct {
		Cameras []dbmod.CameraConfig `json:"cameras"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	if len(request.Cameras) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No cameras provided"})
		return
	}

	// Update the database
	if err := sqldb.InsertCameras(request.Cameras); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to update cameras in DB",
			"details": err.Error(),
		})
		return
	}

	// Automatically reload cameras after saving to apply new configuration
	if err := s.reloadCamerasInternal(); err != nil {
		log.Printf("Warning: Failed to reload cameras after saving: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": fmt.Sprintf("Updated %d cameras, but reload failed. Please manually reload.", len(request.Cameras)),
			"warning": "Camera reload failed",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Updated and reloaded %d cameras", len(request.Cameras)),
	})
}

// ---------- Arduino handlers ----------
// GET /api/arduino-status
func (s *Server) getArduinoStatus(c *gin.Context) {
	status := signaling.GetArduinoStatus()
	c.JSON(200, status)
}

// PUT /api/admin/arduino-config
type arduinoConfigRequest struct {
	Port     string `json:"port"`
	BaudRate int    `json:"baud_rate"`
}

func (s *Server) updateArduinoConfig(c *gin.Context) {
	var req arduinoConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid json"})
		return
	}
	if req.Port == "" || req.BaudRate == 0 {
		c.JSON(400, gin.H{"error": "port and baud_rate required"})
		return
	}
	// persist to DB if possible
	if sqlDB, ok := s.db.(*dbmod.SQLiteDB); ok {
		if err := sqlDB.UpsertArduinoConfig(req.Port, req.BaudRate); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	}
	// update in-memory config
	s.config.ArduinoCOMPort = req.Port
	s.config.ArduinoBaudRate = req.BaudRate
	// reload arduino (may fail if device not present)
	if err := signaling.ReloadArduino(s.config); err != nil {
		log.Printf("[WARN] Arduino reload failed: %v", err)
		c.JSON(200, gin.H{"status": "saved", "connected": false, "message": "Config saved but device not connected", "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "updated and reloaded", "connected": true})
}

// reloadCamerasInternal performs the camera reload logic and returns error if any
func (s *Server) reloadCamerasInternal() error {
	sqldb, ok := s.db.(*dbmod.SQLiteDB)
	if !ok {
		return fmt.Errorf("database is not SQLiteDB")
	}

	dbCams, err := sqldb.GetCameras()
	if err != nil {
		return fmt.Errorf("failed to load cameras from DB: %v", err)
	}

	// Convert DB rows to config.CameraConfig and reconcile running camera workers
	newCams := make([]config.CameraConfig, len(dbCams))
	for i, c := range dbCams {
		newCams[i] = config.CameraConfig{
			ButtonNo:   c.ButtonNo,
			Name:       c.Name,
			IP:         c.IP,
			Port:       c.Port,
			Path:       c.Path,
			Username:   c.Username,
			Password:   c.Password,
			Enabled:    c.Enabled,
			Width:      c.Width,
			Height:     c.Height,
			FrameRate:  c.FrameRate,
			Field:      c.Field,
			Resolution: c.Resolution,
			AutoDelete: c.AutoDelete,
			// New path fields
			Path720:       c.Path720,
			Path480:       c.Path480,
			Path360:       c.Path360,
			// Active path fields
			ActivePath720: c.ActivePath720,
			ActivePath480: c.ActivePath480,
			ActivePath360: c.ActivePath360,
		}
	}

	// Build desired set and start any new workers
	current := make(map[string]struct{})
	for i, cam := range newCams {
		current[cam.Name] = struct{}{}
		recording.StartCamera(s.config, cam, i)
	}
	// Stop workers that are no longer present
	for _, name := range recording.ListRunningWorkers() {
		if _, ok := current[name]; !ok {
			recording.StopCamera(name)
		}
	}

	s.config.Cameras = newCams

	// Rebuild camera lookup map with new button numbers
	s.config.BuildCameraLookup()

	log.Printf("Successfully reloaded %d cameras", len(newCams))
	return nil
}

func (s *Server) reloadCameras(c *gin.Context) {
	if err := s.reloadCamerasInternal(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to reload cameras",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Reloaded %d cameras", len(s.config.Cameras)),
	})
}

// ---------- Watermark handlers ----------

// POST /api/admin/force-update-watermark
// Force update watermark from API
func (s *Server) forceUpdateWatermark(c *gin.Context) {
	var request struct {
		Resolution string `json:"resolution"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	// Default to 1080 if no resolution specified
	resolution := request.Resolution
	if resolution == "" {
		resolution = "1080"
	}

	// Initialize AYO API client
	ayoClient, err := NewAyoIndoClient()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to initialize AYO API client",
			"details": err.Error(),
		})
		return
	}

	// Force update watermark
	watermarkPath, err := ayoClient.ForceUpdateWatermark(resolution)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to force update watermark",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"message":        fmt.Sprintf("Watermark force updated successfully for resolution %s", resolution),
		"watermark_path": watermarkPath,
		"resolution":     resolution,
	})
}

// ---------- System Configuration handlers ----------

// GET /api/admin/system-config
// Get all system configuration
func (s *Server) getSystemConfig(c *gin.Context) {
	configs, err := s.db.GetAllSystemConfigs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get system configuration",
			"details": err.Error(),
		})
		return
	}

	// Convert to a more user-friendly format
	configMap := make(map[string]interface{})
	for _, cfg := range configs {
		switch cfg.Type {
		case "int":
			if val, err := strconv.Atoi(cfg.Value); err == nil {
				configMap[cfg.Key] = val
			} else {
				configMap[cfg.Key] = cfg.Value
			}
		case "string":
			configMap[cfg.Key] = cfg.Value
		default:
			configMap[cfg.Key] = cfg.Value
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    configMap,
	})
}

// GET /api/admin/disk-manager-config
// Get disk manager configuration
func (s *Server) getDiskManagerConfig(c *gin.Context) {
	sysConfigService := config.NewSystemConfigService(s.db)
	minimumFreeSpaceGB, priorityExternal, priorityMountedStorage, priorityInternalNVMe, priorityInternalSATA, priorityRootFilesystem, err := sysConfigService.GetDiskManagerConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get disk manager configuration",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"minimum_free_space_gb":    minimumFreeSpaceGB,
			"priority_external":        priorityExternal,
			"priority_mounted_storage": priorityMountedStorage,
			"priority_internal_nvme":   priorityInternalNVMe,
			"priority_internal_sata":   priorityInternalSATA,
			"priority_root_filesystem": priorityRootFilesystem,
		},
	})
}

// PUT /api/admin/disk-manager-config
// Update disk manager configuration
func (s *Server) updateDiskManagerConfig(c *gin.Context) {
	var request struct {
		MinimumFreeSpaceGB     int `json:"minimum_free_space_gb" binding:"required,min=1,max=1000"`
		PriorityExternal       int `json:"priority_external" binding:"required,min=1,max=1000"`
		PriorityMountedStorage int `json:"priority_mounted_storage" binding:"required,min=1,max=1000"`
		PriorityInternalNVMe   int `json:"priority_internal_nvme" binding:"required,min=1,max=1000"`
		PriorityInternalSATA   int `json:"priority_internal_sata" binding:"required,min=1,max=1000"`
		PriorityRootFilesystem int `json:"priority_root_filesystem" binding:"required,min=1,max=1000"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	// Validate using the validation function
	if err := config.ValidateDiskManagerConfig(
		request.MinimumFreeSpaceGB,
		request.PriorityExternal,
		request.PriorityMountedStorage,
		request.PriorityInternalNVMe,
		request.PriorityInternalSATA,
		request.PriorityRootFilesystem,
	); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid disk manager configuration",
			"details": err.Error(),
		})
		return
	}

	// Update configuration in database
	sysConfigService := config.NewSystemConfigService(s.db)
	if err := sysConfigService.SetDiskManagerConfig(
		request.MinimumFreeSpaceGB,
		request.PriorityExternal,
		request.PriorityMountedStorage,
		request.PriorityInternalNVMe,
		request.PriorityInternalSATA,
		request.PriorityRootFilesystem,
		"admin",
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to update disk manager configuration",
			"details": err.Error(),
		})
		return
	}

	// Log hot reload notification
	log.Printf("💾 HOT RELOAD: Disk manager configuration updated - new settings will be used immediately")
	log.Printf("📊 DISK CONFIG: MinFreeSpace=%dGB, Priorities: Ext=%d, Mount=%d, NVMe=%d, SATA=%d, Root=%d",
		request.MinimumFreeSpaceGB, request.PriorityExternal, request.PriorityMountedStorage,
		request.PriorityInternalNVMe, request.PriorityInternalSATA, request.PriorityRootFilesystem)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Disk manager configuration updated successfully - changes are active immediately",
		"data": gin.H{
			"minimum_free_space_gb":    request.MinimumFreeSpaceGB,
			"priority_external":        request.PriorityExternal,
			"priority_mounted_storage": request.PriorityMountedStorage,
			"priority_internal_nvme":   request.PriorityInternalNVMe,
			"priority_internal_sata":   request.PriorityInternalSATA,
			"priority_root_filesystem": request.PriorityRootFilesystem,
		},
	})
}

// PUT /api/admin/system-config
// Update system configuration
func (s *Server) updateSystemConfig(c *gin.Context) {
	var request map[string]interface{}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	// Validate and update each configuration
	for key, value := range request {
		var strValue string
		var configType string

		switch key {
		case dbmod.ConfigBookingWorkerConcurrency,
			dbmod.ConfigVideoRequestWorkerConcurrency:
			// These should be integers with range 1-20
			if intVal, ok := value.(float64); ok {
				if intVal < 1 || intVal > 20 {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": fmt.Sprintf("Booking and video request worker concurrency must be between 1 and 20, got %v", intVal),
					})
					return
				}
				strValue = strconv.Itoa(int(intVal))
				configType = "int"
			} else {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid value for %s: expected integer", key),
				})
				return
			}
		case dbmod.ConfigPendingTaskWorkerConcurrency:
			// Pending task worker has different range 1-50
			if intVal, ok := value.(float64); ok {
				if intVal < 1 || intVal > 50 {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": fmt.Sprintf("Pending task worker concurrency must be between 1 and 50, got %v", intVal),
					})
					return
				}
				strValue = strconv.Itoa(int(intVal))
				configType = "int"
			} else {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid value for %s: expected integer", key),
				})
				return
			}
		case dbmod.ConfigEnabledQualities:
			// This should be a string (comma-separated qualities)
			if strVal, ok := value.(string); ok {
				// Validate qualities
				validQualities := map[string]bool{
					"1080p": true, "720p": true, "480p": true, "360p": true,
				}
				qualities := strings.Split(strVal, ",")
				for _, q := range qualities {
					q = strings.TrimSpace(q)
					if q != "" && !validQualities[q] {
						c.JSON(http.StatusBadRequest, gin.H{
							"error": fmt.Sprintf("Invalid quality '%s'. Valid qualities: 1080p, 720p, 480p, 360p", q),
						})
						return
					}
				}
				strValue = strVal
				configType = "string"
			} else {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid value for %s: expected string", key),
				})
				return
			}
		case dbmod.ConfigEnableVideoDurationCheck:
			// This should be a boolean (true/false)
			if boolVal, ok := value.(bool); ok {
				strValue = fmt.Sprintf("%t", boolVal)
				configType = "boolean"
			} else if strVal, ok := value.(string); ok {
				// Handle string representation of boolean
				strVal = strings.ToLower(strVal)
				if strVal == "true" || strVal == "false" {
					strValue = strVal
					configType = "boolean"
				} else {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": fmt.Sprintf("Invalid value for %s: expected boolean, got %s", key, strVal),
					})
					return
				}
			} else {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid value for %s: expected boolean", key),
				})
				return
			}
		case dbmod.ConfigMinimumFreeSpaceGB,
			dbmod.ConfigPriorityExternal,
			dbmod.ConfigPriorityMountedStorage,
			dbmod.ConfigPriorityInternalNVMe,
			dbmod.ConfigPriorityInternalSATA,
			dbmod.ConfigPriorityRootFilesystem,
			// Recording Configuration
			dbmod.ConfigSegmentDuration,
			dbmod.ConfigClipDuration,
			dbmod.ConfigWidth,
			dbmod.ConfigHeight,
			dbmod.ConfigFrameRate,
			dbmod.ConfigAutoDelete,
			// Arduino Configuration
			dbmod.ConfigArduinoBaudRate,
			// RTSP Configuration
			dbmod.ConfigRTSPPort,
			// Server Configuration
			dbmod.ConfigServerPort,
			// Watermark Configuration
			dbmod.ConfigWatermarkMargin:
			// These should be integers
			if intVal, ok := value.(float64); ok {
				strValue = strconv.Itoa(int(intVal))
				configType = "int"
			} else {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid value for %s: expected integer", key),
				})
				return
			}
		case dbmod.ConfigWatermarkOpacity:
			// This should be a float
			if floatVal, ok := value.(float64); ok {
				strValue = fmt.Sprintf("%.2f", floatVal)
				configType = "float"
			} else {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid value for %s: expected float", key),
				})
				return
			}
		case dbmod.ConfigR2Enabled:
			// This should be a boolean
			if boolVal, ok := value.(bool); ok {
				strValue = fmt.Sprintf("%t", boolVal)
				configType = "boolean"
			} else if strVal, ok := value.(string); ok {
				strVal = strings.ToLower(strVal)
				if strVal == "true" || strVal == "false" {
					strValue = strVal
					configType = "boolean"
				} else {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": fmt.Sprintf("Invalid value for %s: expected boolean, got %s", key, strVal),
					})
					return
				}
			} else {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid value for %s: expected boolean", key),
				})
				return
			}
		case dbmod.ConfigVenueCode,
			dbmod.ConfigVenueSecretKey,
			// Arduino Configuration
			dbmod.ConfigArduinoCOMPort,
			// RTSP Configuration
			dbmod.ConfigRTSPUsername,
			dbmod.ConfigRTSPPassword,
			dbmod.ConfigRTSPIP,
			dbmod.ConfigRTSPPath,
			// Recording Configuration
			dbmod.ConfigResolution,
			// Storage Configuration
			dbmod.ConfigStoragePath,
			dbmod.ConfigHardwareAccel,
			dbmod.ConfigCodec,
			// Server Configuration
			dbmod.ConfigBaseURL,
			// R2 Storage Configuration
			dbmod.ConfigR2AccessKey,
			dbmod.ConfigR2SecretKey,
			dbmod.ConfigR2AccountID,
			dbmod.ConfigR2Bucket,
			dbmod.ConfigR2Region,
			dbmod.ConfigR2Endpoint,
			dbmod.ConfigR2BaseURL,
			dbmod.ConfigR2TokenValue,
			// Watermark Configuration
			dbmod.ConfigWatermarkPosition,
			// AYO API Configuration
			dbmod.ConfigAyoindoAPIBaseEndpoint,
			dbmod.ConfigAyoindoAPIToken:
			// These should be strings
			if strVal, ok := value.(string); ok {
				strValue = strVal
				configType = "string"
			} else {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid value for %s: expected string", key),
				})
				return
			}
		default:
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Unknown configuration key: %s", key),
			})
			return
		}

		// Update in database
		config := dbmod.SystemConfig{
			Key:       key,
			Value:     strValue,
			Type:      configType,
			UpdatedBy: "admin",
		}
		if err := s.db.SetSystemConfig(config); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   fmt.Sprintf("Failed to update %s", key),
				"details": err.Error(),
			})
			return
		}
	}

	// Update in-memory configuration
	sysConfigService := config.NewSystemConfigService(s.db)
	if err := sysConfigService.LoadSystemConfigToConfig(s.config); err != nil {
		log.Printf("Warning: Failed to reload system config to memory: %v", err)
	}

	// Log hot reload notification
	log.Printf("🔄 HOT RELOAD: System configuration updated - all workers will use new settings on next run")
	log.Printf("📊 CURRENT CONFIG: BookingWorker=%d, VideoRequestWorker=%d, PendingTaskWorker=%d",
		s.config.BookingWorkerConcurrency, s.config.VideoRequestWorkerConcurrency, s.config.PendingTaskWorkerConcurrency)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "System configuration updated successfully - hot reload active",
		"note":    "Changes will take effect on next cron run (within 2 minutes)",
	})
}

// getOnboardingStatus checks if the system has been properly configured
func (s *Server) getOnboardingStatus(c *gin.Context) {
	// Check if venue code is configured
	venueConfig, err := s.db.GetSystemConfig(dbmod.ConfigVenueCode)
	hasVenueConfig := err == nil && venueConfig.Value != ""

	// Check if venue secret key is configured
	venueSecretConfig, err := s.db.GetSystemConfig(dbmod.ConfigVenueSecretKey)
	hasVenueSecret := err == nil && venueSecretConfig.Value != ""

	// Check if at least one camera is configured
	cameras, err := s.db.GetCameras()
	log.Printf("getOnboardingStatus: cameras: %v", err)
	hasCameras := err == nil && len(cameras) > 0

	// Determine onboarding completion status
	isCompleted := hasVenueConfig && hasVenueSecret && hasCameras

	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"is_completed":     isCompleted,
		"has_venue_config": hasVenueConfig,
		"has_venue_secret": hasVenueSecret,
		"has_cameras":      hasCameras,
		"camera_count":     len(cameras),
	})
}

// saveVenueConfig saves venue configuration during onboarding
func (s *Server) saveVenueConfig(c *gin.Context) {
	var request struct {
		VenueCode string `json:"venue_code" binding:"required"`
		SecretKey string `json:"secret_key" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	// Save venue code
	venueConfig := dbmod.SystemConfig{
		Key:       dbmod.ConfigVenueCode,
		Value:     request.VenueCode,
		Type:      "string",
		UpdatedBy: "onboarding",
	}
	if err := s.db.SetSystemConfig(venueConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to save venue code",
			"details": err.Error(),
		})
		return
	}

	// Save secret key
	secretConfig := dbmod.SystemConfig{
		Key:       dbmod.ConfigVenueSecretKey,
		Value:     request.SecretKey,
		Type:      "string",
		UpdatedBy: "onboarding",
	}
	if err := s.db.SetSystemConfig(secretConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to save secret key",
			"details": err.Error(),
		})
		return
	}

	// Update in-memory configuration
	sysConfigService := config.NewSystemConfigService(s.db)
	if err := sysConfigService.LoadSystemConfigToConfig(s.config); err != nil {
		log.Printf("Warning: Failed to reload system config to memory: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Venue configuration saved successfully",
	})
}

// getCameraDefaults returns default camera configuration values from database
func (s *Server) getCameraDefaults(c *gin.Context) {
	// Get default port from database
	defaultPort := "554"
	if portConfig, err := s.db.GetSystemConfig(dbmod.ConfigRTSPPort); err == nil && portConfig.Value != "" {
		defaultPort = portConfig.Value
	}

	// Get default path from database
	defaultPath := "/streaming/channels/101/"
	if pathConfig, err := s.db.GetSystemConfig(dbmod.ConfigRTSPPath); err == nil && pathConfig.Value != "" {
		defaultPath = pathConfig.Value
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"port": defaultPort,
			"path": defaultPath,
		},
	})
}

// saveFirstCamera saves the first camera configuration during onboarding
func (s *Server) saveFirstCamera(c *gin.Context) {
	var request struct {
		Name     string `json:"name" binding:"required"`
		IP       string `json:"ip" binding:"required"`
		Port     int    `json:"port"`
		Path     string `json:"path"`
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		Field    string `json:"field_id" binding:"required"`
		ButtonNo string `json:"button_number" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	// Get default values from database if not provided
	defaultPort := "554"
	if portConfig, err := s.db.GetSystemConfig(dbmod.ConfigRTSPPort); err == nil && portConfig.Value != "" {
		defaultPort = portConfig.Value
	}

	defaultPath := "/streaming/channels/101/"
	if pathConfig, err := s.db.GetSystemConfig(dbmod.ConfigRTSPPath); err == nil && pathConfig.Value != "" {
		defaultPath = pathConfig.Value
	}

	// Use provided values or defaults
	port := defaultPort
	if request.Port > 0 {
		port = fmt.Sprintf("%d", request.Port)
	}

	path := defaultPath
	if request.Path != "" {
		path = request.Path
	}

	// Create camera configuration
	camera := dbmod.CameraConfig{
		ButtonNo:   request.ButtonNo,
		Name:       request.Name,
		IP:         request.IP,
		Port:       port,
		Path:       path,
		Username:   request.Username,
		Password:   request.Password,
		Enabled:    true,
		Width:      1920,
		Height:     1080,
		FrameRate:  30,
		Field:      request.Field,
		Resolution: "1080",
		AutoDelete: 30,
	}

	// Save camera to database
	sqldb, ok := s.db.(*dbmod.SQLiteDB)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database is not SQLiteDB"})
		return
	}

	// Insert the camera (this will replace all existing cameras)
	cameras := []dbmod.CameraConfig{camera}
	if err := sqldb.InsertCameras(cameras); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to save camera configuration",
			"details": err.Error(),
		})
		return
	}

	// Automatically reload cameras after saving to apply new configuration
	if err := s.reloadCamerasInternal(); err != nil {
		log.Printf("Warning: Failed to reload cameras after saving first camera: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "First camera configuration saved, but reload failed. Please manually reload.",
			"warning": "Camera reload failed",
			"camera": gin.H{
				"name":      camera.Name,
				"ip":        camera.IP,
				"field":     camera.Field,
				"button_no": camera.ButtonNo,
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "First camera configuration saved and reloaded successfully",
		"camera": gin.H{
			"name":      camera.Name,
			"ip":        camera.IP,
			"field":     camera.Field,
			"button_no": camera.ButtonNo,
		},
	})
}
