package config

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"ayo-mwr/database"
)

// SystemConfigService manages system configuration from database
type SystemConfigService struct {
	db database.Database
}

// NewSystemConfigService creates a new system configuration service
func NewSystemConfigService(db database.Database) *SystemConfigService {
	return &SystemConfigService{
		db: db,
	}
}

// GetWorkerConcurrency retrieves worker concurrency settings from database
func (s *SystemConfigService) GetWorkerConcurrency() (booking, videoRequest, pendingTask int, err error) {
	// Default values
	booking, videoRequest, pendingTask = 2, 3, 5

	// Get booking worker concurrency
	if config, err := s.db.GetSystemConfig(database.ConfigBookingWorkerConcurrency); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			booking = val
		}
	}

	// Get video request worker concurrency
	if config, err := s.db.GetSystemConfig(database.ConfigVideoRequestWorkerConcurrency); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			videoRequest = val
		}
	}

	// Get pending task worker concurrency
	if config, err := s.db.GetSystemConfig(database.ConfigPendingTaskWorkerConcurrency); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			pendingTask = val
		}
	}

	return booking, videoRequest, pendingTask, nil
}

// GetEnabledQualities retrieves enabled transcoding qualities from database
func (s *SystemConfigService) GetEnabledQualities() ([]string, error) {
	// Default qualities
	defaultQualities := []string{"1080p", "720p", "480p", "360p"}

	config, err := s.db.GetSystemConfig(database.ConfigEnabledQualities)
	if err != nil {
		// Return default if not found
		return defaultQualities, nil
	}

	if config.Value == "" {
		return defaultQualities, nil
	}

	// Parse comma-separated values
	qualities := strings.Split(config.Value, ",")
	for i, quality := range qualities {
		qualities[i] = strings.TrimSpace(quality)
	}

	return qualities, nil
}

// SetWorkerConcurrency updates worker concurrency settings in database
func (s *SystemConfigService) SetWorkerConcurrency(booking, videoRequest, pendingTask int, updatedBy string) error {
	configs := []database.SystemConfig{
		{
			Key:       database.ConfigBookingWorkerConcurrency,
			Value:     strconv.Itoa(booking),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
		{
			Key:       database.ConfigVideoRequestWorkerConcurrency,
			Value:     strconv.Itoa(videoRequest),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
		{
			Key:       database.ConfigPendingTaskWorkerConcurrency,
			Value:     strconv.Itoa(pendingTask),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
	}

	for _, config := range configs {
		if err := s.db.SetSystemConfig(config); err != nil {
			return fmt.Errorf("failed to set %s: %v", config.Key, err)
		}
	}

	log.Printf("⚙️ CONFIG: Updated worker concurrency - Booking: %d, VideoRequest: %d, PendingTask: %d by %s",
		booking, videoRequest, pendingTask, updatedBy)
	return nil
}

// SetEnabledQualities updates enabled transcoding qualities in database
func (s *SystemConfigService) SetEnabledQualities(qualities []string, updatedBy string) error {
	value := strings.Join(qualities, ",")
	config := database.SystemConfig{
		Key:       database.ConfigEnabledQualities,
		Value:     value,
		Type:      "string",
		UpdatedBy: updatedBy,
	}

	if err := s.db.SetSystemConfig(config); err != nil {
		return fmt.Errorf("failed to set enabled qualities: %v", err)
	}

	log.Printf("⚙️ CONFIG: Updated enabled qualities to [%s] by %s", value, updatedBy)
	return nil
}

// GetAllConfigs retrieves all system configurations
func (s *SystemConfigService) GetAllConfigs() ([]database.SystemConfig, error) {
	return s.db.GetAllSystemConfigs()
}

// GetConfig retrieves a specific system configuration
func (s *SystemConfigService) GetConfig(key string) (*database.SystemConfig, error) {
	return s.db.GetSystemConfig(key)
}

// SetConfig sets a system configuration
func (s *SystemConfigService) SetConfig(key, value, configType, updatedBy string) error {
	config := database.SystemConfig{
		Key:       key,
		Value:     value,
		Type:      configType,
		UpdatedBy: updatedBy,
	}
	return s.db.SetSystemConfig(config)
}

// DeleteConfig deletes a system configuration
func (s *SystemConfigService) DeleteConfig(key string) error {
	return s.db.DeleteSystemConfig(key)
}

// LoadSystemConfigToConfig updates Config struct with values from database
func (s *SystemConfigService) LoadSystemConfigToConfig(cfg *Config) error {
	// Load worker concurrency settings
	booking, videoRequest, pendingTask, err := s.GetWorkerConcurrency()
	if err != nil {
		log.Printf("Warning: Failed to load worker concurrency from database: %v", err)
	} else {
		cfg.BookingWorkerConcurrency = booking
		cfg.VideoRequestWorkerConcurrency = videoRequest
		cfg.PendingTaskWorkerConcurrency = pendingTask
		log.Printf("⚙️ CONFIG: Loaded worker concurrency from database - Booking: %d, VideoRequest: %d, PendingTask: %d",
			booking, videoRequest, pendingTask)
	}

	// Load enabled qualities
	qualities, err := s.GetEnabledQualities()
	if err != nil {
		log.Printf("Warning: Failed to load enabled qualities from database: %v", err)
	} else {
		cfg.EnabledQualities = qualities
		log.Printf("⚙️ CONFIG: Loaded enabled qualities from database: [%s]", strings.Join(qualities, ", "))
	}

	return nil
}

// ValidateWorkerConcurrency validates worker concurrency values
func ValidateWorkerConcurrency(booking, videoRequest, pendingTask int) error {
	if booking < 1 || booking > 20 {
		return fmt.Errorf("booking worker concurrency must be between 1 and 20")
	}
	if videoRequest < 1 || videoRequest > 20 {
		return fmt.Errorf("video request worker concurrency must be between 1 and 20")
	}
	if pendingTask < 1 || pendingTask > 50 {
		return fmt.Errorf("pending task worker concurrency must be between 1 and 50")
	}
	return nil
}

// ValidateEnabledQualities validates enabled qualities
func ValidateEnabledQualities(qualities []string) error {
	validQualities := map[string]bool{
		"1080p": true,
		"720p":  true,
		"480p":  true,
		"360p":  true,
	}

	if len(qualities) == 0 {
		return fmt.Errorf("at least one quality must be enabled")
	}

	for _, quality := range qualities {
		if !validQualities[quality] {
			return fmt.Errorf("invalid quality '%s', valid options: 1080p, 720p, 480p, 360p", quality)
		}
	}

	return nil
}