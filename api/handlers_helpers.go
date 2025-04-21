package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// NewTestServer creates a test Gin engine with the handleUpload route registered.
func NewTestServer(s *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/upload", s.handleUpload)
	return r
}

// PerformJSONRequest performs a POST request with JSON body and returns the response recorder.
func PerformJSONRequest(r http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)
	return recorder
}

// CreateTestVideoFile creates the directory and a dummy video file for testing.
func CreateTestVideoFile(basePath, cameraName, filename string) (string, error) {
	dir := filepath.Join(basePath, "recordings", cameraName, "mp4")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	videoPath := filepath.Join(dir, filename)
	// Create a dummy file (empty content is enough for handler existence check)
	if err := os.WriteFile(videoPath, []byte("dummy"), 0644); err != nil {
		return "", err
	}
	return videoPath, nil
}
