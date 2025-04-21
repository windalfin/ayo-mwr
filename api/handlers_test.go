package api

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/storage"

	"github.com/joho/godotenv"
)

func TestMain(m *testing.M) {
	// Print working directory for debugging
	cwd, _ := os.Getwd()
	fmt.Println("[DEBUG] Current working directory:", cwd)

	// Try loading .env from several possible locations
	if err := godotenv.Load(); err != nil {
		fmt.Println("[DEBUG] No .env file found in current dir, trying parent...")
		_ = godotenv.Load("../.env")
	}

	// Print R2 env values for verification
	fmt.Println("[DEBUG] R2_BUCKET:", os.Getenv("R2_BUCKET"))
	fmt.Println("[DEBUG] R2_ACCESS_KEY:", os.Getenv("R2_ACCESS_KEY"))
	fmt.Println("[DEBUG] R2_SECRET_KEY:", os.Getenv("R2_SECRET_KEY"))
	fmt.Println("[DEBUG] R2_ACCOUNT_ID:", os.Getenv("R2_ACCOUNT_ID"))
	fmt.Println("[DEBUG] R2_ENDPOINT:", os.Getenv("R2_ENDPOINT"))
	fmt.Println("[DEBUG] R2_REGION:", os.Getenv("R2_REGION"))

	os.Exit(m.Run())
}

func TestHandleUpload_HappyPath(t *testing.T) {
	storagePath := "../videos"
	cameraName := "camera_2"
	venueCode := "WM"
	videoTimestamp := time.Date(2025, 3, 20, 11, 58, 0, 0, time.Local)

	// Initialize R2 storage using env vars
	r2Cfg := storage.R2Config{
		AccessKey: os.Getenv("R2_ACCESS_KEY"),
		SecretKey: os.Getenv("R2_SECRET_KEY"),
		AccountID: os.Getenv("R2_ACCOUNT_ID"),
		Bucket:    os.Getenv("R2_BUCKET"),
		Endpoint:  os.Getenv("R2_ENDPOINT"),
		Region:    os.Getenv("R2_REGION"),
	}
	r2Storage, err := storage.NewR2Storage(r2Cfg)
	if err != nil {
		t.Fatalf("Failed to initialize R2 storage: %v", err)
	}

	s := &Server{
		config: config.Config{
			StoragePath: storagePath,
			VenueCode:   venueCode, // Hardcoded for testing
		},
		r2Storage: r2Storage,
	}

	r := NewTestServer(s)

	reqBody := map[string]interface{}{
		"timestamp":  videoTimestamp.Format(time.RFC3339),
		"cameraName": cameraName,
		"venueCode":  venueCode,
	}

	recorder := PerformJSONRequest(r, http.MethodPost, "/api/upload", reqBody)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", recorder.Code, recorder.Body.String())
	}
	// Optionally: check response structure, etc.
}
