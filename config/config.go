package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
)

// Config contains all configuration for the application
type Config struct {
	// RTSP Configuration
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
	}

	// Log configuration (without sensitive data)
	log.Printf("RTSP Configuration: %s@%s:%s%s", "****", config.RTSPIP, config.RTSPPort, config.RTSPPath)
	log.Printf("Storage Path: %s", config.StoragePath)
	log.Printf("Server running on port %s with base URL %s", config.ServerPort, config.BaseURL)
	log.Printf("R2 Storage Enabled: %v", config.R2Enabled)

	return config
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
