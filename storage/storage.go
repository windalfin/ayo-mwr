package storage

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// GetBestStoragePath returns the best available storage path based on the configuration
// It checks if external storage is available and has enough free space
func GetBestStoragePath(cfg interface{ GetExternalStoragePath() string }, minFreeGB int) (string, error) {
	extPath := cfg.GetExternalStoragePath()
	
	// If no external storage is configured, use the default storage
	if extPath == "" {
		return "", errors.New("no external storage configured")
	}

	// Check if external storage is available
	if _, err := os.Stat(extPath); os.IsNotExist(err) {
		return "", errors.New("external storage not available")
	}

	// Check free space on external storage
	freeGB, err := getFreeSpaceGB(extPath)
	if err != nil {
		return "", err
	}

	// If external storage has enough free space, use it
	if freeGB >= float64(minFreeGB) {
		return extPath, nil
	}

	return "", errors.New("not enough free space on external storage")
}

// getFreeSpaceGB returns the free space in GB for the given path
func getFreeSpaceGB(path string) (float64, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		return 0, err
	}

	// Calculate free space in GB
	freeBytes := stat.Bavail * uint64(stat.Bsize)
	freeGB := float64(freeBytes) / (1024 * 1024 * 1024)

	return freeGB, nil
}

// EnsurePath creates the directory structure if it doesn't exist
func EnsurePath(basePath string, subDirs ...string) (string, error) {
	fullPath := filepath.Join(append([]string{basePath}, subDirs...)...)
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return "", err
	}
	return fullPath, nil
}
