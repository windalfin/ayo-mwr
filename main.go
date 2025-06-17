package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

	// Initialize Arduino signal handler
	fmt.Println("[MAIN] About to initialize Arduino signal handler")
	fmt.Printf("[MAIN] Arduino config: port=%s, baud rate=%d\n", cfg.ArduinoCOMPort, cfg.ArduinoBaudRate)
	log.Printf("Starting Arduino signal handler initialization with port: %s, baud rate: %d", cfg.ArduinoCOMPort, cfg.ArduinoBaudRate)

	signalCallback := func(signal string) error {
		log.Printf("Received signal from Arduino: %s", signal)
		// Ignore semicolons as separate signals
		if signal == ";" || strings.TrimSpace(signal) == ";" {
			log.Printf("Ignoring semicolon as a separate signal")
			return nil
		}

		// Ignore carriage return and newline characters
		if strings.TrimSpace(signal) == "" || signal == "\r" || signal == "\n" || signal == "\r\n" {
			log.Printf("Ignoring whitespace/control characters")
			return nil
		}

		// Trim the trailing semicolon from the signal to get the button number
		buttonNo := strings.TrimSuffix(strings.TrimSpace(signal), ";")

		// Map button number to field ID using camera configuration
		fieldID := buttonNo // Default to using button number as field ID
		
		if cfg.CameraByButtonNo != nil {
			if camera, exists := cfg.CameraByButtonNo[buttonNo]; exists && camera.Field != "" {
				fieldID = camera.Field
				log.Printf("Mapped button %s to field ID %s", buttonNo, fieldID)
				fmt.Printf("[ARDUINO] Mapped button %s to field ID %s\n", buttonNo, fieldID)
			} else {
				log.Printf("Warning: No mapping found for button %s, using button number as field ID", buttonNo)
				fmt.Printf("[ARDUINO] Warning: No mapping found for button %s, using button number as field ID\n", buttonNo)
			}
		} else {
			log.Printf("Warning: Camera configuration not available, using button number as field ID")
			fmt.Printf("[ARDUINO] Warning: Camera configuration not available, using button number as field ID\n", buttonNo)
		}
		
		// Call the API using the utility function
		err := signaling.CallProcessBookingVideoAPI(fieldID)
		if err != nil {
			log.Printf("Error calling Process Booking Video API: %v", err)
			fmt.Printf("[ARDUINO] Error calling Process Booking Video API: %v\n", err)
			return fmt.Errorf("failed to process booking video API call: %w", err)
		}

		return nil
	}

	// Check if the Arduino port exists before trying to connect
	fmt.Printf("[MAIN] Checking if Arduino port %s exists...\n", cfg.ArduinoCOMPort)
	if _, err := os.Stat(cfg.ArduinoCOMPort); os.IsNotExist(err) {
		fmt.Printf("[MAIN] ERROR: Arduino port %s does not exist!\n", cfg.ArduinoCOMPort)
		// List available ports for debugging
		fmt.Println("[MAIN] Available serial ports:")
		ports, _ := filepath.Glob("/dev/tty.*")
		for _, port := range ports {
			fmt.Printf("[MAIN] - %s\n", port)
		}
	} else {
		fmt.Printf("[MAIN] Arduino port %s exists, proceeding with connection\n", cfg.ArduinoCOMPort)
	}

	arduino, err := signaling.NewArduinoSignal(cfg.ArduinoCOMPort, cfg.ArduinoBaudRate, signalCallback, &cfg)
	if err != nil {
		fmt.Printf("[MAIN] ERROR: Failed to initialize Arduino signal handler: %v\n", err)
		log.Printf("ERROR: Failed to initialize Arduino signal handler: %v", err)
	} else {
		fmt.Println("[MAIN] Arduino signal handler initialized successfully, attempting to connect...")
		log.Printf("Arduino signal handler initialized successfully, attempting to connect...")

		connectErr := arduino.Connect()
		if connectErr != nil {
			fmt.Printf("[MAIN] ERROR: Failed to connect to Arduino: %v\n", connectErr)
			log.Printf("ERROR: Failed to connect to Arduino: %v", connectErr)
		} else {
			fmt.Printf("[MAIN] Arduino connected successfully and listening for signals on port: %s\n", cfg.ArduinoCOMPort)
			log.Printf("Arduino connected successfully and listening for signals on port: %s", cfg.ArduinoCOMPort)
		}
		defer arduino.Close()
	}

	fmt.Println("[MAIN] Arduino setup complete, starting RTSP stream recording")
	log.Println("Starting 24/7 RTSP stream recording")

	// Start capturing from all cameras
	fmt.Println("[MAIN] Starting camera capture")
	if err := recording.CaptureMultipleRTSPStreams(&cfg); err != nil {
		log.Fatalf("Error capturing RTSP streams: %v", err)
	}

	fmt.Println("[MAIN] Application running. Press Ctrl+C to exit.")
}
