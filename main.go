package main

import (
	"flag"
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
	configFile := flag.String("config", "", "Path to config file (optional)")
	envFile := flag.String("env", ".env", "Path to .env file")
	flag.Parse()

	// Load environment variables
	if err := godotenv.Load(*envFile); err != nil {
		log.Printf("Warning: .env file not found at %s, using environment variables", *envFile)
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

	// Start resource monitoring (every 30 seconds)
	monitoring.StartMonitoring(30 * time.Second)

	// Start camera status cron job (every 5 minutes)
	cron.StartCameraStatusCron(&cfg)

	// Start booking video processing cron job (every 30 minutes)
	cron.StartBookingVideoCron(&cfg)

	// Start video request processing cron job (every 30 minutes)
	cron.StartVideoRequestCron(&cfg)

	// Start config update cron job (every 24 hours)
	cron.StartConfigUpdateCron(&cfg)

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
	apiServer := api.NewServer(&cfg, db, r2Storage, uploadService)
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

	// Start capturing from all cameras
	if err := recording.CaptureMultipleRTSPStreams(&cfg); err != nil {
		log.Fatalf("Error capturing RTSP streams: %v", err)
	}
}
