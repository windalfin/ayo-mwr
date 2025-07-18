package api

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Updated %d cameras", len(request.Cameras)),
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

func (s *Server) reloadCameras(c *gin.Context) {
	sqldb, ok := s.db.(*dbmod.SQLiteDB)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database is not SQLiteDB"})
		return
	}

	dbCams, err := sqldb.GetCameras()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to load cameras from DB",
			"details": err.Error(),
		})
		return
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Reloaded %d cameras", len(newCams)),
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
			dbmod.ConfigPriorityRootFilesystem:
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
