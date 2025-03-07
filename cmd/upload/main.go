package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	
	// Update these imports to match your local module path
	"ayo-mwr/storage"
)

func main() {
	// Parse command line arguments
	cameraID := flag.String("camera", "", "Camera ID (directory name) to upload")
	srcDir := flag.String("src", "./videos/hls", "Source directory containing HLS files")
	allCameras := flag.Bool("all", false, "Upload all camera directories found in source directory")
	envFile := flag.String("env", ".env", "Path to .env file")
	flag.Parse()

	// Load environment variables
	if err := godotenv.Load(*envFile); err != nil {
		log.Printf("Warning: .env file not found at %s, using environment variables", *envFile)
	}

	// Check required environment variables
	if os.Getenv("R2_ACCESS_KEY") == "" || os.Getenv("R2_SECRET_KEY") == "" {
		log.Fatal("Error: R2 credentials not set in environment variables")
	}

	// Initialize R2 storage
	r2Config := storage.R2Config{
		AccessKey: os.Getenv("R2_ACCESS_KEY"),
		SecretKey: os.Getenv("R2_SECRET_KEY"),
		AccountID: os.Getenv("R2_ACCOUNT_ID"),
		Bucket:    os.Getenv("R2_BUCKET"),
		Endpoint:  os.Getenv("R2_ENDPOINT"),
		Region:    os.Getenv("R2_REGION"),
	}

	log.Printf("Initializing R2 storage with account ID: %s, bucket: %s", 
		r2Config.AccountID, r2Config.Bucket)

	r2Storage, err := storage.NewR2Storage(r2Config)
	if err != nil {
		log.Fatalf("Failed to initialize R2 storage: %v", err)
	}

	// Check if we should upload all cameras or just one
	if *allCameras {
		uploadAllCameras(r2Storage, *srcDir)
	} else if *cameraID != "" {
		uploadSingleCamera(r2Storage, *cameraID, *srcDir)
	} else {
		log.Fatal("Either provide a camera ID with -camera or use -all to upload all cameras")
	}
}

func uploadSingleCamera(r2Storage *storage.R2Storage, cameraID, srcDir string) {
	cameraPath := filepath.Join(srcDir, cameraID)

	// Check if directory exists
	if _, err := os.Stat(cameraPath); os.IsNotExist(err) {
		log.Fatalf("Camera directory does not exist: %s", cameraPath)
	}

	log.Printf("Uploading HLS stream for camera %s from %s", cameraID, cameraPath)

	// Verify the directory contains expected files
	verifyHLSDirectory(cameraPath)

	// Upload the HLS stream
	hlsURL, err := r2Storage.UploadHLSStream(cameraPath, cameraID)
	if err != nil {
		log.Fatalf("Failed to upload HLS stream: %v", err)
	}

	log.Printf("Successfully uploaded HLS stream for %s", cameraID)
	log.Printf("HLS URL: %s", hlsURL)
}

func uploadAllCameras(r2Storage *storage.R2Storage, srcDir string) {
	// List all camera directories in the HLS directory
	cameraDirs, err := os.ReadDir(srcDir)
	if err != nil {
		log.Fatalf("Failed to read HLS directory: %v", err)
	}

	// Track success/failure counts
	successCount := 0
	failureCount := 0

	// Process each camera directory
	for _, cameraDir := range cameraDirs {
		if !cameraDir.IsDir() {
			continue // Skip if not a directory
		}

		cameraID := cameraDir.Name()
		cameraPath := filepath.Join(srcDir, cameraID)

		log.Printf("Processing camera directory: %s", cameraID)

		// Verify the directory contains expected files
		if !isValidHLSDirectory(cameraPath) {
			log.Printf("Skipping %s: Not a valid HLS directory", cameraID)
			continue
		}

		// Upload this camera's HLS stream
		hlsURL, err := r2Storage.UploadHLSStream(cameraPath, cameraID)
		if err != nil {
			log.Printf("Failed to upload HLS stream for %s: %v", cameraID, err)
			failureCount++
			continue
		}

		log.Printf("Successfully uploaded HLS stream for %s", cameraID)
		log.Printf("HLS URL: %s", hlsURL)
		successCount++
	}

	log.Printf("Upload complete. Successfully uploaded %d streams, failed to upload %d streams", 
		successCount, failureCount)
}

func verifyHLSDirectory(dirPath string) {
	// Check for playlist.m3u8
	playlistPath := filepath.Join(dirPath, "playlist.m3u8")
	if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
		log.Fatalf("Missing playlist.m3u8 in %s", dirPath)
	}

	// Check for at least one segment file
	files, err := os.ReadDir(dirPath)
	if err != nil {
		log.Fatalf("Failed to read directory %s: %v", dirPath, err)
	}

	hasSegments := false
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if filepath.Ext(file.Name()) == ".ts" {
			hasSegments = true
			break
		}
	}

	if !hasSegments {
		log.Fatalf("No .ts segment files found in %s", dirPath)
	}

	log.Printf("Directory %s contains valid HLS content", dirPath)
}

func isValidHLSDirectory(dirPath string) bool {
	// Check for playlist.m3u8
	playlistPath := filepath.Join(dirPath, "playlist.m3u8")
	if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
		return false
	}

	// Check for at least one segment file
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}

	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".ts" {
			return true
		}
	}

	return false
}