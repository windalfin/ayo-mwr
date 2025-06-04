package main

import (
	"encoding/json"
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
)

func main() {
	logFile, err := os.OpenFile("server.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	} else {
		log.Println("Failed to log to file, using default stderr")
	}

	// Parse command line arguments
	envFile := flag.String("env", ".env", "Path to .env file")
	flag.Parse()

	// Load environment variables
	if err := godotenv.Load(*envFile); err != nil {
		log.Printf("Warning: .env file not found at %s, using environment variables", *envFile)
	}

	// Load configuration
	var cfg config.Config

	// Load configuration from environment variables first
	cfg = config.LoadConfig()

	// Ensure required paths exist
	config.EnsurePaths(cfg)

	// Initialize database
	db, err := database.NewSQLiteDB(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize configuration in database
	if err := api.InitConfigFromDatabase(db, cfg); err != nil {
		log.Printf("Warning: Failed to initialize config in database: %v", err)
	}

	// Try to load config from database
	log.Println("Attempting to load configuration from database")
	configData, err := db.GetConfig("app_config")
	if err != nil {
		log.Printf("Error loading config from database: %v", err)
	} else {
		// Config exists in database, use it
		var dbConfig config.Config
		if err := json.Unmarshal([]byte(configData), &dbConfig); err != nil {
			log.Printf("Error unmarshaling config from database: %v", err)
		} else {
			log.Println("Successfully loaded configuration from database")
			// Log some key config values
			log.Printf("Database config - ServerPort: %s, VenueCode: %s", dbConfig.ServerPort, dbConfig.VenueCode)
			
			// Log camera configuration
			if len(dbConfig.Cameras) > 0 {
				log.Printf("Loaded %d cameras from database", len(dbConfig.Cameras))
				for i, cam := range dbConfig.Cameras {
					rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s", 
						cam.Username, cam.Password, cam.IP, cam.Port, cam.Path)
					log.Printf("Camera %d: Name=%s, RTSP URL=%s, Enabled=%v", 
						i, cam.Name, rtspURL, cam.Enabled)
				}
			} else {
				log.Println("No cameras found in database configuration")
			}
			
			cfg = dbConfig
		}
	}

	// Create a config manager with the current configuration
	configManager := config.NewConfigManager(cfg)

	// Start resource monitoring (every 30 seconds)
	monitoring.StartMonitoring(30 * time.Second)

	// Start camera status cron job (every 5 minutes)
	cron.StartCameraStatusCron(configManager)

	// Start booking video processing cron job (every 30 minutes)
	cron.StartBookingVideoCron(configManager)

	// Start video request processing cron job (every 30 minutes)
	cron.StartVideoRequestCron(configManager)

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
	uploadService := service.NewUploadService(db, r2Storage, cfg)

	// Initialize and start API server
	apiServer := api.NewServer(configManager, db, r2Storage, uploadService)
	go apiServer.Start()

	// Initialize Arduino signal handler
	signalCallback := func(signal string) error {
		log.Printf("Received signal from Arduino: %s", signal)
		// TODO: Add your signal handling logic here
		return nil
	}

	arduino, err := signaling.NewArduinoSignal(cfg.ArduinoCOMPort, cfg.ArduinoBaudRate, signalCallback)
	if err != nil {
		log.Printf("Warning: Failed to initialize Arduino signal handler: %v", err)
	} else {
		if err := arduino.Connect(); err != nil {
			log.Printf("Warning: Failed to connect to Arduino: %v", err)
		}
		defer arduino.Close()
	}

	log.Println("Starting 24/7 RTSP stream recording")

	// Start capturing from all cameras using the config manager
	if err := recording.CaptureMultipleRTSPStreams(configManager); err != nil {
		log.Fatalf("Error capturing RTSP streams: %v", err)
	}
}
