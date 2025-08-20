package config

import (
	"encoding/json"
	"log"

	"ayo-mwr/database"
)

// ChunkConfigService handles chunk processing configuration
type ChunkConfigService struct {
	db database.Database
}

// NewChunkConfigService creates a new chunk configuration service
func NewChunkConfigService(db database.Database) *ChunkConfigService {
	return &ChunkConfigService{
		db: db,
	}
}

// ChunkProcessingConfig represents the chunk processing configuration
type ChunkProcessingConfig struct {
	Enabled                  bool `json:"enabled"`                  // Whether chunk processing is enabled
	ChunkDurationMinutes     int  `json:"chunkDurationMinutes"`     // Duration of each chunk in minutes (default: 15)
	MinSegmentsForChunk      int  `json:"minSegmentsForChunk"`      // Minimum segments required to create a chunk
	RetentionDays            int  `json:"retentionDays"`            // How long to keep chunks
	ProcessingTimeoutMinutes int  `json:"processingTimeoutMinutes"` // Timeout for chunk processing
	MaxConcurrentProcessing  int  `json:"maxConcurrentProcessing"`  // Maximum concurrent chunk processing jobs
}

// GetChunkConfig retrieves the chunk processing configuration
func (ccs *ChunkConfigService) GetChunkConfig() (*ChunkProcessingConfig, error) {
	config, err := ccs.db.GetSystemConfig("chunk_processing")
	if err != nil {
		// Return default configuration if not found
		return ccs.getDefaultChunkConfig(), nil
	}

	var chunkConfig ChunkProcessingConfig
	if err := json.Unmarshal([]byte(config.Value), &chunkConfig); err != nil {
		log.Printf("[ChunkConfig] Warning: Failed to parse chunk config, using defaults: %v", err)
		return ccs.getDefaultChunkConfig(), nil
	}

	return &chunkConfig, nil
}

// SetChunkConfig saves the chunk processing configuration
func (ccs *ChunkConfigService) SetChunkConfig(config *ChunkProcessingConfig) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	systemConfig := database.SystemConfig{
		Key:       "chunk_processing",
		Value:     string(configJSON),
		Type:      "json",
		UpdatedBy: "system",
	}

	return ccs.db.SetSystemConfig(systemConfig)
}

// getDefaultChunkConfig returns the default chunk processing configuration
func (ccs *ChunkConfigService) getDefaultChunkConfig() *ChunkProcessingConfig {
	return &ChunkProcessingConfig{
		Enabled:                  true,
		ChunkDurationMinutes:     15,
		MinSegmentsForChunk:      10, // At least 40 seconds of video
		RetentionDays:            7,
		ProcessingTimeoutMinutes: 10,
		MaxConcurrentProcessing:  2,
	}
}

// IsChunkProcessingEnabled checks if chunk processing is enabled
func (ccs *ChunkConfigService) IsChunkProcessingEnabled() bool {
	config, err := ccs.GetChunkConfig()
	if err != nil {
		log.Printf("[ChunkConfig] Warning: Failed to get chunk config, assuming enabled: %v", err)
		return true
	}

	return config.Enabled
}

// EnableChunkProcessing enables or disables chunk processing
func (ccs *ChunkConfigService) EnableChunkProcessing(enabled bool) error {
	config, err := ccs.GetChunkConfig()
	if err != nil {
		config = ccs.getDefaultChunkConfig()
	}

	config.Enabled = enabled

	if err := ccs.SetChunkConfig(config); err != nil {
		return err
	}

	if enabled {
		log.Println("[ChunkConfig] ✅ Chunk processing enabled")
	} else {
		log.Println("[ChunkConfig] ❌ Chunk processing disabled")
	}

	return nil
}

// UpdateChunkDuration updates the chunk duration configuration
func (ccs *ChunkConfigService) UpdateChunkDuration(minutes int) error {
	config, err := ccs.GetChunkConfig()
	if err != nil {
		config = ccs.getDefaultChunkConfig()
	}

	config.ChunkDurationMinutes = minutes

	if err := ccs.SetChunkConfig(config); err != nil {
		return err
	}

	log.Printf("[ChunkConfig] Updated chunk duration to %d minutes", minutes)
	return nil
}

// GetChunkConfigJSON returns the chunk configuration as JSON for API responses
func (ccs *ChunkConfigService) GetChunkConfigJSON() (map[string]interface{}, error) {
	config, err := ccs.GetChunkConfig()
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"enabled":                    config.Enabled,
		"chunk_duration_minutes":     config.ChunkDurationMinutes,
		"min_segments_for_chunk":     config.MinSegmentsForChunk,
		"retention_days":             config.RetentionDays,
		"processing_timeout_minutes": config.ProcessingTimeoutMinutes,
		"max_concurrent_processing":  config.MaxConcurrentProcessing,
	}, nil
}