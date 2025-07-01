package database

import (
	"time"
)

// VideoStatus represents the current state of a video
type VideoStatus string

const (
	StatusPending     VideoStatus = "pending"     // Video is pending processing
	StatusRecording   VideoStatus = "recording"   // Video is currently being recorded
	StatusProcessing  VideoStatus = "processing"  // Video is being processed
	StatusUploading   VideoStatus = "uploading"   // Video is being uploaded to cloud storage
	StatusReady       VideoStatus = "ready"       // Video is ready for playback
	StatusFailed      VideoStatus = "failed"      // Video processing failed
	StatusUnavailable VideoStatus = "unavailable" // Video has been auto-deleted
	StatusCancelled   VideoStatus = "cancelled"   // Video processing cancelled
	StatusInitial 	  VideoStatus = "initial"     // Video processing initial
)

// VideoMetadata represents the metadata for a recorded video
type VideoMetadata struct {
	ID               string      `json:"id"`               // Unique identifier for the video
	CreatedAt        time.Time   `json:"createdAt"`        // When the recording started
	FinishedAt       *time.Time  `json:"finishedAt"`       // When the recording finished (nil if still recording)
	UploadedAt       *time.Time  `json:"uploadedAt"`       // When the video was uploaded to R2/cloud storage
	Status           VideoStatus `json:"status"`           // Current status
	Duration         float64     `json:"duration"`         // Duration in seconds
	Size             int64       `json:"size"`             // Size in bytes
	LocalPath        string      `json:"localPath"`        // Path to local file
	HLSPath          string      `json:"hlsPath"`          // Path to HLS stream directory
	HLSURL           string      `json:"hlsUrl"`           // URL to HLS playlist
	R2HLSPath        string      `json:"r2HlsPath"`        // R2 path to HLS stream
	R2HLSURL         string      `json:"r2HlsUrl"`         // R2 URL to HLS playlist
	R2MP4Path        string      `json:"r2Mp4Path"`        // R2 path to MP4 file
	R2MP4URL         string      `json:"r2Mp4Url"`         // R2 URL to MP4 file
	R2PreviewMP4Path string      `json:"r2PreviewMp4Path"` // R2 path to preview MP4 file
	R2PreviewMP4URL  string      `json:"r2PreviewMp4Url"`  // R2 URL to preview MP4 file
	R2PreviewPNGPath string      `json:"r2PreviewPngPath"` // R2 path to preview PNG file
	R2PreviewPNGURL  string      `json:"r2PreviewPngUrl"`  // R2 URL to preview PNG file
	MP4Path          string      `json:"mp4Path"`          // Path to MP4 file
	MP4URL           string      `json:"mp4Url"`           // URL to MP4 file
	CameraName       string      `json:"cameraName"`       // Name of the camera that recorded this video
	UniqueID         string      `json:"uniqueId"`         // Unique ID for the video (used for API)
	OrderDetailID    string      `json:"orderDetailId"`    // Order detail ID from booking
	BookingID        string      `json:"bookingId"`        // Booking ID reference
	RawJSON          string      `json:"rawJson"`          // Raw JSON data for additional metadata
	ErrorMessage     string      `json:"errorMessage"`     // Error message if processing failed
	Resolution       string      `json:"resolution"`       // Resolution of the video
	HasRequest       bool        `json:"hasRequest"`       // Whether there is a request for this video
	LastCheckFile    *time.Time  `json:"lastCheckFile"`    // When the file was last checked for existence
	VideoType        string      `json:"videoType"`        // Type of video: "clip" or "full"
	RequestID        string      `json:"requestId"`        // ID of the request for this video
	StorageDiskID    string      `json:"storageDiskId"`    // ID of the storage disk where this video is stored
	MP4FullPath      string      `json:"mp4FullPath"`      // Complete path to MP4 file including disk
	DeprecatedHLS    bool        `json:"deprecatedHls"`    // Whether HLS files have been deprecated/cleaned up
}

// CameraConfig represents camera configuration stored in the database
type CameraConfig struct {
	ButtonNo   string `json:"button_no"`
	Name       string `json:"name"`
	IP         string `json:"ip"`
	Port       string `json:"port"`
	Path       string `json:"path"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Enabled    bool   `json:"enabled"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	FrameRate  int    `json:"frame_rate"`
	Field      string `json:"field"`
	Resolution string `json:"resolution"`
	AutoDelete int    `json:"auto_delete"`
}

// StorageDisk represents a storage disk for recording data
type StorageDisk struct {
	ID               string    `json:"id"`               // Unique identifier for the disk
	Path             string    `json:"path"`             // Mount path of the disk
	TotalSpaceGB     int64     `json:"totalSpaceGb"`     // Total disk space in GB
	AvailableSpaceGB int64     `json:"availableSpaceGb"` // Available disk space in GB
	IsActive         bool      `json:"isActive"`         // Whether this disk is currently active for recording
	PriorityOrder    int       `json:"priorityOrder"`    // Priority order for disk selection (lower = higher priority)
	LastScan         time.Time `json:"lastScan"`         // When this disk was last scanned
	CreatedAt        time.Time `json:"createdAt"`        // When this disk was added to the system
}

// RecordingSegment represents an individual MP4 recording segment
type RecordingSegment struct {
	ID            string    `json:"id"`            // Unique identifier for the segment
	CameraName    string    `json:"cameraName"`    // Name of the camera that recorded this segment
	StorageDiskID string    `json:"storageDiskId"` // ID of the storage disk where this segment is stored
	MP4Path       string    `json:"mp4Path"`       // Relative path to the MP4 file on the storage disk
	SegmentStart  time.Time `json:"segmentStart"`  // Start time of the recording segment
	SegmentEnd    time.Time `json:"segmentEnd"`    // End time of the recording segment
	FileSizeBytes int64     `json:"fileSizeBytes"` // Size of the MP4 file in bytes
	CreatedAt     time.Time `json:"createdAt"`     // When this segment record was created
}

// Database defines the interface for database operations
type Database interface {
	// Video operations
	CreateVideo(metadata VideoMetadata) error
	GetVideo(id string) (*VideoMetadata, error)
	UpdateVideo(metadata VideoMetadata) error
	UpdateLocalPathVideo(metadata VideoMetadata) error
	ListVideos(limit, offset int) ([]VideoMetadata, error)
	DeleteVideo(id string) error

	// Status operations
	GetVideosByStatus(status VideoStatus, limit, offset int) ([]VideoMetadata, error)
	UpdateVideoStatus(id string, status VideoStatus, errorMsg string) error
	UpdateLastCheckFile(id string, lastCheckTime time.Time) error

	// Booking operations
	GetVideosByBookingID(bookingID string) ([]VideoMetadata, error)
	GetVideoByUniqueID(uniqueID string) (*VideoMetadata, error)

	// Camera configuration operations
	GetCameras() ([]CameraConfig, error)
	InsertCameras(cameras []CameraConfig) error

	// Storage disk operations
	CreateStorageDisk(disk StorageDisk) error
	GetStorageDisks() ([]StorageDisk, error)
	GetActiveDisk() (*StorageDisk, error)
	UpdateDiskSpace(id string, totalGB, availableGB int64) error
	SetActiveDisk(id string) error
	GetStorageDisk(id string) (*StorageDisk, error)

	// Recording segment operations
	CreateRecordingSegment(segment RecordingSegment) error
	GetRecordingSegments(cameraName string, start, end time.Time) ([]RecordingSegment, error)
	DeleteRecordingSegment(id string) error
	GetRecordingSegmentsByDisk(diskID string) ([]RecordingSegment, error)

	// R2 storage operations
	UpdateVideoR2Paths(id, hlsPath, mp4Path string) error
	UpdateVideoR2URLs(id, hlsURL, mp4URL string) error
	UpdateVideoRequestID(id, requestId string, remove bool) error

	// Helper operations
	Close() error
}
