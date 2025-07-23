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

// GetDiskManagerConfig retrieves disk manager configuration from database
func (s *SystemConfigService) GetDiskManagerConfig() (minimumFreeSpaceGB int, priorityExternal, priorityMountedStorage, priorityInternalNVMe, priorityInternalSATA, priorityRootFilesystem int, err error) {
	// Default values from storage/disk_manager.go constants
	minimumFreeSpaceGB = 100
	priorityExternal = 1
	priorityMountedStorage = 50
	priorityInternalNVMe = 101
	priorityInternalSATA = 201
	priorityRootFilesystem = 500

	// Get minimum free space GB
	if config, err := s.db.GetSystemConfig(database.ConfigMinimumFreeSpaceGB); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			minimumFreeSpaceGB = val
		}
	}

	// Get priority external
	if config, err := s.db.GetSystemConfig(database.ConfigPriorityExternal); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			priorityExternal = val
		}
	}

	// Get priority mounted storage
	if config, err := s.db.GetSystemConfig(database.ConfigPriorityMountedStorage); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			priorityMountedStorage = val
		}
	}

	// Get priority internal NVMe
	if config, err := s.db.GetSystemConfig(database.ConfigPriorityInternalNVMe); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			priorityInternalNVMe = val
		}
	}

	// Get priority internal SATA
	if config, err := s.db.GetSystemConfig(database.ConfigPriorityInternalSATA); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			priorityInternalSATA = val
		}
	}

	// Get priority root filesystem
	if config, err := s.db.GetSystemConfig(database.ConfigPriorityRootFilesystem); err == nil {
		if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
			priorityRootFilesystem = val
		}
	}

	return minimumFreeSpaceGB, priorityExternal, priorityMountedStorage, priorityInternalNVMe, priorityInternalSATA, priorityRootFilesystem, nil
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

// SetDiskManagerConfig updates disk manager configuration in database
func (s *SystemConfigService) SetDiskManagerConfig(minimumFreeSpaceGB, priorityExternal, priorityMountedStorage, priorityInternalNVMe, priorityInternalSATA, priorityRootFilesystem int, updatedBy string) error {
	configs := []database.SystemConfig{
		{
			Key:       database.ConfigMinimumFreeSpaceGB,
			Value:     strconv.Itoa(minimumFreeSpaceGB),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
		{
			Key:       database.ConfigPriorityExternal,
			Value:     strconv.Itoa(priorityExternal),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
		{
			Key:       database.ConfigPriorityMountedStorage,
			Value:     strconv.Itoa(priorityMountedStorage),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
		{
			Key:       database.ConfigPriorityInternalNVMe,
			Value:     strconv.Itoa(priorityInternalNVMe),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
		{
			Key:       database.ConfigPriorityInternalSATA,
			Value:     strconv.Itoa(priorityInternalSATA),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
		{
			Key:       database.ConfigPriorityRootFilesystem,
			Value:     strconv.Itoa(priorityRootFilesystem),
			Type:      "int",
			UpdatedBy: updatedBy,
		},
	}

	for _, config := range configs {
		if err := s.db.SetSystemConfig(config); err != nil {
			return fmt.Errorf("failed to set %s: %v", config.Key, err)
		}
	}

	log.Printf("💾 CONFIG: Updated disk manager config - MinFreeSpace: %dGB, Priorities: Ext=%d, Mount=%d, NVMe=%d, SATA=%d, Root=%d by %s",
		minimumFreeSpaceGB, priorityExternal, priorityMountedStorage, priorityInternalNVMe, priorityInternalSATA, priorityRootFilesystem, updatedBy)
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

	// Load all other system configurations from database
	configs, err := s.db.GetAllSystemConfigs()
	if err != nil {
		log.Printf("Warning: Failed to load system configs from database: %v", err)
		return nil
	}

	// Apply configurations to Config struct
	for _, config := range configs {
		switch config.Key {
		// Venue Configuration
		case database.ConfigVenueCode:
			cfg.VenueCode = config.Value
			log.Printf("⚙️ CONFIG: Loaded venue_code from database: %s", config.Value)

		// Arduino Configuration
		case database.ConfigArduinoCOMPort:
			cfg.ArduinoCOMPort = config.Value
		case database.ConfigArduinoBaudRate:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.ArduinoBaudRate = val
			}

		// RTSP Configuration
		case database.ConfigRTSPUsername:
			cfg.RTSPUsername = config.Value
		case database.ConfigRTSPPassword:
			cfg.RTSPPassword = config.Value
		case database.ConfigRTSPIP:
			cfg.RTSPIP = config.Value
		case database.ConfigRTSPPort:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.RTSPPort = strconv.Itoa(val)
			}
		case database.ConfigRTSPPath:
			cfg.RTSPPath = config.Value

		// Recording Configuration
		case database.ConfigSegmentDuration:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.SegmentDuration = val
			}
		case database.ConfigClipDuration:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.ClipDuration = val
			}
		case database.ConfigWidth:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.Width = val
			}
		case database.ConfigHeight:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.Height = val
			}
		case database.ConfigFrameRate:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.FrameRate = val
			}
		case database.ConfigResolution:
			cfg.Resolution = config.Value
		case database.ConfigAutoDelete:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.AutoDelete = val
			}

		// Storage Configuration
		case database.ConfigStoragePath:
			// Skip setting StoragePath from database - it will be set by diskManager.GetActiveDiskPath()
			// This prevents conflict between system config and disk manager's active disk selection
			// cfg.StoragePath = config.Value
		case database.ConfigHardwareAccel:
			cfg.HardwareAccel = config.Value
		case database.ConfigCodec:
			cfg.Codec = config.Value

		// Server Configuration
		case database.ConfigServerPort:
			if val, parseErr := strconv.Atoi(config.Value); parseErr == nil {
				cfg.ServerPort = strconv.Itoa(val)
			}
		case database.ConfigBaseURL:
			cfg.BaseURL = config.Value



		// R2 Storage Configuration
		case database.ConfigR2AccessKey:
			cfg.R2AccessKey = config.Value
		case database.ConfigR2SecretKey:
			cfg.R2SecretKey = config.Value
		case database.ConfigR2AccountID:
			cfg.R2AccountID = config.Value
		case database.ConfigR2Bucket:
			cfg.R2Bucket = config.Value
		case database.ConfigR2Region:
			cfg.R2Region = config.Value
		case database.ConfigR2Endpoint:
			cfg.R2Endpoint = config.Value
		case database.ConfigR2BaseURL:
			cfg.R2BaseURL = config.Value
		case database.ConfigR2Enabled:
			if val, parseErr := strconv.ParseBool(config.Value); parseErr == nil {
				cfg.R2Enabled = val
			}
		case database.ConfigR2TokenValue:
			cfg.R2TokenValue = config.Value
		}
	}

	log.Printf("⚙️ CONFIG: Loaded all system configurations from database")
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

// ValidateDiskManagerConfig validates disk manager configuration values
func ValidateDiskManagerConfig(minimumFreeSpaceGB, priorityExternal, priorityMountedStorage, priorityInternalNVMe, priorityInternalSATA, priorityRootFilesystem int) error {
	// Validate minimum free space
	if minimumFreeSpaceGB < 1 || minimumFreeSpaceGB > 1000 {
		return fmt.Errorf("minimum free space must be between 1 and 1000 GB")
	}

	// Validate priority values (1-1000, where 1 is highest priority)
	priorities := map[string]int{
		"external":         priorityExternal,
		"mounted storage":  priorityMountedStorage,
		"internal NVMe":    priorityInternalNVMe,
		"internal SATA":    priorityInternalSATA,
		"root filesystem":  priorityRootFilesystem,
	}

	for name, priority := range priorities {
		if priority < 1 || priority > 1000 {
			return fmt.Errorf("%s priority must be between 1 and 1000 (1 = highest priority)", name)
		}
	}

	// Check for duplicate priorities (optional warning, not error)
	priorityValues := []int{priorityExternal, priorityMountedStorage, priorityInternalNVMe, priorityInternalSATA, priorityRootFilesystem}
	priorityCount := make(map[int]int)
	for _, p := range priorityValues {
		priorityCount[p]++
	}

	for priority, count := range priorityCount {
		if count > 1 {
			log.Printf("⚠️ WARNING: Priority %d is used by %d disk types. Consider using unique priorities for better disk selection.", priority, count)
		}
	}

	return nil
}