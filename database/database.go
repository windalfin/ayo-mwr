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
	StatusInitial     VideoStatus = "initial"     // Video processing initial
)

// VideoMetadata represents the metadata for a recorded video
type VideoMetadata struct {
	ID               string      `json:"id"`                  // Unique identifier for the video
	CreatedAt        time.Time   `json:"createdAt"`           // When the recording started
	FinishedAt       *time.Time  `json:"finishedAt"`          // When the recording finished (nil if still recording)
	UploadedAt       *time.Time  `json:"uploadedAt"`          // When the video was uploaded to R2/cloud storage
	Status           VideoStatus `json:"status"`              // Current status
	Duration         float64     `json:"duration"`            // Duration in seconds
	Size             int64       `json:"size"`                // Size in bytes
	StartTime        *time.Time  `json:"startTime,omitempty"` // Actual start time of the clip (from booking)
	EndTime          *time.Time  `json:"endTime,omitempty"`   // Actual end time of the clip (from booking)
	LocalPath        string      `json:"localPath"`           // Path to local file
	HLSPath          string      `json:"hlsPath"`             // Path to HLS stream directory
	HLSURL           string      `json:"hlsUrl"`              // URL to HLS playlist
	R2HLSPath        string      `json:"r2HlsPath"`           // R2 path to HLS stream
	R2HLSURL         string      `json:"r2HlsUrl"`            // R2 URL to HLS playlist
	R2MP4Path        string      `json:"r2Mp4Path"`           // R2 path to MP4 file
	R2MP4URL         string      `json:"r2Mp4Url"`            // R2 URL to MP4 file
	R2PreviewMP4Path string      `json:"r2PreviewMp4Path"`    // R2 path to preview MP4 file
	R2PreviewMP4URL  string      `json:"r2PreviewMp4Url"`     // R2 URL to preview MP4 file
	R2PreviewPNGPath string      `json:"r2PreviewPngPath"`    // R2 path to preview PNG file
	R2PreviewPNGURL  string      `json:"r2PreviewPngUrl"`     // R2 URL to preview PNG file
	MP4Path          string      `json:"mp4Path"`             // Path to MP4 file
	MP4URL           string      `json:"mp4Url"`              // URL to MP4 file
	CameraName       string      `json:"cameraName"`          // Name of the camera that recorded this video
	UniqueID         string      `json:"uniqueId"`            // Unique ID for the video (used for API)
	OrderDetailID    string      `json:"orderDetailId"`       // Order detail ID from booking
	BookingID        string      `json:"bookingId"`           // Booking ID reference
	RawJSON          string      `json:"rawJson"`             // Raw JSON data for additional metadata
	ErrorMessage     string      `json:"errorMessage"`        // Error message if processing failed
	Resolution       string      `json:"resolution"`          // Resolution of the video
	HasRequest       bool        `json:"hasRequest"`          // Whether there is a request for this video
	LastCheckFile    *time.Time  `json:"lastCheckFile"`       // When the file was last checked for existence
	VideoType        string      `json:"videoType"`           // Type of video: "clip" or "full"
	RequestID        string      `json:"requestId"`           // ID of the request for this video
	StorageDiskID    string      `json:"storageDiskId"`       // ID of the storage disk where this video is stored
	MP4FullPath      string      `json:"mp4FullPath"`         // Complete path to MP4 file including disk
	DeprecatedHLS    bool        `json:"deprecatedHls"`       // Whether HLS files have been deprecated/cleaned up
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

// PendingTask represents a task waiting to be executed
type PendingTask struct {
	ID          int       `json:"id"`
	TaskType    string    `json:"taskType"`    // "upload_r2", "notify_ayo_api"
	TaskData    string    `json:"taskData"`    // JSON encoded task-specific data
	Attempts    int       `json:"attempts"`    // Number of attempts made
	MaxAttempts int       `json:"maxAttempts"` // Maximum number of attempts
	NextRetryAt time.Time `json:"nextRetryAt"` // When to retry next
	Status      string    `json:"status"`      // "pending", "processing", "completed", "failed"
	CreatedAt   time.Time `json:"createdAt"`   // When task was created
	UpdatedAt   time.Time `json:"updatedAt"`   // When task was last updated
	ErrorMsg    string    `json:"errorMsg"`    // Last error message
}

// Task types
const (
	TaskUploadR2     = "upload_r2"
	TaskNotifyAyoAPI = "notify_ayo_api"
)

// Task statuses
const (
	TaskStatusPending    = "pending"
	TaskStatusProcessing = "processing"
	TaskStatusCompleted  = "completed"
	TaskStatusFailed     = "failed"
)

// R2UploadTaskData represents data for R2 upload task
type R2UploadTaskData struct {
	VideoID            string `json:"videoId"`
	LocalMP4Path       string `json:"localMp4Path"`
	LocalPreviewPath   string `json:"localPreviewPath"`
	LocalThumbnailPath string `json:"localThumbnailPath"`
	R2Key              string `json:"r2Key"`
	R2PreviewKey       string `json:"r2PreviewKey"`
	R2ThumbnailKey     string `json:"r2ThumbnailKey"`
}

// AyoAPINotifyTaskData represents data for AYO API notification task
type AyoAPINotifyTaskData struct {
	VideoID      string  `json:"videoId"`
	UniqueID     string  `json:"uniqueId"`
	MP4URL       string  `json:"mp4Url"`
	PreviewURL   string  `json:"previewUrl"`
	ThumbnailURL string  `json:"thumbnailUrl"`
	Duration     float64 `json:"duration"`
}

// BookingData represents a booking from AYO API
type BookingData struct {
	ID            int       `json:"id"`            // Auto-increment primary key
	BookingID     string    `json:"bookingId"`     // Booking ID from API
	OrderDetailID int       `json:"orderDetailId"` // Order detail ID
	FieldID       int       `json:"fieldId"`       // Field ID
	Date          string    `json:"date"`          // Booking date (YYYY-MM-DD)
	StartTime     string    `json:"startTime"`     // Start time (HH:MM:SS)
	EndTime       string    `json:"endTime"`       // End time (HH:MM:SS)
	BookingSource string    `json:"bookingSource"` // Booking source (reservation, order_detail, etc.)
	Status        string    `json:"status"`        // Booking status (SUCCESS, CANCELLED, etc.)
	CreatedAt     time.Time `json:"createdAt"`     // When record was created in our DB
	UpdatedAt     time.Time `json:"updatedAt"`     // When record was last updated in our DB
	RawJSON       string    `json:"rawJson"`       // Complete raw JSON from API
	LastSyncAt    time.Time `json:"lastSyncAt"`    // Last time we synced this booking
}

// SystemConfig represents system configuration stored in the database
type SystemConfig struct {
	Key       string    `json:"key"`       // Configuration key
	Value     string    `json:"value"`     // Configuration value
	Type      string    `json:"type"`      // Value type: "string", "int", "bool", "json"
	UpdatedAt time.Time `json:"updatedAt"` // When configuration was last updated
	UpdatedBy string    `json:"updatedBy"` // Who updated the configuration
}

// User represents a user in the authentication system
type User struct {
	ID           int       `json:"id"`           // Auto-increment primary key
	Username     string    `json:"username"`     // Unique username
	PasswordHash string    `json:"-"`            // Hashed password (not exposed in JSON)
	CreatedAt    time.Time `json:"createdAt"`    // When user was created
	UpdatedAt    time.Time `json:"updatedAt"`    // When user was last updated
}

// System configuration keys
const (
	ConfigBookingWorkerConcurrency      = "booking_worker_concurrency"
	ConfigVideoRequestWorkerConcurrency = "video_request_worker_concurrency"
	ConfigPendingTaskWorkerConcurrency  = "pending_task_worker_concurrency"
	ConfigEnabledQualities              = "enabled_qualities"
	// Video Processing Configuration
	ConfigEnableVideoDurationCheck = "enable_video_duration_check"
	// Disk Manager Configuration
	ConfigMinimumFreeSpaceGB     = "minimum_free_space_gb"
	ConfigPriorityExternal       = "priority_external"
	ConfigPriorityMountedStorage = "priority_mounted_storage"
	ConfigPriorityInternalNVMe   = "priority_internal_nvme"
	ConfigPriorityInternalSATA   = "priority_internal_sata"
	ConfigPriorityRootFilesystem = "priority_root_filesystem"
)

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

	// Cleanup operations
	CleanupStuckVideosOnStartup() error

	// Booking operations
	GetVideosByBookingID(bookingID string) ([]VideoMetadata, error)
	GetVideoByUniqueID(uniqueID string) (*VideoMetadata, error)

	// Camera configuration operations
	GetCameras() ([]CameraConfig, error)
	InsertCameras(cameras []CameraConfig) error
	UpdateCameraConfig(cameraName string, frameRate int, autoDelete int) error

	// Storage disk operations
	CreateStorageDisk(disk StorageDisk) error
	GetStorageDisks() ([]StorageDisk, error)
	GetActiveDisk() (*StorageDisk, error)
	UpdateDiskSpace(id string, totalGB, availableGB int64) error
	UpdateDiskPriority(id string, priority int) error
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

	// Offline queue operations
	CreatePendingTask(task PendingTask) error
	GetPendingTasks(limit int) ([]PendingTask, error)
	UpdateTaskStatus(taskID int, status string, errorMsg string) error
	UpdateTaskNextRetry(taskID int, nextRetryAt time.Time, attempts int) error
	DeleteCompletedTasks(olderThan time.Time) error
	GetTaskByID(taskID int) (*PendingTask, error)

	// Booking operations
	CreateOrUpdateBooking(booking BookingData) error
	GetBookingByID(bookingID string) (*BookingData, error)
	GetBookingsByDate(date string) ([]BookingData, error)
	GetBookingsByStatus(status string) ([]BookingData, error)
	UpdateBookingStatus(bookingID string, status string) error
	DeleteOldBookings(olderThan time.Time) error

	// System configuration operations
	GetSystemConfig(key string) (*SystemConfig, error)
	SetSystemConfig(config SystemConfig) error
	GetAllSystemConfigs() ([]SystemConfig, error)
	DeleteSystemConfig(key string) error

	// User authentication operations
	CreateUser(username, passwordHash string) error
	GetUserByUsername(username string) (*User, error)
	HasUsers() (bool, error)

	// Helper operations
	Close() error
}
