package cron

import (
	"log"
	"time"

	"ayo-mwr/database"
	"ayo-mwr/storage"
)

// DiskManagementCron handles disk space monitoring and management tasks
type DiskManagementCron struct {
	db          database.Database
	diskManager *storage.DiskManager
	running     bool
}

// NewDiskManagementCron creates a new disk management cron instance
func NewDiskManagementCron(db database.Database, diskManager *storage.DiskManager) *DiskManagementCron {
	return &DiskManagementCron{
		db:          db,
		diskManager: diskManager,
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

		// Then run nightly at 2 AM
		for dmc.running {
			now := time.Now()
			next2AM := time.Date(now.Year(), now.Month(), now.Day()+1, 2, 0, 0, 0, now.Location())
			duration := next2AM.Sub(now)

			log.Printf("Disk management cron: next run scheduled in %v at %v", duration, next2AM)

			select {
			case <-time.After(duration):
				if dmc.running {
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

// runDiskSpaceScan performs the nightly disk space scan
func (dmc *DiskManagementCron) runDiskSpaceScan() {
	log.Println("=== Starting nightly disk space scan ===")

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

// runDiskSelection selects the active disk for the next day's recordings
func (dmc *DiskManagementCron) runDiskSelection() {
	log.Println("=== Selecting active disk for recording ===")

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