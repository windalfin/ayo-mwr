package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	database "ayo-mwr/database"
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
	// New path fields for different resolutions
	Path720 string `json:"path_720"` // RTSP path for 720p
	Path480 string `json:"path_480"` // RTSP path for 480p
	Path360 string `json:"path_360"` // RTSP path for 360p
	// Active path fields for different resolutions
	ActivePath720 bool `json:"active_path_720"` // Whether 720p path is active
	ActivePath480 bool `json:"active_path_480"` // Whether 480p path is active
	ActivePath360 bool `json:"active_path_360"` // Whether 360p path is active
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

	// Worker Concurrency Configuration
	BookingWorkerConcurrency     int // Max concurrent booking process workers
	VideoRequestWorkerConcurrency int // Max concurrent video request workers
	PendingTaskWorkerConcurrency  int // Max concurrent pending task workers
	UploadWorkerConcurrency       int // Max concurrent upload workers

	// Transcoding Quality Configuration
	EnabledQualities []string // Enabled transcoding quality presets
}

// LoadConfig loads configuration from environment variables
func LoadConfig() Config {
	// Initialize config with default values only for essential startup configurations
	cfg := Config{
		// Database Configuration - needed to load other configs from database
		DatabasePath: getEnv("DATABASE_PATH", "./data/videos.db"),
		
		// Default values that will be overridden by database values
		VenueCode:                     "",
		ArduinoCOMPort:                "COM4",
		ArduinoBaudRate:               9600,
		RTSPUsername:                  "winda",
		RTSPPassword:                  "Morgana12",
		RTSPIP:                        "192.168.31.152",
		RTSPPort:                      "554",
		RTSPPath:                      "/streaming/channels/101/",
		SegmentDuration:               30,
		ClipDuration:                  0,
		Width:                         800,
		Height:                        600,
		FrameRate:                     30,
		Resolution:                    "720p",
		AutoDelete:                    30,
		StoragePath:                   "./videos",
		HardwareAccel:                 "",
		Codec:                         "avc",
		ServerPort:                    "3000",
		BaseURL:                       "http://localhost:3000",
		R2Enabled:                     false,
		R2TokenValue:                  "",
		R2AccessKey:                   "",
		R2SecretKey:                   "",
		R2AccountID:                   "",
		R2Bucket:                      "",
		R2Endpoint:                    "",
		R2BaseURL:                     "",
		R2Region:                      "auto",
		BookingWorkerConcurrency:      2,
		VideoRequestWorkerConcurrency: 3,
		PendingTaskWorkerConcurrency:  5,
		UploadWorkerConcurrency:       3,
		EnabledQualities:              []string{"1080p", "720p", "480p", "360p"},
	}



	// --- CAMERA CONFIG LOAD via SQLite ---
	// Open SQLite DB
	dbPath := cfg.DatabasePath
	db, err := database.NewSQLiteDB(dbPath)
	if err != nil {
		log.Printf("ERROR: Failed to open SQLite DB for camera config: %v", err)
	} else {
		cameras, err := db.GetCameras()
		if err != nil {
			log.Printf("ERROR loading cameras from SQLite: %v", err)
		} else if len(cameras) == 0 {
			// First run: load from env, store to DB
			camerasJSON := getEnv("CAMERAS_CONFIG", "")
			log.Printf("First run: loading cameras from CAMERAS_CONFIG env: %s", camerasJSON)
			if camerasJSON != "" {
				var envCams []CameraConfig
				if err := json.Unmarshal([]byte(camerasJSON), &envCams); err != nil {
					log.Printf("Warning: Failed to parse CAMERAS_CONFIG: %v", err)
				} else {
					// Convert []config.CameraConfig -> []database.CameraConfig
					dbCams := make([]database.CameraConfig, len(envCams))
					for i, c := range envCams {
						dbCams[i] = database.CameraConfig{
							ButtonNo:   c.ButtonNo,
							Name:       c.Name,
							IP:         c.IP,
							Port:       c.Port,
							Path:       c.Path,
							Username:   c.Username,
							Password:   c.Password,
							Enabled:    c.Enabled,
							Width:      c.Width,
							Height:     c.Height,
							FrameRate:  c.FrameRate,
							Field:      c.Field,
							Resolution: c.Resolution,
							AutoDelete: c.AutoDelete,
							// New path fields
							Path720: c.Path720, Path480: c.Path480, Path360: c.Path360,
							// Active path fields
							ActivePath720: c.ActivePath720, ActivePath480: c.ActivePath480, ActivePath360: c.ActivePath360,
						}
					}
					if err := db.InsertCameras(dbCams); err != nil {
						log.Printf("ERROR inserting cameras to SQLite: %v", err)
					} else {
						log.Printf("Inserted %d cameras to SQLite from env", len(dbCams))
					}
				}
			}
			// Re-query after insert
			cameras, err = db.GetCameras()
		}
		// Convert []database.CameraConfig -> []config.CameraConfig
		cfg.Cameras = make([]CameraConfig, len(cameras))
		for i, c := range cameras {
			cfg.Cameras[i] = CameraConfig{
				ButtonNo: c.ButtonNo,
				Name:     c.Name, IP: c.IP, Port: c.Port, Path: c.Path, Username: c.Username, Password: c.Password,
				Enabled: c.Enabled, Width: c.Width, Height: c.Height, FrameRate: c.FrameRate, Field: c.Field, Resolution: c.Resolution, AutoDelete: c.AutoDelete,
				// New path fields
				Path720: c.Path720, Path480: c.Path480, Path360: c.Path360,
				// Active path fields
				ActivePath720: c.ActivePath720, ActivePath480: c.ActivePath480, ActivePath360: c.ActivePath360,
			}
		}
		log.Printf("Loaded %d cameras from SQLite", len(cfg.Cameras))

		// Load system configuration from database
		sysConfigService := NewSystemConfigService(db)
		if err := sysConfigService.LoadSystemConfigToConfig(&cfg); err != nil {
			log.Printf("Warning: Failed to load system config from database: %v", err)
		}
	}
	if db != nil {
		db.Close()
	}
	// --- END CAMERA CONFIG LOAD ---

	// Build lookup map now that cameras are loaded
	cfg.BuildCameraLookup()

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

		// Rebuild lookup map to include legacy camera
		cfg.BuildCameraLookup()
	}

	// Log configuration
	log.Printf("Loaded configuration with %d cameras", len(cfg.Cameras))
	for i, camera := range cfg.Cameras {
		log.Printf("Camera %d: %s @ %s:%s%s (Enabled: %v)",
			i+1, camera.Name, camera.IP, camera.Port, camera.Path, camera.Enabled)
	}

	log.Printf("Storage Path: %s (multi-disk managed automatically)", cfg.StoragePath)
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
func LoadConfigFromAPI(cfg Config, client APIClient, db database.Database) (Config, error) {
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
	UpdateConfigFromAPIResponse(&cfg, data, db)

	return cfg, nil
}

// getEnv returns environment variable or fallback value
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// BuildCameraLookup constructs the CameraByButtonNo map for quick lookup.
// Call this whenever cfg.Cameras may have changed.
func (cfg *Config) BuildCameraLookup() {
	if cfg == nil {
		return
	}
	if cfg.CameraByButtonNo == nil {
		cfg.CameraByButtonNo = make(map[string]*CameraConfig)
	}
	// clear existing
	for k := range cfg.CameraByButtonNo {
		delete(cfg.CameraByButtonNo, k)
	}
	for i := range cfg.Cameras {
		cam := &cfg.Cameras[i]
		if cam.ButtonNo != "" {
			cfg.CameraByButtonNo[cam.ButtonNo] = cam
		}
	}
}

// EnsurePaths creates necessary paths
func EnsurePaths(config Config) {

	// Create database directory
	dbDir := filepath.Dir(config.DatabasePath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Printf("Failed to create database directory %s: %v", dbDir, err)
	}
}
