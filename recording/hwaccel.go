package recording

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
)

// HWAccelType represents the type of hardware acceleration available
type HWAccelType string

const (
	HWAccelNone   HWAccelType = "none"
	HWAccelIntel  HWAccelType = "qsv"     // Intel Quick Sync Video
	HWAccelNVIDIA HWAccelType = "nvenc"   // NVIDIA NVENC
	HWAccelAMD    HWAccelType = "amf"     // AMD AMF
	HWAccelVAAPI  HWAccelType = "vaapi"   // Linux VA-API
)

// HWAccelConfig contains hardware acceleration configuration
type HWAccelConfig struct {
	Type        HWAccelType
	Available   bool
	Device      string
	EncoderH264 string
	EncoderHEVC string
	DecoderH264 string
	DecoderHEVC string
}

// DetectHardwareAcceleration detects available hardware acceleration
func DetectHardwareAcceleration() HWAccelConfig {
	log.Println("[hwaccel] 🔍 Detecting hardware acceleration capabilities...")
	
	// On Linux, prioritize VA-API for Intel GPUs (more reliable than QSV)
	if runtime.GOOS == "linux" {
		log.Println("[hwaccel] 🐧 Running on Linux - testing VA-API first")
		if vaapiConfig := detectVAAPI(); vaapiConfig.Available {
			log.Printf("[hwaccel] ✅ VA-API detected and available")
			return vaapiConfig
		}
	}
	
	// Test Intel QSV
	log.Println("[hwaccel] 🔵 Testing Intel QSV...")
	if qsvConfig := detectIntelQSV(); qsvConfig.Available {
		log.Printf("[hwaccel] ✅ Intel Quick Sync Video (QSV) detected and available")
		return qsvConfig
	}
	
	// Test NVIDIA NVENC
	log.Println("[hwaccel] 🟢 Testing NVIDIA NVENC...")
	if nvencConfig := detectNVIDIA(); nvencConfig.Available {
		log.Printf("[hwaccel] ✅ NVIDIA NVENC detected and available")
		return nvencConfig
	}
	
	// Test AMD AMF
	log.Println("[hwaccel] 🔴 Testing AMD AMF...")
	if amfConfig := detectAMD(); amfConfig.Available {
		log.Printf("[hwaccel] ✅ AMD AMF detected and available")
		return amfConfig
	}
	
	// Fallback to VA-API on non-Linux systems
	if runtime.GOOS != "linux" {
		log.Println("[hwaccel] 🎬 Testing VA-API as fallback...")
		if vaapiConfig := detectVAAPI(); vaapiConfig.Available {
			log.Printf("[hwaccel] ✅ VA-API detected and available")
			return vaapiConfig
		}
	}
	
	log.Println("[hwaccel] ❌ No hardware acceleration available, using software encoding")
	return HWAccelConfig{
		Type:        HWAccelNone,
		Available:   false,
		EncoderH264: "libx264",
		EncoderHEVC: "libx265",
	}
}

// detectIntelQSV detects Intel Quick Sync Video capabilities
func detectIntelQSV() HWAccelConfig {
	log.Println("[hwaccel] 🔍 Checking Intel QSV availability...")
	
	config := HWAccelConfig{
		Type:        HWAccelIntel,
		Available:   false,
		Device:      "auto",
		EncoderH264: "h264_qsv",
		EncoderHEVC: "hevc_qsv",
		DecoderH264: "h264_qsv",
		DecoderHEVC: "hevc_qsv",
	}
	
	// Check if FFmpeg has QSV support
	if !checkFFmpegEncoder("h264_qsv") {
		log.Println("[hwaccel] ❌ FFmpeg does not have QSV support compiled")
		return config
	}
	
	// Test QSV encoder with a simple encode
	if testQSVEncoder() {
		config.Available = true
		log.Println("[hwaccel] ✅ Intel QSV hardware acceleration is working")
		
		// Try to detect specific device
		if device := detectQSVDevice(); device != "" {
			config.Device = device
			log.Printf("[hwaccel] 📱 Intel QSV device detected: %s", device)
		}
	} else {
		log.Println("[hwaccel] ❌ Intel QSV test failed")
	}
	
	return config
}

// detectNVIDIA detects NVIDIA NVENC capabilities
func detectNVIDIA() HWAccelConfig {
	config := HWAccelConfig{
		Type:        HWAccelNVIDIA,
		Available:   false,
		EncoderH264: "h264_nvenc",
		EncoderHEVC: "hevc_nvenc",
	}
	
	if checkFFmpegEncoder("h264_nvenc") && testNVIDIAEncoder() {
		config.Available = true
		log.Println("[hwaccel] ✅ NVIDIA NVENC available")
	}
	
	return config
}

// detectAMD detects AMD AMF capabilities
func detectAMD() HWAccelConfig {
	config := HWAccelConfig{
		Type:        HWAccelAMD,
		Available:   false,
		EncoderH264: "h264_amf",
		EncoderHEVC: "hevc_amf",
	}
	
	if checkFFmpegEncoder("h264_amf") && testAMDEncoder() {
		config.Available = true
		log.Println("[hwaccel] ✅ AMD AMF available")
	}
	
	return config
}

// detectVAAPI detects VA-API capabilities (Linux)
func detectVAAPI() HWAccelConfig {
	log.Println("[hwaccel] 🔍 Checking VA-API availability...")
	
	config := HWAccelConfig{
		Type:        HWAccelVAAPI,
		Available:   false,
		Device:      "/dev/dri/renderD128",
		EncoderH264: "h264_vaapi",
		EncoderHEVC: "hevc_vaapi",
		DecoderH264: "h264",
		DecoderHEVC: "hevc",
	}
	
	// Check if FFmpeg has VA-API support
	if !checkFFmpegEncoder("h264_vaapi") {
		log.Println("[hwaccel] ❌ FFmpeg does not have VA-API support compiled")
		return config
	}
	
	// Test VA-API encoder
	if runtime.GOOS == "linux" && testVAAPIEncoder() {
		config.Available = true
		log.Println("[hwaccel] ✅ VA-API available")
	} else {
		log.Println("[hwaccel] ❌ VA-API test failed")
	}
	
	return config
}

// checkFFmpegEncoder checks if FFmpeg has a specific encoder
func checkFFmpegEncoder(encoder string) bool {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-encoders")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[hwaccel] ❌ Failed to check FFmpeg encoders: %v", err)
		return false
	}
	
	return strings.Contains(string(output), encoder)
}

// testQSVEncoder tests Intel QSV encoder
func testQSVEncoder() bool {
	log.Println("[hwaccel] 🧪 Testing Intel QSV encoder...")
	
	// Create a simple test encode
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc2=duration=1:size=320x240:rate=1",
		"-c:v", "h264_qsv",
		"-preset", "fast",
		"-f", "null",
		"-",
	)
	
	err := cmd.Run()
	if err != nil {
		log.Printf("[hwaccel] ❌ QSV test encode failed: %v", err)
		return false
	}
	
	log.Println("[hwaccel] ✅ QSV test encode successful")
	return true
}

// testNVIDIAEncoder tests NVIDIA encoder
func testNVIDIAEncoder() bool {
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc2=duration=1:size=320x240:rate=1",
		"-c:v", "h264_nvenc",
		"-preset", "fast",
		"-f", "null",
		"-",
	)
	
	return cmd.Run() == nil
}

// testAMDEncoder tests AMD encoder
func testAMDEncoder() bool {
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc2=duration=1:size=320x240:rate=1",
		"-c:v", "h264_amf",
		"-f", "null",
		"-",
	)
	
	return cmd.Run() == nil
}

// testVAAPIEncoder tests VA-API encoder
func testVAAPIEncoder() bool {
	log.Println("[hwaccel] 🧪 Testing VA-API encoder...")
	
	// Test with proper format conversion for VA-API
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc2=duration=1:size=320x240:rate=1",
		"-vf", "format=nv12,hwupload",
		"-vaapi_device", "/dev/dri/renderD128",
		"-c:v", "h264_vaapi",
		"-f", "null",
		"-",
	)
	
	err := cmd.Run()
	if err != nil {
		log.Printf("[hwaccel] ❌ VA-API test encode failed: %v", err)
		return false
	}
	
	log.Println("[hwaccel] ✅ VA-API test encode successful")
	return true
}

// detectQSVDevice tries to detect the QSV device path
func detectQSVDevice() string {
	// On Windows, QSV usually works with "auto"
	if runtime.GOOS == "windows" {
		return "auto"
	}
	
	// On Linux, try to detect Intel GPU device
	devices := []string{
		"/dev/dri/renderD128",
		"/dev/dri/renderD129",
		"auto",
	}
	
	for _, device := range devices {
		cmd := exec.Command("ffmpeg",
			"-hide_banner",
			"-loglevel", "error",
			"-f", "lavfi",
			"-i", "testsrc2=duration=1:size=320x240:rate=1",
			"-init_hw_device", fmt.Sprintf("qsv=%s", device),
			"-c:v", "h264_qsv",
			"-f", "null",
			"-",
		)
		
		if cmd.Run() == nil {
			return device
		}
	}
	
	return "auto"
}

// GetOptimalPreset returns the optimal preset for the hardware acceleration type
func (hw HWAccelConfig) GetOptimalPreset() string {
	switch hw.Type {
	case HWAccelIntel:
		return "balanced" // QSV presets: fast, balanced, slow
	case HWAccelNVIDIA:
		return "p4"      // NVENC presets: p1-p7
	case HWAccelAMD:
		return "balanced" // AMF presets: speed, balanced, quality
	case HWAccelVAAPI:
		return "medium"   // VA-API: fast, medium, slow
	default:
		return "fast"     // Software encoding preset
	}
}

// GetQualityLevel returns quality settings for different use cases
func (hw HWAccelConfig) GetQualityLevel(quality string) map[string]string {
	settings := make(map[string]string)
	
	switch hw.Type {
	case HWAccelIntel:
		switch quality {
		case "high":
			settings["preset"] = "slow"
			settings["global_quality"] = "18"
			settings["look_ahead"] = "1"
		case "medium":
			settings["preset"] = "balanced"
			settings["global_quality"] = "23"
		case "fast":
			settings["preset"] = "fast"
			settings["global_quality"] = "28"
		}
	case HWAccelNVIDIA:
		switch quality {
		case "high":
			settings["preset"] = "p6"
			settings["cq"] = "18"
			settings["rc"] = "vbr"
		case "medium":
			settings["preset"] = "p4"
			settings["cq"] = "23"
			settings["rc"] = "vbr"
		case "fast":
			settings["preset"] = "p1"
			settings["cq"] = "28"
			settings["rc"] = "cbr"
		}
	default:
		// Software fallback
		switch quality {
		case "high":
			settings["preset"] = "slow"
			settings["crf"] = "18"
		case "medium":
			settings["preset"] = "medium"
			settings["crf"] = "23"
		case "fast":
			settings["preset"] = "fast"
			settings["crf"] = "28"
		}
	}
	
	return settings
}

// BuildEncoderArgs builds FFmpeg encoder arguments for hardware acceleration
func (hw HWAccelConfig) BuildEncoderArgs(quality string, resolution string) []string {
	var args []string
	
	// Add hardware device initialization if needed
	if hw.Available && hw.Type != HWAccelNone {
		switch hw.Type {
		case HWAccelIntel:
			if hw.Device != "auto" {
				args = append(args, "-init_hw_device", fmt.Sprintf("qsv=%s", hw.Device))
			}
		case HWAccelVAAPI:
			args = append(args, "-vaapi_device", "/dev/dri/renderD128")
		}
	}
	
	// Add encoder
	if hw.Available {
		args = append(args, "-c:v", hw.EncoderH264)
	} else {
		args = append(args, "-c:v", "libx264")
	}
	
	// Add quality settings
	qualitySettings := hw.GetQualityLevel(quality)
	for key, value := range qualitySettings {
		args = append(args, "-"+key, value)
	}
	
	// Add resolution-specific settings
	if resolution != "" && hw.Available && hw.Type == HWAccelIntel {
		// Intel QSV can benefit from resolution-specific optimizations
		switch resolution {
		case "1080", "1080p":
			args = append(args, "-max_frame_size", "4000000") // 4Mbps max
		case "720", "720p":
			args = append(args, "-max_frame_size", "2000000") // 2Mbps max
		case "480", "480p":
			args = append(args, "-max_frame_size", "1000000") // 1Mbps max
		}
	}
	
	log.Printf("[hwaccel] 🔧 Built encoder args for %s (%s quality): %v", hw.Type, quality, args)
	return args
}

// BuildDecoderArgs builds FFmpeg decoder arguments for hardware acceleration
func (hw HWAccelConfig) BuildDecoderArgs() []string {
	var args []string
	
	if hw.Available && hw.DecoderH264 != "" {
		switch hw.Type {
		case HWAccelIntel:
			args = append(args, "-hwaccel", "qsv")
			if hw.Device != "auto" {
				args = append(args, "-hwaccel_device", hw.Device)
			}
		case HWAccelNVIDIA:
			args = append(args, "-hwaccel", "cuda")
		case HWAccelVAAPI:
			args = append(args, "-hwaccel", "vaapi")
			args = append(args, "-hwaccel_device", "/dev/dri/renderD128")
		}
		
		log.Printf("[hwaccel] 🔧 Built decoder args for %s: %v", hw.Type, args)
	}
	
	return args
}

// GetBenchmarkInfo returns performance information about the hardware acceleration
func (hw HWAccelConfig) GetBenchmarkInfo() map[string]interface{} {
	return map[string]interface{}{
		"type":         string(hw.Type),
		"available":    hw.Available,
		"device":       hw.Device,
		"encoder_h264": hw.EncoderH264,
		"encoder_hevc": hw.EncoderHEVC,
		"decoder_h264": hw.DecoderH264,
		"decoder_hevc": hw.DecoderHEVC,
		"optimal_preset": hw.GetOptimalPreset(),
		"description": hw.getDescription(),
	}
}

// getDescription returns a human-readable description of the hardware acceleration
func (hw HWAccelConfig) getDescription() string {
	switch hw.Type {
	case HWAccelIntel:
		return "Intel Quick Sync Video - Hardware-accelerated encoding/decoding using Intel integrated graphics"
	case HWAccelNVIDIA:
		return "NVIDIA NVENC - Hardware-accelerated encoding using NVIDIA GPU"
	case HWAccelAMD:
		return "AMD AMF - Hardware-accelerated encoding using AMD GPU"
	case HWAccelVAAPI:
		return "VA-API - Video Acceleration API for Linux"
	default:
		return "Software encoding - Using CPU-based encoding"
	}
}