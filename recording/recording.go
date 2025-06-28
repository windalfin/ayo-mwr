package recording

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// per-camera locks to avoid race when writing concat list & mp4
var segmenterLocks sync.Map

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

	// Start the MP4 segmenter in the background
	StartMP4Segmenter(cameraName, cameraHLSDir, cameraMP4Dir)

	log.Printf("Starting capture for camera: %s", cameraName)

	// Start continuous HLS streaming
	hlsPlaylistPath := filepath.Join(cameraHLSDir, "playlist.m3u8")

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

		err = cmd.Run()
		if err != nil {
			log.Printf("[%s] FFmpeg process exited with error: %v", cameraName, err)
			log.Printf("[%s] Restarting FFmpeg in 5 seconds...", cameraName)
			time.Sleep(5 * time.Second)
			continue
		}
		log.Printf("[%s] FFmpeg process exited normally, restarting in 2 seconds...", cameraName)
		time.Sleep(2 * time.Second)
	}
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

// MergeSessionVideos merges MP4 segments in inputPath between startTime and endTime into outputPath.
func MergeSessionVideos(inputPath string, startTime, endTime time.Time, outputPath string, resolution string) error {

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

	// Base ffmpeg command
	ffmpegArgs := []string{
		"-y", "-f", "concat", "-safe", "0", "-i", concatListPath,
	}

	// Add resolution parameters if a valid resolution is provided
	if res, found := resolutions[resolution]; found {
		// Use transcoding with specified resolution
		ffmpegArgs = append(ffmpegArgs,
			"-c:v", "libx264", "-preset", "fast",
			"-vf", fmt.Sprintf("scale=%s:%s", res.width, res.height),
			"-c:a", "aac", outputPath,
		)
	} else {
		// Use copy codec (no transcoding) if no valid resolution specified
		ffmpegArgs = append(ffmpegArgs, "-c", "copy", outputPath)
	}

	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg concat failed: %v\nOutput: %s", err, string(output))
	}
	return nil
}

// MergeAndWatermark combines video segments and adds watermark in a single FFmpeg operation
// This is more efficient than running MergeSessionVideos followed by AddWatermarkWithPosition
func MergeAndWatermark(inputPath string, startTime, endTime time.Time, outputPath, watermarkPath string,
	position WatermarkPosition, margin int, opacity float64, resolution string) error {

	log.Printf("MergeAndWatermark : Merging video segments and adding watermark in one operation")
	// Find segments in range of the startTime and endTime
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

	// Verify watermark file exists and is readable
	if _, err := os.Stat(watermarkPath); err != nil {
		log.Printf("MergeAndWatermark : WARNING - Watermark file not found or not accessible: %s", watermarkPath)
		return fmt.Errorf("watermark file not found or not accessible: %v", err)
	}

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

	// Tambahkan parameter opasitas ke filter watermark
	filter := fmt.Sprintf("%s:enable='between(t,0,999999)':alpha=%.1f", overlayExpr, opacity)

	// Run FFmpeg command from project root
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

	// Base FFmpeg command - match arguments with MergeSessionVideos
	ffmpegArgs := []string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", concatListPath,
		"-i", watermarkPath,
	}

	// Add resolution parameters if a valid resolution is provided - match with MergeSessionVideos
	if res, found := resolutions[resolution]; found {
		// Complete filter with scaling and overlay watermark
		completeFilter := fmt.Sprintf("scale=%s:%s,%s", res.width, res.height, filter)

		// Use transcoding with specified resolution - same as MergeSessionVideos
		ffmpegArgs = append(ffmpegArgs,
			"-c:v", "libx264",
			"-preset", "fast",
			"-filter_complex", completeFilter,
			"-c:a", "aac",
			outputPath,
		)
	} else {
		// Use copy codec (no transcoding) if no valid resolution specified
		// but still apply watermark filter
		ffmpegArgs = append(ffmpegArgs,
			"-filter_complex", filter,
			"-c:a", "copy",
			outputPath)
	}

	log.Printf("MergeAndWatermark : Executing ffmpeg with combined merge and watermark operations")
	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg merge and watermark failed: %v\nOutput: %s", err, string(output))
	}
	log.Printf("MergeAndWatermark : Video segments merged and watermark added successfully")

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
			// log.Printf("[%s] MP4 segmenter: Segments to add: %v", cameraName, segs) // Noise reduced
			hlsDir = filepath.Clean(hlsDir)

			// ---- Concurrency control ----
			l, _ := segmenterLocks.LoadOrStore(cameraName, &sync.Mutex{})
			mutex := l.(*sync.Mutex)
			mutex.Lock()
			// ensure unlock even on early continue
			defer mutex.Unlock()

			// ---- build concat list in a unique tmp file then process ----
			tmpConcat, err := os.CreateTemp(hlsDir, "concat_*.txt")
			if err != nil {
				log.Printf("[%s] MP4 segmenter: failed to create concat list: %v", cameraName, err)
				continue
			}
			hlsDir = filepath.Clean(hlsDir)
			for _, s := range segs {
				absPath := filepath.Join(hlsDir, s)
				tmpConcat.WriteString("file '" + absPath + "'\n")
			}
			tmpConcat.Close()

			mp4Name := fmt.Sprintf("%s_%s.mp4", cameraName, time.Now().Format("20060102_150405"))
			mp4Path := filepath.Join(mp4Dir, mp4Name)
			mp4Tmp := filepath.Join(mp4Dir, "."+mp4Name+".tmp")

			cmd := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", tmpConcat.Name(), "-c", "copy", mp4Tmp)
			out, err := cmd.CombinedOutput()
			if err != nil {
				log.Printf("[%s] MP4 segmenter: ffmpeg concat failed: %v, output: %s", cameraName, err, string(out))
				os.Remove(tmpConcat.Name())
				os.Remove(mp4Tmp)
				continue
			}

			// rename tmp MP4 atomically
			if err := os.Rename(mp4Tmp, mp4Path); err != nil {
				log.Printf("[%s] MP4 segmenter: failed to rename temp MP4: %v", cameraName, err)
			} else {
				log.Printf("[%s] MP4 segmenter: wrote MP4 %s", cameraName, mp4Path)
			}
			os.Remove(tmpConcat.Name())
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

			// FFmpeg arguments for direct MP4 segmented recording
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
			}

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
	segmentStart, err := time.Parse("20060102_150405", timestampStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to parse timestamp %s: %v", timestampStr, err)
	}

	// Assume 1-minute segments
	segmentEnd := segmentStart.Add(1 * time.Minute)

	return segmentStart, segmentEnd, nil
}
