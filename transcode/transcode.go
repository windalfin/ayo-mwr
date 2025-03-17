package transcode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"ayo-mwr/config"
)

// QualityPreset defines parameters for a specific video quality
type QualityPreset struct {
	Name      string
	Width     int
	Height    int
	Bitrate   string
	Bandwidth int // For playlist metadata (bits per second)
}

// GetQualityPresets returns an array of quality presets for transcoding
func GetQualityPresets() []QualityPreset {
	return []QualityPreset{
		{
			Name:      "1080p",
			Width:     1920,
			Height:    1080,
			Bitrate:   "5000k",
			Bandwidth: 5000000,
		},
		{
			Name:      "720p",
			Width:     1280,
			Height:    720,
			Bitrate:   "2800k",
			Bandwidth: 2800000,
		},
		{
			Name:      "480p",
			Width:     854,
			Height:    480,
			Bitrate:   "1400k",
			Bandwidth: 1400000,
		},
		{
			Name:      "360p",
			Width:     640,
			Height:    360,
			Bitrate:   "800k",
			Bandwidth: 800000,
		},
	}
}

// TranscodeVideo generates multi-quality HLS and DASH formats from the MP4 file
func TranscodeVideo(inputPath, videoID string, cfg config.Config) (map[string]string, map[string]float64, error) {
	// Extract camera name from the input path
	cameraName := filepath.Base(filepath.Dir(filepath.Dir(inputPath)))
	
	// Set up camera-specific paths
	cameraDir := filepath.Join(cfg.StoragePath, "recordings", cameraName)
	hlsPath := filepath.Join(cameraDir, "hls", videoID)
	dashPath := filepath.Join(cameraDir, "dash", videoID)

	// Create output directories
	os.MkdirAll(hlsPath, 0755)
	os.MkdirAll(dashPath, 0755)

	timings := make(map[string]float64)
	// inputParams, _ := GetInputParams(cfg.HardwareAccel)

	// Create channels for error handling and synchronization
	errChan := make(chan error, 2)
	timesChan := make(chan struct {
		key   string
		value float64
	}, 2)

	// Start HLS transcoding in a goroutine
	go func() {
		hlsStart := time.Now()
		if err := generateHLS(inputPath, hlsPath, videoID, cfg); err != nil {
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
		if err := generateDASH(inputPath, dashPath, videoID, cfg); err != nil {
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
		"hls":  fmt.Sprintf("%s/recordings/%s/hls/%s/master.m3u8", cfg.BaseURL, cameraName, videoID),
		"dash": fmt.Sprintf("%s/recordings/%s/dash/%s/manifest.mpd", cfg.BaseURL, cameraName, videoID),
	}, timings, nil
}

// generateHLS creates a multi-quality HLS stream
func generateHLS(inputPath, outputDir, videoID string, cfg config.Config) error {
	inputParams, _ := GetInputParams(cfg.HardwareAccel)
	qualityPresets := GetQualityPresets()

	// Create variant streams for each quality
	for _, preset := range qualityPresets {
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

		if err := hlsCmd.Run(); err != nil {
			return fmt.Errorf("error creating HLS variant %s: %v", preset.Name, err)
		}
	}

	// Create master playlist
	return createHLSMasterPlaylist(outputDir, qualityPresets)
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

// generateDASH creates a multi-quality DASH stream
func generateDASH(inputPath, outputDir, videoID string, cfg config.Config) error {
	inputParams, _ := GetInputParams(cfg.HardwareAccel)
	qualityPresets := GetQualityPresets()

	// Prepare filter complex and map options for multiple quality renditions
	var filterComplex string
	var mapOptions []string

	for i, preset := range qualityPresets {
		filterComplex += fmt.Sprintf("[0:v]scale=%d:%d,format=yuv420p[v%d];",
			preset.Width, preset.Height, i)
		mapOptions = append(mapOptions,
			fmt.Sprintf("-map", "[v%d]", i),
			"-c:v", GetVideoCodec(cfg.HardwareAccel, cfg.Codec),
			"-b:v", preset.Bitrate)
	}

	// Add audio mapping
	filterComplex = filterComplex[:len(filterComplex)-1] // Remove trailing semicolon
	mapOptions = append(mapOptions, "-map", "0:a", "-c:a", "aac", "-b:a", "128k")

	// Build the command
	dashCmd := exec.Command("ffmpeg",
		append(append(inputParams,
			"-i", inputPath,
			"-filter_complex", filterComplex),
			append(mapOptions,
				"-f", "dash",
				"-use_timeline", "1",
				"-use_template", "1",
				"-seg_duration", "4",
				"-adaptation_sets", "id=0,streams=v id=1,streams=a",
				"-init_seg_name", filepath.Join(outputDir, "init-stream$RepresentationID$.m4s"),
				"-media_seg_name", filepath.Join(outputDir, "chunk-stream$RepresentationID$-$Number$.m4s"),
				filepath.Join(outputDir, "manifest.mpd"))...)...)

	dashCmd.Stdout = os.Stdout
	dashCmd.Stderr = os.Stderr

	return dashCmd.Run()
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
				"-preset", "p4",
				"-profile:v", "main",
				"-rc", "vbr",
				"-cq", "28",
			}, baseParams...)
		} else {
			outputParams = append([]string{
				"-c:v", "h264_nvenc",
				"-preset", "p4",
				"-profile:v", "high",
				"-rc", "vbr",
				"-cq", "23",
			}, baseParams...)
		}
	case "intel":
		if codec == "hevc" {
			outputParams = append([]string{
				"-c:v", "hevc_qsv",
				"-preset", "medium",
				"-profile:v", "main",
			}, baseParams...)
		} else {
			outputParams = append([]string{
				"-c:v", "h264_qsv",
				"-preset", "medium",
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
				"-preset", "fast",
				"-crf", "28",
			}, baseParams...)
		} else {
			outputParams = append([]string{
				"-c:v", "libx264",
				"-preset", "fast",
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
func SplitFFmpegParams(hwAccel, codec string) ([]string, []string) {
	inputParams, _ := GetInputParams(hwAccel)

	// Use the 720p preset as default for backward compatibility
	qualityPresets := GetQualityPresets()
	var defaultPreset QualityPreset
	for _, preset := range qualityPresets {
		if preset.Name == "720p" {
			defaultPreset = preset
			break
		}
	}

	outputParams := GetOutputParams(hwAccel, codec, defaultPreset)
	return inputParams, outputParams
}
