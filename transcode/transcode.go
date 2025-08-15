package transcode

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/metrics"
)

// QualityPreset defines parameters for a specific video quality
type QualityPreset struct {
	Name      string
	Width     int
	Height    int
	Bitrate   string
	Bandwidth int // For playlist metadata (bits per second)
}

// GetQualityPresets returns an array of quality presets for transcoding based on configuration
func GetQualityPresets(cfg config.Config) []QualityPreset {
	// Define all available quality presets
	allPresets := map[string]QualityPreset{
		"1080p": {
			Name:      "1080p",
			Width:     1920,
			Height:    1080,
			Bitrate:   "5000k",
			Bandwidth: 5000000,
		},
		"720p": {
			Name:      "720p",
			Width:     1280,
			Height:    720,
			Bitrate:   "2800k",
			Bandwidth: 2800000,
		},
		"480p": {
			Name:      "480p",
			Width:     854,
			Height:    480,
			Bitrate:   "1400k",
			Bandwidth: 1400000,
		},
		"360p": {
			Name:      "360p",
			Width:     640,
			Height:    360,
			Bitrate:   "800k",
			Bandwidth: 800000,
		},
	}

	// Filter presets based on enabled qualities from config
	var enabledPresets []QualityPreset
	for _, qualityName := range cfg.EnabledQualities {
		if preset, exists := allPresets[qualityName]; exists {
			enabledPresets = append(enabledPresets, preset)
		}
	}

	// If no valid presets found, return all presets as fallback
	if len(enabledPresets) == 0 {
		return []QualityPreset{
			allPresets["1080p"],
			allPresets["720p"],
			allPresets["480p"],
			allPresets["360p"],
		}
	}

	return enabledPresets
}

// TranscodeVideo generates multi-quality HLS format from the MP4 file
func TranscodeVideo(inputPath, videoID, cameraName string, cfg *config.Config) (map[string]string, map[string]float64, error) {
	return TranscodeVideoWithMetrics(inputPath, videoID, cameraName, cfg, nil)
}

// TranscodeVideoWithMetrics generates multi-quality HLS format from the MP4 file with metrics tracking
func TranscodeVideoWithMetrics(inputPath, videoID, cameraName string, cfg *config.Config, videoMetrics *metrics.VideoProcessingMetrics) (map[string]string, map[string]float64, error) {
	// Set up camera-specific paths
	baseDir := filepath.Join(cfg.StoragePath, "recordings", cameraName)
	hlsPath := filepath.Join(baseDir, "hls", videoID)

	// Create HLS output directory
	if err := os.MkdirAll(hlsPath, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create HLS directory: %v", err)
	}

	timings := make(map[string]float64)

	// Start HLS metrics if provided
	if videoMetrics != nil {
		videoMetrics.StartHLS()
	}

	hlsStart := time.Now()
	if err := GenerateHLS(inputPath, hlsPath, videoID, cfg); err != nil {
		return nil, nil, fmt.Errorf("HLS transcoding error: %v", err)
	}
	timings["hlsTranscode"] = time.Since(hlsStart).Seconds()

	// End HLS metrics if provided
	if videoMetrics != nil {
		videoMetrics.EndHLS()
	}

	return map[string]string{
		"hls": fmt.Sprintf("%s/recordings/%s/hls/%s/master.m3u8", cfg.BaseURL, cameraName, videoID),
	}, timings, nil
}

// copyFile copies a single file from source to destination
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// getPresetNames returns slice of preset names for logging
func getPresetNames(presets []QualityPreset) []string {
	names := make([]string, len(presets))
	for i, preset := range presets {
		names[i] = preset.Name
	}
	return names
}

// parseSegmentTimestamp extracts timestamp from HLS segment filename
// Supports formats: segment_YYYYMMDD_HHMMSS.ts and HHMMSS.ts
func parseSegmentTimestamp(filename string) (time.Time, error) {
	// Remove extension
	nameWithoutExt := strings.TrimSuffix(filename, filepath.Ext(filename))

	// Check if it starts with segment_
	if strings.HasPrefix(nameWithoutExt, "segment_") {
		// Format: segment_YYYYMMDD_HHMMSS
		// Remove prefix
		timestampStr := strings.TrimPrefix(nameWithoutExt, "segment_")

		// Parse timestamp (format: YYYYMMDD_HHMMSS)
		timestamp, err := time.ParseInLocation("20060102_150405", timestampStr, time.Local)
		if err != nil {
			return time.Time{}, fmt.Errorf("failed to parse timestamp from %s: %v", filename, err)
		}

		return timestamp, nil
	} else if len(nameWithoutExt) == 6 {
		// Try numeric format: HHMMSS (e.g., 112003.ts)
		hour, err1 := strconv.Atoi(nameWithoutExt[:2])
		minute, err2 := strconv.Atoi(nameWithoutExt[2:4])
		second, err3 := strconv.Atoi(nameWithoutExt[4:6])

		if err1 == nil && err2 == nil && err3 == nil && hour < 24 && minute < 60 && second < 60 {
			// For numeric segments without date, use today's date as default
			now := time.Now()
			return time.Date(now.Year(), now.Month(), now.Day(),
				hour, minute, second, 0, time.Local), nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid segment filename format: %s", filename)
}

// findSegmentsInTimeRange finds HLS segments within the specified time range
func findSegmentsInTimeRange(hlsDir string, startTime, endTime time.Time) ([]string, error) {
	// Add 1 minute tolerance to end time
	endTime = endTime.Add(1 * time.Minute)

	entries, err := os.ReadDir(hlsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %v", err)
	}

	var matches []struct {
		path string
		ts   time.Time
	}

	// Find segments that match the time range
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only process .ts files
		if filepath.Ext(entry.Name()) == ".ts" {
			// Parse timestamp from filename (supports both segment_ and numeric formats)
			timestamp, err := parseSegmentTimestamp(entry.Name())
			if err != nil {
				log.Printf("[1080P-OPT] WARNING: Failed to parse timestamp from %s: %v", entry.Name(), err)
				continue
			}

			// Check if segment is within time range
			if !timestamp.Before(startTime) && !timestamp.After(endTime) {
				segmentPath := filepath.Join(hlsDir, entry.Name())
				matches = append(matches, struct {
					path string
					ts   time.Time
				}{segmentPath, timestamp})
			}
		}
	}

	// Sort by timestamp ascending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].ts.Before(matches[j].ts)
	})

	// Extract just the paths
	result := make([]string, len(matches))
	for i, match := range matches {
		result[i] = match.path
	}

	return result, nil
}

// GenerateHLS creates a multi-quality HLS stream
func GenerateHLS(inputPath, outputDir, videoID string, cfg *config.Config) error {
	log.Printf("[TRANSCODE] Starting HLS generation for video ID: %s", videoID)
	log.Printf("[TRANSCODE] Input path: %s", inputPath)
	log.Printf("[TRANSCODE] Output directory: %s", outputDir)

	inputParams, _ := GetInputParams(cfg.HardwareAccel)
	qualityPresets := GetQualityPresets(*cfg)
	log.Printf("[TRANSCODE] Quality presets enabled: %v", getPresetNames(qualityPresets))

	// Get video metadata from database to get start_time and end_time
	log.Printf("[TRANSCODE] Attempting to connect to database for quality optimization")
	db, err := database.NewSQLiteDB(cfg.DatabasePath)
	if err != nil {
		log.Printf("[TRANSCODE] WARNING: Failed to init database for quality optimization: %v", err)
		log.Printf("[TRANSCODE] Proceeding with standard FFmpeg processing for all qualities")
	} else {
		log.Printf("[TRANSCODE] Database connected successfully, looking up video metadata")
		video, err := db.GetVideo(videoID)
		if err == nil && video.StartTime != nil && video.EndTime != nil {
			log.Printf("[TRANSCODE] Video metadata found - StartTime: %v, EndTime: %v",
				video.StartTime.Format("15:04:05"), video.EndTime.Format("15:04:05"))

			// Extract camera name from input path
			cameraName := ""
			pathParts := strings.Split(inputPath, string(os.PathSeparator))
			for i, part := range pathParts {
				if part == "recordings" && i+1 < len(pathParts) {
					cameraName = pathParts[i+1]
					break
				}
			}

			log.Printf("[TRANSCODE] Extracted camera name from path: %s", cameraName)

			if cameraName != "" {
				log.Printf("[TRANSCODE] Attempting quality segment optimization for camera: %s", cameraName)

				// Check all qualities for existing segments and copy if available
				var remainingPresets []QualityPreset
				for _, preset := range qualityPresets {
					// Special handling for 1080p - no subfolder, directly in hls directory
					var qualityDir string
					if preset.Name == "1080p" {
						qualityDir = filepath.Join("videos", "recordings", cameraName, "hls")
					} else {
						// Remove 'p' suffix from quality name for folder path
						qualityFolderName := strings.TrimSuffix(preset.Name, "p")
						qualityDir = filepath.Join("videos", "recordings", cameraName, "hls", qualityFolderName)
					}

					if err := copyQualitySegments(cfg, cameraName, preset.Name, qualityDir, *video.StartTime, *video.EndTime, outputDir); err == nil {
						log.Printf("[TRANSCODE] SUCCESS: %s optimization completed for video %s", preset.Name, videoID)
						log.Printf("[TRANSCODE] Removing %s from FFmpeg processing queue", preset.Name)
					} else {
						log.Printf("[TRANSCODE] WARNING: %s optimization failed: %v", preset.Name, err)
						remainingPresets = append(remainingPresets, preset)
					}
				}

				qualityPresets = remainingPresets
				log.Printf("[TRANSCODE] Remaining qualities for FFmpeg: %v", getPresetNames(qualityPresets))
			} else {
				log.Printf("[TRANSCODE] WARNING: Could not extract camera name from input path")
			}
		} else {
			log.Printf("[TRANSCODE] WARNING: Video metadata not found or incomplete for video %s", videoID)
			if err != nil {
				log.Printf("[TRANSCODE] Database error: %v", err)
			}
			log.Printf("[TRANSCODE] Proceeding with standard FFmpeg processing")
		}
	}

	// Create variant streams for each quality (excluding 1080p if already copied)
	log.Printf("[TRANSCODE] Starting FFmpeg processing for %d qualities", len(qualityPresets))

	for i, preset := range qualityPresets {
		log.Printf("[TRANSCODE] Processing quality %d/%d: %s", i+1, len(qualityPresets), preset.Name)

		qualityDir := filepath.Join(outputDir, preset.Name)
		os.MkdirAll(qualityDir, 0755)

		outputParams := GetOutputParams(cfg.HardwareAccel, cfg.Codec, preset)

		hlsCmd := exec.Command("ffmpeg", append(append(inputParams, "-i", inputPath),
			append(outputParams,
				"-hls_time", "4",
				"-hls_playlist_type", "vod",
				"-hls_segment_filename", filepath.Join(qualityDir, "segment_%03d.ts"),
				filepath.Join(qualityDir, "playlist.m3u8"))...)...)
		hlsCmd.Stdout = os.Stdout
		hlsCmd.Stderr = os.Stderr

		log.Printf("[TRANSCODE] Executing FFmpeg for %s quality", preset.Name)
		start := time.Now()

		if err := hlsCmd.Run(); err != nil {
			log.Printf("[TRANSCODE] ERROR: Failed to create HLS variant %s: %v", preset.Name, err)
			return fmt.Errorf("error creating HLS variant %s: %v", preset.Name, err)
		}

		duration := time.Since(start)
		log.Printf("[TRANSCODE] SUCCESS: %s quality completed in %.2f seconds", preset.Name, duration.Seconds())
	}

	log.Printf("[TRANSCODE] All FFmpeg processing completed")

	// Create master playlist with all original presets (including 1080p)
	log.Printf("[TRANSCODE] Creating master playlist...")
	originalPresets := GetQualityPresets(*cfg)
	if err := createHLSMasterPlaylist(outputDir, originalPresets); err != nil {
		log.Printf("[TRANSCODE] ERROR: Failed to create master playlist: %v", err)
		return err
	}

	log.Printf("[TRANSCODE] SUCCESS: HLS generation completed for video %s", videoID)
	log.Printf("[TRANSCODE] Master playlist created with %d quality variants", len(originalPresets))
	return nil
}

// copyQualitySegments finds and copies existing HLS segments for the given quality and time range
func copyQualitySegments(cfg *config.Config, cameraName, qualityName, qualityDir string, startTime, endTime time.Time, outputDir string) error {
	log.Printf("[%s-OPT] Starting %s segment search for camera: %s", strings.ToUpper(qualityName), qualityName, cameraName)
	log.Printf("[%s-OPT] Time range: %s - %s", strings.ToUpper(qualityName), startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"))
	log.Printf("[%s-OPT] Searching in directory: %s", strings.ToUpper(qualityName), qualityDir)

	// Check if quality HLS directory exists
	if _, err := os.Stat(qualityDir); os.IsNotExist(err) {
		log.Printf("[%s-OPT] ERROR: HLS quality directory does not exist: %s", strings.ToUpper(qualityName), qualityDir)
		return fmt.Errorf("HLS quality directory does not exist: %s", qualityDir)
	}

	// Use the optimized time range filtering function
	log.Printf("[%s-OPT] Searching for segments within time range...", strings.ToUpper(qualityName))
	matchingSegments, err := findSegmentsInTimeRange(qualityDir, startTime, endTime)
	if err != nil {
		log.Printf("[%s-OPT] ERROR: Failed to find segments in time range: %v", strings.ToUpper(qualityName), err)
		return fmt.Errorf("failed to find segments in time range: %v", err)
	}

	if len(matchingSegments) == 0 {
		log.Printf("[%s-OPT] ERROR: No HLS segments found in time range %s - %s",
			strings.ToUpper(qualityName), startTime.Format("15:04:05"), endTime.Format("15:04:05"))
		return fmt.Errorf("no HLS segments found in specified time range")
	}

	log.Printf("[%s-OPT] FOUND: %d segments matching time range", strings.ToUpper(qualityName), len(matchingSegments))

	// Log first and last segment for verification
	if len(matchingSegments) > 0 {
		firstSegment := filepath.Base(matchingSegments[0])
		lastSegment := filepath.Base(matchingSegments[len(matchingSegments)-1])
		log.Printf("[%s-OPT] First segment: %s", strings.ToUpper(qualityName), firstSegment)
		log.Printf("[%s-OPT] Last segment: %s", strings.ToUpper(qualityName), lastSegment)

		// Parse and log timestamps for verification
		if firstTs, err := parseSegmentTimestamp(firstSegment); err == nil {
			log.Printf("[%s-OPT] First segment time: %s", strings.ToUpper(qualityName), firstTs.Format("2006-01-02 15:04:05"))
		}
		if lastTs, err := parseSegmentTimestamp(lastSegment); err == nil {
			log.Printf("[%s-OPT] Last segment time: %s", strings.ToUpper(qualityName), lastTs.Format("2006-01-02 15:04:05"))
		}
	}

	// Create quality directory in output
	outputQualityDir := filepath.Join(outputDir, qualityName)
	if err := os.MkdirAll(outputQualityDir, 0755); err != nil {
		log.Printf("[%s-OPT] ERROR: Failed to create output directory: %v", strings.ToUpper(qualityName), err)
		return fmt.Errorf("failed to create %s output directory: %v", qualityName, err)
	}

	log.Printf("[%s-OPT] Created output directory: %s", strings.ToUpper(qualityName), outputQualityDir)
	log.Printf("[%s-OPT] Starting segment copy process...", strings.ToUpper(qualityName))

	// Copy segments and create playlist
	var copiedSegments []string
	copyStart := time.Now()

	for i, segmentPath := range matchingSegments {
		destFile := filepath.Join(outputQualityDir, fmt.Sprintf("segment_%03d.ts", i))
		if err := copyFile(segmentPath, destFile); err != nil {
			log.Printf("[%s-OPT] ERROR: Failed to copy segment %s: %v", strings.ToUpper(qualityName), filepath.Base(segmentPath), err)
			return fmt.Errorf("failed to copy segment: %v", err)
		}
		copiedSegments = append(copiedSegments, fmt.Sprintf("segment_%03d.ts", i))

		// Log progress every 20 segments (since we expect fewer segments now)
		if (i+1)%20 == 0 || i == len(matchingSegments)-1 {
			log.Printf("[%s-OPT] Copied %d/%d segments", strings.ToUpper(qualityName), i+1, len(matchingSegments))
		}
	}

	copyDuration := time.Since(copyStart)
	log.Printf("[%s-OPT] Segment copying completed in %.2f seconds", strings.ToUpper(qualityName), copyDuration.Seconds())

	// Create playlist.m3u8 file
	log.Printf("[%s-OPT] Creating playlist.m3u8...", strings.ToUpper(qualityName))
	playlistPath := filepath.Join(outputQualityDir, "playlist.m3u8")
	playlistFile, err := os.Create(playlistPath)
	if err != nil {
		log.Printf("[%s-OPT] ERROR: Failed to create playlist file: %v", strings.ToUpper(qualityName), err)
		return fmt.Errorf("failed to create playlist file: %v", err)
	}
	defer playlistFile.Close()

	// Write playlist content
	playlistFile.WriteString("#EXTM3U\n")
	playlistFile.WriteString("#EXT-X-VERSION:3\n")
	playlistFile.WriteString("#EXT-X-TARGETDURATION:4\n")
	playlistFile.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")

	for _, segmentName := range copiedSegments {
		playlistFile.WriteString("#EXTINF:4.0,\n")
		playlistFile.WriteString(segmentName + "\n")
	}

	playlistFile.WriteString("#EXT-X-ENDLIST\n")

	totalDuration := time.Since(copyStart)
	log.Printf("[1080P-OPT] SUCCESS: 1080p optimization completed!")
	log.Printf("[1080P-OPT] - Segments copied: %d", len(copiedSegments))
	log.Printf("[1080P-OPT] - Total processing time: %.2f seconds", totalDuration.Seconds())
	log.Printf("[1080P-OPT] - Average time per segment: %.3f seconds", totalDuration.Seconds()/float64(len(copiedSegments)))
	log.Printf("[1080P-OPT] - Time range efficiency: Only copied segments within specified range")

	return nil
}

// copy1080pSegments finds and copies existing 1080p HLS segments for the given time range (deprecated, use copyQualitySegments)
func copy1080pSegments(cfg *config.Config, cameraName string, startTime, endTime time.Time, outputDir string) error {
	// Use "videos" as the base path instead of cfg.StoragePath
	qualityDir := filepath.Join("videos", "recordings", cameraName, "hls")
	return copyQualitySegments(cfg, cameraName, "1080p", qualityDir, startTime, endTime, outputDir)
}

// createHLSMasterPlaylist generates the HLS master playlist file
func createHLSMasterPlaylist(outputDir string, presets []QualityPreset) error {
	masterFile, err := os.Create(filepath.Join(outputDir, "master.m3u8"))
	if err != nil {
		return err
	}
	defer masterFile.Close()

	// Write master playlist header
	masterFile.WriteString("#EXTM3U\n")
	masterFile.WriteString("#EXT-X-VERSION:3\n")

	// Add each quality variant
	for _, preset := range presets {
		masterFile.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,NAME=\"%s\"\n",
			preset.Bandwidth, preset.Width, preset.Height, preset.Name))
		masterFile.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	return nil
}

// getProfileForQuality returns the H.264 profile based on quality level
func getProfileForQuality(qualityIndex int) string {
	switch qualityIndex {
	case 0:
		return "baseline" // For lowest quality
	case 1:
		return "main" // For medium quality
	default:
		return "high" // For highest qualities
	}
}

// GetInputParams returns appropriate FFmpeg input parameters based on hardware acceleration
func GetInputParams(hwAccel string) ([]string, error) {
	// Default to software encoding if not specified
	if hwAccel == "" {
		hwAccel = "software"
	}

	var inputParams []string

	switch hwAccel {
	case "nvidia":
		inputParams = []string{
			"-hwaccel", "cuda",
			"-hwaccel_output_format", "cuda",
		}
	case "intel":
		inputParams = []string{
			"-hwaccel", "qsv",
			"-hwaccel_output_format", "qsv",
		}
	case "amd":
		inputParams = []string{
			"-hwaccel", "amf",
		}
	default:
		// Software encoding (CPU)
		inputParams = []string{}
	}

	return inputParams, nil
}

// GetVideoCodec returns the appropriate video codec for the hardware acceleration and codec
func GetVideoCodec(hwAccel, codec string) string {
	// Default to h264 if not specified
	if codec == "" {
		codec = "h264"
	}

	// Default to software encoding if not specified
	if hwAccel == "" {
		hwAccel = "software"
	}

	switch hwAccel {
	case "nvidia":
		if codec == "hevc" {
			return "hevc_nvenc"
		}
		return "h264_nvenc"
	case "intel":
		if codec == "hevc" {
			return "hevc_qsv"
		}
		return "h264_qsv"
	case "amd":
		if codec == "hevc" {
			return "hevc_amf"
		}
		return "h264_amf"
	default:
		// Software encoding
		if codec == "hevc" {
			return "libx265"
		}
		return "libx264"
	}
}

// GetOutputParams returns appropriate FFmpeg output parameters for a specific quality preset
func GetOutputParams(hwAccel, codec string, preset QualityPreset) []string {
	var outputParams []string

	// Default to software encoding if not specified
	if hwAccel == "" {
		hwAccel = "software"
	}

	// Default to h264 if not specified
	if codec == "" {
		codec = "h264"
	}

	// Base quality parameters
	baseParams := []string{
		"-vf", fmt.Sprintf("scale=%d:%d", preset.Width, preset.Height),
		"-b:v", preset.Bitrate,
	}

	switch hwAccel {
	case "nvidia":
		if codec == "hevc" {
			outputParams = append([]string{
				"-c:v", "hevc_nvenc",
				"-preset", "p1",
				"-profile:v", "main",
				"-rc", "vbr",
				"-cq", "28",
			}, baseParams...)
		} else {
			outputParams = append([]string{
				"-c:v", "h264_nvenc",
				"-preset", "p1",
				"-profile:v", "high",
				"-rc", "vbr",
				"-cq", "23",
			}, baseParams...)
		}
	case "intel":
		if codec == "hevc" {
			outputParams = append([]string{
				"-c:v", "hevc_qsv",
				"-preset", "veryfast",
				"-profile:v", "main",
			}, baseParams...)
		} else {
			outputParams = append([]string{
				"-c:v", "h264_qsv",
				"-preset", "veryfast",
				"-profile:v", "high",
			}, baseParams...)
		}
	case "amd":
		if codec == "hevc" {
			outputParams = append([]string{
				"-c:v", "hevc_amf",
				"-quality", "speed",
				"-profile:v", "main",
				"-level", "5.2",
			}, baseParams...)
		} else {
			outputParams = append([]string{
				"-c:v", "h264_amf",
				"-quality", "speed",
				"-profile:v", "high",
				"-level", "5.2",
			}, baseParams...)
		}
	default:
		// Software encoding (CPU)
		if codec == "hevc" {
			outputParams = append([]string{
				"-c:v", "libx265",
				"-preset", "ultrafast",
				"-crf", "28",
			}, baseParams...)
		} else {
			outputParams = append([]string{
				"-c:v", "libx264",
				"-preset", "ultrafast",
				"-profile:v", "high",
				"-crf", "23",
			}, baseParams...)
		}
	}

	// Add audio encoding parameters
	outputParams = append(outputParams,
		"-c:a", "aac",
		"-b:a", "128k")

	return outputParams
}

// SplitFFmpegParams remains for backward compatibility
func SplitFFmpegParams(hwAccel, codec string, cfg config.Config) ([]string, []string) {
	inputParams, _ := GetInputParams(hwAccel)

	// Use the 720p preset as default for backward compatibility
	qualityPresets := GetQualityPresets(cfg)
	var defaultPreset QualityPreset
	for _, preset := range qualityPresets {
		if preset.Name == "720p" {
			defaultPreset = preset
			break
		}
	}

	// If 720p is not available, use the first available preset
	if defaultPreset.Name == "" && len(qualityPresets) > 0 {
		defaultPreset = qualityPresets[0]
	}

	outputParams := GetOutputParams(hwAccel, codec, defaultPreset)
	return inputParams, outputParams
}

// ConvertTSToMP4 converts a TS file to MP4 format without changing quality
// This is essentially a remux operation that preserves the original quality
func ConvertTSToMP4(inputPath, outputPath string) error {
	return ConvertTSToMP4WithMetrics(inputPath, outputPath, nil)
}

// ConvertTSToMP4WithMetrics converts a TS file to MP4 format with metrics tracking
func ConvertTSToMP4WithMetrics(inputPath, outputPath string, videoMetrics *metrics.VideoProcessingMetrics) error {
	// Create output directory if it doesn't exist (do this first)
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Check if input file exists
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("input TS file does not exist: %s", inputPath)
	}

	// Start transcode metrics if provided
	if videoMetrics != nil {
		videoMetrics.StartTranscode()
	}

	// FFmpeg command to convert TS to MP4 without re-encoding
	// -c copy means copy streams without re-encoding (preserves quality)
	cmd := exec.Command("ffmpeg",
		"-i", inputPath,
		"-c", "copy", // Copy streams without re-encoding
		"-avoid_negative_ts", "make_zero", // Handle negative timestamps
		"-fflags", "+genpts", // Generate presentation timestamps
		"-movflags", "+faststart", // Optimize for streaming/playback
		"-f", "mp4", // Explicitly specify MP4 format
		"-y", // Overwrite output file if exists
		outputPath)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()

	// End transcode metrics if provided
	if videoMetrics != nil {
		videoMetrics.EndTranscode()
	}

	if err != nil {
		return fmt.Errorf("failed to convert TS to MP4: %v", err)
	}

	return nil
}

// ConvertTSToMP4WithWatermark converts a TS file to MP4 with watermark in a single step
// This is more efficient than remuxing then watermarking separately
func ConvertTSToMP4WithWatermark(inputPath, outputPath, watermarkPath string, overlayPosition string, margin int) error {
	// Create output directory if it doesn't exist
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Check if input file exists
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("input TS file does not exist: %s", inputPath)
	}

	// Check if watermark file exists
	if _, err := os.Stat(watermarkPath); os.IsNotExist(err) {
		return fmt.Errorf("watermark file does not exist: %s", watermarkPath)
	}

	log.Printf("[ConvertTSToMP4WithWatermark] Converting %s to MP4 with watermark in single step", filepath.Base(inputPath))

	// Build overlay expression based on position
	var overlayExpr string
	switch overlayPosition {
	case "top_left":
		overlayExpr = fmt.Sprintf("%d:%d", margin, margin)
	case "top_right":
		overlayExpr = fmt.Sprintf("main_w-overlay_w-%d:%d", margin, margin)
	case "bottom_left":
		overlayExpr = fmt.Sprintf("%d:main_h-overlay_h-%d", margin, margin)
	case "bottom_right":
		overlayExpr = fmt.Sprintf("main_w-overlay_w-%d:main_h-overlay_h-%d", margin, margin)
	default:
		overlayExpr = fmt.Sprintf("main_w-overlay_w-%d:%d", margin, margin) // Default to top_right
	}

	// Single-step TS to MP4 conversion with watermark overlay
	cmd := exec.Command("ffmpeg",
		"-i", inputPath,
		"-i", watermarkPath,
		"-filter_complex", fmt.Sprintf("overlay=%s", overlayExpr),
		"-c:v", "libx264", // H.264 video codec
		"-crf", "28", // Higher CRF for faster encoding
		"-preset", "fast", // Fast preset (balance of speed vs quality)
		"-profile:v", "high", // High profile for quality
		"-pix_fmt", "yuv420p", // Standard pixel format
		"-color_range", "tv", // TV color range
		"-colorspace", "bt709", // Standard colorspace
		"-c:a", "copy", // Copy audio without re-encoding
		"-avoid_negative_ts", "make_zero",
		"-fflags", "+genpts",
		"-movflags", "+faststart", // Optimize for streaming
		"-max_muxing_queue_size", "1024",
		"-threads", "4", // Limited threads to prevent resource issues
		"-y", // Overwrite output file if exists
		outputPath)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("[ConvertTSToMP4WithWatermark] Running single-step conversion with watermark...")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to convert TS to MP4 with watermark: %v", err)
	}

	// Verify output file was created
	if info, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("output file was not created: %v", err)
	} else {
		log.Printf("[ConvertTSToMP4WithWatermark] ✅ Single-step conversion complete: %s (%.2f MB)",
			filepath.Base(outputPath), float64(info.Size())/(1024*1024))
	}

	return nil
}

// IsTSFile checks if the given file is a TS file based on extension
func IsTSFile(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".ts"
}

// IsMP4File checks if the given file is an MP4 file based on extension
func IsMP4File(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".mp4"
}

// GetVideoDuration returns the duration of a video file in seconds using ffprobe
func GetVideoDuration(filePath string) (float64, error) {
	// Check if input file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return 0, fmt.Errorf("video file does not exist: %s", filePath)
	}

	// Use ffprobe to get video duration
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		filePath)

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get video duration using ffprobe: %v", err)
	}

	// Parse duration from output
	durationStr := strings.TrimSpace(string(output))
	if durationStr == "" {
		return 0, fmt.Errorf("empty duration output from ffprobe")
	}

	// Convert string to float64
	var duration float64
	if _, err := fmt.Sscanf(durationStr, "%f", &duration); err != nil {
		return 0, fmt.Errorf("failed to parse duration '%s': %v", durationStr, err)
	}

	return duration, nil
}
