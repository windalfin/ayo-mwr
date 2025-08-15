package api

import (
	"net/http"
	"strconv"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"

	"github.com/gin-gonic/gin"
)

// ChunkHandlers contains handlers for chunk processing management
type ChunkHandlers struct {
	chunkConfigService *config.ChunkConfigService
	db                 database.Database
}

// NewChunkHandlers creates new chunk handlers
func NewChunkHandlers(chunkConfigService *config.ChunkConfigService, db database.Database) *ChunkHandlers {
	return &ChunkHandlers{
		chunkConfigService: chunkConfigService,
		db:                 db,
	}
}

// GetChunkConfig returns the current chunk processing configuration
func (ch *ChunkHandlers) GetChunkConfig(c *gin.Context) {
	config, err := ch.chunkConfigService.GetChunkConfigJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get chunk configuration",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config,
	})
}

// UpdateChunkConfig updates the chunk processing configuration
func (ch *ChunkHandlers) UpdateChunkConfig(c *gin.Context) {
	var updateConfig struct {
		Enabled                  *bool `json:"enabled"`
		ChunkDurationMinutes     *int  `json:"chunkDurationMinutes"`
		MinSegmentsForChunk      *int  `json:"minSegmentsForChunk"`
		RetentionDays            *int  `json:"retentionDays"`
		ProcessingTimeoutMinutes *int  `json:"processingTimeoutMinutes"`
		MaxConcurrentProcessing  *int  `json:"maxConcurrentProcessing"`
	}

	if err := c.ShouldBindJSON(&updateConfig); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	// Get current configuration
	currentConfig, err := ch.chunkConfigService.GetChunkConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get current configuration",
			"details": err.Error(),
		})
		return
	}

	// Update only provided fields
	if updateConfig.Enabled != nil {
		currentConfig.Enabled = *updateConfig.Enabled
	}
	if updateConfig.ChunkDurationMinutes != nil {
		if *updateConfig.ChunkDurationMinutes < 5 || *updateConfig.ChunkDurationMinutes > 60 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Chunk duration must be between 5 and 60 minutes",
			})
			return
		}
		currentConfig.ChunkDurationMinutes = *updateConfig.ChunkDurationMinutes
	}
	if updateConfig.MinSegmentsForChunk != nil {
		if *updateConfig.MinSegmentsForChunk < 1 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Minimum segments must be at least 1",
			})
			return
		}
		currentConfig.MinSegmentsForChunk = *updateConfig.MinSegmentsForChunk
	}
	if updateConfig.RetentionDays != nil {
		if *updateConfig.RetentionDays < 1 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Retention days must be at least 1",
			})
			return
		}
		currentConfig.RetentionDays = *updateConfig.RetentionDays
	}
	if updateConfig.ProcessingTimeoutMinutes != nil {
		if *updateConfig.ProcessingTimeoutMinutes < 5 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Processing timeout must be at least 5 minutes",
			})
			return
		}
		currentConfig.ProcessingTimeoutMinutes = *updateConfig.ProcessingTimeoutMinutes
	}
	if updateConfig.MaxConcurrentProcessing != nil {
		if *updateConfig.MaxConcurrentProcessing < 1 || *updateConfig.MaxConcurrentProcessing > 10 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Max concurrent processing must be between 1 and 10",
			})
			return
		}
		currentConfig.MaxConcurrentProcessing = *updateConfig.MaxConcurrentProcessing
	}

	// Save updated configuration
	if err := ch.chunkConfigService.SetChunkConfig(currentConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to update chunk configuration",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Chunk configuration updated successfully",
		"data":    currentConfig,
	})
}

// GetChunkStatistics returns chunk processing statistics
func (ch *ChunkHandlers) GetChunkStatistics(c *gin.Context) {
	stats, err := ch.db.GetChunkStatistics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get chunk statistics",
			"details": err.Error(),
		})
		return
	}

	// Add additional runtime information
	stats["server_time"] = time.Now()
	stats["chunk_processing_enabled"] = ch.chunkConfigService.IsChunkProcessingEnabled()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// ForceChunkProcessing manually triggers chunk processing
func (ch *ChunkHandlers) ForceChunkProcessing(c *gin.Context) {
	// Check if chunk processing is enabled
	if !ch.chunkConfigService.IsChunkProcessingEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Chunk processing is currently disabled",
		})
		return
	}

	// TODO: Implement manual chunk processing trigger
	// This would require a service interface or message queue
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Manual chunk processing endpoint - implementation pending",
		"timestamp": time.Now(),
	})
}

// ForceChunkCleanup manually triggers chunk cleanup
func (ch *ChunkHandlers) ForceChunkCleanup(c *gin.Context) {
	// TODO: Implement manual chunk cleanup trigger
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Manual chunk cleanup endpoint - implementation pending",
		"timestamp": time.Now(),
	})
}

// GetChunkDiscoveryStats returns segment discovery performance stats for a specific time range
func (ch *ChunkHandlers) GetChunkDiscoveryStats(c *gin.Context) {
	cameraName := c.Query("camera")
	startTimeStr := c.Query("start_time")
	endTimeStr := c.Query("end_time")

	if cameraName == "" || startTimeStr == "" || endTimeStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Missing required parameters: camera, start_time, end_time",
		})
		return
	}

	startTime, err := time.Parse("2006-01-02T15:04:05", startTimeStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid start_time format, expected: 2006-01-02T15:04:05",
		})
		return
	}

	endTime, err := time.Parse("2006-01-02T15:04:05", endTimeStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid end_time format, expected: 2006-01-02T15:04:05",
		})
		return
	}

	if endTime.Before(startTime) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "end_time must be after start_time",
		})
		return
	}

	// This would require access to the ChunkDiscoveryService
	// For now, return a placeholder response
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Discovery stats endpoint - implementation needed",
		"data": map[string]interface{}{
			"camera":     cameraName,
			"start_time": startTime,
			"end_time":   endTime,
			"note":       "This endpoint will be implemented when ChunkDiscoveryService is integrated",
		},
	})
}

// EnableChunkProcessing enables or disables chunk processing
func (ch *ChunkHandlers) EnableChunkProcessing(c *gin.Context) {
	enabledStr := c.Query("enabled")
	if enabledStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Missing 'enabled' query parameter (true/false)",
		})
		return
	}

	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid 'enabled' value, must be true or false",
		})
		return
	}

	if err := ch.chunkConfigService.EnableChunkProcessing(enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to update chunk processing status",
			"details": err.Error(),
		})
		return
	}

	status := "disabled"
	if enabled {
		status = "enabled"
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Chunk processing " + status,
		"enabled": enabled,
	})
}