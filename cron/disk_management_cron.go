package cron

import (
	"log"
	"os"
	"time"

    "ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// DiskManagementCron handles disk space monitoring and management tasks
type DiskManagementCron struct {
    db          database.Database
    diskManager *storage.DiskManager
    cfg         *config.Config
    running     bool
}

// NewDiskManagementCron creates a new disk management cron instance
func NewDiskManagementCron(db database.Database, diskManager *storage.DiskManager, cfg *config.Config) *DiskManagementCron {
    return &DiskManagementCron{
        db:          db,
        diskManager: diskManager,
        cfg:         cfg,
        running:     false,
    }
}

// Start begins the disk management cron job
func (dmc *DiskManagementCron) Start() {
	if dmc.running {
		log.Println("Disk management cron is already running")
		return
	}

	dmc.running = true
	log.Println("Starting disk management cron job")

	go func() {
		// Run initial scan immediately
		dmc.runDiskSpaceScan()
		dmc.runDiskSelection()

		// Then run every 4 hours
		ticker := time.NewTicker(4 * time.Hour)
		defer ticker.Stop()

		for dmc.running {
			select {
			case <-ticker.C:
				if dmc.running {
					log.Println("Running scheduled 4-hour disk management check")
					dmc.runDiskSpaceScan()
					dmc.runDiskSelection()
				}
			}
		}
	}()
}

// Stop stops the disk management cron job
func (dmc *DiskManagementCron) Stop() {
	log.Println("Stopping disk management cron job")
	dmc.running = false
}

// runDiskSpaceScan performs the disk space scan
func (dmc *DiskManagementCron) runDiskSpaceScan() {
	log.Println("=== Starting disk space scan ===")
	log.Printf("Scan time: %s", time.Now().Format("2006-01-02 15:04:05"))

    // Discover and register any newly mounted disks before scanning
    dmc.diskManager.DiscoverAndRegisterDisks()
	startTime := time.Now()

	err := dmc.diskManager.ScanAndUpdateDiskSpace()
	if err != nil {
		log.Printf("ERROR: Disk space scan failed: %v", err)
		return
	}

	// Check disk health
	err = dmc.diskManager.CheckDiskHealth()
	if err != nil {
		log.Printf("WARNING: Disk health check found issues: %v", err)
	}

	duration := time.Since(startTime)
	log.Printf("=== Disk space scan completed in %v ===", duration)

	// Log disk usage statistics
	dmc.logDiskUsageStats()
}

// runDiskSelection selects the active disk for recording
func (dmc *DiskManagementCron) runDiskSelection() {
	log.Println("=== Selecting active disk for recording ===")
	
	// Get current active disk before selection
	currentDisk, _ := dmc.db.GetActiveDisk()
	currentPath := ""
	if currentDisk != nil {
		currentPath = currentDisk.Path
	}

	err := dmc.diskManager.SelectActiveDisk()
	if err != nil {
		log.Printf("ERROR: Failed to select active disk: %v", err)
		return
	}

	// Get the selected active disk
	activeDiskPath, err := dmc.diskManager.GetActiveDiskPath()
	if err != nil {
		log.Printf("ERROR: Failed to get active disk path: %v", err)
		return
	}
	
	// Check if disk changed
	if currentPath != "" && currentPath != activeDiskPath {
		log.Printf("🔄 DISK ROTATION: Active disk changed from %s to %s", currentPath, activeDiskPath)
		log.Printf("🔄 DISK ROTATION: New recordings will be saved to: %s", activeDiskPath)
	}

	// Update the global storage path so legacy recorders will use the new disk for future restarts
    if dmc.cfg != nil {
        dmc.cfg.StoragePath = activeDiskPath
        // Update environment variable so all functions use the active disk
        os.Setenv("STORAGE_PATH", activeDiskPath)
    }

    log.Printf("=== Active disk selected: %s ===", activeDiskPath)
}

// logDiskUsageStats logs current disk usage statistics
func (dmc *DiskManagementCron) logDiskUsageStats() {
	stats, err := dmc.diskManager.GetDiskUsageStats()
	if err != nil {
		log.Printf("WARNING: Failed to get disk usage stats: %v", err)
		return
	}

	log.Printf("=== Disk Usage Statistics ===")
	log.Printf("Total disks: %v", stats["total_disks"])
	log.Printf("Active disks: %v", stats["active_disks"])
	log.Printf("Overall space: %d GB total, %d GB available (%.1f%% used)",
		stats["total_space_gb"], stats["available_space_gb"], stats["overall_usage_percent"])

	// Log individual disk stats
	if disks, ok := stats["disks"].([]map[string]interface{}); ok {
		for _, disk := range disks {
			status := "inactive"
			if disk["is_active"].(bool) {
				status = "ACTIVE"
			}
			
			log.Printf("Disk %s [%s]: %s - %d GB total, %d GB available (%.1f%% used)",
				disk["id"], status, disk["path"],
				int64(disk["total_space_gb"].(int64)),
				int64(disk["available_space_gb"].(int64)),
				disk["usage_percent"].(float64))
		}
	}
	log.Printf("=== End Disk Usage Statistics ===")
}

// RunManualScan triggers a manual disk scan (useful for testing or on-demand checks)
func (dmc *DiskManagementCron) RunManualScan() error {
	log.Println("Running manual disk space scan...")
	
	err := dmc.diskManager.ScanAndUpdateDiskSpace()
	if err != nil {
		return err
	}

	err = dmc.diskManager.CheckDiskHealth()
	if err != nil {
		log.Printf("WARNING: Disk health issues found: %v", err)
	}

	dmc.logDiskUsageStats()
	
	log.Println("Manual disk scan completed")
	return nil
}

// IsRunning returns whether the cron job is currently running
func (dmc *DiskManagementCron) IsRunning() bool {
	return dmc.running
}