package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	"ayo-mwr/transcode"
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

	// --- RTSP Camera HLS Streaming Startup ---
	for _, cam := range cfg.Cameras {
		if !cam.Enabled {
			continue
		}
		// Construct RTSP URL
		rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s", cam.Username, cam.Password, cam.IP, cam.Port, cam.Path)
		// Output dir: ./videos/recordings/<camera_name>/hls
		outDir := filepath.Join("videos", "recordings", cam.Name, "hls")
		_, err := transcode.StartCameraHLS(rtspURL, outDir)
		if err != nil {
			log.Printf("Failed to start HLS for %s: %v", cam.Name, err)
		} else {
			log.Printf("Started HLS for %s at /hls/%s/hls/stream.m3u8", cam.Name, cam.Name)
		}
	}
	// Serve HLS directory at /hls/
	go func() {
		if err := transcode.ServeHLSDir(filepath.Join("videos", "recordings"), ":8080"); err != nil {
			log.Fatalf("Failed to serve HLS: %v", err)
		}
	}()

	// Initialize SQLite database
	db, err := database.NewSQLiteDB(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Start resource monitoring (every 30 seconds)
	monitoring.StartMonitoring(30 * time.Second)

	// Start camera status cron job (every 5 minutes)
	cron.StartCameraStatusCron(cfg)

	// Initialize R2 storage with config
	r2Config := storage.R2Config{
		AccessKey: cfg.R2AccessKey,
		SecretKey: cfg.R2SecretKey,
		AccountID: cfg.R2AccountID,
		Bucket:    cfg.R2Bucket,
		Region:    cfg.R2Region,
		Endpoint:  cfg.R2Endpoint,
	}
	r2Storage, err := storage.NewR2Storage(r2Config)
	if err != nil {
		log.Printf("Warning: Failed to initialize R2 storage: %v", err)
	}

	// Initialize upload service
	uploadService := service.NewUploadService(db, r2Storage, cfg)

	// Initialize and start API server
	apiServer := api.NewServer(cfg, db, r2Storage, uploadService)
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
	if err := recording.CaptureMultipleRTSPStreams(cfg); err != nil {
		log.Fatalf("Error capturing RTSP streams: %v", err)
	}
}
