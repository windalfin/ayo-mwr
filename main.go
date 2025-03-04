package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/service"
	"ayo-mwr/storage"
)

// UploadQueue represents a queue for handling captured video files
type UploadQueue struct {
	queue []string
	mu    sync.Mutex
}

// NewUploadQueue creates a new upload queue
func NewUploadQueue() *UploadQueue {
	return &UploadQueue{
		queue: make([]string, 0),
	}
}

// Put adds a filename to the upload queue
func (q *UploadQueue) Put(filename string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queue = append(q.queue, filename)
	log.Printf("Added file to processing queue: %s\n", filename)
}

// Get retrieves and removes the next file from the queue
func (q *UploadQueue) Get() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.queue) == 0 {
		return "", false
	}

	filename := q.queue[0]
	q.queue = q.queue[1:]
	return filename, true
}

// ProcessQueue continuously processes videos in the queue
func (q *UploadQueue) ProcessQueue(cfg config.Config, db database.Database, r2Storage *storage.R2Storage) {
	for {
		filename, ok := q.Get()
		if ok {
			videoID := filepath.Base(filename)
			videoID = videoID[:len(videoID)-len(filepath.Ext(videoID))]

			log.Printf("Processing video: %s\n", videoID)

			// Create metadata record in database
			fileInfo, err := os.Stat(filename)
			if err != nil {
				log.Printf("Error getting file info for %s: %v\n", filename, err)
				continue
			}

			// Create metadata
			metadata := database.VideoMetadata{
				ID:        videoID,
				CreatedAt: time.Now(),
				Status:    database.StatusProcessing,
				Size:      fileInfo.Size(),
				LocalPath: filename,
				CameraID:  "camera_A", // Could be parameterized
			}

			// Add to database
			if err := db.CreateVideo(metadata); err != nil {
				log.Printf("Error creating video record in database: %v\n", err)
			}

			// Generate HLS and DASH streams
			hlsPath := filepath.Join(cfg.StoragePath, "hls", videoID)
			dashPath := filepath.Join(cfg.StoragePath, "dash", videoID)

			// Start transcoding
			urls, _, err := transcodeVideo(filename, videoID, cfg)
			if err != nil {
				log.Printf("Error transcoding video %s: %v\n", videoID, err)
				db.UpdateVideoStatus(videoID, database.StatusFailed, fmt.Sprintf("Transcoding error: %v", err))
				continue
			}

			// Update metadata with successful transcoding
			metadata.Status = database.StatusReady
			metadata.HLSPath = hlsPath
			metadata.DASHPath = dashPath
			metadata.HLSURL = urls["hls"]
			metadata.DASHURL = urls["dash"]
			now := time.Now()
			metadata.FinishedAt = &now

			if err := db.UpdateVideo(metadata); err != nil {
				log.Printf("Error updating video record in database: %v\n", err)
			}

			// If R2 is enabled, the upload service worker will handle uploading
		} else {
			// No videos in queue, wait before checking again
			time.Sleep(1 * time.Second)
		}
	}
}

// CaptureRTSPSegments captures video from an RTSP stream using FFmpeg and saves it in segments
func CaptureRTSPSegments(cfg config.Config, uploadQueue *UploadQueue) error {
	// Construct the RTSP URL
	rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
		cfg.RTSPUsername,
		cfg.RTSPPassword,
		cfg.RTSPIP,
		cfg.RTSPPort,
		cfg.RTSPPath,
	)

	for {
		// Create a new segment file with timestamp
		timestamp := time.Now().Format("20060102_150405")
		outputFilename := fmt.Sprintf("camera_A_%s.mp4", timestamp)
		outputPath := filepath.Join(cfg.StoragePath, "uploads", outputFilename)

		log.Printf("Creating new video segment: %s\n", outputFilename)

		// Get input and output parameters based on hardware acceleration
		inputParams, _ := splitFFmpegParams(cfg.HardwareAccel, cfg.Codec)

		// For testing the connection first
		testCmd := exec.Command("ffmpeg", "-i", rtspURL, "-t", "1", "-f", "null", "-")
		var testOutput bytes.Buffer
		testCmd.Stderr = &testOutput

		log.Printf("Testing RTSP connection: %s", rtspURL)
		err := testCmd.Run()
		if err != nil {
			log.Printf("Error connecting to RTSP: %v", err)
			log.Printf("FFmpeg output: %s", testOutput.String())
			log.Printf("Retrying in 10 seconds...")
			time.Sleep(10 * time.Second)
			continue
		}

		// Construct FFmpeg command for capturing a segment with more detailed parameters
		ffmpegArgs := []string{
			"-rtsp_transport", "tcp", // Use TCP (more reliable than UDP)
		}

		// Add hardware acceleration parameters if configured
		ffmpegArgs = append(ffmpegArgs, inputParams...)

		// Add input
		ffmpegArgs = append(ffmpegArgs,
			"-i", rtspURL,
			"-t", fmt.Sprintf("%d", cfg.SegmentDuration),
		)

		// Add output parameters for better reliability
		ffmpegArgs = append(ffmpegArgs,
			"-c:v", "libx264", // Use H.264 codec
			"-preset", "ultrafast", // Fastest encoding
			"-tune", "zerolatency", // Reduce latency
			"-profile:v", "baseline",
			"-pix_fmt", "yuv420p",
			"-b:v", "2M", // 2 Mbps video bitrate
			"-bufsize", "4M",
			"-max_muxing_queue_size", "1024", // Prevent muxing queue errors
			"-f", "mp4",
			"-reset_timestamps", "1", // Reset timestamps to avoid errors
			"-movflags", "+faststart", // Optimize for web playback
			outputPath,
		)

		// Create and start FFmpeg command
		cmd := exec.Command("ffmpeg", ffmpegArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		log.Printf("Starting FFmpeg capture with command: ffmpeg %v", ffmpegArgs)

		if err := cmd.Start(); err != nil {
			log.Printf("Error starting FFmpeg: %v", err)
			time.Sleep(5 * time.Second) // Wait before retrying
			continue
		}

		// Wait for the FFmpeg process to complete
		err = cmd.Wait()
		if err != nil {
			log.Printf("FFmpeg process ended with error: %v", err)
		} else {
			log.Printf("FFmpeg process completed successfully")

			// Check if the file exists and has content
			if fileInfo, err := os.Stat(outputPath); err == nil && fileInfo.Size() > 0 {
				log.Printf("Recorded video segment: %s (%.2f MB)", outputPath, float64(fileInfo.Size())/(1024*1024))
				if uploadQueue != nil {
					uploadQueue.Put(outputPath)
				}
			} else {
				log.Printf("Output file is empty or doesn't exist")
			}
		}
	}
}

// transcodeVideo generates HLS and DASH formats from the MP4 file
func transcodeVideo(inputPath, videoID string, cfg config.Config) (map[string]string, map[string]float64, error) {
	hlsPath := filepath.Join(cfg.StoragePath, "hls", videoID)
	dashPath := filepath.Join(cfg.StoragePath, "dash", videoID)

	// Create output directories
	os.MkdirAll(hlsPath, 0755)
	os.MkdirAll(dashPath, 0755)

	timings := make(map[string]float64)
	inputParams, outputParams := splitFFmpegParams(cfg.HardwareAccel, cfg.Codec)

	// Create channels for error handling and synchronization
	errChan := make(chan error, 2)
	timesChan := make(chan struct {
		key   string
		value float64
	}, 2)

	// Start HLS transcoding in a goroutine
	go func() {
		hlsStart := time.Now()
		hlsCmd := exec.Command("ffmpeg", append(append(inputParams, "-i", inputPath),
			append(outputParams,
				"-hls_time", "4",
				"-hls_playlist_type", "vod",
				"-hls_segment_filename", filepath.Join(hlsPath, "segment_%03d.ts"),
				filepath.Join(hlsPath, "playlist.m3u8"))...)...)
		hlsCmd.Stdout = os.Stdout
		hlsCmd.Stderr = os.Stderr

		if err := hlsCmd.Run(); err != nil {
			errChan <- fmt.Errorf("HLS transcoding error: %v", err)
			return
		}
		timesChan <- struct {
			key   string
			value float64
		}{key: "hlsTranscode", value: time.Since(hlsStart).Seconds()}
		errChan <- nil
	}()

	// Start DASH transcoding in a goroutine
	go func() {
		dashStart := time.Now()
		dashCmd := exec.Command("ffmpeg", append(append(inputParams, "-i", inputPath),
			append(outputParams,
				"-f", "dash",
				"-use_timeline", "1",
				"-use_template", "1",
				"-seg_duration", "4",
				"-adaptation_sets", "id=0,streams=v id=1,streams=a",
				"-init_seg_name", filepath.Join(dashPath, "init-stream$RepresentationID$.m4s"),
				"-media_seg_name", filepath.Join(dashPath, "chunk-stream$RepresentationID$-$Number$.m4s"),
				filepath.Join(dashPath, "manifest.mpd"))...)...)
		dashCmd.Stdout = os.Stdout
		dashCmd.Stderr = os.Stderr

		if err := dashCmd.Run(); err != nil {
			errChan <- fmt.Errorf("DASH transcoding error: %v", err)
			return
		}
		timesChan <- struct {
			key   string
			value float64
		}{key: "dashTranscode", value: time.Since(dashStart).Seconds()}
		errChan <- nil
	}()

	// Wait for both transcoding processes to complete
	for i := 0; i < 2; i++ {
		if err := <-errChan; err != nil {
			return nil, nil, err
		}
	}

	// Collect timing information
	for i := 0; i < 2; i++ {
		timing := <-timesChan
		timings[timing.key] = timing.value
	}

	return map[string]string{
		"hls":  fmt.Sprintf("%s/hls/%s/playlist.m3u8", cfg.BaseURL, videoID),
		"dash": fmt.Sprintf("%s/dash/%s/manifest.mpd", cfg.BaseURL, videoID),
	}, timings, nil
}

// splitFFmpegParams returns appropriate FFmpeg parameters based on hardware acceleration and codec
func splitFFmpegParams(hwAccel, codec string) ([]string, []string) {
	var commonOutput []string
	switch codec {
	case "av1":
		commonOutput = []string{"-c:v", "libaom-av1", "-crf", "30", "-b:v", "0", "-strict", "experimental", "-c:a", "aac", "-b:a", "128k"}
	case "hevc":
		commonOutput = []string{"-c:v", "libx265", "-crf", "28", "-preset", "medium", "-c:a", "aac", "-b:a", "128k"}
	default: // avc
		commonOutput = []string{
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-tune", "fastdecode",
			"-profile:v", "baseline",
			"-level", "3.0",
			"-b:v", "2M",
			"-maxrate", "2.5M",
			"-bufsize", "5M",
			"-pix_fmt", "yuv420p",
			"-c:a", "aac",
			"-b:a", "128k",
			"-movflags", "+faststart",
			"-g", "48",
			"-keyint_min", "48",
			"-sc_threshold", "0",
			"-bf", "0",
		}
	}

	switch hwAccel {
	case "nvidia":
		codecParams := map[string][]string{
			"av1":  {"-c:v", "av1_nvenc"},
			"hevc": {"-c:v", "hevc_nvenc"},
			"avc":  {"-c:v", "h264_nvenc", "-preset", "p4", "-tune", "ll"},
		}
		return []string{"-hwaccel", "cuda"}, append(codecParams[codec], commonOutput...)
	case "intel":
		codecParams := map[string][]string{
			"hevc": {"-c:v", "hevc_qsv"},
			"avc":  {"-c:v", "h264_qsv", "-preset", "faster"},
		}
		if codec == "av1" {
			return []string{}, commonOutput // Fall back to software encoding for AV1
		}
		return []string{"-hwaccel", "qsv"}, append(codecParams[codec], commonOutput...)
	case "amd":
		codecParams := map[string][]string{
			"hevc": {"-c:v", "hevc_amf"},
			"avc":  {"-c:v", "h264_amf", "-quality", "speed"},
		}
		if codec == "av1" {
			return []string{}, commonOutput // Fall back to software encoding for AV1
		}
		return []string{"-hwaccel", "amf"}, append(codecParams[codec], commonOutput...)
	case "videotoolbox": // macOS
		codecParams := map[string][]string{
			"hevc": {"-c:v", "hevc_videotoolbox"},
			"avc":  {"-c:v", "h264_videotoolbox", "-allow_sw", "0", "-realtime", "1", "-profile:v", "high", "-tag:v", "avc1", "-threads", "0"},
		}
		if codec == "avc" {
			return []string{"-hwaccel", "videotoolbox"},
				[]string{
					"-c:v", "h264_videotoolbox",
					"-b:v", "2M",
					"-maxrate", "2.5M",
					"-bufsize", "5M",
					"-pix_fmt", "nv12",
					"-c:a", "aac",
					"-b:a", "128k",
				}
		}
		return []string{"-hwaccel", "videotoolbox", "-hwaccel_output_format", "videotoolbox_vld"}, append(codecParams[codec], commonOutput...)
	default: // No hardware acceleration
		return []string{}, commonOutput
	}
}

// setupWebServer configures and starts the web server for streaming
func setupWebServer(cfg config.Config, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService) {
	r := gin.Default()

	// Enable CORS
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Serve static files for HLS and DASH streaming
	r.Static("/hls", filepath.Join(cfg.StoragePath, "hls"))
	r.Static("/dash", filepath.Join(cfg.StoragePath, "dash"))

	// API endpoint to list available streams
	r.GET("/api/streams", func(c *gin.Context) {
		limit := 20
		offset := 0
		videos, err := db.ListVideos(limit, offset)
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

			// Add URLs based on whether R2 is enabled and URLs are available
			if video.R2HLSURL != "" && video.R2DASHURL != "" {
				stream["hlsUrl"] = video.R2HLSURL
				stream["dashUrl"] = video.R2DASHURL
				stream["usingCloud"] = true
			} else {
				stream["hlsUrl"] = video.HLSURL
				stream["dashUrl"] = video.DASHURL
				stream["usingCloud"] = false
			}

			streams = append(streams, stream)
		}

		c.JSON(http.StatusOK, gin.H{"streams": streams})
	})

	// API endpoint to get details for a specific stream
	r.GET("/api/streams/:id", func(c *gin.Context) {
		id := c.Param("id")
		video, err := db.GetVideo(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get stream: %v", err)})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":         video.ID,
			"status":     video.Status,
			"createdAt":  video.CreatedAt,
			"size":       video.Size,
			"usingCloud": video.R2HLSURL != "" && video.R2DASHURL != "",
			"hlsUrl":     video.R2HLSURL,
			"dashUrl":    video.R2DASHURL,
		})
	})

}
