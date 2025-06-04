package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"

	"ayo-mwr/config"

	"github.com/gin-gonic/gin"
)

// GetConfig returns the current application configuration
func (s *Server) GetConfig(c *gin.Context) {
	// Get current config from manager
	currentConfig := s.configManager.GetConfig()
	
	// Log the config being returned
	configJSON, _ := json.Marshal(currentConfig)
	log.Printf("Returning config: %s", string(configJSON))
	
	// Return the current config
	c.JSON(http.StatusOK, currentConfig)
}

// UpdateConfig updates the application configuration
func (s *Server) UpdateConfig(c *gin.Context) {
	// Parse the request body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	// Unmarshal into a new config
	var newConfig config.Config
	if err := json.Unmarshal(body, &newConfig); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
		return
	}

	// Validate the config (basic validation)
	if newConfig.ServerPort == "" {
		newConfig.ServerPort = "3000" // Default port
	}

	// Save to database
	if err := saveConfigToDatabase(s, newConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration"})
		return
	}

	// Update the server's config in the manager
	s.configManager.UpdateConfig(newConfig)

	// Return success
	c.JSON(http.StatusOK, gin.H{
		"message": "Configuration updated successfully",
		"config":  newConfig,
	})
}

// saveConfigToDatabase saves the configuration to the database
func saveConfigToDatabase(s *Server, cfg config.Config) error {
	// Convert config struct to map of key-value pairs
	configData, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	// Store the entire config as a JSON string
	if err := s.db.SetConfig("app_config", string(configData)); err != nil {
		return err
	}

	// Also store individual fields for easier querying
	// Venue config
	s.db.SetConfig("venue_code", cfg.VenueCode)

	// Server config
	s.db.SetConfig("server_port", cfg.ServerPort)
	s.db.SetConfig("base_url", cfg.BaseURL)

	// Storage config
	s.db.SetConfig("storage_path", cfg.StoragePath)
	s.db.SetConfig("hardware_accel", cfg.HardwareAccel)
	s.db.SetConfig("codec", cfg.Codec)

	// R2 config
	s.db.SetConfig("r2_enabled", strconv.FormatBool(cfg.R2Enabled))
	s.db.SetConfig("r2_access_key", cfg.R2AccessKey)
	s.db.SetConfig("r2_secret_key", cfg.R2SecretKey)
	s.db.SetConfig("r2_account_id", cfg.R2AccountID)
	s.db.SetConfig("r2_bucket", cfg.R2Bucket)
	s.db.SetConfig("r2_endpoint", cfg.R2Endpoint)
	s.db.SetConfig("r2_region", cfg.R2Region)

	// Recording config
	s.db.SetConfig("segment_duration", strconv.Itoa(cfg.SegmentDuration))
	s.db.SetConfig("width", strconv.Itoa(cfg.Width))
	s.db.SetConfig("height", strconv.Itoa(cfg.Height))
	s.db.SetConfig("frame_rate", strconv.Itoa(cfg.FrameRate))

	// Arduino config
	s.db.SetConfig("arduino_com_port", cfg.ArduinoCOMPort)
	s.db.SetConfig("arduino_baud_rate", strconv.Itoa(cfg.ArduinoBaudRate))

	// Cameras config
	camerasJSON, err := json.Marshal(cfg.Cameras)
	if err != nil {
		return err
	}
	s.db.SetConfig("cameras_config", string(camerasJSON))

	log.Println("Configuration saved to database")
	return nil
}

// InitConfigFromDatabase loads config from database or initializes it if not present
func InitConfigFromDatabase(db interface{}, cfg config.Config) error {
	// Check if we have a database implementation
	dbImpl, ok := db.(interface {
		GetConfig(key string) (string, error)
		SetConfig(key, value string) error
	})

	if !ok {
		log.Println("Database does not implement config methods, skipping config initialization")
		return nil
	}

	// Check if config exists in database
	_, err := dbImpl.GetConfig("app_config")
	if err != nil {
		// Config doesn't exist, initialize it
		configData, err := json.Marshal(cfg)
		if err != nil {
			return err
		}

		// Store the entire config
		if err := dbImpl.SetConfig("app_config", string(configData)); err != nil {
			return err
		}

		log.Println("Initialized configuration in database")
	}

	return nil
}
