package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"ayo-mwr/config"
	"ayo-mwr/service"
)

func main() {
	// Get the project root directory
	projDir, err := filepath.Abs(filepath.Join(".."))
	if err != nil {
		log.Fatalf("Failed to get project directory: %v", err)
	}

	// Define input and output paths
	inputFileName := "BK-0003-250106-0000001_camera_1_20250505085752.mp4"
	inputPath := filepath.Join(projDir, "videos", "tmp", "watermark", inputFileName)
	outputPath := filepath.Join(projDir, "videos", "tmp", "preview", "test_interval_preview_"+inputFileName)

	// Ensure the output directory exists
	outputDir := filepath.Join(projDir, "videos", "tmp", "preview")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Create a minimal configuration
	cfg := config.Config{
		StoragePath: "./videos",
	}

	// Create a service instance
	bookingService := service.NewBookingVideoService(nil, nil, nil, cfg)

	// Verify input file exists
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		log.Fatalf("Input file not found: %s", inputPath)
	}
	fmt.Printf("Found input file: %s\n", inputPath)

	// Generate the preview
	fmt.Println("Generating preview video...")
	if err := bookingService.CreateVideoPreview(inputPath, outputPath); err != nil {
		log.Fatalf("Failed to create preview: %v", err)
	}

	fmt.Printf("Preview successfully generated at: %s\n", outputPath)
}
