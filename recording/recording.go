package recording

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// per-camera locks to avoid race when writing concat list & mp4
var segmenterLocks sync.Map

// Initialize random seed for unique ID generation
func init() {
	rand.Seed(time.Now().UnixNano())
}

// getOverlayExpression returns FFmpeg overlay expression based on watermark position
func getOverlayExpression(position WatermarkPosition, margin int) string {
	switch position {
	case TopLeft:
		return fmt.Sprintf("overlay=%d:%d", margin, margin)
	case TopRight:
		return fmt.Sprintf("overlay=main_w-overlay_w-%d:%d", margin, margin)
	case BottomLeft:
		return fmt.Sprintf("overlay=%d:main_h-overlay_h-%d", margin, margin)
	case BottomRight:
		return fmt.Sprintf("overlay=main_w-overlay_w-%d:main_h-overlay_h-%d", margin, margin)
	default:
		return fmt.Sprintf("overlay=%d:%d", margin, margin)
	}
}

// isRealtimeWatermarkEnabled checks if real-time watermarking is enabled in system configuration
func isRealtimeWatermarkEnabled() bool {
	// For now, always return true to enable real-time watermarking
	// This will be updated to check database configuration in a future version
	log.Printf("ℹ️ REALTIME-WATERMARK: Real-time watermarking is enabled (default)")
	return true
}

// Fetch venue code from database first, then environment as fallback
func getVenueCode() string {

	// First try to get from database
	db, err := database.NewSQLiteDB("./data/videos.db")
	if err != nil {
		log.Printf("⚠️ VENUE-CODE: Failed to connect to database: %v", err)
	} else {
		defer db.Close()

		if config, err := db.GetSystemConfig(database.ConfigVenueCode); err == nil && config.Value != "" {
			return config.Value
		}
	}

	// Fallback to environment variable if database value is empty or unavailable
	if venueCode := os.Getenv("VENUE_CODE"); venueCode != "" {
		return venueCode
	}

	return ""
}

// startQualityStream starts and manages a single quality stream
func startQualityStream(ctx context.Context, stream *QualityStream, cameraName, cameraLogsDir string) {
	hlsPlaylistPath := filepath.Join(stream.HLSDir, "playlist.m3u8")

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s-%s] Stream context cancelled", cameraName, stream.Quality)
			return
		default:
			logFile, err := os.Create(filepath.Join(cameraLogsDir, fmt.Sprintf("ffmpeg_%s_%s.log", stream.Quality, time.Now().Format("20060102_150405"))))
			if err != nil {
				log.Printf("[%s-%s] Error creating FFmpeg log file: %v", cameraName, stream.Quality, err)
				time.Sleep(5 * time.Second)
				continue
			}
			defer logFile.Close()

			// Detect stream info first
			streamInfo := detectStreamInfo(stream.RTSPURL, fmt.Sprintf("%s-%s", cameraName, stream.Quality))

			// Check for watermark settings and real-time watermark configuration
			useWatermark := false
			var watermarkPath string

			log.Printf("[%s-%s] 🔍 WATERMARK-CHECK: Starting watermark detection for camera", cameraName, stream.Quality)

			// Only check for real-time watermarking if enabled in config
			if isRealtimeWatermarkEnabled() {
				log.Printf("[%s-%s] ✅ WATERMARK-CHECK: Real-time watermarking is enabled, checking for watermark file", cameraName, stream.Quality)

				// Get venue code from database - this is what GetWatermark expects
				venueCode := getVenueCode()
				log.Printf("[%s-%s] 🏢 VENUE-CODE: Using venue code: %s", cameraName, stream.Quality, venueCode)

				if venueCode == "" {
					log.Printf("[%s-%s] ⚠️ WATERMARK-CHECK: No venue code configured, watermark disabled", cameraName, stream.Quality)
				} else {
					var err error
					watermarkPath, err = GetWatermark(venueCode)
					if err != nil {
						log.Printf("[%s-%s] ⚠️ WATERMARK-CHECK: Failed to get watermark for venue '%s': %v", cameraName, stream.Quality, venueCode, err)
					} else if watermarkPath == "" {
						log.Printf("[%s-%s] ℹ️ WATERMARK-CHECK: No watermark path found for venue '%s'", cameraName, stream.Quality, venueCode)
					} else {
						log.Printf("[%s-%s] ✅ WATERMARK-CHECK: Found watermark file for venue '%s': %s", cameraName, stream.Quality, venueCode, watermarkPath)
					}
					useWatermark = err == nil && watermarkPath != ""
				}
			} else {
				log.Printf("[%s-%s] ❌ WATERMARK-CHECK: Real-time watermarking is disabled", cameraName, stream.Quality)
			}

			log.Printf("[%s-%s] 📋 WATERMARK-DECISION: useWatermark=%v, watermarkPath=%s", cameraName, stream.Quality, useWatermark, watermarkPath)

			ffmpegArgs := []string{
				"-rtsp_transport", "tcp",
				"-timeout", "5000000",
				"-fflags", "nobuffer+discardcorrupt",
				"-analyzeduration", "2000000",
				"-probesize", "1000000",
				"-re",
				"-i", stream.RTSPURL,
			}

			// Add watermark input if available
			if useWatermark {
				ffmpegArgs = append(ffmpegArgs, "-i", watermarkPath)
				log.Printf("[%s-%s] Real-time watermarking enabled with: %s", cameraName, stream.Quality, watermarkPath)
			}

			// Configure video processing based on watermark availability
			if useWatermark {
				// Real-time watermarking with re-encoding
				position, margin, opacity := GetWatermarkSettings()
				log.Printf("[%s-%s] 🎨 WATERMARK-SETTINGS: position=%d, margin=%d, opacity=%.2f", cameraName, stream.Quality, position, margin, opacity)

				overlayExpr := getOverlayExpression(position, margin)
				filter := fmt.Sprintf("[1:v]colorchannelmixer=aa=%.1f[wm];[0:v][wm]%s", opacity, overlayExpr)

				log.Printf("[%s-%s] 🎬 WATERMARK-FILTER: %s", cameraName, stream.Quality, filter)
				log.Printf("[%s-%s] 🎬 WATERMARK-OVERLAY: %s", cameraName, stream.Quality, overlayExpr)

				ffmpegArgs = append(ffmpegArgs,
					"-filter_complex", filter,
					"-c:v", "libx264",
					"-preset", "ultrafast",
					"-tune", "zerolatency",
					"-crf", "30",
					"-profile:v", "baseline",
					"-level", "3.1",
				)
				log.Printf("[%s-%s] ✅ WATERMARK-ENCODING: Using real-time watermark encoding with re-encode", cameraName, stream.Quality)
			} else {
				// Stream copy for zero CPU encoding when no watermark
				ffmpegArgs = append(ffmpegArgs, "-c:v", "copy")

				// Add appropriate bitstream filter based on video codec
				if streamInfo.VideoCodec == "h264" {
					ffmpegArgs = append(ffmpegArgs, "-bsf:v", "h264_mp4toannexb")
					log.Printf("[%s-%s] ⚡ NO-WATERMARK: Using H.264 bitstream filter with stream copy", cameraName, stream.Quality)
				} else if streamInfo.VideoCodec == "hevc" {
					ffmpegArgs = append(ffmpegArgs, "-bsf:v", "hevc_mp4toannexb")
					log.Printf("[%s-%s] ⚡ NO-WATERMARK: Using HEVC bitstream filter with stream copy", cameraName, stream.Quality)
				} else {
					log.Printf("[%s-%s] ⚡ NO-WATERMARK: Using stream copy without bitstream filter", cameraName, stream.Quality)
				}
			}

			ffmpegArgs = append(ffmpegArgs,
				"-flags", "+global_header",
				"-sc_threshold", "0",
				"-force_key_frames", "expr:gte(t,n_forced*4)",
				"-c:a", "aac",
				"-b:a", "128k",
				"-ar", "44100",
				"-max_muxing_queue_size", "1024",
				"-f", "hls",
				"-hls_time", "4",
				"-hls_list_size", "0",
				"-hls_flags", "independent_segments+delete_segments",
				"-hls_segment_type", "mpegts",
				"-reset_timestamps", "1",
				"-strftime", "1",
				"-hls_segment_filename", filepath.Join(stream.HLSDir, "segment_%Y%m%d_%H%M%S.ts"),
				hlsPlaylistPath,
			)

			log.Printf("[%s-%s] 🚀 FFMPEG-COMMAND: Starting FFmpeg with graceful shutdown support", cameraName, stream.Quality)
			log.Printf("[%s-%s] 🔧 FFMPEG-ARGS: %v", cameraName, stream.Quality, ffmpegArgs)

			stream.Cmd = exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
			stream.Cmd.Stdout = logFile
			stream.Cmd.Stderr = logFile

			err = stream.Cmd.Start()
			if err != nil {
				log.Printf("[%s-%s] Failed to start FFmpeg: %v", cameraName, stream.Quality, err)
				stream.Cmd = nil
				time.Sleep(5 * time.Second)
				continue
			}

			// Wait for FFmpeg to complete
			err = stream.Cmd.Wait()
			stream.Cmd = nil

			if ctx.Err() != nil {
				// Context was canceled, this is expected
				log.Printf("[%s-%s] FFmpeg stopped due to context cancellation", cameraName, stream.Quality)
				return
			}

			if err != nil {
				log.Printf("[%s-%s] FFmpeg process exited with error: %v", cameraName, stream.Quality, err)
			} else {
				log.Printf("[%s-%s] FFmpeg process exited normally", cameraName, stream.Quality)
			}

			// Wait before restarting to avoid rapid restarts
			log.Printf("[%s-%s] Restarting FFmpeg in 2 seconds...", cameraName, stream.Quality)
			time.Sleep(2 * time.Second)
		}
	}
}

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
func CaptureMultipleRTSPStreams(cfg *config.Config) error {
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

		wg.Add(1)
		cameraID := i
		go func(camera config.CameraConfig, id int) {
			defer wg.Done()
			captureRTSPStreamForCamera(context.Background(), cfg, camera, id)
		}(camera, cameraID)
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

// captureRTSPStreamForCamera handles a single camera's RTSP stream
// Now only records continuously to HLS. MP4 segmenter runs separately.
func captureRTSPStreamForCamera(ctx context.Context, cfg *config.Config, camera config.CameraConfig, cameraID int) {
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

	// Start the MP4 segmenter in the background (legacy HLS-based segmentation)
	// StartMP4Segmenter(cameraName, cameraHLSDir, cameraMP4Dir)

	log.Printf("[%s] ⚠️ OLD-RECORDING: Starting capture using OLD recording function (no watermark support)", cameraName)

	// Start continuous HLS streaming
	hlsPlaylistPath := filepath.Join(cameraHLSDir, "playlist.m3u8")

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] Context canceled, stopping capture", cameraName)
			return
		default:
			logFile, err := os.Create(filepath.Join(cameraLogsDir, fmt.Sprintf("ffmpeg_%s.log", time.Now().Format("20060102_150405"))))
			if err != nil {
				log.Printf("[%s] Error creating FFmpeg log file: %v", cameraName, err)
				time.Sleep(5 * time.Second)
				continue
			}
			defer logFile.Close() // Ensure log file is closed properly

			// Detect stream info first to choose appropriate filters
			streamInfo := detectStreamInfo(rtspURL, cameraName)

			ffmpegArgs := []string{
				"-rtsp_transport", "tcp",
				"-timeout", "5000000",
				"-fflags", "nobuffer+discardcorrupt",
				"-analyzeduration", "2000000",
				"-probesize", "1000000",
				"-re",
				"-i", rtspURL,
				"-c:v", "copy", // Stream copy for zero CPU encoding
			}

			// Add appropriate bitstream filter based on video codec
			if streamInfo.VideoCodec == "h264" {
				ffmpegArgs = append(ffmpegArgs, "-bsf:v", "h264_mp4toannexb") // Convert H.264 format for HLS
				log.Printf("[%s] 🔧 Using H.264 bitstream filter", cameraName)
			} else if streamInfo.VideoCodec == "hevc" {
				ffmpegArgs = append(ffmpegArgs, "-bsf:v", "hevc_mp4toannexb") // Convert HEVC format for HLS
				log.Printf("[%s] 🔧 Using HEVC bitstream filter", cameraName)
			} else {
				log.Printf("[%s] 🔧 Skipping bitstream filter for codec: %s", cameraName, streamInfo.VideoCodec)
			}

			ffmpegArgs = append(ffmpegArgs,
				"-flags", "+global_header", // Ensure codec parameters in each segment
				"-sc_threshold", "0", // Disable scene change detection to enforce keyframe splits
				"-force_key_frames", "expr:gte(t,n_forced*4)", // Force keyframes every 4 seconds
			)

			// Audio settings: always attempt to include audio
			ffmpegArgs = append(ffmpegArgs,
				"-c:a", "aac",
				"-b:a", "128k",
				"-ar", "44100",
			)
			if streamInfo.HasAudio {
				log.Printf("[%s] 🔊 Including audio stream in HLS", cameraName)
			} else {
				log.Printf("[%s] 🔈 Attempting to include audio (none detected, FFmpeg will continue without it)", cameraName)
			}

			ffmpegArgs = append(ffmpegArgs,
				"-max_muxing_queue_size", "1024",
				"-f", "hls",
				"-hls_time", "4", // 4-second segments
				"-hls_list_size", "0",
				"-hls_flags", "independent_segments+delete_segments", // Ensure independent segments and clean up old ones
				"-hls_segment_type", "mpegts", // Explicitly use MPEG-TS
				"-reset_timestamps", "1", // Reset timestamps for each segment
				"-strftime", "1", // Enable strftime for filename template
				"-hls_segment_filename", filepath.Join(cameraHLSDir, "segment_%Y%m%d_%H%M%S.ts"),
				hlsPlaylistPath,
			)

			log.Printf("[%s] Starting continuous HLS FFmpeg recording with args: %v", cameraName, ffmpegArgs)

			cmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
			cmd.Stdout = logFile
			cmd.Stderr = logFile

			err = cmd.Start()
			if err != nil {
				log.Printf("[%s] Failed to start FFmpeg: %v", cameraName, err)
				time.Sleep(5 * time.Second)
				continue
			}

			// Wait for FFmpeg to complete and handle errors
			err = cmd.Wait()
			if err != nil {
				log.Printf("[%s] FFmpeg process exited with error: %v", cameraName, err)
			} else {
				log.Printf("[%s] FFmpeg process exited normally", cameraName)
			}

			// Wait before restarting to avoid overlap
			log.Printf("[%s] Restarting FFmpeg in 2 seconds...", cameraName)
			time.Sleep(2 * time.Second)
		}
	}
}

// StreamInfo holds information about detected streams
type StreamInfo struct {
	VideoCodec string
	HasAudio   bool
}

// detectStreamInfo detects the video codec and audio presence of an RTSP stream
func detectStreamInfo(rtspURL, cameraName string) StreamInfo {
	log.Printf("[%s] 🔍 Detecting stream info...", cameraName)

	// Use ffprobe to detect stream information
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-show_entries", "stream=codec_type,codec_name",
		"-of", "csv=p=0",
		"-rtsp_transport", "tcp",
		"-timeout", "10000000", // 10 second timeout
		rtspURL,
	)

	output, err := cmd.Output()
	if err != nil {
		log.Printf("[%s] ⚠️ WARNING: Failed to detect stream info: %v", cameraName, err)
		return StreamInfo{VideoCodec: "unknown", HasAudio: false}
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	info := StreamInfo{VideoCodec: "unknown", HasAudio: false}

	for _, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) >= 2 {
			streamType := parts[0]
			codecName := parts[1]

			if streamType == "video" {
				switch codecName {
				case "h264":
					info.VideoCodec = "h264"
				case "hevc", "h265":
					info.VideoCodec = "hevc"
				default:
					log.Printf("[%s] ⚠️ WARNING: Unknown video codec '%s'", cameraName, codecName)
					info.VideoCodec = "unknown"
				}
				log.Printf("[%s] 📹 VIDEO: Detected codec: %s", cameraName, codecName)
			} else if streamType == "audio" {
				info.HasAudio = true
				log.Printf("[%s] 🔊 AUDIO: Detected audio stream: %s", cameraName, codecName)
			}
		}
	}

	if !info.HasAudio {
		log.Printf("[%s] 🔇 AUDIO: No audio stream detected", cameraName)
	}

	return info
}

// CaptureRTSPSegments is the legacy single-camera capture function
// Kept for backward compatibility
func CaptureRTSPSegments(cfg *config.Config) error {
	if len(cfg.Cameras) > 0 {
		camera := cfg.Cameras[0]
		captureRTSPStreamForCamera(context.Background(), cfg, camera, 0)
		return nil
	}
	return fmt.Errorf("no cameras configured")
}

// MergeSessionVideos merges MP4 segments in inputPath between startTime and endTime into outputPath with hardware acceleration.
func MergeSessionVideos(inputPath string, startTime, endTime time.Time, outputPath string, resolution string) error {

	log.Printf("MergeSessionVideos: Merging video segments with hardware acceleration")
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

	// Define supported resolutions
	resolutions := map[string]struct {
		width  string
		height string
	}{
		"360":  {"640", "360"},
		"480":  {"854", "480"},
		"720":  {"1280", "720"},
		"1080": {"1920", "1080"},
	}

	// Base FFmpeg command (software encoding only)
	ffmpegArgs := []string{"-y"}

	// Add input arguments
	ffmpegArgs = append(ffmpegArgs,
		"-f", "concat",
		"-safe", "0",
		"-i", concatListPath,
	)

	// Add resolution parameters with software encoding
	if res, found := resolutions[resolution]; found {
		// Software scaling and encoding
		ffmpegArgs = append(ffmpegArgs,
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-crf", "23",
			"-vf", fmt.Sprintf("scale=%s:%s", res.width, res.height),
			"-c:a", "aac",
			outputPath,
		)
	} else {
		// No resolution specified - use copy codec (no transcoding)
		ffmpegArgs = append(ffmpegArgs, "-c", "copy", outputPath)
	}

	log.Printf("MergeSessionVideos: Executing ffmpeg with software encoding")
	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg concat failed: %v\nOutput: %s", err, string(output))
	}
	log.Printf("MergeSessionVideos: Video segments merged successfully with hardware acceleration")

	return nil
}

// MergeAndWatermark combines video segments and adds watermark in optimized two-step process:
// 1. Fast concatenation using copy codec (no transcoding)
// 2. Apply watermark and encoding to the concatenated result
// This approach is typically 2-3x faster than single-step complex filter operations
func MergeAndWatermark(inputPath string, startTime, endTime time.Time, outputPath, watermarkPath string,
	position WatermarkPosition, margin int, opacity float64, resolution string) error {

	// Generate unique ID to prevent race conditions
	uniqueID := fmt.Sprintf("%d_%d", time.Now().Unix(), rand.Intn(100000))

	log.Printf("MergeAndWatermark: Starting optimized merge and watermark process (ID: %s)", uniqueID)
	log.Printf("MergeAndWatermark: Input: %s, Output: %s, Resolution: %s (ID: %s)", inputPath, outputPath, resolution, uniqueID)

	// Find segments in range of the startTime and endTime
	segments, err := FindSegmentsInRange(inputPath, startTime, endTime)
	if err != nil {
		return fmt.Errorf("failed to find segments: %w", err)
	}
	if len(segments) == 0 {
		return fmt.Errorf("no video segments found in the specified range")
	}

	log.Printf("MergeAndWatermark: Found %d segments to process (ID: %s)", len(segments), uniqueID)

	// Ensure output directory exists
	outDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create temporary concatenated file (before watermarking) with unique ID
	tempConcatPath := filepath.Join(outDir, fmt.Sprintf("temp_concat_%s_%s", uniqueID, filepath.Base(outputPath)))
	defer os.Remove(tempConcatPath) // Clean up temp file

	// Verify watermark file exists and is readable
	if _, err := os.Stat(watermarkPath); err != nil {
		log.Printf("MergeAndWatermark: WARNING - Watermark file not found or not accessible: %s", watermarkPath)
		return fmt.Errorf("watermark file not found or not accessible: %v", err)
	}

	// STEP 1: Fast concatenation with copy codec (no transcoding)
	log.Printf("MergeAndWatermark: Step 1 - Fast concatenation with copy codec (ID: %s)", uniqueID)
	err = fastConcatSegments(segments, tempConcatPath, outDir, uniqueID, startTime, endTime)
	if err != nil {
		return fmt.Errorf("failed to concatenate segments: %w", err)
	}

	// STEP 2: Apply watermark and encoding to the concatenated file
	log.Printf("MergeAndWatermark: Step 2 - Applying watermark and encoding (ID: %s)", uniqueID)
	err = applyWatermarkWithPosition(tempConcatPath, watermarkPath, outputPath, position, margin, opacity, resolution)
	if err != nil {
		return fmt.Errorf("failed to apply watermark: %w", err)
	}

	log.Printf("MergeAndWatermark: Process completed successfully (ID: %s)", uniqueID)
	return nil
}

// fastConcatSegments performs fast concatenation using copy codec (no transcoding)
func fastConcatSegments(segments []string, outputPath, workingDir, uniqueID string, startTime, endTime time.Time) error {
	// Create concat list file with unique ID to prevent race conditions
	concatListPath := filepath.Join(workingDir, fmt.Sprintf("segments_concat_list_%s.txt", uniqueID))
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

	// Run FFmpeg with copy codec for fast concatenation
	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get project root: %w", err)
	}

	// Calculate duration from booking times to ensure exact duration
	// regardless of when recording actually started
	bookingDuration := endTime.Sub(startTime)
	durationStr := fmt.Sprintf("%.3f", bookingDuration.Seconds())

	ffmpegArgs := []string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", concatListPath,
		"-t", durationStr, // Exact booking duration to ensure precise timing
		"-avoid_negative_ts", "make_zero", // Handle timing adjustments
		"-c", "copy", // Copy codec - no transcoding, very fast
		outputPath,
	}

	log.Printf("fastConcatSegments: Executing fast concat with copy codec (ID: %s)", uniqueID)
	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg fast concat failed: %v\nOutput: %s", err, string(output))
	}
	log.Printf("fastConcatSegments: Fast concatenation completed (ID: %s)", uniqueID)

	return nil
}

// applyWatermarkWithPosition applies watermark to a single video file with optional resolution scaling
func applyWatermarkWithPosition(inputVideo, watermarkPath, outputPath string, position WatermarkPosition, margin int, opacity float64, resolution string) error {
	// Validate opacity value
	if opacity < 0.0 {
		opacity = 0.0
	} else if opacity > 1.0 {
		opacity = 1.0
	}

	// Define overlay expression based on position
	var overlayExpr string
	switch position {
	case TopLeft:
		overlayExpr = fmt.Sprintf("overlay=%d:%d", margin, margin)
	case TopRight:
		overlayExpr = fmt.Sprintf("overlay=main_w-overlay_w-%d:%d", margin, margin)
	case BottomLeft:
		overlayExpr = fmt.Sprintf("overlay=%d:main_h-overlay_h-%d", margin, margin)
	case BottomRight:
		overlayExpr = fmt.Sprintf("overlay=main_w-overlay_w-%d:main_h-overlay_h-%d", margin, margin)
	default:
		overlayExpr = fmt.Sprintf("overlay=%d:%d", margin, margin)
	}

	// Define supported resolutions
	resolutions := map[string]struct {
		width  string
		height string
	}{
		"360":  {"640", "360"},
		"480":  {"854", "480"},
		"720":  {"1280", "720"},
		"1080": {"1920", "1080"},
	}

	// Run FFmpeg command from project root
	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get project root: %w", err)
	}

	// Build FFmpeg command
	ffmpegArgs := []string{
		"-y",
		"-i", inputVideo,
		"-i", watermarkPath,
	}

	// Add resolution and watermark filters
	if res, found := resolutions[resolution]; found {
		// Scale video and apply watermark
		filter := fmt.Sprintf("[0:v]scale=%s:%s[scaled];[1:v]colorchannelmixer=aa=%.1f[wm];[scaled][wm]%s",
			res.width, res.height, opacity, overlayExpr)
		ffmpegArgs = append(ffmpegArgs,
			"-filter_complex", filter,
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-crf", "23",
			"-c:a", "aac",
			outputPath,
		)
	} else {
		// No resolution specified - apply watermark only
		filter := fmt.Sprintf("[1:v]colorchannelmixer=aa=%.1f[wm];[0:v][wm]%s", opacity, overlayExpr)
		ffmpegArgs = append(ffmpegArgs,
			"-filter_complex", filter,
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-crf", "23",
			"-c:a", "copy",
			outputPath,
		)
	}

	log.Printf("applyWatermarkWithPosition: Executing watermark operation")
	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg watermark failed: %v\nOutput: %s", err, string(output))
	}
	log.Printf("applyWatermarkWithPosition: Watermark applied successfully")

	return nil
}

// StartMP4Segmenter periodically merges the last 1 minute of HLS .ts segments into a single MP4 file
func StartMP4Segmenter(cameraName, hlsDir, mp4Dir string) {
	// Ensure hlsDir is absolute for robust concat list pathing
	// log.Printf("[%s] MP4 segmenter: starting for hlsDir: %s", cameraName, hlsDir)
	var err error
	hlsDir, err = filepath.Abs(hlsDir)
	if err != nil {
		log.Printf("[%s] MP4 segmenter: failed to get absolute path for hlsDir: %v", cameraName, err)
		return
	}
	go func() {
		for {
			// sleep until the next wall-clock minute boundary plus 2-second buffer
			now := time.Now()
			next := now.Truncate(time.Minute).Add(time.Minute)
			time.Sleep(time.Until(next) + 6*time.Second)
			log.Printf("[%s] MP4 segmenter: sleeping until next minute boundary", cameraName)
			// we build MP4 for the previous minute window [startWindow, endWindow)
			startWindow := next.Add(-1 * time.Minute)
			endWindow := next
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

				// Try to parse segment time from filename first (more accurate)
				segmentTime, err := parseSegmentTimeFromFilename(e.Name())
				if err == nil {
					// Use segment timestamp for precise selection
					if !segmentTime.Before(startWindow) && segmentTime.Before(endWindow) {
						segs = append(segs, filepath.Base(e.Name()))
					}
					continue
				}

				// Fallback to file modification time if filename parsing fails
				info, err := e.Info()
				if err != nil {
					continue
				}
				if !info.ModTime().Before(startWindow) && info.ModTime().Before(endWindow) {
					segs = append(segs, filepath.Base(e.Name()))
				}
			}
			if len(segs) == 0 {
				continue
			}
			sort.Strings(segs)
			hlsDir = filepath.Clean(hlsDir)

			// ---- Concurrency control ----
			l, _ := segmenterLocks.LoadOrStore(cameraName, &sync.Mutex{})
			mutex := l.(*sync.Mutex)
			mutex.Lock()

			// ---- build concat list in a unique tmp file then process ----
			tmpConcat, err := os.CreateTemp(hlsDir, "concat_*.txt")
			if err != nil {
				log.Printf("[%s] MP4 segmenter: failed to create concat list: %v", cameraName, err)
				mutex.Unlock()
				continue
			}

			for _, s := range segs {
				absPath := filepath.Join(hlsDir, s)
				tmpConcat.WriteString("file '" + absPath + "'\n")
			}
			tmpConcat.Close()

			mp4Name := fmt.Sprintf("%s_%s.mp4", cameraName, startWindow.Format("20060102_150405"))
			mp4Path := filepath.Join(mp4Dir, mp4Name)
			mp4Tmp := filepath.Join(mp4Dir, "."+mp4Name+".tmp")

			cmd := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", tmpConcat.Name(), "-c", "copy", "-t", "65", "-f", "mp4", mp4Tmp)
			out, err := cmd.CombinedOutput()
			if err != nil {
				log.Printf("[%s] MP4 segmenter: ffmpeg concat failed: %v, output: %s", cameraName, err, string(out))
				os.Remove(tmpConcat.Name())
				os.Remove(mp4Tmp)
				mutex.Unlock()
				continue
			}

			// rename tmp MP4 atomically
			if err := os.Rename(mp4Tmp, mp4Path); err != nil {
				log.Printf("[%s] MP4 segmenter: failed to rename temp MP4: %v", cameraName, err)
			} else {
				log.Printf("[%s] MP4 segmenter: wrote MP4 %s", cameraName, mp4Path)
			}
			os.Remove(tmpConcat.Name())
			mutex.Unlock()
		}
	}()
}

// captureRTSPStreamForCameraEnhanced captures an RTSP stream with multi-disk support and MP4-only recording
func captureRTSPStreamForCameraEnhanced(ctx context.Context, cfg *config.Config, camera config.CameraConfig, cameraID int, db database.Database, diskManager *storage.DiskManager) {
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

	// Get recording path from active disk
	recordingDir, activeDiskID, err := diskManager.GetRecordingPath(cameraName)
	if err != nil {
		log.Printf("[%s] Error getting recording path: %v", cameraName, err)
		return
	}

	// Create camera-specific directories - MP4 and logs only (no HLS)
	cameraLogsDir := filepath.Join(recordingDir, "logs")
	cameraMP4Dir := filepath.Join(recordingDir, "mp4")

	// Create required directories
	for _, dir := range []string{cameraLogsDir, cameraMP4Dir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("[%s] Error creating directory %s: %v", cameraName, dir, err)
		}
	}

	// Start the enhanced MP4 segmenter that records directly to database
	StartEnhancedMP4Segmenter(cameraName, cameraMP4Dir, activeDiskID, db)

	log.Printf("Starting enhanced capture for camera: %s on disk: %s (path: %s)", cameraName, activeDiskID, recordingDir)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] Context cancelled, stopping camera capture", cameraName)
			return
		default:
			// Create log file for this session
			logFile, err := os.Create(filepath.Join(cameraLogsDir, fmt.Sprintf("ffmpeg_%s.log", time.Now().Format("20060102_150405"))))
			if err != nil {
				log.Printf("[%s] Error creating FFmpeg log file: %v", cameraName, err)
				time.Sleep(5 * time.Second)
				continue
			}

			// Detect hardware acceleration for enhanced recording
			hwAccel := DetectHardwareAcceleration()
			log.Printf("[%s] Enhanced recording using hardware acceleration: %s", cameraName, hwAccel.Type)

			// FFmpeg arguments for direct MP4 segmented recording with hardware acceleration
			ffmpegArgs := []string{
				"-rtsp_transport", "tcp",
				"-timeout", "5000000",
				"-fflags", "nobuffer+discardcorrupt",
				"-analyzeduration", "2000000",
				"-probesize", "1000000",
				"-re",
			}

			ffmpegArgs = append(ffmpegArgs, "-i", rtspURL)

			ffmpegArgs = append(ffmpegArgs, "-c:v", "copy")
			ffmpegArgs = append(ffmpegArgs, "-bsf:v", "h264_mp4toannexb") // Convert H.264 format for segmentation

			ffmpegArgs = append(ffmpegArgs,
				"-flags", "+global_header", // Ensure codec parameters in each segment
				"-c:a", "aac",
				"-b:a", "128k",
				"-ar", "44100",
				"-max_muxing_queue_size", "1024",
				"-f", "segment",
				"-segment_time", "60", // 1-minute segments
				"-segment_format", "mp4",
				"-segment_list_flags", "cache",
				"-strftime", "1",
				"-segment_filename", filepath.Join(cameraMP4Dir, fmt.Sprintf("%s_%%Y%%m%%d_%%H%%M%%S.mp4", cameraName)),
				"-reset_timestamps", "1",
				"-avoid_negative_ts", "make_zero",
				"-y", // Overwrite existing files
				filepath.Join(cameraMP4Dir, "output.m3u8"), // Placeholder output (not used but required)
			)

			// Execute FFmpeg command
			cmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
			cmd.Stdout = logFile
			cmd.Stderr = logFile

			log.Printf("[%s] Starting FFmpeg with direct MP4 segmentation", cameraName)
			err = cmd.Run()

			logFile.Close()

			if ctx.Err() != nil {
				log.Printf("[%s] Context cancelled during FFmpeg execution", cameraName)
				return
			}

			if err != nil {
				log.Printf("[%s] FFmpeg error: %v", cameraName, err)
			}

			log.Printf("[%s] FFmpeg stopped, restarting in 5 seconds...", cameraName)
			time.Sleep(5 * time.Second)
		}
	}
}

// StartEnhancedMP4Segmenter monitors MP4 segment creation and records them in the database
func StartEnhancedMP4Segmenter(cameraName, mp4Dir, diskID string, db database.Database) {
	go func() {
		log.Printf("[%s] Enhanced MP4 segmenter started for disk: %s", cameraName, diskID)

		// Keep track of processed files to avoid duplicates
		processedFiles := make(map[string]bool)

		for {
			time.Sleep(30 * time.Second) // Check every 30 seconds for new segments

			entries, err := os.ReadDir(mp4Dir)
			if err != nil {
				log.Printf("[%s] Enhanced MP4 segmenter: failed to read MP4 dir: %v", cameraName, err)
				continue
			}

			for _, entry := range entries {
				if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".mp4" {
					continue
				}

				// Skip if already processed
				if processedFiles[entry.Name()] {
					continue
				}

				// Parse segment information from filename
				segmentStart, segmentEnd, err := parseSegmentTime(entry.Name())
				if err != nil {
					log.Printf("[%s] Enhanced MP4 segmenter: failed to parse segment time from %s: %v", cameraName, entry.Name(), err)
					continue
				}

				// Get file size
				fileInfo, err := entry.Info()
				if err != nil {
					log.Printf("[%s] Enhanced MP4 segmenter: failed to get file info for %s: %v", cameraName, entry.Name(), err)
					continue
				}

				// Create recording segment record
				segment := database.RecordingSegment{
					ID:            fmt.Sprintf("%s_%s_%d", cameraName, segmentStart.Format("20060102_150405"), time.Now().Unix()),
					CameraName:    cameraName,
					StorageDiskID: diskID,
					MP4Path:       filepath.Join("recordings", cameraName, "mp4", entry.Name()), // Relative path
					SegmentStart:  segmentStart,
					SegmentEnd:    segmentEnd,
					FileSizeBytes: fileInfo.Size(),
					CreatedAt:     time.Now(),
				}

				// Save to database
				err = db.CreateRecordingSegment(segment)
				if err != nil {
					log.Printf("[%s] Enhanced MP4 segmenter: failed to save segment to database: %v", cameraName, err)
					continue
				}

				processedFiles[entry.Name()] = true
				log.Printf("[%s] Enhanced MP4 segmenter: recorded segment %s (%d bytes)", cameraName, entry.Name(), fileInfo.Size())
			}
		}
	}()
}

// parseSegmentTimeFromFilename extracts timestamp from HLS segment filename
func parseSegmentTimeFromFilename(filename string) (time.Time, error) {
	// Expected format: segment_YYYYMMDD_HHMMSS.ts
	if !strings.HasPrefix(filename, "segment_") || !strings.HasSuffix(filename, ".ts") {
		return time.Time{}, fmt.Errorf("invalid segment filename format: %s", filename)
	}

	// Remove prefix and suffix
	timeStr := strings.TrimPrefix(filename, "segment_")
	timeStr = strings.TrimSuffix(timeStr, ".ts")

	// Parse timestamp in LOCAL timezone to match startWindow/endWindow
	segmentTime, err := time.ParseInLocation("20060102_150405", timeStr, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse timestamp %s: %v", timeStr, err)
	}

	return segmentTime, nil
}

// QualityStream represents a recording stream for a specific quality
type QualityStream struct {
	RTSPURL string
	HLSDir  string
	Quality string
	Cmd     *exec.Cmd
}

// captureRTSPStreamForCameraWithGracefulShutdown handles graceful shutdown for FFmpeg processes
func captureRTSPStreamForCameraWithGracefulShutdown(ctx context.Context, cfg *config.Config, camera config.CameraConfig, cameraID int) {
	cameraName := camera.Name
	if cameraName == "" {
		cameraName = fmt.Sprintf("camera_%d", cameraID)
	}

	log.Printf("[%s] 🚀 GRACEFUL-RECORDING: Starting graceful shutdown recording with watermark support", cameraName)

	// Create camera-specific directories
	cameraDir := filepath.Join(cfg.StoragePath, "recordings", cameraName)
	cameraLogsDir := filepath.Join(cameraDir, "logs")
	cameraMP4Dir := filepath.Join(cameraDir, "mp4")

	// Prepare quality streams
	var qualityStreams []QualityStream

	// Main quality (original path)
	if camera.Path != "" {
		rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
			camera.Username,
			camera.Password,
			camera.IP,
			camera.Port,
			camera.Path,
		)
		cameraHLSDir := filepath.Join(cameraDir, "hls")
		qualityStreams = append(qualityStreams, QualityStream{
			RTSPURL: rtspURL,
			HLSDir:  cameraHLSDir,
			Quality: "main",
		})
	}

	// 720p quality
	if camera.Path720 != "" && camera.ActivePath720 {
		rtspURL720 := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
			camera.Username,
			camera.Password,
			camera.IP,
			camera.Port,
			camera.Path720,
		)
		cameraHLSDir720 := filepath.Join(cameraDir, "hls", "720")
		qualityStreams = append(qualityStreams, QualityStream{
			RTSPURL: rtspURL720,
			HLSDir:  cameraHLSDir720,
			Quality: "720p",
		})
	}

	// 480p quality
	if camera.Path480 != "" && camera.ActivePath480 {
		rtspURL480 := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
			camera.Username,
			camera.Password,
			camera.IP,
			camera.Port,
			camera.Path480,
		)
		cameraHLSDir480 := filepath.Join(cameraDir, "hls", "480")
		qualityStreams = append(qualityStreams, QualityStream{
			RTSPURL: rtspURL480,
			HLSDir:  cameraHLSDir480,
			Quality: "480p",
		})
	}

	// 360p quality
	if camera.Path360 != "" && camera.ActivePath360 {
		rtspURL360 := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
			camera.Username,
			camera.Password,
			camera.IP,
			camera.Port,
			camera.Path360,
		)
		cameraHLSDir360 := filepath.Join(cameraDir, "hls", "360")
		qualityStreams = append(qualityStreams, QualityStream{
			RTSPURL: rtspURL360,
			HLSDir:  cameraHLSDir360,
			Quality: "360p",
		})
	}

	if len(qualityStreams) == 0 {
		log.Printf("[%s] No active streams configured, skipping recording", cameraName)
		return
	}

	// Create all required directories
	dirs := []string{cameraDir, cameraLogsDir, cameraMP4Dir}
	for _, stream := range qualityStreams {
		dirs = append(dirs, stream.HLSDir)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("[%s] Error creating directory %s: %v", cameraName, dir, err)
		}
	}

	log.Printf("[%s] Starting capture with graceful shutdown support for %d quality streams", cameraName, len(qualityStreams))

	// Start goroutines for each quality stream
	var wg sync.WaitGroup
	for i := range qualityStreams {
		wg.Add(1)
		go func(stream *QualityStream) {
			defer wg.Done()
			startQualityStream(ctx, stream, cameraName, cameraLogsDir)
		}(&qualityStreams[i])
	}

	// Wait for context cancellation
	<-ctx.Done()
	log.Printf("[%s] Graceful shutdown requested for all streams", cameraName)

	// Stop all FFmpeg processes gracefully
	for i := range qualityStreams {
		if qualityStreams[i].Cmd != nil && qualityStreams[i].Cmd.Process != nil {
			log.Printf("[%s-%s] Sending SIGTERM to FFmpeg process", cameraName, qualityStreams[i].Quality)
			qualityStreams[i].Cmd.Process.Signal(syscall.SIGTERM)
		}
	}

	// Wait for all streams to finish gracefully
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Wait up to 15 seconds for graceful termination
	select {
	case <-done:
		log.Printf("[%s] All streams terminated gracefully", cameraName)
	case <-time.After(15 * time.Second):
		log.Printf("[%s] Graceful shutdown timeout, forcing kill all processes", cameraName)
		for i := range qualityStreams {
			if qualityStreams[i].Cmd != nil && qualityStreams[i].Cmd.Process != nil {
				qualityStreams[i].Cmd.Process.Kill()
			}
		}
	}

	log.Printf("[%s] Camera capture stopped gracefully", cameraName)
}

// parseSegmentTime extracts start and end times from segment filename
func parseSegmentTime(filename string) (time.Time, time.Time, error) {
	// Expected format: camera_name_YYYYMMDD_HHMMSS.mp4
	parts := strings.Split(filename, "_")
	if len(parts) < 3 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid filename format: %s", filename)
	}

	// Get the last two parts (date and time)
	dateStr := parts[len(parts)-2]
	timeStr := strings.TrimSuffix(parts[len(parts)-1], ".mp4")

	// Parse the timestamp
	timestampStr := dateStr + "_" + timeStr
	segmentStart, err := time.ParseInLocation("20060102_150405", timestampStr, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to parse timestamp %s: %v", timestampStr, err)
	}

	// Assume 1-minute segments
	segmentEnd := segmentStart.Add(1 * time.Minute)

	return segmentStart, segmentEnd, nil
}
