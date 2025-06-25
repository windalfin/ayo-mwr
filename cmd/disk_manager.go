package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"
)

func main() {
	// Parse command line arguments
	action := flag.String("action", "list", "Action to perform: list, add, remove, scan, status")
	diskPath := flag.String("path", "", "Path to disk (required for add/remove actions)")
	priority := flag.Int("priority", 1, "Priority order for disk (lower = higher priority)")
	configFile := flag.String("config", "", "Path to config file (optional)")
	flag.Parse()

	// Load configuration
	var cfg config.Config
	if *configFile != "" {
		var err error
		cfg, err = config.LoadConfigFromFile(*configFile)
		if err != nil {
			log.Printf("Error loading config from file: %v, falling back to environment variables", err)
			cfg = config.LoadConfig()
		}
	} else {
		cfg = config.LoadConfig()
	}

	// Initialize database
	db, err := database.NewSQLiteDB(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize disk manager
	diskManager := storage.NewDiskManager(db)

	// Execute the requested action
	switch *action {
	case "list":
		listDisks(diskManager)
	case "add":
		if *diskPath == "" {
			fmt.Println("Error: -path is required for add action")
			flag.Usage()
			os.Exit(1)
		}
		addDisk(diskManager, *diskPath, *priority)
	case "remove":
		if *diskPath == "" {
			fmt.Println("Error: -path is required for remove action")
			flag.Usage()
			os.Exit(1)
		}
		removeDisk(diskManager, *diskPath)
	case "scan":
		scanDisks(diskManager)
	case "status":
		showStatus(diskManager)
	default:
		fmt.Printf("Unknown action: %s\n", *action)
		flag.Usage()
		os.Exit(1)
	}
}

func listDisks(diskManager *storage.DiskManager) {
	fmt.Println("=== Storage Disks ===")
	
	disks, err := diskManager.ListDisks()
	if err != nil {
		log.Fatalf("Failed to list disks: %v", err)
	}

	if len(disks) == 0 {
		fmt.Println("No storage disks registered.")
		fmt.Println("\nTo add your first disk, run:")
		fmt.Println("  go run cmd/disk_manager.go -action add -path /path/to/your/storage")
		return
	}

	fmt.Printf("Found %d registered storage disks:\n\n", len(disks))
	
	for i, disk := range disks {
		status := "inactive"
		if disk.IsActive {
			status = "ACTIVE"
		}
		
		fmt.Printf("%d. Disk ID: %s\n", i+1, disk.ID)
		fmt.Printf("   Path: %s\n", disk.Path)
		fmt.Printf("   Status: %s\n", status)
		fmt.Printf("   Priority: %d\n", disk.PriorityOrder)
		fmt.Printf("   Total Space: %d GB\n", disk.TotalSpaceGB)
		fmt.Printf("   Available Space: %d GB\n", disk.AvailableSpaceGB)
		fmt.Printf("   Last Scan: %v\n", disk.LastScan)
		fmt.Printf("   Created: %v\n", disk.CreatedAt)
		fmt.Println()
	}
}

func addDisk(diskManager *storage.DiskManager, path string, priority int) {
	fmt.Printf("Adding disk: %s (priority: %d)\n", path, priority)
	
	// Check if path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		log.Fatalf("Path does not exist: %s", path)
	}

	err := diskManager.RegisterDisk(path, priority)
	if err != nil {
		log.Fatalf("Failed to register disk: %v", err)
	}

	fmt.Println("✓ Disk registered successfully")
	
	// Run scan to get initial space info
	fmt.Println("Scanning disk space...")
	err = diskManager.ScanAndUpdateDiskSpace()
	if err != nil {
		log.Printf("Warning: Failed to scan disk space: %v", err)
	}

	// Show the added disk
	fmt.Println("\nDisk added:")
	listDisks(diskManager)
}

func removeDisk(diskManager *storage.DiskManager, path string) {
	fmt.Printf("Removing disk: %s\n", path)
	
	// This is a placeholder - you'd need to implement removal in the database interface
	fmt.Println("Note: Disk removal not yet implemented in database interface")
	fmt.Println("For now, you can manually remove from the storage_disks table")
}

func scanDisks(diskManager *storage.DiskManager) {
	fmt.Println("Scanning all disks...")
	
	err := diskManager.ScanAndUpdateDiskSpace()
	if err != nil {
		log.Fatalf("Failed to scan disks: %v", err)
	}

	fmt.Println("✓ Disk scan completed")
	
	// Select active disk
	err = diskManager.SelectActiveDisk()
	if err != nil {
		log.Printf("Warning: Failed to select active disk: %v", err)
	} else {
		activePath, _ := diskManager.GetActiveDiskPath()
		fmt.Printf("✓ Active disk: %s\n", activePath)
	}

	// Show updated status
	showStatus(diskManager)
}

func showStatus(diskManager *storage.DiskManager) {
	fmt.Println("=== Disk Status ===")
	
	// Get usage statistics
	stats, err := diskManager.GetDiskUsageStats()
	if err != nil {
		log.Fatalf("Failed to get disk stats: %v", err)
	}

	fmt.Printf("Total disks: %v\n", stats["total_disks"])
	fmt.Printf("Active disks: %v\n", stats["active_disks"])
	fmt.Printf("Total space: %d GB\n", stats["total_space_gb"])
	fmt.Printf("Available space: %d GB\n", stats["available_space_gb"])
	fmt.Printf("Used space: %d GB\n", stats["used_space_gb"])
	fmt.Printf("Overall usage: %.1f%%\n", stats["overall_usage_percent"])

	// Check disk health
	fmt.Println("\n=== Health Check ===")
	err = diskManager.CheckDiskHealth()
	if err != nil {
		fmt.Printf("⚠️  Health issues: %v\n", err)
	} else {
		fmt.Println("✓ All disks are healthy")
	}

	// Show active disk
	activePath, err := diskManager.GetActiveDiskPath()
	if err != nil {
		fmt.Printf("⚠️  No active disk: %v\n", err)
	} else {
		fmt.Printf("✓ Active disk: %s\n", activePath)
	}
}