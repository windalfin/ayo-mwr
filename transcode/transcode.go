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

// TranscodeVideo generates multi-quality HLS format from the MP4 file
func TranscodeVideo(inputPath, videoID, cameraName string, cfg *config.Config) (map[string]string, map[string]float64, error) {
	// Set up camera-specific paths
	baseDir := filepath.Join(cfg.StoragePath, "recordings", cameraName)
	hlsPath := filepath.Join(baseDir, "hls", videoID)

	// Create HLS output directory
	if err := os.MkdirAll(hlsPath, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create HLS directory: %v", err)
	}

	timings := make(map[string]float64)

	hlsStart := time.Now()
	if err := GenerateHLS(inputPath, hlsPath, videoID, cfg); err != nil {
		return nil, nil, fmt.Errorf("HLS transcoding error: %v", err)
	}
	timings["hlsTranscode"] = time.Since(hlsStart).Seconds()

	return map[string]string{
		"hls": fmt.Sprintf("%s/recordings/%s/hls/%s/master.m3u8", cfg.BaseURL, cameraName, videoID),
	}, timings, nil
}

// GenerateHLS creates a multi-quality HLS stream
func GenerateHLS(inputPath, outputDir, videoID string, cfg *config.Config) error {
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
