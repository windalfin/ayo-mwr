package main

import (
	"flag"
	"log"

	"github.com/joho/godotenv"

	// Update these imports to match your local module path
	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/recording"
)

func main() {
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

	// TODO
	// This is commented as we will not be doing automatic upload to R2 Server.
	// This piece of code will either be modified or deleted in another Ticket

	// // Initialize R2 storage
	// r2Config := storage.R2Config{
	// 	AccessKey: cfg.R2AccessKey,
	// 	SecretKey: cfg.R2SecretKey,
	// 	AccountID: cfg.R2AccountID,
	// 	Bucket:    cfg.R2Bucket,
	// 	Endpoint:  cfg.R2Endpoint,
	// 	Region:    cfg.R2Region,
	// }
	// r2Storage, err := storage.NewR2Storage(r2Config)
	// if err != nil {
	// 	log.Fatalf("Failed to initialize R2 storage: %v", err)
	// }

	// // Initialize upload service with all required dependencies
	// uploadService := service.NewUploadService(db, r2Storage, cfg)

	log.Println("Starting 24/7 RTSP stream recording")

	// Start capturing from all cameras
	if err := recording.CaptureMultipleRTSPStreams(cfg); err != nil {
		log.Fatalf("Error capturing RTSP streams: %v", err)
	}
}
