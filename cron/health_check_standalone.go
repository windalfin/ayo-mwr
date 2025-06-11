// +build ignore

package main

import (
	"ayo-mwr/cron"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
)

// This is a standalone executable for running just the health check cron job
// To run: go run cron/health_check_standalone.go
func main() {
	// Set up logging
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	
	// Load environment variables
	if err := godotenv.Load(".env"); err != nil {
		log.Printf("Warning: .env file not found, using environment variables")
	}

	log.Println("Starting standalone health check cron job")

	// Create health check cron
	healthCheckCron, err := cron.NewHealthCheckCron()
	if err != nil {
		log.Fatalf("Failed to create health check cron: %v", err)
	}

	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal catching for graceful shutdown
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	
	// Start health check cron in a goroutine
	go func() {
		if err := healthCheckCron.Start(ctx); err != nil {
			log.Printf("Health check cron stopped with error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sig := <-signalChannel
	log.Printf("Received signal: %v, shutting down gracefully", sig)
	
	// Cancel context to stop the cron job
	cancel()
	
	log.Println("Health check cron job stopped")
}
