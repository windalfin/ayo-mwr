package database

import (
	"time"
)

// VideoStatus represents the current state of a video
type VideoStatus string

const (
	StatusRecording    VideoStatus = "recording"    // Video is currently being recorded
	StatusProcessing   VideoStatus = "processing"   // Video is being processed
	StatusUploading    VideoStatus = "uploading"    // Video is being uploaded to cloud storage
	StatusReady        VideoStatus = "ready"        // Video is ready for playback
	StatusFailed       VideoStatus = "failed"       // Video processing failed
)

// VideoMetadata represents the metadata for a recorded video
type VideoMetadata struct {
	ID           string      `json:"id"`           // Unique identifier for the video
	CreatedAt    time.Time   `json:"createdAt"`    // When the recording started
	FinishedAt   *time.Time  `json:"finishedAt"`   // When the recording finished (nil if still recording)
	Status       VideoStatus `json:"status"`       // Current status
	Duration     float64     `json:"duration"`     // Duration in seconds
	Size         int64       `json:"size"`         // Size in bytes
	LocalPath    string      `json:"localPath"`    // Path to local file
	HLSPath      string      `json:"hlsPath"`      // Path to HLS stream directory
	DASHPath     string      `json:"dashPath"`     // Path to DASH stream directory
	HLSURL       string      `json:"hlsUrl"`       // URL to HLS playlist
	DASHURL      string      `json:"dashUrl"`      // URL to DASH manifest
	R2HLSPath    string      `json:"r2HlsPath"`    // R2 path to HLS stream
	R2DASHPath   string      `json:"r2DashPath"`   // R2 path to DASH stream
	R2HLSURL     string      `json:"r2HlsUrl"`     // R2 URL to HLS playlist
	R2DASHURL    string      `json:"r2DashUrl"`    // R2 URL to DASH manifest
	CameraID     string      `json:"cameraId"`     // ID of the camera that recorded this video
	ErrorMessage string      `json:"errorMessage"` // Error message if processing failed
}

// Database defines the interface for database operations
type Database interface {
	// Video operations
	CreateVideo(metadata VideoMetadata) error
	GetVideo(id string) (*VideoMetadata, error)
	UpdateVideo(metadata VideoMetadata) error
	ListVideos(limit, offset int) ([]VideoMetadata, error)
	DeleteVideo(id string) error
	
	// Status operations
	GetVideosByStatus(status VideoStatus, limit, offset int) ([]VideoMetadata, error)
	UpdateVideoStatus(id string, status VideoStatus, errorMsg string) error
	
	// R2 storage operations
	UpdateVideoR2Paths(id, hlsPath, dashPath string) error
	UpdateVideoR2URLs(id, hlsURL, dashURL string) error

	// Helper operations
	Close() error
}