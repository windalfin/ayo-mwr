package main

import (
	"flag"
	"log"
	"time"

	"github.com/joho/godotenv"

	// Update these imports to match your local module path
	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/monitoring"
	"ayo-mwr/recording"
	"ayo-mwr/signaling"
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

	// Start resource monitoring (every 30 seconds)
	monitoring.StartMonitoring(30 * time.Second)

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
	if err := recording.CaptureMultipleRTSPStreams(cfg); err != nil {
		log.Fatalf("Error capturing RTSP streams: %v", err)
	}
}
