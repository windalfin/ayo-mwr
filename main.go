package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	// Update these imports to match your local module path
	"ayo-mwr/api"
	"ayo-mwr/config"
	"ayo-mwr/cron"
	"ayo-mwr/database"
	"ayo-mwr/monitoring"
	"ayo-mwr/recording"
	"ayo-mwr/service"
	"ayo-mwr/signaling"
	"ayo-mwr/storage"
	"context"
)

//go:embed .env
var embeddedEnv embed.FS

//go:embed dashboard
var embeddedDashboardFS embed.FS

func main() {
	// Add execution tracking logs
	fmt.Println("[MAIN] Starting application...")

	logFile, err := os.OpenFile("server.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	} else {
		log.Println("Failed to log to file, using default stderr")
	}

	// Print directly to console for debugging
	fmt.Println("[MAIN] Log file setup complete")

	// Parse command line arguments
	configFile := flag.String("config", "", "Path to config file (optional)")
	envFile := flag.String("env", ".env", "Path to .env file")
	flag.Parse()

	// First try to load from embedded .env file
	if envData, err := embeddedEnv.ReadFile(".env"); err == nil {
		log.Println("Loading environment variables from embedded .env file")
		envMap, err := godotenv.Unmarshal(string(envData))
		if err != nil {
			log.Printf("Error parsing embedded .env file: %v", err)
		} else {
			// Set environment variables from the embedded file
			for k, v := range envMap {
				if os.Getenv(k) == "" { // Don't override existing env vars
					os.Setenv(k, v)
				}
			}
		}
	} else {
		// Fall back to loading from filesystem
		log.Printf("Embedded .env not found, trying filesystem at %s", *envFile)
		if err := godotenv.Load(*envFile); err != nil {
			log.Printf("Warning: .env file not found at %s, using environment variables", *envFile)
		}
	}

	// Load configuration
	var cfg config.Config

	// If config file is specified, load from file
	if *configFile != "" {
		var err error
		cfg, err = config.LoadConfigFromFile(*configFile)
		if err != nil {
			log.Printf("Error loading config from file: %v, falling back to environment variables", err)
			cfg = config.LoadConfig()
		}
	} else {
		// Otherwise load from environment variables
		cfg = config.LoadConfig()
	}

	// Ensure required paths exist
	config.EnsurePaths(cfg)

	// Initialize SQLite database
	db, err := database.NewSQLiteDB(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// 🧹 STARTUP CLEANUP: Clean up stuck videos from previous runs (SYNCHRONOUS)
	// This MUST complete before starting any other services to ensure clean state
	log.Println("🧹 STARTUP CLEANUP: Starting synchronous cleanup process...")

	// Wait for stuck videos and request_ids cleanup to complete
	if err := db.CleanupStuckVideosOnStartup(); err != nil {
		log.Printf("❌ STARTUP CLEANUP: Error during cleanup: %v", err)
		// Continue anyway, don't fail startup
	}

	log.Println("✅ STARTUP CLEANUP: Cleanup completed successfully - system is ready to start services!")

	// Only proceed with other initializations AFTER cleanup is done
	log.Println("🚀 SYSTEM: Starting other services after cleanup completion...")

	// Initialize disk management system
	diskManager := storage.NewDiskManager(db)

	// Setup initial disk - register current storage path as first disk
	// Note: cfg.StoragePath will be updated from diskManager.GetActiveDiskPath() inside setupInitialDisk
	err = setupInitialDisk(diskManager, &cfg)
	if err != nil {
		log.Printf("Warning: Failed to setup initial disk: %v", err)
	}
	
	// Normalize disk paths to ensure all paths are absolute (safe migration)
	log.Printf("🔧 STARTUP: Normalizing disk paths to absolute paths...")
	if err := diskManager.NormalizeDiskPaths(); err != nil {
		log.Printf("Warning: Failed to normalize disk paths: %v", err)
	}
	// Start config update cron job (every 24 hours)
	cron.StartConfigUpdateCron(&cfg, db)
	// delay 15 seconds before first run
	time.Sleep(15 * time.Second)

	// Start resource monitoring (every 30 seconds)
	monitoring.StartMonitoring(30 * time.Second)

	// Start camera status cron job (every 5 minutes)
	cron.StartCameraStatusCron(&cfg)

	// Start booking sync cron job (every 5 minutes) - Sync booking data from API to database
	cron.StartBookingSyncCron(&cfg)

	// Start booking video processing cron job (every 2 minutes) - Process videos from database
	cron.StartBookingVideoCron(&cfg)

	// Start video request processing cron job (every 30 minutes)
	cron.StartVideoRequestCron(&cfg)

	// // Start disk management cron job (nightly at 2 AM)
	// // Commented disk management Cron Job since it will be handled by auto-restart service
	// diskCron := cron.NewDiskManagementCron(db, diskManager, &cfg)
	// diskCron.Start()
	// log.Println("Started disk management cron job")

	// Start HLS cleanup cron job (nightly at 3 AM)
	hlsCron := cron.NewHLSCleanupCron(db)
	hlsCron.Start()
	log.Println("Started HLS cleanup cron job")

	// Start chunk processing cron job (every 15 minutes)
	chunkCron := cron.NewChunkProcessingCron(db, diskManager)
	if err := chunkCron.Start(); err != nil {
		log.Printf("Warning: Failed to start chunk processing cron: %v", err)
	} else {
		log.Println("Started chunk processing cron job (15-minute intervals)")
	}

	// Start health check cron job (every minute)
	healthCheckCron, err := cron.NewHealthCheckCron()
	if err != nil {
		log.Printf("Warning: Failed to initialize health check cron: %v", err)
	} else {
		go func() {
			ctx := context.Background()
			if err := healthCheckCron.Start(ctx); err != nil {
				log.Printf("Error running health check cron: %v", err)
			}
		}()
	}
	// ---- Arduino configuration load ----
	// Try to load port/baud from database; fall back to env and persist if missing
	port, baud, err := db.GetArduinoConfig()
	if err == nil {
		cfg.ArduinoCOMPort = port
		cfg.ArduinoBaudRate = baud
		log.Printf("[Arduino] Loaded config from DB: port=%s baud=%d", port, baud)
	} else {
		// Likely sql.ErrNoRows
		log.Printf("[Arduino] No config in DB, using env values and persisting: port=%s baud=%d", cfg.ArduinoCOMPort, cfg.ArduinoBaudRate)
		if upErr := db.UpsertArduinoConfig(cfg.ArduinoCOMPort, cfg.ArduinoBaudRate); upErr != nil {
			log.Printf("[Arduino] Failed to persist initial config: %v", upErr)
		}
	}

	// Initialize Arduino using the (possibly updated) cfg
	if _, err := signaling.InitArduino(&cfg); err != nil {
		log.Printf("Warning: Arduino initialization failed: %v", err)
	}

	// Initialize AyoIndo API client for video cleanup
	apiClient, apiErr := api.NewAyoIndoClient()
	if apiErr != nil {
		log.Printf("Warning: Failed to initialize AyoIndo API client: %v", apiErr)
		apiClient = nil // Explicitly set to nil for clarity
	} else {
		// Set AYO client for chunk processor watermarking
		chunkCron.SetAyoClient(apiClient)
		log.Println("Set AYO client for chunk processor watermarking")
		// Start video cleanup cron job (every 24 hours)
		// delay 10 seconds before first run
		// time.Sleep(15 * time.Second)
		log.Println("Starting video cleanup job...")
		// Use the underlying *sql.DB for the scheduled job
		cron.StartVideoCleanupJob(db.GetDB(), apiClient, cfg.AutoDelete, cfg.VenueCode)

		// For testing: Immediately run the video cleanup function once
		log.Println("Running immediate test of video cleanup function...")
		go cron.CleanupExpiredVideosWithSQLiteDB(db, apiClient, cfg.AutoDelete, cfg.VenueCode)
	}

	// Initialize R2 storage with config
	r2Config := storage.R2Config{
		AccessKey: cfg.R2AccessKey,
		SecretKey: cfg.R2SecretKey,
		AccountID: cfg.R2AccountID,
		Bucket:    cfg.R2Bucket,
		Region:    cfg.R2Region,
		Endpoint:  cfg.R2Endpoint,
		BaseURL:   cfg.R2BaseURL, // Menggunakan R2_BASE_URL dari environment
	}
	r2Storage, err := storage.NewR2StorageWithConcurrency(r2Config, cfg.UploadWorkerConcurrency)
	if err != nil {
		log.Printf("Warning: Failed to initialize R2 storage: %v", err)
	}

	// Initialize upload service with AYO API client
	uploadService := service.NewUploadService(db, r2Storage, &cfg, apiClient)

	// Initialize and start API server with chunk optimization
	apiServer := api.NewServer(&cfg, db, r2Storage, uploadService, embeddedDashboardFS, diskManager)
	go apiServer.Start()

	// Initialize Arduino signal handler via signaling package
	if _, err := signaling.InitArduino(&cfg); err != nil {
		log.Printf("Warning: Arduino initialization failed: %v", err)
	}

	fmt.Println("[MAIN] Arduino setup complete, starting RTSP stream recording")
	log.Println("Starting 24/7 RTSP stream recording")

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	var wg sync.WaitGroup

	// Initialize recording manager with disk change capabilities
	fmt.Println("[MAIN] Starting recording manager")
	recordingManager := recording.NewRecordingManager(&cfg, db, diskManager)
	
	// Register recording manager with disk manager for restart notifications
	diskManager.SetRecordingManager(recordingManager)
	
	// Start all cameras with recording manager
	if err := recordingManager.StartAllCameras(); err != nil {
		log.Printf("Failed to start cameras with recording manager: %v", err)
		log.Println("Falling back to direct camera start...")
		
		// Fallback to original method if recording manager fails
		wg.Add(1)
		go func() {
			defer wg.Done()
			recording.StartAllCamerasWithContext(ctx, &cfg)
		}()
	} else {
		log.Printf("✅ All cameras started successfully with recording manager")
	}

	// Wait for shutdown signal
	go func() {
		sig := <-sigChan
		fmt.Printf("[MAIN] Received signal: %v. Starting graceful shutdown...\n", sig)
		log.Printf("Received signal: %v. Starting graceful shutdown...", sig)

		// Stop recording manager first
		if recordingManager != nil {
			fmt.Println("[MAIN] Stopping recording manager...")
			recordingManager.StopAllCameras()
		}

		// Cancel context to signal all goroutines to stop
		cancel()

		// Give services time to shutdown gracefully
		shutdownTimer := time.NewTimer(30 * time.Second)
		shutdownComplete := make(chan struct{})

		go func() {
			wg.Wait()
			close(shutdownComplete)
		}()

		select {
		case <-shutdownComplete:
			fmt.Println("[MAIN] Graceful shutdown completed successfully")
			log.Println("Graceful shutdown completed successfully")
		case <-shutdownTimer.C:
			fmt.Println("[MAIN] Shutdown timeout reached, forcing exit")
			log.Println("Shutdown timeout reached, forcing exit")
		}

		os.Exit(0)
	}()

	// Keep main alive until shutdown signal
	select {}

}

// setupInitialDisk registers the current storage path as the first disk if no disks exist
func setupInitialDisk(diskManager *storage.DiskManager, cfg *config.Config) error {
	// Check if any disks are already registered
	disks, err := diskManager.ListDisks()
	if err != nil {
		return fmt.Errorf("failed to list existing disks: %v", err)
	}

	if len(disks) > 0 {
		log.Printf("Found %d existing storage disks, skipping initial setup", len(disks))

		// First discover any new disks
		diskManager.DiscoverAndRegisterDisks()

		// Run a manual scan to update disk space
		err = diskManager.ScanAndUpdateDiskSpace()
		if err != nil {
			log.Printf("Warning: Failed to scan existing disks: %v", err)
		}

		// Ensure we have an active disk
		err = diskManager.SelectActiveDisk()
		if err != nil {
			log.Printf("Warning: Failed to select active disk: %v", err)
		}

		// After active disk is determined, update cfg.StoragePath
		if activePath, apErr := diskManager.GetActiveDiskPath(); apErr == nil {
			log.Printf("[MAIN] Using active disk path for recordings: %s", activePath)
			cfg.StoragePath = activePath
			// Update environment variable so all functions use the active disk
			os.Setenv("STORAGE_PATH", activePath)
		} else {
			log.Printf("Warning: Unable to resolve active disk path: %v", apErr)
		}

		return nil
	}

	// No disks exist, register the current storage path as the first disk
	log.Printf("No storage disks found, registering current storage path as first disk: %s", cfg.StoragePath)

	err = diskManager.RegisterDisk(cfg.StoragePath, 1)
	if err != nil {
		return fmt.Errorf("failed to register initial disk: %v", err)
	}

	// Discover any disks before initial scan
	diskManager.DiscoverAndRegisterDisks()

	// Run initial scan and selection
	err = diskManager.ScanAndUpdateDiskSpace()
	if err != nil {
		log.Printf("Warning: Failed to scan initial disk: %v", err)
	}

	err = diskManager.SelectActiveDisk()
	if err != nil {
		log.Printf("Warning: Failed to select initial disk: %v", err)
	}

	// After initial disk setup, update cfg.StoragePath from active disk
	if activePath, apErr := diskManager.GetActiveDiskPath(); apErr == nil {
		log.Printf("[MAIN] Using active disk path for recordings: %s", activePath)
		cfg.StoragePath = activePath
		// Update environment variable so all functions use the active disk
		os.Setenv("STORAGE_PATH", activePath)
	} else {
		log.Printf("Warning: Unable to resolve active disk path: %v", apErr)
	}

	log.Println("Initial disk setup completed successfully")
	return nil
}
