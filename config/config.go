package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
)

// CameraConfig holds configuration for a single RTSP camera
type CameraConfig struct {
	Name      string `json:"name"`       // Unique camera name (used for file naming)
	IP        string `json:"ip"`         // Camera IP address
	Port      string `json:"port"`       // RTSP port (typically 554)
	Path      string `json:"path"`       // RTSP URL path (e.g., "/cam/realmonitor?channel=1&subtype=0")
	Username  string `json:"username"`   // RTSP authentication username
	Password  string `json:"password"`   // RTSP authentication password
	Enabled   bool   `json:"enabled"`    // Whether this camera is enabled for capture
	Width     int    `json:"width"`      // Video width
	Height    int    `json:"height"`     // Video height
	FrameRate int    `json:"frame_rate"` // Video frame rate
}

// Config contains all configuration for the application
type Config struct {
	// RTSP Configuration (Legacy single camera)
	RTSPUsername string
	RTSPPassword string
	RTSPIP       string
	RTSPPort     string
	RTSPPath     string

	// Recording Configuration
	SegmentDuration int
	Width           int
	Height          int
	FrameRate       int

	// Storage Configuration
	StoragePath   string
	HardwareAccel string
	Codec         string

	// Server Configuration
	ServerPort string
	BaseURL    string

	// Database Configuration
	DatabasePath string

	// R2 Configuration
	R2Enabled   bool
	R2AccessKey string
	R2SecretKey string
	R2AccountID string
	R2Bucket    string
	R2Endpoint  string
	R2Region    string

	// Multi-camera configuration
	Cameras []CameraConfig
}

// LoadConfig loads configuration from environment variables
func LoadConfig() Config {
	segmentDuration, _ := strconv.Atoi(getEnv("SEGMENT_DURATION", "30"))
	width, _ := strconv.Atoi(getEnv("WIDTH", "800"))
	height, _ := strconv.Atoi(getEnv("HEIGHT", "600"))
	frameRate, _ := strconv.Atoi(getEnv("FRAME_RATE", "30"))
	r2Enabled, _ := strconv.ParseBool(getEnv("R2_ENABLED", "false"))

	config := Config{
		// RTSP Configuration
		RTSPUsername: getEnv("RTSP_USERNAME", "admin"),
		RTSPPassword: getEnv("RTSP_PASSWORD", "admin"),
		RTSPIP:       getEnv("RTSP_IP", "192.168.1.100"),
		RTSPPort:     getEnv("RTSP_PORT", "554"),
		RTSPPath:     getEnv("RTSP_PATH", "/streaming/channels/101/"),

		// Recording Configuration
		SegmentDuration: segmentDuration,
		Width:           width,
		Height:          height,
		FrameRate:       frameRate,

		// Storage Configuration
		StoragePath:   getEnv("STORAGE_PATH", "./videos"),
		HardwareAccel: getEnv("HW_ACCEL", ""),
		Codec:         getEnv("CODEC", "avc"),

		// Server Configuration
		ServerPort: getEnv("PORT", "3000"),
		BaseURL:    getEnv("BASE_URL", "http://localhost:3000"),

		// Database Configuration
		DatabasePath: getEnv("DATABASE_PATH", "./data/videos.db"),

		// R2 Configuration
		R2Enabled:   r2Enabled,
		R2AccessKey: getEnv("R2_ACCESS_KEY", ""),
		R2SecretKey: getEnv("R2_SECRET_KEY", ""),
		R2AccountID: getEnv("R2_ACCOUNT_ID", ""),
		R2Bucket:    getEnv("R2_BUCKET", "videos"),
		R2Endpoint:  getEnv("R2_ENDPOINT", ""),
		R2Region:    getEnv("R2_REGION", "auto"),
		
		// Initialize empty cameras array
		Cameras: []CameraConfig{},
	}

	// Try to load cameras from CAMERAS_CONFIG env var (JSON string)
	camerasJSON := getEnv("CAMERAS_CONFIG", "")
	if camerasJSON != "" {
		if err := json.Unmarshal([]byte(camerasJSON), &config.Cameras); err != nil {
			log.Printf("Failed to parse CAMERAS_CONFIG environment variable: %v", err)
		} else {
			log.Printf("Loaded %d cameras from CAMERAS_CONFIG", len(config.Cameras))
		}
	}

	// If no cameras configured yet, create one from legacy settings
	if len(config.Cameras) == 0 {
		log.Println("No cameras configured, using legacy camera settings")
		config.Cameras = append(config.Cameras, CameraConfig{
			Name:      "camera_A",
			IP:        config.RTSPIP,
			Port:      config.RTSPPort,
			Path:      config.RTSPPath,
			Username:  config.RTSPUsername,
			Password:  config.RTSPPassword,
			Enabled:   true,
			Width:     config.Width,
			Height:    config.Height,
			FrameRate: config.FrameRate,
		})
	}

	// Log configuration (without sensitive data)
	log.Printf("Loaded configuration with %d cameras", len(config.Cameras))
	for i, camera := range config.Cameras {
		log.Printf("Camera %d: %s @ %s:%s%s (Enabled: %v)", 
			i+1, camera.Name, camera.IP, camera.Port, camera.Path, camera.Enabled)
	}
	
	log.Printf("Storage Path: %s", config.StoragePath)
	log.Printf("Server running on port %s with base URL %s", config.ServerPort, config.BaseURL)
	log.Printf("R2 Storage Enabled: %v", config.R2Enabled)
	
	return config
}

// LoadConfigFromFile loads configuration from a JSON file
func LoadConfigFromFile(filePath string) (Config, error) {
	var config Config

	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return config, fmt.Errorf("failed to read config file: %w", err)
	}

	// Unmarshal JSON
	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("failed to parse config file: %w", err)
	}

	// If no cameras but legacy settings exist, create a single camera
	if len(config.Cameras) == 0 && config.RTSPIP != "" {
		log.Println("No cameras in config file, using legacy camera settings")
		config.Cameras = append(config.Cameras, CameraConfig{
			Name:      "camera_A",
			IP:        config.RTSPIP,
			Port:      config.RTSPPort,
			Path:      config.RTSPPath,
			Username:  config.RTSPUsername,
			Password:  config.RTSPPassword,
			Enabled:   true,
			Width:     config.Width,
			Height:    config.Height,
			FrameRate: config.FrameRate,
		})
	}

	return config, nil
}

// getEnv returns environment variable or fallback value
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// EnsurePaths creates necessary paths
func EnsurePaths(config Config) {
	// Create storage directories
	for _, dir := range []string{"uploads", "hls", "dash", "temp"} {
		err := os.MkdirAll(filepath.Join(config.StoragePath, dir), 0755)
		if err != nil {
			log.Printf("Failed to create directory %s: %v", filepath.Join(config.StoragePath, dir), err)
		}
	}

	// Create database directory
	dbDir := filepath.Dir(config.DatabasePath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Printf("Failed to create database directory %s: %v", dbDir, err)
	}
}