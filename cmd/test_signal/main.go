package main

import (
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"ayo-mwr/config"
	"ayo-mwr/signaling"
	"ayo-mwr/transcode"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found, using environment variables")
	}

	// Load configuration
	cfg := config.LoadConfig()

	// Create a channel for signal handling
	signalChan := make(chan string)

	// Create and start Arduino signal handler (optional)
	arduinoSignal, err := signaling.NewArduinoSignal(cfg.ArduinoCOMPort, cfg.ArduinoBaudRate, func(signal string) error {
		signalChan <- signal
		return nil
	})
	if err != nil {
		log.Printf("Warning: Failed to create Arduino signal handler: %v", err)
	} else {
		if err := arduinoSignal.Connect(); err != nil {
			log.Printf("Warning: Failed to connect to Arduino: %v", err)
		} else {
			defer arduinoSignal.Close()
			log.Printf("Connected to Arduino on %s at %d baud", cfg.ArduinoCOMPort, cfg.ArduinoBaudRate)
		}
	}

	// Create and start HTTP signal handler
	httpSignal, err := signaling.NewHTTPSignal(8085, func(signal string) error {
		signalChan <- signal
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to create HTTP signal handler: %v", err)
	}
	if err := httpSignal.Start(); err != nil {
		log.Fatalf("Failed to start HTTP signal handler: %v", err)
	}
	defer httpSignal.Stop()

	log.Printf("Signal handlers started. Listening for signals...")
	log.Printf("HTTP Server running on port 8085")

	for signal := range signalChan {
		log.Printf("Received signal: %s", signal)

		// Check if it's an Arduino signal (time format) or HTTP signal (camera:timestamp:format)
		parts := strings.Split(signal, ":")
		if len(parts) == 3 {
			// HTTP signal: camera:timestamp:format
			cameraName := parts[0]
			timestamp, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				log.Printf("Error parsing timestamp: %v", err)
				continue
			}
			format := parts[2]

			if err := handleTranscodeSignal(cameraName, timestamp, format, cfg); err != nil {
				log.Printf("Error handling transcode signal: %v", err)
			}
		} else if strings.HasPrefix(signal, "Current time: ") {
			// Arduino signal
			log.Printf("Arduino time signal received: %s", signal)
		} else {
			log.Printf("Error handling signal: invalid signal format: %s", signal)
		}
	}
}

type req struct {
	CameraName string
	Timestamp  time.Time
}

func handleTranscodeSignal(cameraName string, timestamp int64, format string, cfg config.Config) error {
	// Parse signal format: camera_name:timestamp:format
	log.Printf("Received transcode signal for camera %s at timestamp %v in format %s", cameraName, timestamp, format)

	// Convert timestamp to time.Time
	t := time.Unix(timestamp, 0)

	// Find the closest video file to the requested timestamp
	closestFile, err := signaling.FindClosestVideo(cfg.StoragePath, cameraName, t)
	if err != nil {
		return fmt.Errorf("failed to find video: %v", err)
	}

	// Generate a unique ID for the transcoded video
	videoID := filepath.Base(closestFile[:len(closestFile)-4]) // Remove .mp4

	log.Printf("Found video %s, transcoding to %s format", videoID, format)
	log.Printf("Input path: %s", closestFile)
	log.Printf("Storage path: %s", cfg.StoragePath)

	// Transcode the video
	_, timings, err := transcode.TranscodeVideo(closestFile, videoID, "hls", cfg)
	if err != nil {
		log.Printf("Transcoding error: %v", err)
		return fmt.Errorf("failed to transcode video: %v", err)
	}

	for task, duration := range timings {
		log.Printf("Transcoding task %s took %.2f seconds", task, duration)
	}

	return nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
