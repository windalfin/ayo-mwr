package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/service"
	"ayo-mwr/storage"
	"ayo-mwr/transcode"

	"github.com/joho/godotenv"
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

			// Start transcoding using the transcode package
			urls, _, err := transcode.TranscodeVideo(filename, videoID, cfg)
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
func CaptureRTSPSegments(cfg config.Config, uploadService *service.UploadService) error {
	// Construct the RTSP URL
	rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
		cfg.RTSPUsername,
		cfg.RTSPPassword,
		cfg.RTSPIP,
		cfg.RTSPPort,
		cfg.RTSPPath,
	)

	// Create logs directory if it doesn't exist
	logDir := filepath.Join(cfg.StoragePath, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Error creating logs directory: %v", err)
	}

	for {
		// Create a new segment file with timestamp
		timestamp := time.Now().Format("20060102_150405")
		outputFilename := fmt.Sprintf("camera_A_%s.mp4", timestamp)
		outputPath := filepath.Join(cfg.StoragePath, "uploads", outputFilename)

		log.Printf("Creating new video segment: %s\n", outputFilename)

		// Get input and output parameters based on hardware acceleration
		inputParams, _ := transcode.SplitFFmpegParams(cfg.HardwareAccel, cfg.Codec)

		// Test the connection with a timeout
		testCmd := exec.Command("ffmpeg",
			"-rtsp_transport", "tcp", // Use TCP for testing too
			"-i", rtspURL,
			"-t", "1",
			"-f", "null",
			"-")

		var testOutput bytes.Buffer
		testCmd.Stderr = &testOutput

		// Create a channel to signal completion
		done := make(chan error, 1)

		log.Printf("Testing RTSP connection: %s", rtspURL)

		// Start the command
		if err := testCmd.Start(); err != nil {
			log.Printf("Error starting RTSP test: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// Wait for command in a goroutine with timeout
		go func() {
			done <- testCmd.Wait()
		}()

		// Wait with timeout
		select {
		case err := <-done:
			if err != nil {
				log.Printf("Error connecting to RTSP: %v", err)
				log.Printf("FFmpeg output: %s", testOutput.String())
				time.Sleep(10 * time.Second)
				continue
			}
			log.Printf("RTSP connection successful")
		case <-time.After(15 * time.Second):
			// Kill the process if it takes too long
			log.Printf("RTSP connection test timed out after 15 seconds")
			testCmd.Process.Kill()
			time.Sleep(10 * time.Second)
			continue
		}

		// Construct FFmpeg command for capturing a segment with more detailed parameters
		ffmpegArgs := []string{
			"-rtsp_transport", "tcp", // Use TCP (more reliable than UDP)
			"-timeout", "5000000", // General IO timeout in microseconds (5 seconds) - older param
			"-fflags", "nobuffer", // Reduce buffering and latency
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

		// Create a log file for FFmpeg error output
		logFile, err := os.Create(filepath.Join(logDir, fmt.Sprintf("ffmpeg_%s.log", timestamp)))
		if err != nil {
			log.Printf("Error creating FFmpeg log file: %v", err)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		} else {
			defer logFile.Close()
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		}

		log.Printf("Starting FFmpeg capture with command: ffmpeg %v", ffmpegArgs)

		// Create a channel for capture command completion
		captureDone := make(chan error, 1)

		if err := cmd.Start(); err != nil {
			log.Printf("Error starting FFmpeg: %v", err)
			time.Sleep(5 * time.Second) // Wait before retrying
			continue
		}

		// Wait for the FFmpeg process to complete with timeout
		go func() {
			captureDone <- cmd.Wait()
		}()

		// Wait with a generous timeout - it should complete normally based on -t parameter
		// but this handles potential hangs
		select {
		case err := <-captureDone:
			if err != nil {
				log.Printf("FFmpeg process ended with error: %v", err)
			} else {
				log.Printf("FFmpeg process completed successfully")

				// Check if the file exists and has content
				if fileInfo, err := os.Stat(outputPath); err == nil && fileInfo.Size() > 0 {
					log.Printf("Recorded video segment: %s (%.2f MB)", outputPath, float64(fileInfo.Size())/(1024*1024))
					if uploadService != nil {
						// Use the new ProcessVideoFile method instead of UploadVideo
						go uploadService.ProcessVideoFile(outputPath)
					}
				} else {
					log.Printf("Output file is empty or doesn't exist")
				}
			}
		case <-time.After(time.Duration(cfg.SegmentDuration+30) * time.Second):
			// Kill the process if it takes too long (segment duration + 30 seconds buffer)
			log.Printf("FFmpeg capture timed out, killing process")
			cmd.Process.Kill()
			time.Sleep(5 * time.Second)
		}
	}
}

// setupWebServer configures and starts the web server for streaming
func setupWebServer(cfg config.Config, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService) {
	server := api.NewServer(cfg, db, r2Storage, uploadService)
	server.Start()
}

func main() {

	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file:", err)
	}

	// Load config
	cfg := config.LoadConfig()

	// Ensure all required directories exist
	config.EnsurePaths(cfg)

	// Initialize database
	db, err := database.NewSQLiteDB(cfg.DatabasePath)
	if err != nil {
		log.Fatal("Failed to initialize SQLite database:", err)
	}
	defer db.Close()

	// Initialize R2 storage
	r2Storage, err := storage.NewR2Storage(storage.R2Config{
		AccessKey: cfg.R2AccessKey,
		SecretKey: cfg.R2SecretKey,
		AccountID: cfg.R2AccountID,
		Bucket:    cfg.R2Bucket,
		Endpoint:  cfg.R2Endpoint,
		Region:    cfg.R2Region,
	})
	if err != nil {
		log.Fatal("Failed to initialize R2 storage:", err)
	}

	// Initialize upload service
	uploadService := service.NewUploadService(db, r2Storage, cfg)

	// Create and start upload queue
	uploadService.StartUploadWorker()

	// Start RTSP capture in background
	go func() {
		if err := CaptureRTSPSegments(cfg, uploadService); err != nil {
			log.Fatal(err)
		}
	}()

	// Start web server
	setupWebServer(cfg, db, r2Storage, uploadService)
}
