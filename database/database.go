package database

import (
	"time"
)

// VideoStatus represents the current state of a video
type VideoStatus string

const (
	StatusPending    VideoStatus = "pending"    // Video is pending processing
	StatusRecording  VideoStatus = "recording"  // Video is currently being recorded
	StatusProcessing VideoStatus = "processing" // Video is being processed
	StatusUploading  VideoStatus = "uploading"  // Video is being uploaded to cloud storage
	StatusReady      VideoStatus = "ready"      // Video is ready for playback
	StatusFailed     VideoStatus = "failed"     // Video processing failed
)

// VideoMetadata represents the metadata for a recorded video
type VideoMetadata struct {
	ID                string      `json:"id"`                // Unique identifier for the video
	CreatedAt         time.Time   `json:"createdAt"`         // When the recording started
	FinishedAt        *time.Time  `json:"finishedAt"`        // When the recording finished (nil if still recording)
	UploadedAt        *time.Time  `json:"uploadedAt"`        // When the video was uploaded to R2/cloud storage
	Status            VideoStatus `json:"status"`            // Current status
	Duration          float64     `json:"duration"`          // Duration in seconds
	Size              int64       `json:"size"`              // Size in bytes
	LocalPath         string      `json:"localPath"`         // Path to local file
	HLSPath           string      `json:"hlsPath"`           // Path to HLS stream directory
	HLSURL            string      `json:"hlsUrl"`            // URL to HLS playlist
	R2HLSPath         string      `json:"r2HlsPath"`         // R2 path to HLS stream
	R2HLSURL          string      `json:"r2HlsUrl"`          // R2 URL to HLS playlist
	R2MP4Path         string      `json:"r2Mp4Path"`         // R2 path to MP4 file
	R2MP4URL          string      `json:"r2Mp4Url"`          // R2 URL to MP4 file
	R2PreviewMP4Path  string      `json:"r2PreviewMp4Path"`  // R2 path to preview MP4 file
	R2PreviewMP4URL   string      `json:"r2PreviewMp4Url"`   // R2 URL to preview MP4 file
	R2PreviewPNGPath  string      `json:"r2PreviewPngPath"`  // R2 path to preview PNG file
	R2PreviewPNGURL   string      `json:"r2PreviewPngUrl"`   // R2 URL to preview PNG file
	MP4Path           string      `json:"mp4Path"`           // Path to MP4 file
	MP4URL            string      `json:"mp4Url"`            // URL to MP4 file
	CameraName        string      `json:"cameraName"`        // Name of the camera that recorded this video
	UniqueID          string      `json:"uniqueId"`          // Unique ID for the video (used for API)
	OrderDetailID     string      `json:"orderDetailId"`     // Order detail ID from booking
	BookingID         string      `json:"bookingId"`         // Booking ID reference
	RawJSON           string      `json:"rawJson"`           // Raw JSON data for additional metadata
	ErrorMessage      string      `json:"errorMessage"`      // Error message if processing failed
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

	// Booking operations
	GetVideosByBookingID(bookingID string) ([]VideoMetadata, error)

	// R2 storage operations
	UpdateVideoR2Paths(id, hlsPath, mp4Path string) error
	UpdateVideoR2URLs(id, hlsURL, mp4URL string) error

	// Helper operations
	Close() error
}
