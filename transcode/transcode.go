package transcode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"ayo-mwr/config"
)

// TranscodeVideo generates HLS and DASH formats from the MP4 file
func TranscodeVideo(inputPath, videoID string, cfg config.Config) (map[string]string, map[string]float64, error) {
	hlsPath := filepath.Join(cfg.StoragePath, "hls", videoID)
	dashPath := filepath.Join(cfg.StoragePath, "dash", videoID)

	// Create output directories
	os.MkdirAll(hlsPath, 0755)
	os.MkdirAll(dashPath, 0755)

	timings := make(map[string]float64)
	inputParams, outputParams := SplitFFmpegParams(cfg.HardwareAccel, cfg.Codec)

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

// SplitFFmpegParams returns appropriate FFmpeg parameters based on hardware acceleration and codec
func SplitFFmpegParams(hwAccel, codec string) ([]string, []string) {
	var inputParams []string
	var outputParams []string

	// Default to software encoding if not specified
	if hwAccel == "" {
		hwAccel = "software"
	}

	// Default to h264 if not specified
	if codec == "" {
		codec = "h264"
	}

	switch hwAccel {
	case "nvidia":
		// NVIDIA GPU acceleration
		inputParams = []string{
			"-hwaccel", "cuda",
			"-hwaccel_output_format", "cuda",
		}
		switch codec {
		case "h264":
			outputParams = []string{
				"-c:v", "h264_nvenc",
				"-preset", "p4",
				"-profile:v", "high",
				"-rc", "vbr",
				"-cq", "23",
				"-c:a", "aac",
				"-b:a", "128k",
			}
		case "hevc":
			outputParams = []string{
				"-c:v", "hevc_nvenc",
				"-preset", "p4",
				"-profile:v", "main",
				"-rc", "vbr",
				"-cq", "28",
				"-c:a", "aac",
				"-b:a", "128k",
			}
		}
	case "intel":
		// Intel QuickSync acceleration
		inputParams = []string{
			"-hwaccel", "qsv",
			"-hwaccel_output_format", "qsv",
		}
		switch codec {
		case "h264":
			outputParams = []string{
				"-c:v", "h264_qsv",
				"-preset", "medium",
				"-profile:v", "high",
				"-b:v", "4M",
				"-c:a", "aac",
				"-b:a", "128k",
			}
		case "hevc":
			outputParams = []string{
				"-c:v", "hevc_qsv",
				"-preset", "medium",
				"-profile:v", "main",
				"-b:v", "4M",
				"-c:a", "aac",
				"-b:a", "128k",
			}
		}
	case "amd":
		// AMD GPU acceleration
		inputParams = []string{
			"-hwaccel", "amf",
		}
		switch codec {
		case "h264":
			outputParams = []string{
				"-c:v", "h264_amf",
				"-quality", "speed",
				"-profile:v", "high",
				"-level", "5.2",
				"-c:a", "aac",
				"-b:a", "128k",
			}
		case "hevc":
			outputParams = []string{
				"-c:v", "hevc_amf",
				"-quality", "speed",
				"-profile:v", "main",
				"-level", "5.2",
				"-c:a", "aac",
				"-b:a", "128k",
			}
		}
	default:
		// Software encoding (CPU)
		inputParams = []string{}
		switch codec {
		case "h264":
			outputParams = []string{
				"-c:v", "libx264",
				"-preset", "fast",
				"-profile:v", "high",
				"-crf", "23",
				"-c:a", "aac",
				"-b:a", "128k",
			}
		case "hevc":
			outputParams = []string{
				"-c:v", "libx265",
				"-preset", "fast",
				"-crf", "28",
				"-c:a", "aac",
				"-b:a", "128k",
			}
		}
	}

	return inputParams, outputParams
}
