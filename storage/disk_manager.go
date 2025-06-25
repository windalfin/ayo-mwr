package storage

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"ayo-mwr/database"
	"github.com/google/uuid"
)

const (
	MinimumFreeSpaceGB = 100 // Minimum free space in GB required for a disk to be active
)

// DiskManager handles storage disk management and selection
type DiskManager struct {
	db database.Database
}

// NewDiskManager creates a new disk manager instance
func NewDiskManager(db database.Database) *DiskManager {
	return &DiskManager{
		db: db,
	}
}

// ScanAndUpdateDiskSpace scans all registered disks and updates their space information
func (dm *DiskManager) ScanAndUpdateDiskSpace() error {
	log.Println("Starting disk space scan...")

	disks, err := dm.db.GetStorageDisks()
	if err != nil {
		return fmt.Errorf("failed to get storage disks: %v", err)
	}

	for _, disk := range disks {
		totalGB, availableGB, err := dm.getDiskSpace(disk.Path)
		if err != nil {
			log.Printf("Warning: Failed to get disk space for %s (%s): %v", disk.ID, disk.Path, err)
			continue
		}

		err = dm.db.UpdateDiskSpace(disk.ID, totalGB, availableGB)
		if err != nil {
			log.Printf("Warning: Failed to update disk space for %s: %v", disk.ID, err)
			continue
		}

		log.Printf("Updated disk %s (%s): %dGB total, %dGB available",
			disk.ID, disk.Path, totalGB, availableGB)
	}

	log.Println("Disk space scan completed")
	return nil
}

// SelectActiveDisk selects the best available disk for recording
func (dm *DiskManager) SelectActiveDisk() error {
	log.Println("Selecting active disk for recording...")

	disks, err := dm.db.GetStorageDisks()
	if err != nil {
		return fmt.Errorf("failed to get storage disks: %v", err)
	}

	// Find the first disk with sufficient free space (sorted by priority)
	var selectedDisk *database.StorageDisk
	for _, disk := range disks {
		if disk.AvailableSpaceGB >= MinimumFreeSpaceGB {
			selectedDisk = &disk
			break
		}
	}

	if selectedDisk == nil {
		return fmt.Errorf("no disk has sufficient free space (minimum %dGB required)", MinimumFreeSpaceGB)
	}

	// Set the selected disk as active
	err = dm.db.SetActiveDisk(selectedDisk.ID)
	if err != nil {
		return fmt.Errorf("failed to set active disk: %v", err)
	}

	log.Printf("Selected disk %s (%s) as active: %dGB available",
		selectedDisk.ID, selectedDisk.Path, selectedDisk.AvailableSpaceGB)

	return nil
}

// GetActiveDiskPath returns the path of the currently active disk
func (dm *DiskManager) GetActiveDiskPath() (string, error) {
	activeDisk, err := dm.db.GetActiveDisk()
	if err != nil {
		return "", fmt.Errorf("failed to get active disk: %v", err)
	}

	if activeDisk == nil {
		return "", fmt.Errorf("no active disk found")
	}

	return activeDisk.Path, nil
}

// RegisterDisk adds a new storage disk to the system
func (dm *DiskManager) RegisterDisk(path string, priorityOrder int) error {
	// Verify the path exists and is accessible
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("path does not exist: %s", path)
	}

	// Get initial disk space
	totalGB, availableGB, err := dm.getDiskSpace(path)
	if err != nil {
		return fmt.Errorf("failed to get disk space: %v", err)
	}

	// Create storage disk record
	disk := database.StorageDisk{
		ID:               uuid.New().String(),
		Path:             path,
		TotalSpaceGB:     totalGB,
		AvailableSpaceGB: availableGB,
		IsActive:         false,
		PriorityOrder:    priorityOrder,
		LastScan:         time.Now(),
		CreatedAt:        time.Now(),
	}

	err = dm.db.CreateStorageDisk(disk)
	if err != nil {
		return fmt.Errorf("failed to create storage disk: %v", err)
	}

	log.Printf("Registered new storage disk: %s (%s) - %dGB total, %dGB available",
		disk.ID, disk.Path, disk.TotalSpaceGB, disk.AvailableSpaceGB)

	return nil
}

// ListDisks returns all registered storage disks with their current status
func (dm *DiskManager) ListDisks() ([]database.StorageDisk, error) {
	return dm.db.GetStorageDisks()
}

// GetRecordingPath returns the full path for a new recording on the active disk
func (dm *DiskManager) GetRecordingPath(cameraName string) (string, string, error) {
	activeDisk, err := dm.db.GetActiveDisk()
	if err != nil {
		return "", "", fmt.Errorf("failed to get active disk: %v", err)
	}

	if activeDisk == nil {
		return "", "", fmt.Errorf("no active disk available")
	}

	// Create camera-specific directory on active disk
	recordingDir := filepath.Join(activeDisk.Path, "recordings", cameraName)
	err = os.MkdirAll(recordingDir, 0755)
	if err != nil {
		return "", "", fmt.Errorf("failed to create recording directory: %v", err)
	}

	return recordingDir, activeDisk.ID, nil
}

// CheckDiskHealth verifies all disks are accessible and reports issues
func (dm *DiskManager) CheckDiskHealth() error {
	disks, err := dm.db.GetStorageDisks()
	if err != nil {
		return fmt.Errorf("failed to get storage disks: %v", err)
	}

	var issues []string

	for _, disk := range disks {
		// Check if path is accessible
		if _, err := os.Stat(disk.Path); os.IsNotExist(err) {
			issues = append(issues, fmt.Sprintf("Disk %s path not accessible: %s", disk.ID, disk.Path))
			continue
		}

		// Check if disk is nearly full
		if disk.AvailableSpaceGB < MinimumFreeSpaceGB {
			issues = append(issues, fmt.Sprintf("Disk %s is nearly full: %dGB available (minimum %dGB)",
				disk.ID, disk.AvailableSpaceGB, MinimumFreeSpaceGB))
		}

		// Check if disk hasn't been scanned recently
		if time.Since(disk.LastScan) > 25*time.Hour { // Allow some buffer beyond 24 hours
			issues = append(issues, fmt.Sprintf("Disk %s hasn't been scanned recently: last scan %v",
				disk.ID, disk.LastScan))
		}
	}

	if len(issues) > 0 {
		for _, issue := range issues {
			log.Printf("DISK HEALTH WARNING: %s", issue)
		}
		return fmt.Errorf("disk health issues detected: %d warnings", len(issues))
	}

	log.Println("All disks are healthy")
	return nil
}

// getDiskSpace returns total and available space in GB for a given path
func (dm *DiskManager) getDiskSpace(path string) (int64, int64, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		return 0, 0, err
	}

	// Convert to GB
	totalGB := int64(stat.Blocks * uint64(stat.Bsize) / (1024 * 1024 * 1024))
	availableGB := int64(stat.Bavail * uint64(stat.Bsize) / (1024 * 1024 * 1024))

	return totalGB, availableGB, nil
}

// GetDiskUsageStats returns usage statistics for all disks
func (dm *DiskManager) GetDiskUsageStats() (map[string]interface{}, error) {
	disks, err := dm.db.GetStorageDisks()
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"total_disks":        len(disks),
		"active_disks":       0,
		"total_space_gb":     int64(0),
		"available_space_gb": int64(0),
		"used_space_gb":      int64(0),
		"disks":              []map[string]interface{}{},
	}

	var totalSpace, availableSpace int64

	for _, disk := range disks {
		totalSpace += disk.TotalSpaceGB
		availableSpace += disk.AvailableSpaceGB

		if disk.IsActive {
			stats["active_disks"] = stats["active_disks"].(int) + 1
		}

		diskStats := map[string]interface{}{
			"id":                 disk.ID,
			"path":               disk.Path,
			"is_active":          disk.IsActive,
			"total_space_gb":     disk.TotalSpaceGB,
			"available_space_gb": disk.AvailableSpaceGB,
			"used_space_gb":      disk.TotalSpaceGB - disk.AvailableSpaceGB,
			"usage_percent":      float64(disk.TotalSpaceGB-disk.AvailableSpaceGB) / float64(disk.TotalSpaceGB) * 100,
			"last_scan":          disk.LastScan,
		}

		stats["disks"] = append(stats["disks"].([]map[string]interface{}), diskStats)
	}

	stats["total_space_gb"] = totalSpace
	stats["available_space_gb"] = availableSpace
	stats["used_space_gb"] = totalSpace - availableSpace
	stats["overall_usage_percent"] = float64(totalSpace-availableSpace) / float64(totalSpace) * 100

	return stats, nil
}
