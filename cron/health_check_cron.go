package cron

import (
	"ayo-mwr/api"
	"context"
	"log"
	"os"
	"time"

	"github.com/robfig/cron/v3"
)

type HealthCheckCron struct {
	cron        *cron.Cron
	client      *api.AyoIndoClient
	cameraToken string
}

// NewHealthCheckCron creates a new health check cron job
func NewHealthCheckCron() (*HealthCheckCron, error) {
	// Create API client
	client, err := api.NewAyoIndoClient()
	if err != nil {
		return nil, err
	}

	// Get camera token from environment or use default
	cameraToken := os.Getenv("CAMERA_TOKEN")
	if cameraToken == "" {
		cameraToken = "health-check-camera-token"
	}

	// Create cron instance with second precision
	c := cron.New(cron.WithSeconds())

	return &HealthCheckCron{
		cron:        c,
		client:      client,
		cameraToken: cameraToken,
	}, nil
}

// Start begins the health check cron job (every minute)
func (h *HealthCheckCron) Start(ctx context.Context) error {
	log.Println("Starting health check cron job (every minute)")

	// Add cron job to run every minute (0 */1 * * * *)
	_, err := h.cron.AddFunc("0 */1 * * * *", func() {
		h.runHealthCheck()
	})
	if err != nil {
		return err
	}

	// Start the cron scheduler
	h.cron.Start()

	// Run initial health check immediately
	h.runHealthCheck()

	// Wait for context cancellation
	<-ctx.Done()
	
	// Stop the cron job
	h.Stop()
	return nil
}

// Stop stops the health check cron job
func (h *HealthCheckCron) Stop() {
	log.Println("Stopping health check cron job")
	h.cron.Stop()
}

// runHealthCheck executes the health check API call
func (h *HealthCheckCron) runHealthCheck() {
	log.Println("Running health check...")
	
	startTime := time.Now()
	result, err := h.client.HealthCheck()
	duration := time.Since(startTime)
	
	if err != nil {
		log.Printf("Health check failed: %v (took %v)", err, duration)
		return
	}

	// Check for error in response
	if errorValue, ok := result["is_error"]; ok {
		if errorValue == false {
			log.Printf("Health check successful (took %v)", duration)
			
			// Log additional info if available
			if statusCode, ok := result["status_code"]; ok {
				log.Printf("Status code: %v", statusCode)
			}
			if message, ok := result["message"]; ok {
				log.Printf("Message: %v", message)
			}
		} else {
			log.Printf("Health check returned error response (took %v): %v", duration, result)
		}
	} else {
		log.Printf("Health check response missing 'error' field (took %v): %v", duration, result)
	}
}

// RunHealthCheckCronStandalone runs the health check cron as a standalone service
func RunHealthCheckCronStandalone() {
	healthCheckCron, err := NewHealthCheckCron()
	if err != nil {
		log.Fatalf("Failed to create health check cron: %v", err)
	}

	ctx := context.Background()
	if err := healthCheckCron.Start(ctx); err != nil {
		log.Fatalf("Failed to start health check cron: %v", err)
	}
}
