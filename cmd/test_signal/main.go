package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"ayo-mwr/config"
	"ayo-mwr/signaling"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found, using environment variables")
	}

	// Load configuration
	cfg := config.LoadConfig()

	// Initialize Arduino signal handler with a simple callback
	signalCallback := func(signal string) error {
		log.Printf("Received signal from Arduino: %s", signal)
		return nil
	}

	// Create and connect to Arduino using config values
	arduino, err := signaling.NewArduinoSignal(cfg.ArduinoCOMPort, cfg.ArduinoBaudRate, signalCallback)
	if err != nil {
		log.Fatalf("Failed to initialize Arduino signal handler: %v", err)
	}
	defer arduino.Close()

	if err := arduino.Connect(); err != nil {
		log.Fatalf("Failed to connect to Arduino: %v", err)
	}

	log.Printf("Connected to Arduino on %s at %d baud. Waiting for signals...", 
		cfg.ArduinoCOMPort, cfg.ArduinoBaudRate)

	// Wait for interrupt signal to gracefully shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
}
