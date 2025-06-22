package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
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
	// Start config update cron job (every 24 hours)
	cron.StartConfigUpdateCron(&cfg)
	// delay 15 seconds before first run
	time.Sleep(15 * time.Second)

	// Start resource monitoring (every 30 seconds)
	monitoring.StartMonitoring(30 * time.Second)

	// Start camera status cron job (every 5 minutes)
	cron.StartCameraStatusCron(&cfg)

	// Start booking video processing cron job (every 30 minutes)
	cron.StartBookingVideoCron(&cfg)

	// Start video request processing cron job (every 30 minutes)
	cron.StartVideoRequestCron(&cfg)

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
	// Initialize AyoIndo API client for video cleanup
	apiClient, apiErr := api.NewAyoIndoClient()
	if apiErr != nil {
		log.Printf("Warning: Failed to initialize AyoIndo API client: %v", apiErr)
	} else {
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
	r2Storage, err := storage.NewR2Storage(r2Config)
	if err != nil {
		log.Printf("Warning: Failed to initialize R2 storage: %v", err)
	}

	// Initialize upload service
	uploadService := service.NewUploadService(db, r2Storage, &cfg)

	// Initialize and start API server
	apiServer := api.NewServer(&cfg, db, r2Storage, uploadService, embeddedDashboardFS)
	go apiServer.Start()

	// Initialize Arduino signal handler via signaling package
	if _, err := signaling.InitArduino(&cfg); err != nil {
		log.Printf("Warning: Arduino initialization failed: %v", err)
	}

	fmt.Println("[MAIN] Arduino setup complete, starting RTSP stream recording")
	log.Println("Starting 24/7 RTSP stream recording")

	// Start capturing from all cameras
	fmt.Println("[MAIN] Starting camera workers")
	recording.StartAllCameras(&cfg)
	// Keep main alive
	select {}

}
