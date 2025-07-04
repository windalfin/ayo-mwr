package storage

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"ayo-mwr/database"
	"github.com/google/uuid"
)

const (
	MinimumFreeSpaceGB = 100 // Minimum free space in GB required for a disk to be active
	
	// Priority ranges for different disk types
	PriorityExternal    = 1   // External USB/removable disks: 1-100
	PriorityInternalNVMe = 101 // Internal NVMe disks: 101-200  
	PriorityInternalSATA = 201 // Internal SATA disks: 201-300
)

// DiskType represents the type of storage disk
type DiskType string

const (
	DiskTypeExternal    DiskType = "external"    // External USB/removable disk
	DiskTypeInternalNVMe DiskType = "internal_nvme" // Internal NVMe SSD
	DiskTypeInternalSATA DiskType = "internal_sata" // Internal SATA disk
	DiskTypeUnknown     DiskType = "unknown"     // Unknown disk type
)

// DiskManager handles storage disk management and selection
// It also supports automatic discovery of new storage disks (Linux only).
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
// ScanAndUpdateDiskSpace updates space info for all known disks.
// NOTE: It does *not* discover new disks; call DiscoverAndRegisterDisks first if you
// want to pick up freshly mounted devices.
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

// RegisterDisk adds a new storage disk to the system with automatic priority detection
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

	// Auto-detect disk type and assign priority if not manually specified
	finalPriority := priorityOrder
	diskType := DiskTypeUnknown
	
	if priorityOrder == 0 {
		diskType = dm.detectDiskType(path)
		finalPriority = dm.getAutoPriority(diskType)
		log.Printf("Auto-detected disk type: %s, assigned priority: %d", diskType, finalPriority)
	}

	// Create storage disk record
	disk := database.StorageDisk{
		ID:               uuid.New().String(),
		Path:             path,
		TotalSpaceGB:     totalGB,
		AvailableSpaceGB: availableGB,
		IsActive:         false,
		PriorityOrder:    finalPriority,
		LastScan:         time.Now(),
		CreatedAt:        time.Now(),
	}

	err = dm.db.CreateStorageDisk(disk)
	if err != nil {
		return fmt.Errorf("failed to create storage disk: %v", err)
	}

	log.Printf("Registered new storage disk: %s (%s) - %dGB total, %dGB available, type: %s, priority: %d",
		disk.ID, disk.Path, disk.TotalSpaceGB, disk.AvailableSpaceGB, diskType, disk.PriorityOrder)

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

// detectDiskType detects the type of disk based on the mount path (Linux only)
func (dm *DiskManager) detectDiskType(mountPath string) DiskType {
	if runtime.GOOS != "linux" {
		log.Printf("Disk type detection is only supported on Linux, got: %s", runtime.GOOS)
		return DiskTypeUnknown
	}

	// Find the block device for this mount path
	blockDevice := dm.findBlockDevice(mountPath)
	if blockDevice == "" {
		log.Printf("Could not find block device for mount path: %s", mountPath)
		return DiskTypeUnknown
	}

	log.Printf("Found block device %s for mount path %s", blockDevice, mountPath)

	// Check if it's a removable/external device
	if dm.isRemovableDevice(blockDevice) {
		return DiskTypeExternal
	}

	// Check if it's rotational (SATA) or non-rotational (NVMe/SSD)
	if dm.isRotationalDevice(blockDevice) {
		return DiskTypeInternalSATA
	}

	return DiskTypeInternalNVMe
}

// findBlockDevice finds the block device name for a given mount path
func (dm *DiskManager) findBlockDevice(mountPath string) string {
	// Get absolute path
	absPath, err := filepath.Abs(mountPath)
	if err != nil {
		log.Printf("Failed to get absolute path for %s: %v", mountPath, err)
		return ""
	}

	// Use findmnt to get the device for this mount point
	// This is more reliable than parsing /proc/mounts
	content, err := ioutil.ReadFile("/proc/mounts")
	if err != nil {
		log.Printf("Failed to read /proc/mounts: %v", err)
		return ""
	}

	lines := strings.Split(string(content), "\n")
	var bestMatch struct {
		device string
		path   string
		length int
	}

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		device := fields[0]
		mountPoint := fields[1]

		// Skip non-block devices
		if !strings.HasPrefix(device, "/dev/") {
			continue
		}

		// Check if this mount point is a parent of our path
		if strings.HasPrefix(absPath, mountPoint) && len(mountPoint) > bestMatch.length {
			bestMatch.device = device
			bestMatch.path = mountPoint
			bestMatch.length = len(mountPoint)
		}
	}

	if bestMatch.device == "" {
		return ""
	}

	// Extract the base device name (e.g., /dev/sda1 -> sda)
	deviceName := filepath.Base(bestMatch.device)
	
	// Remove partition numbers (e.g., sda1 -> sda, nvme0n1p1 -> nvme0n1)
	if strings.Contains(deviceName, "nvme") {
		// NVMe devices: nvme0n1p1 -> nvme0n1
		parts := strings.Split(deviceName, "p")
		if len(parts) > 1 {
			deviceName = parts[0]
		}
	} else {
		// Traditional devices: sda1 -> sda
		deviceName = strings.TrimRightFunc(deviceName, func(r rune) bool {
			return r >= '0' && r <= '9'
		})
	}

	return deviceName
}

// isRemovableDevice checks if a block device is removable (external)
func (dm *DiskManager) isRemovableDevice(blockDevice string) bool {
	removablePath := fmt.Sprintf("/sys/block/%s/removable", blockDevice)
	
	content, err := ioutil.ReadFile(removablePath)
	if err != nil {
		log.Printf("Failed to read %s: %v", removablePath, err)
		return false
	}

	removable := strings.TrimSpace(string(content))
	return removable == "1"
}

// isRotationalDevice checks if a block device is rotational (SATA HDD)
func (dm *DiskManager) isRotationalDevice(blockDevice string) bool {
	rotationalPath := fmt.Sprintf("/sys/block/%s/queue/rotational", blockDevice)
	
	content, err := ioutil.ReadFile(rotationalPath)
	if err != nil {
		log.Printf("Failed to read %s: %v", rotationalPath, err)
		// Default to rotational if we can't determine
		return true
	}

	rotational := strings.TrimSpace(string(content))
	return rotational == "1"
}

// getAutoPriority returns the automatic priority for a given disk type
// DiscoverAndRegisterDisks scans /proc/mounts (Linux) and registers any block
// devices that are not yet present in the storage_disks table. It intentionally
// skips well-known system mount points (/, /boot, /proc, etc.) and read-only/virtual
// filesystems.
func (dm *DiskManager) DiscoverAndRegisterDisks() {
    if runtime.GOOS != "linux" {
        log.Println("[DiskManager] Auto discovery only supported on Linux")
        return
    }

    disks, err := dm.db.GetStorageDisks()
    if err != nil {
        log.Printf("[DiskManager] Failed to get existing disks: %v", err)
        return
    }
    existing := make(map[string]struct{})
    for _, d := range disks {
        existing[d.Path] = struct{}{}
    }

    content, err := ioutil.ReadFile("/proc/mounts")
    if err != nil {
        log.Printf("[DiskManager] Failed to read /proc/mounts: %v", err)
        return
    }

    for _, line := range strings.Split(string(content), "\n") {
        fields := strings.Fields(line)
        if len(fields) < 3 {
            continue
        }
        device := fields[0]
        mountPoint := fields[1]
        fsType := fields[2]

        if !strings.HasPrefix(device, "/dev/") { // ignore pseudo/virtual mounts
            continue
        }
        if dm.shouldSkipMount(mountPoint, fsType) {
            continue
        }
        if _, ok := existing[mountPoint]; ok {
            continue // already registered
        }

        // Attempt to register
        if err := dm.RegisterDisk(mountPoint, 0); err != nil {
            log.Printf("[DiskManager] Auto-register skipped %s: %v", mountPoint, err)
        } else {
            log.Printf("[DiskManager] Auto-registered new disk: %s", mountPoint)
        }
    }
}

// shouldSkipMount returns true for system mount points or unsupported fs types.
func (dm *DiskManager) shouldSkipMount(mountPoint, fsType string) bool {
    // System mount prefixes that we do not want to treat as recording disks.
    skipPrefixes := []string{"/proc", "/sys", "/run", "/dev", "/boot", "/snap", "/var", "/tmp"}
    for _, p := range skipPrefixes {
        if strings.HasPrefix(mountPoint, p) {
            return true
        }
    }
    // Always ignore root filesystem ("/") to prevent filling OS disk inadvertently
    if mountPoint == "/" {
        return true
    }
    // Skip read-only or special filesystems
    specialFSTypes := []string{"squashfs", "ramfs", "tmpfs", "devtmpfs"}
    for _, t := range specialFSTypes {
        if fsType == t {
            return true
        }
    }
    return false
}

func (dm *DiskManager) getAutoPriority(diskType DiskType) int {
	switch diskType {
	case DiskTypeExternal:
		return PriorityExternal
	case DiskTypeInternalNVMe:
		return PriorityInternalNVMe
	case DiskTypeInternalSATA:
		return PriorityInternalSATA
	default:
		return PriorityInternalSATA // Default to SATA priority for unknown types
	}
}
