package config

import (
	"sync"
)

// ConfigManager provides thread-safe access to the application configuration
type ConfigManager struct {
	mu     sync.RWMutex
	config Config
}

// NewConfigManager creates a new configuration manager with the provided initial config
func NewConfigManager(initialConfig Config) *ConfigManager {
	return &ConfigManager{
		config: initialConfig,
	}
}

// GetConfig returns a copy of the current configuration
func (cm *ConfigManager) GetConfig() Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config
}

// UpdateConfig updates the configuration with a new version
func (cm *ConfigManager) UpdateConfig(newConfig Config) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.config = newConfig
}

// GetCameraByName returns a camera configuration by name, or nil if not found
func (cm *ConfigManager) GetCameraByName(name string) *CameraConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	for i, camera := range cm.config.Cameras {
		if camera.Name == name {
			// Return a copy of the camera config to prevent race conditions
			cameraCopy := cm.config.Cameras[i]
			return &cameraCopy
		}
	}
	return nil
}
