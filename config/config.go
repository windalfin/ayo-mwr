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
	ButtonNo   string `json:"button_no"`   // Button number for hardware mapping
	Name       string `json:"name"`        // Unique camera name (used for file naming)
	IP         string `json:"ip"`          // Camera IP address
	Port       string `json:"port"`        // RTSP port (typically 554)
	Path       string `json:"path"`        // RTSP URL path (e.g., "/cam/realmonitor?channel=1&subtype=0")
	Username   string `json:"username"`    // RTSP authentication username
	Password   string `json:"password"`    // RTSP authentication password
	Enabled    bool   `json:"enabled"`     // Whether this camera is enabled for capture
	Width      int    `json:"width"`       // Video width
	Height     int    `json:"height"`      // Video height
	FrameRate  int    `json:"frame_rate"`  // Video frame rate
	Field      string `json:"field"`       // Camera field ID
	Resolution string `json:"resolution"`  // Camera resolution
	AutoDelete int    `json:"auto_delete"` // Auto delete video after x days
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
	ClipDuration    int // Duration of video clips in seconds
	Width           int
	Height          int
	FrameRate       int
	Resolution      string
	AutoDelete      int

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
	R2BaseURL    string // URL publik untuk akses file (mis. https://media.beligem.com)
	R2Enabled    bool
	R2TokenValue string

	// Multi-camera Configuration
	Cameras          []CameraConfig
	CameraByButtonNo map[string]*CameraConfig // Fast lookup by button_no
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
		AutoDelete: func() int {
			days, _ := strconv.Atoi(getEnv("AUTO_DELETE", "30"))
			return days
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
		R2BaseURL:    getEnv("R2_BASE_URL", ""),
		R2Region:     getEnv("R2_REGION", "auto"),
	}

	// Load ClipDuration from environment variable if available
	if clipDurationStr := getEnv("CLIP_DURATION", ""); clipDurationStr != "" {
		if clipDuration, err := strconv.Atoi(clipDurationStr); err == nil {
			cfg.ClipDuration = clipDuration
			log.Printf("Loaded ClipDuration from environment: %d seconds", cfg.ClipDuration)
		}
	}

	// Load multiple cameras configuration
	camerasJSON := getEnv("CAMERAS_CONFIG", "")
	log.Printf("Raw CAMERAS_CONFIG: %s", camerasJSON) // Debug: Print raw JSON
	log.Printf("Length of cfg.Cameras: %d", len(cfg.Cameras))
	if camerasJSON != "" {
		var cameras []CameraConfig
		if err := json.Unmarshal([]byte(camerasJSON), &cameras); err != nil {
			log.Printf("Warning: Failed to parse CAMERAS_CONFIG: %v", err)
		} else {
			cfg.Cameras = cameras
			// Build CameraByButtonNo map for fast lookup
			cfg.CameraByButtonNo = make(map[string]*CameraConfig)
			for i := range cfg.Cameras {
				cam := &cfg.Cameras[i]
				if cam.ButtonNo != "" {
					cfg.CameraByButtonNo[cam.ButtonNo] = cam
				}
			}
			log.Printf("Loaded %d cameras from CAMERAS_CONFIG", len(cameras))
			for i, cam := range cameras {
				log.Printf("Debug Camera %d: %+v", i, cam) // Debug: Print each camera after unmarshaling
			}
		}
	}

	// If no cameras configured, use legacy camera settings
	if len(cfg.Cameras) == 0 {
		log.Println("No cameras configured, using legacy camera settings")
		cfg.Cameras = append(cfg.Cameras, CameraConfig{
			Name:       "camera_1",
			IP:         cfg.RTSPIP,
			Port:       cfg.RTSPPort,
			Path:       cfg.RTSPPath,
			Username:   cfg.RTSPUsername,
			Password:   cfg.RTSPPassword,
			Enabled:    true,
			Width:      cfg.Width,
			Height:     cfg.Height,
			FrameRate:  cfg.FrameRate,
			Resolution: cfg.Resolution,
			AutoDelete: cfg.AutoDelete,
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

	// Initial configuration loaded from environment - will be updated by config update cron job
	log.Println("Initial configuration loaded from environment and defaults")
	log.Println("Configuration will be updated from AYO API via scheduled cron job")

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
			Name:       "camera_A",
			IP:         config.RTSPIP,
			Port:       config.RTSPPort,
			Path:       config.RTSPPath,
			Username:   config.RTSPUsername,
			Password:   config.RTSPPassword,
			Enabled:    true,
			Width:      config.Width,
			Height:     config.Height,
			FrameRate:  config.FrameRate,
			Resolution: config.Resolution,
			AutoDelete: config.AutoDelete,
		})
	}

	return config, nil
}

// LoadConfigFromAPI loads configuration from AYO API using the provided client
func LoadConfigFromAPI(cfg Config, client APIClient) (Config, error) {
	// Get video configuration from API
	response, err := client.GetVideoConfiguration()
	if err != nil {
		return cfg, fmt.Errorf("failed to get video configuration from API: %w", err)
	}

	// Check if the response has data field
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		return cfg, fmt.Errorf("invalid response format from API: missing data field")
	}

	log.Printf("Video configuration from API: %+v", data)

	// Update config from API response
	UpdateConfigFromAPIResponse(&cfg, data)

	return cfg, nil
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
