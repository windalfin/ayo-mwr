package signaling

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/transcode"
)

// TranscodeRequest represents an HTTP request for transcoding
type TranscodeRequest struct {
	Timestamp  time.Time `json:"timestamp"`
	CameraName string    `json:"cameraName"` // Camera identifier
}

// HTTPSignal handles signals from HTTP endpoints
type HTTPSignal struct {
	server *http.Server
	cfg    config.Config
}

// NewHTTPSignal creates a new HTTP signal handler
func NewHTTPSignal(cfg config.Config) *HTTPSignal {
	return &HTTPSignal{
		cfg: cfg,
	}
}

// Start initializes and starts the HTTP server
func (h *HTTPSignal) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/transcode", h.handleTranscode)

	h.server = &http.Server{
		Addr:    ":8085", // Use fixed port 8085 for transcoding service
		Handler: mux,
	}

	log.Printf("Starting HTTP signal handler on port 8085")
	go func() {
		if err := h.server.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	return nil
}

// handleTranscode processes incoming transcode requests
func (h *HTTPSignal) handleTranscode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TranscodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Find the closest video file
	inputPath, err := FindClosestVideo(h.cfg.StoragePath, req.CameraName, req.Timestamp)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to find video: %v", err), http.StatusNotFound)
		return
	}

	// Extract video ID from the file path
	videoID := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))

	// Transcode the video
	urls, timings, err := transcode.TranscodeVideo(inputPath, videoID, req.CameraName, h.cfg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Transcoding failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return the URLs and timings
	response := struct {
		URLs     map[string]string  `json:"urls"`
		Timings  map[string]float64 `json:"timings"`
		VideoID  string             `json:"videoId"`
		Filename string             `json:"filename"`
	}{
		URLs:     urls,
		Timings:  timings,
		VideoID:  videoID,
		Filename: filepath.Base(inputPath),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Stop gracefully shuts down the HTTP server
func (h *HTTPSignal) Stop() error {
	if h.server != nil {
		return h.server.Close()
	}
	return nil
}
