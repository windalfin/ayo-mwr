package recording

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"ayo-mwr/config"
)

// ExtractThumbnail extracts a frame from the middle of the video (duration/2) and saves it as an image (e.g., PNG).
// Uses ffprobe to get duration, ffmpeg to extract frame. Returns error if any step fails.
func ExtractThumbnail(videoPath, outPath string) error {
	getVideoDuration := func(video string) (float64, error) {
		cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", video)
		output, err := cmd.Output()
		if err != nil {
			return 0, err
		}
		var dur float64
		_, err = fmt.Sscanf(string(output), "%f", &dur)
		return dur, err
	}
	dur, err := getVideoDuration(videoPath)
	if err != nil {
		return err
	}
	middle := fmt.Sprintf("%.2f", dur/2)
	cmd := exec.Command("ffmpeg", "-y", "-ss", middle, "-i", videoPath, "-vframes", "1", outPath)
	return cmd.Run()
}

// CaptureMultipleRTSPStreams captures video from multiple RTSP streams using FFmpeg and saves them in segments
func CaptureMultipleRTSPStreams(configManager *config.ConfigManager) error {
	// Get current config from manager
	cfg := configManager.GetConfig()
  
	// Create logs directory if it doesn't exist
	logDir := filepath.Join(cfg.StoragePath, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Error creating logs directory: %v", err)
	}

	// Create recordings directory if it doesn't exist
	recordingsDir := filepath.Join(cfg.StoragePath, "recordings")
	if err := os.MkdirAll(recordingsDir, 0755); err != nil {
		log.Printf("Error creating recordings directory: %v", err)
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
		
		// Log which camera we're starting to record
		log.Printf("Starting recording goroutine for camera: %s (ID: %d)", camera.Name, i)
		
		// Important: Create local copies of the loop variables to avoid closure issues
		currentCamera := camera
		currentID := i
		
		wg.Add(1)
		go func() {
			defer wg.Done()
			captureRTSPStreamForCamera(configManager, currentCamera.Name, currentID)
		}()
	}

	// Wait for all camera capture routines to complete (they shouldn't unless there's an error)
	wg.Wait()
	return nil
}

// TestRTSPConnection tests the RTSP connection for a given camera and URL. Returns true if successful, false otherwise, and the error (if any).
func TestRTSPConnection(cameraName, rtspURL string) (bool, error) {
	var testOutput bytes.Buffer
	testCmd := exec.Command("ffmpeg",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-t", "1",
		"-f", "null",
		"-")
	testCmd.Stderr = &testOutput

	log.Printf("[%s] Testing RTSP connection: %s", cameraName, rtspURL)

	done := make(chan error, 1)
	if err := testCmd.Start(); err != nil {
		log.Printf("[%s] Error starting RTSP test: %v", cameraName, err)
		return false, err
	}
	go func() {
		done <- testCmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			log.Printf("[%s] Error connecting to RTSP: %v", cameraName, err)
			log.Printf("[%s] FFmpeg output: %s", cameraName, testOutput.String())
			return false, err
		}
		log.Printf("[%s] RTSP connection successful", cameraName)
		return true, nil
	case <-time.After(15 * time.Second):
		log.Printf("[%s] RTSP connection test timed out after 15 seconds", cameraName)
		testCmd.Process.Kill()
		return false, fmt.Errorf("timeout")
	}
}

// captureRTSPStreamForCamera captures RTSP stream for a specific camera
func captureRTSPStreamForCamera(configManager *config.ConfigManager, cameraName string, cameraID int) {
	log.Printf("[%s] Starting continuous RTSP capture (ID: %d)", cameraName, cameraID)
	// Get current config from manager
	cfg := configManager.GetConfig()

	// Function to get the latest camera configuration and build RTSP URL
	getLatestRTSPURL := func() string {
		// Get the latest camera config from the manager
		currentCamera := configManager.GetCameraByName(cameraName)

		if currentCamera == nil {
			log.Printf("[%s] Camera configuration not found, using default", cameraName)
			return ""
		}

		// Construct the RTSP URL with the latest camera config
		rtspURL := ""
		if currentCamera.Username != "" && currentCamera.Password != "" {
			rtspURL = fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
				currentCamera.Username,
				currentCamera.Password,
				currentCamera.IP,
				currentCamera.Port,
				currentCamera.Path,
			)
		} else {
			rtspURL = fmt.Sprintf("rtsp://%s:%s%s",
				currentCamera.IP,
				currentCamera.Port,
				currentCamera.Path,
			)
		}
		return rtspURL
	}

	// Create camera-specific directories and add HLS and MP4 folders
	cameraDir := filepath.Join(cfg.StoragePath, "recordings", cameraName)
	cameraLogsDir := filepath.Join(cameraDir, "logs")
	cameraHLSDir := filepath.Join(cameraDir, "hls")
	cameraMP4Dir := filepath.Join(cameraDir, "mp4")

	// Create all required directories
	for _, dir := range []string{cameraDir, cameraLogsDir, cameraHLSDir, cameraMP4Dir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("[%s] Error creating directory %s: %v", cameraName, dir, err)
		}
	}

	// Start the MP4 segmenter in the background
	StartMP4Segmenter(cameraName, cameraHLSDir, cameraMP4Dir)

	log.Printf("Starting capture for camera: %s", cameraName)

	// Start continuous HLS streaming
	hlsPlaylistPath := filepath.Join(cameraHLSDir, "playlist.m3u8")

		// Get the latest RTSP URL using current camera configuration
		currentRTSPURL := getLatestRTSPURL()

		ok, err := TestRTSPConnection(cameraName, currentRTSPURL)
		if !ok {
			log.Printf("[%s] Skipping recording due to failed RTSP connection", cameraName)
			time.Sleep(10 * time.Second)
			continue
		}
		if err != nil {
			log.Printf("[%s] Error testing RTSP connection: %v", cameraName, err)
			time.Sleep(10 * time.Second)
			continue
		}

	for {
		logFile, err := os.Create(filepath.Join(cameraLogsDir, fmt.Sprintf("ffmpeg_%s.log", time.Now().Format("20060102_150405"))))
		if err != nil {
			log.Printf("[%s] Error creating FFmpeg log file: %v", cameraName, err)
			time.Sleep(5 * time.Second)
			continue
		}

		ffmpegArgs := []string{
			"-rtsp_transport", "tcp",
			"-timeout", "5000000",
			"-fflags", "nobuffer+discardcorrupt",
			"-analyzeduration", "2000000",
			"-probesize", "1000000",
			"-re",
			"-i", rtspURL,
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-tune", "zerolatency",
			"-profile:v", "baseline",
			"-pix_fmt", "yuv420p",
			"-color_range", "tv",
			"-b:v", "2M",
			"-bufsize", "4M",
			"-c:a", "aac",
			"-b:a", "128k",
			"-ar", "44100",
			"-max_muxing_queue_size", "1024",
			"-f", "hls",
			"-hls_time", "2",
			"-hls_list_size", "0",
			"-strftime", "1",
			"-hls_segment_filename", filepath.Join(cameraHLSDir, "segment_%Y%m%d_%H%M%S.ts"),
			hlsPlaylistPath,
		}

		log.Printf("[%s] Starting continuous HLS FFmpeg recording", cameraName)

		cmd := exec.Command("ffmpeg", ffmpegArgs...)
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		log.Printf("[%s] Starting FFmpeg capture with command: ffmpeg %v", cameraName, ffmpegArgs)
		log.Printf("[%s] Recording to file: %s", cameraName, outputPath)

		// Create a channel for capture command completion
		captureDone := make(chan error, 1)

		if err := cmd.Start(); err != nil {
			log.Printf("[%s] Error starting FFmpeg: %v", cameraName, err)
			time.Sleep(5 * time.Second) // Wait before retrying
			continue
		}
		log.Printf("[%s] FFmpeg process exited normally, restarting in 2 seconds...", cameraName)
		time.Sleep(2 * time.Second)
	}
}

// CaptureRTSPSegments is the legacy single-camera capture function
// Kept for backward compatibility
func CaptureRTSPSegments(configManager *config.ConfigManager) error {
	// Get current config from manager
	cfg := configManager.GetConfig()

	if len(cfg.Cameras) > 0 {
		camera := cfg.Cameras[0]
		captureRTSPStreamForCamera(configManager, camera.Name, 0)
		return nil
	}
	return fmt.Errorf("no cameras configured")
}

// MergeSessionVideos merges MP4 segments in inputPath between startTime and endTime into outputPath.
func MergeSessionVideos(inputPath string, startTime, endTime time.Time, outputPath string) error {

	// find segment in range of the startTime and endTime
	segments, err := FindSegmentsInRange(inputPath, startTime, endTime)
	if err != nil {
		return fmt.Errorf("failed to find segments: %w", err)
	}
	if len(segments) == 0 {
		return fmt.Errorf("no video segments found in the specified range")
	}

	// Ensure output directory exists
	outDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create concat list file in project folder (next to output)
	concatListPath := filepath.Join(outDir, "segments_concat_list.txt")
	tmpFile, err := os.Create(concatListPath)
	if err != nil {
		return fmt.Errorf("failed to create concat list file: %w", err)
	}
	defer os.Remove(concatListPath)

	for _, seg := range segments {
		absSeg, err := filepath.Abs(seg)
		if err != nil {
			tmpFile.Close()
			return fmt.Errorf("failed to get absolute path for segment: %w", err)
		}
		line := fmt.Sprintf("file '%s'\n", absSeg)
		if _, err := tmpFile.WriteString(line); err != nil {
			tmpFile.Close()
			return fmt.Errorf("failed to write to concat list: %w", err)
		}
	}
	tmpFile.Close()

	// Run FFmpeg concat command from project root
	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get project root: %w", err)
	}
	cmd := exec.Command(
		"ffmpeg", "-y", "-f", "concat", "-safe", "0",
		"-i", concatListPath, "-c", "copy", outputPath,
	)
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg concat failed: %v\nOutput: %s", err, string(output))
	}
	return nil
}

// StartMP4Segmenter periodically merges the last 1 minute of HLS .ts segments into a single MP4 file
func StartMP4Segmenter(cameraName, hlsDir, mp4Dir string) {
	// Ensure hlsDir is absolute for robust concat list pathing
	var err error
	hlsDir, err = filepath.Abs(hlsDir)
	if err != nil {
		log.Printf("[%s] MP4 segmenter: failed to get absolute path for hlsDir: %v", cameraName, err)
		return
	}
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			cutoff := time.Now().Add(-1 * time.Minute)
			entries, err := os.ReadDir(hlsDir)
			if err != nil {
				log.Printf("[%s] MP4 segmenter: failed to read HLS dir: %v", cameraName, err)
				continue
			}
			var segs []string
			for _, e := range entries {
				if !e.Type().IsRegular() || filepath.Ext(e.Name()) != ".ts" {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				if info.ModTime().After(cutoff) {
					segs = append(segs, filepath.Base(e.Name()))
				}
			}
			if len(segs) == 0 {
				continue
			}
			sort.Strings(segs)
			log.Printf("[%s] MP4 segmenter: Segments to add: %v", cameraName, segs)
			hlsDir = filepath.Clean(hlsDir)
			concatList := filepath.Join(hlsDir, "concat_list.txt")
			f, err := os.Create(concatList)
			if err != nil {
				log.Printf("[%s] MP4 segmenter: failed to create concat list: %v", cameraName, err)
				continue
			}
			hlsDir = filepath.Clean(hlsDir)
			for _, s := range segs {
				// s should already be just the base filename
				absPath := filepath.Join(hlsDir, s)
				log.Printf("[%s] MP4 segmenter: Adding segment to concat list: s=%q, absPath=%q", cameraName, s, absPath)
				f.WriteString("file '" + absPath + "'\n")
			}
			f.Close()
			mp4Name := fmt.Sprintf("%s_%s.mp4", cameraName, time.Now().Format("20060102_150405"))
			mp4Path := filepath.Join(mp4Dir, mp4Name)
			cmd := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", concatList, "-c", "copy", mp4Path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				log.Printf("[%s] MP4 segmenter: ffmpeg concat failed: %v, output: %s", cameraName, err, string(out))
				continue
			}
			log.Printf("[%s] MP4 segmenter: wrote MP4 %s", cameraName, mp4Path)
		}
	}()
}
