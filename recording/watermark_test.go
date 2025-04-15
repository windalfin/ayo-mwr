package recording

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

func init() {
	// Load .env file for tests
	err := godotenv.Load("../../.env")
	if err != nil {
		fmt.Println("Could not load .env file, using system environment variables")
	}
}

func TestMain(m *testing.M) {
	godotenv.Load() // This loads .env into the environment
	os.Exit(m.Run())
}

func TestAddWatermark(t *testing.T) {
	inputVideo := filepath.Join("..", "test", "videos", "uploads", "camera_A_20250304_120503.mp4")
	watermarkImg := filepath.Join("..", "test", "watermark", "ayologo.png")
	outputVideo := filepath.Join("..", "test", "videos", "uploads", "output_watermarked.mp4")
	frameOriginal := filepath.Join("..", "test", "videos", "uploads", "frame_original.png")
	frameWatermarked := filepath.Join("..", "test", "videos", "uploads", "frame_watermarked.png")

	// Clean up output files before/after
	defer os.Remove(outputVideo)
	defer os.Remove(frameOriginal)
	defer os.Remove(frameWatermarked)

	// Call the function under test (to be implemented)
	err := AddWatermark(inputVideo, watermarkImg, outputVideo, 100, 100) // position (x, y) example
	if err != nil {
		t.Fatalf("AddWatermark failed: %v", err)
	}

	// Extract frame at 1 second from both videos
	extractFrame := func(video, out string) error {
		cmd := exec.Command("ffmpeg", "-y", "-ss", "1", "-i", video, "-vframes", "1", out)
		return cmd.Run()
	}
	if err := extractFrame(inputVideo, frameOriginal); err != nil {
		t.Fatalf("Failed to extract original frame: %v", err)
	}
	if err := extractFrame(outputVideo, frameWatermarked); err != nil {
		t.Fatalf("Failed to extract watermarked frame: %v", err)
	}

	// Open both frames
	origF, err := os.Open(frameOriginal)
	if err != nil {
		t.Fatalf("Open original frame: %v", err)
	}
	defer origF.Close()
	origImg, err := png.Decode(origF)
	if err != nil {
		t.Fatalf("Decode original frame: %v", err)
	}

	wmF, err := os.Open(frameWatermarked)
	if err != nil {
		t.Fatalf("Open watermarked frame: %v", err)
	}
	defer wmF.Close()
	wmImg, err := png.Decode(wmF)
	if err != nil {
		t.Fatalf("Decode watermarked frame: %v", err)
	}

	// Compare region where watermark should be
	// (100,100) is the top-left of the watermark, adjust width/height as needed
	w, h := 50, 50 // Example watermark size, adjust as needed
	diff := false
	for x := 100; x < 100+w; x++ {
		for y := 100; y < 100+h; y++ {
			if origImg.At(x, y) != wmImg.At(x, y) {
				diff = true
				break
			}
		}
	}
	if !diff {
		t.Error("No difference detected in watermark region; watermark may not have been applied")
	}
}

func TestAddWatermarkWithPosition(t *testing.T) {
	inputVideo := filepath.Join("..", "test", "videos", "uploads", "camera_A_20250304_120503.mp4")
	watermarkImg := filepath.Join("..", "test", "watermark", "ayologo.png")
	outputVideo := filepath.Join("..", "test", "videos", "uploads", "output_watermarked_pos.mp4")
	frameOriginal := filepath.Join("..", "test", "videos", "uploads", "frame_original.png")
	frameWatermarked := filepath.Join("..", "test", "videos", "uploads", "frame_watermarked_pos.png")

	// defer os.Remove(outputVideo)
	// defer os.Remove(frameOriginal)
	// defer os.Remove(frameWatermarked)

	// Use top right, 10px margin, 60% opacity
	err := AddWatermarkWithPosition(inputVideo, watermarkImg, outputVideo, PositionTopRight, 10, 0.6)
	if err != nil {
		t.Fatalf("AddWatermarkWithPosition failed: %v", err)
	}

	// Extract frame at 1 second from both videos
	extractFrame := func(video, out string) error {
		cmd := exec.Command("ffmpeg", "-y", "-ss", "1", "-i", video, "-vframes", "1", out)
		return cmd.Run()
	}
	if err := extractFrame(inputVideo, frameOriginal); err != nil {
		t.Fatalf("Extract frame from original: %v", err)
	}
	if err := extractFrame(outputVideo, frameWatermarked); err != nil {
		t.Fatalf("Extract frame from watermarked: %v", err)
	}

	// Open images and compare region where watermark should be (top right, margin=10)
	origF, err := os.Open(frameOriginal)
	if err != nil {
		t.Fatalf("Open original frame: %v", err)
	}
	defer origF.Close()
	origImg, err := png.Decode(origF)
	if err != nil {
		t.Fatalf("Decode original frame: %v", err)
	}
	wmF, err := os.Open(frameWatermarked)
	if err != nil {
		t.Fatalf("Open watermarked frame: %v", err)
	}
	defer wmF.Close()
	wmImg, err := png.Decode(wmF)
	if err != nil {
		t.Fatalf("Decode watermarked frame: %v", err)
	}

	// Check for difference in the top right region
	bounds := origImg.Bounds()
	w, h := 50, 50 // Example watermark size
	diff := false
	for x := bounds.Max.X - 10 - w; x < bounds.Max.X-10; x++ {
		for y := 10; y < 10+h; y++ {
			if origImg.At(x, y) != wmImg.At(x, y) {
				diff = true
				break
			}
		}
	}
	if !diff {
		t.Error("No difference detected in watermark region; watermark may not have been applied")
	}
}

func TestGetWatermark(t *testing.T) {
	// Setup test server for image downloads
	tsImage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a simple dummy image
		img := image.NewRGBA(image.Rect(0, 0, 100, 100))
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			http.Error(w, "Failed to encode image", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(buf.Bytes())
	}))
	defer tsImage.Close()

	// Setup test server for API
	tsAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate API response
		type WatermarkData struct {
			Resolution string `json:"resolution"`
			Path       string `json:"path"`
		}
		response := struct {
			Error      bool            `json:"error"`
			StatusCode int             `json:"status_code"`
			Data       []WatermarkData `json:"data"`
		}{
			Error:      false,
			StatusCode: 200,
			Data: []WatermarkData{
				{Resolution: "480", Path: tsImage.URL + "/mock-image/480.png"},
				{Resolution: "720", Path: tsImage.URL + "/mock-image/720.png"},
				{Resolution: "1080", Path: tsImage.URL + "/mock-image/1080.png"},
			},
		}
		jsonResponse, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/json")
		w.Write(jsonResponse)
	}))
	defer tsAPI.Close()

	// Override environment variable for test
	originalEndpoint := os.Getenv("AYOINDO_API_BASE_ENDPOINT")
	os.Setenv("AYOINDO_API_BASE_ENDPOINT", tsAPI.URL)
	defer os.Setenv("AYOINDO_API_BASE_ENDPOINT", originalEndpoint)

	// We need to override the http.Get function to redirect image downloads to our mock image server
	originalTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if strings.Contains(req.URL.String(), "/mock-image/") {
				return url.Parse(tsImage.URL)
			}
			return nil, nil
		},
	}
	defer func() { http.DefaultTransport = originalTransport }()

	venueCode := "testvenue"
	folder := filepath.Join(".", "watermark", venueCode)
	os.RemoveAll(folder) // Clean before test
	defer os.RemoveAll(folder)

	// Debug environment variable
	t.Logf("AYOINDO_API_BASE_ENDPOINT: %s", os.Getenv("AYOINDO_API_BASE_ENDPOINT"))

	imgPath, err := getWatermark(venueCode)
	if err != nil {
		t.Fatalf("getWatermark failed: %v", err)
	}

	if _, err := os.Stat(imgPath); os.IsNotExist(err) {
		t.Fatalf("Watermark image not downloaded")
	}

	// Check if all expected files exist
	expectedResolutions := []string{"480", "720", "1080"}
	for _, res := range expectedResolutions {
		filePath := filepath.Join(folder, fmt.Sprintf("watermark_%s.png", res))
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("Expected watermark file for resolution %s not found at %s", res, filePath)
		}
	}
}
