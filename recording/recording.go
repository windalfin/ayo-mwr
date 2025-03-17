package recording

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/service"
	"ayo-mwr/transcode"
)

// CaptureMultipleRTSPStreams captures video from multiple RTSP streams using FFmpeg and saves them in segments
func CaptureMultipleRTSPStreams(cfg config.Config, uploadService *service.UploadService) error {
	// Create logs directory if it doesn't exist
	logDir := filepath.Join(cfg.StoragePath, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Error creating logs directory: %v", err)
	}

	// Create uploads directory if it doesn't exist
	uploadsDir := filepath.Join(cfg.StoragePath, "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		log.Printf("Error creating uploads directory: %v", err)
		return err
	}

	// Use a wait group to manage goroutines
	var wg sync.WaitGroup

	// Start a goroutine for each camera
	for i, camera := range cfg.Cameras {
		if !camera.Enabled {
			log.Printf("Camera %s is disabled, skipping", camera.Name)
			continue
		}

		wg.Add(1)
		cameraID := i
		go func(camera config.CameraConfig, id int) {
			defer wg.Done()
			captureRTSPStreamForCamera(cfg, camera, id, uploadService)
		}(camera, cameraID)
	}

	// Wait for all camera capture routines to complete (they shouldn't unless there's an error)
	wg.Wait()
	return nil
}

// captureRTSPStreamForCamera handles a single camera's RTSP stream
func captureRTSPStreamForCamera(cfg config.Config, camera config.CameraConfig, cameraID int, uploadService *service.UploadService) {
	// Construct the RTSP URL
	rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
		camera.Username,
		camera.Password,
		camera.IP,
		camera.Port,
		camera.Path,
	)

	cameraName := camera.Name
	if cameraName == "" {
		cameraName = fmt.Sprintf("camera_%d", cameraID)
	}

	// Create camera-specific directories
	cameraDir := filepath.Join(cfg.StoragePath, "recordings", cameraName)
	cameraUploadsDir := filepath.Join(cameraDir, "uploads")
	cameraHLSDir := filepath.Join(cameraDir, "hls")
	cameraDASHDir := filepath.Join(cameraDir, "dash")
	cameraLogsDir := filepath.Join(cameraDir, "logs")

	// Create all required directories
	for _, dir := range []string{cameraDir, cameraUploadsDir, cameraHLSDir, cameraDASHDir, cameraLogsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("[%s] Error creating directory %s: %v", cameraName, dir, err)
		}
	}

	log.Printf("Starting capture for camera: %s", cameraName)

	for {
		// Create a new segment file with timestamp
		timestamp := time.Now().Format("20060102_150405")
		outputFilename := fmt.Sprintf("%s_%s.mp4", cameraName, timestamp)
		outputPath := filepath.Join(cameraUploadsDir, outputFilename)

		log.Printf("[%s] Creating new video segment: %s\n", cameraName, outputFilename)

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

		log.Printf("[%s] Testing RTSP connection: %s", cameraName, rtspURL)

		// Start the command
		if err := testCmd.Start(); err != nil {
			log.Printf("[%s] Error starting RTSP test: %v", cameraName, err)
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
				log.Printf("[%s] Error connecting to RTSP: %v", cameraName, err)
				log.Printf("[%s] FFmpeg output: %s", cameraName, testOutput.String())
				time.Sleep(10 * time.Second)
				continue
			}
			log.Printf("[%s] RTSP connection successful", cameraName)
		case <-time.After(15 * time.Second):
			// Kill the process if it takes too long
			log.Printf("[%s] RTSP connection test timed out after 15 seconds", cameraName)
			testCmd.Process.Kill()
			time.Sleep(10 * time.Second)
			continue
		}

		// Create a log file for FFmpeg error output
		logFile, err := os.Create(filepath.Join(cameraLogsDir, fmt.Sprintf("ffmpeg_%s.log", timestamp)))
		if err != nil {
			log.Printf("[%s] Error creating FFmpeg log file: %v", cameraName, err)
		} else {
			defer logFile.Close()
		}

		// Construct FFmpeg command for capturing a segment with more detailed parameters
		ffmpegArgs := []string{
			"-rtsp_transport", "tcp", // Use TCP (more reliable than UDP)
			"-timeout", "5000000", // General IO timeout in microseconds (5 seconds)
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
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		log.Printf("[%s] Starting FFmpeg capture with command: ffmpeg %v", cameraName, ffmpegArgs)

		// Create a channel for capture command completion
		captureDone := make(chan error, 1)

		if err := cmd.Start(); err != nil {
			log.Printf("[%s] Error starting FFmpeg: %v", cameraName, err)
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
				log.Printf("[%s] FFmpeg process ended with error: %v", cameraName, err)
			} else {
				log.Printf("[%s] FFmpeg process completed successfully", cameraName)

				// Check if the file exists and has content
				if fileInfo, err := os.Stat(outputPath); err == nil && fileInfo.Size() > 0 {
					log.Printf("[%s] Recorded video segment: %s (%.2f MB)", cameraName, outputPath, float64(fileInfo.Size())/(1024*1024))
					if uploadService != nil {
						// Use the new ProcessVideoFile method instead of UploadVideo
						go uploadService.ProcessVideoFile(outputPath)
					}
				} else {
					log.Printf("[%s] Output file is empty or doesn't exist", cameraName)
				}
			}
		case <-time.After(time.Duration(cfg.SegmentDuration+30) * time.Second):
			// Kill the process if it takes too long (segment duration + 30 seconds buffer)
			log.Printf("[%s] FFmpeg capture timed out, killing process", cameraName)
			cmd.Process.Kill()
			time.Sleep(5 * time.Second)
		}
	}
}

// CaptureRTSPSegments is the legacy single-camera capture function
// Kept for backward compatibility
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