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

	// Venue Configuration
	VenueCode string

	// Arduino Configuration
	ArduinoCOMPort  string
	ArduinoBaudRate int

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
	BaseURL    string // Base URL for accessing transcoded videos

	// Database Configuration
	DatabasePath string

	// R2 Storage Configuration
	R2AccessKey  string
	R2SecretKey  string
	R2AccountID  string
	R2Bucket     string
	R2Region     string
	R2Endpoint   string
	R2Enabled    bool
	R2TokenValue string

	// Multi-camera Configuration
	Cameras []CameraConfig
}

// LoadConfig loads configuration from environment variables
func LoadConfig() Config {
	cfg := Config{
		// Arduino Configuration
		ArduinoCOMPort: getEnv("ARDUINO_COM_PORT", "COM4"),
		ArduinoBaudRate: func() int {
			rate, _ := strconv.Atoi(getEnv("ARDUINO_BAUD_RATE", "9600"))
			return rate
		}(),

		// RTSP Configuration
		RTSPUsername: getEnv("RTSP_USERNAME", "winda"),
		RTSPPassword: getEnv("RTSP_PASSWORD", "Morgana12"),
		RTSPIP:       getEnv("RTSP_IP", "192.168.31.152"),
		RTSPPort:     getEnv("RTSP_PORT", "554"),
		RTSPPath:     getEnv("RTSP_PATH", "/streaming/channels/101/"),

		// Recording Configuration
		SegmentDuration: func() int {
			duration, _ := strconv.Atoi(getEnv("SEGMENT_DURATION", "30"))
			return duration
		}(),
		Width: func() int {
			width, _ := strconv.Atoi(getEnv("WIDTH", "800"))
			return width
		}(),
		Height: func() int {
			height, _ := strconv.Atoi(getEnv("HEIGHT", "600"))
			return height
		}(),
		FrameRate: func() int {
			rate, _ := strconv.Atoi(getEnv("FRAME_RATE", "30"))
			return rate
		}(),

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
		R2Enabled: func() bool {
			enabled, _ := strconv.ParseBool(getEnv("R2_ENABLED", "false"))
			return enabled
		}(),
		R2TokenValue: getEnv("R2_TOKEN_VALUE", ""),
		R2AccessKey:  getEnv("R2_ACCESS_KEY", ""),
		R2SecretKey:  getEnv("R2_SECRET_KEY", ""),
		R2AccountID:  getEnv("R2_ACCOUNT_ID", ""),
		R2Bucket:     getEnv("R2_BUCKET", ""),
		R2Endpoint:   getEnv("R2_ENDPOINT", ""),
		R2Region:     getEnv("R2_REGION", "auto"),
	}

	// Load multiple cameras configuration
	camerasJSON := getEnv("CAMERAS_CONFIG", "")
	if camerasJSON != "" {
		var cameras []CameraConfig
		if err := json.Unmarshal([]byte(camerasJSON), &cameras); err != nil {
			log.Printf("Warning: Failed to parse CAMERAS_CONFIG: %v", err)
		} else {
			cfg.Cameras = cameras
			log.Printf("Loaded %d cameras from CAMERAS_CONFIG", len(cameras))
		}
	}

	// If no cameras configured, use legacy camera settings
	if len(cfg.Cameras) == 0 {
		log.Println("No cameras configured, using legacy camera settings")
		cfg.Cameras = append(cfg.Cameras, CameraConfig{
			Name:      "camera_1",
			IP:        cfg.RTSPIP,
			Port:      cfg.RTSPPort,
			Path:      cfg.RTSPPath,
			Username:  cfg.RTSPUsername,
			Password:  cfg.RTSPPassword,
			Enabled:   true,
			Width:     cfg.Width,
			Height:    cfg.Height,
			FrameRate: cfg.FrameRate,
		})
	}

	// Log configuration
	log.Printf("Loaded configuration with %d cameras", len(cfg.Cameras))
	for i, camera := range cfg.Cameras {
		log.Printf("Camera %d: %s @ %s:%s%s (Enabled: %v)",
			i+1, camera.Name, camera.IP, camera.Port, camera.Path, camera.Enabled)
	}

	log.Printf("Storage Path: %s", cfg.StoragePath)
	log.Printf("Server running on port %s with base URL %s", cfg.ServerPort, cfg.BaseURL)
	log.Printf("R2 Storage Enabled: %v", cfg.R2Enabled)
	log.Printf("Arduino COM Port: %s", cfg.ArduinoCOMPort)
	log.Printf("Arduino Baud Rate: %d", cfg.ArduinoBaudRate)

	return cfg
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

	// Create database directory
	dbDir := filepath.Dir(config.DatabasePath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Printf("Failed to create database directory %s: %v", dbDir, err)
	}
}
