package database

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteDB implements the Database interface using SQLite
type SQLiteDB struct {
	db *sql.DB
}

// InsertCameras inserts a slice of cameras into the database (replaces all)
func (s *SQLiteDB) InsertCameras(cameras []CameraConfig) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Clear old
	if _, err := tx.Exec("DELETE FROM cameras"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO cameras (button_no, name, ip, port, path, username, password, enabled, width, height, frame_rate, field, resolution, auto_delete) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range cameras {
		_, err := stmt.Exec(c.ButtonNo, c.Name, c.IP, c.Port, c.Path, c.Username, c.Password, c.Enabled, c.Width, c.Height, c.FrameRate, c.Field, c.Resolution, c.AutoDelete)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetCameras loads all cameras from the DB
func (s *SQLiteDB) GetCameras() ([]CameraConfig, error) {
	rows, err := s.db.Query(`SELECT button_no, name, ip, port, path, username, password, enabled, width, height, frame_rate, field, resolution, auto_delete FROM cameras`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cameras []CameraConfig
	for rows.Next() {
		var c CameraConfig
		err := rows.Scan(&c.ButtonNo, &c.Name, &c.IP, &c.Port, &c.Path, &c.Username, &c.Password, &c.Enabled, &c.Width, &c.Height, &c.FrameRate, &c.Field, &c.Resolution, &c.AutoDelete)
		if err != nil {
			return nil, err
		}
		cameras = append(cameras, c)
	}
	return cameras, nil
}


// NewSQLiteDB creates a new SQLite database instance
func NewSQLiteDB(dbPath string) (*SQLiteDB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %v", err)
	}

	// Create tables if they don't exist
	err = initTables(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize tables: %v", err)
	}

	return &SQLiteDB{db: db}, nil
}

// initTables creates the necessary tables if they don't exist
func initTables(db *sql.DB) error {
	// Create cameras table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS cameras (
			button_no TEXT,
			name TEXT PRIMARY KEY,
			ip TEXT,
			port TEXT,
			path TEXT,
			username TEXT,
			password TEXT,
			enabled BOOLEAN,
			width INTEGER,
			height INTEGER,
			frame_rate INTEGER,
			field TEXT,
			resolution TEXT,
			auto_delete INTEGER
		)
	`)
	if err != nil {
		return err
	}

	// Try to add button_no column if missing
	_, migrationErr := db.Exec("ALTER TABLE cameras ADD COLUMN button_no TEXT")
	if migrationErr != nil {
		log.Printf("Info: Migration for button_no: %v (ignore if column exists)", migrationErr)
	} else {
		log.Printf("Success: Added button_no column to cameras table")
	}

	// Create arduino_config table (single-row table, id always 1)
    _, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS arduino_config (
            id INTEGER PRIMARY KEY CHECK(id = 1),
            port TEXT,
            baud_rate INTEGER
        )
    `)
    if err != nil {
        return err
    }

    // Create storage_disks table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS storage_disks (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			total_space_gb INTEGER,
			available_space_gb INTEGER,
			is_active BOOLEAN DEFAULT 0,
			priority_order INTEGER DEFAULT 0,
			last_scan DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create recording_segments table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS recording_segments (
			id TEXT PRIMARY KEY,
			camera_name TEXT NOT NULL,
			storage_disk_id TEXT NOT NULL,
			mp4_path TEXT NOT NULL,
			segment_start DATETIME NOT NULL,
			segment_end DATETIME NOT NULL,
			file_size_bytes INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (storage_disk_id) REFERENCES storage_disks(id)
		)
	`)
	if err != nil {
		return err
	}

	// Create videos table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS videos (
			id TEXT PRIMARY KEY,
			camera_name TEXT,
			local_path TEXT,
			hls_path TEXT,
			hls_url TEXT,
			r2_hls_path TEXT,
			r2_mp4_path TEXT,
			r2_hls_url TEXT,
			r2_mp4_url TEXT,
			r2_preview_mp4_path TEXT,
			r2_preview_mp4_url TEXT,
			r2_preview_png_path TEXT,
			r2_preview_png_url TEXT,
			unique_id TEXT,
			order_detail_id TEXT,
			booking_id TEXT,
			raw_json TEXT,
			status TEXT,
			error TEXT,
			created_at DATETIME,
			finished_at DATETIME,
			uploaded_at DATETIME,
			size INTEGER,
			duration REAL,
			resolution TEXT,
			has_request BOOLEAN DEFAULT 0,
			last_check_file DATETIME,
			video_type TEXT,
			request_id TEXT,
			storage_disk_id TEXT,
			mp4_full_path TEXT,
			deprecated_hls BOOLEAN DEFAULT 0
		)
	`)
	if err != nil {
		return err
	}

	// Migrasi: Coba tambahkan kolom-kolom yang mungkin belum ada
	// Ini akan gagal dengan error tetapi tidak kritis jika kolom sudah ada
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN camera_name TEXT")
	if migrationErr != nil {
		log.Printf("Info: Migration for camera_name: %v (bisa abaikan jika kolom sudah ada)", migrationErr)
	} else {
		log.Printf("Success: Menambahkan kolom camera_name ke tabel videos")
	}

	// Tambahkan kolom size jika belum ada
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN size INTEGER")
	if migrationErr != nil {
		log.Printf("Info: Migration for size: %v (bisa abaikan jika kolom sudah ada)", migrationErr)
	} else {
		log.Printf("Success: Menambahkan kolom size ke tabel videos")
	}
	// Tambahkan kolom request_id jika belum ada
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN request_id TEXT")
	if migrationErr != nil {
		log.Printf("Info: Migration for request_id: %v (bisa abaikan jika kolom sudah ada)", migrationErr)
	} else {
		log.Printf("Success: Menambahkan kolom request_id ke tabel videos")
	}

	// Tambahkan kolom duration jika belum ada
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN duration REAL")
	if migrationErr != nil {
		log.Printf("Info: Migration for duration: %v (bisa abaikan jika kolom sudah ada)", migrationErr)
	} else {
		log.Printf("Success: Menambahkan kolom duration ke tabel videos")
	}

	// Tambahkan kolom resolution jika belum ada
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN resolution TEXT")
	if migrationErr != nil {
		log.Printf("Info: Migration for resolution: %v (bisa abaikan jika kolom sudah ada)", migrationErr)
	} else {
		log.Printf("Success: Menambahkan kolom resolution ke tabel videos")
	}

	// Tambahkan kolom has_request jika belum ada dengan default false (0)
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN has_request BOOLEAN DEFAULT 0")
	if migrationErr != nil {
		log.Printf("Info: Migration for has_request: %v (bisa abaikan jika kolom sudah ada)", migrationErr)
	} else {
		log.Printf("Success: Menambahkan kolom has_request ke tabel videos")
	}

	// Tambahkan kolom last_check_file jika belum ada
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN last_check_file DATETIME")
	if migrationErr != nil {
		log.Printf("Info: Migration for last_check_file: %v (bisa abaikan jika kolom sudah ada)", migrationErr)
	} else {
		log.Printf("Success: Menambahkan kolom last_check_file ke tabel videos")
	}

	// Tambahkan kolom video_type jika belum ada
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN video_type TEXT")
	if migrationErr != nil {
		log.Printf("Info: Migration for video_type: %v (bisa abaikan jika kolom sudah ada)", migrationErr)
	} else {
		log.Printf("Success: Menambahkan kolom video_type ke tabel videos")
	}

	// Add new columns for multi-disk support
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN storage_disk_id TEXT")
	if migrationErr != nil {
		log.Printf("Info: Migration for storage_disk_id: %v (ignore if column exists)", migrationErr)
	} else {
		log.Printf("Success: Added storage_disk_id column to videos table")
	}

	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN mp4_full_path TEXT")
	if migrationErr != nil {
		log.Printf("Info: Migration for mp4_full_path: %v (ignore if column exists)", migrationErr)
	} else {
		log.Printf("Success: Added mp4_full_path column to videos table")
	}

	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN deprecated_hls BOOLEAN DEFAULT 0")
	if migrationErr != nil {
		log.Printf("Info: Migration for deprecated_hls: %v (ignore if column exists)", migrationErr)
	} else {
		log.Printf("Success: Added deprecated_hls column to videos table")
	}

	// Add start_time and end_time columns for clip time tracking
	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN start_time DATETIME")
	if migrationErr != nil {
		log.Printf("Info: Migration for start_time: %v (ignore if column exists)", migrationErr)
	} else {
		log.Printf("Success: Added start_time column to videos table")
	}

	_, migrationErr = db.Exec("ALTER TABLE videos ADD COLUMN end_time DATETIME")
	if migrationErr != nil {
		log.Printf("Info: Migration for end_time: %v (ignore if column exists)", migrationErr)
	} else {
		log.Printf("Success: Added end_time column to videos table")
	}

	// Create indexes
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_videos_status ON videos (status)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_videos_storage_disk ON videos (storage_disk_id)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_recording_segments_camera_time ON recording_segments (camera_name, segment_start, segment_end)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_recording_segments_disk ON recording_segments (storage_disk_id)
	`)
	if err != nil {
		return err
	}

	// Create pending_tasks table for offline queue
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS pending_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_type TEXT NOT NULL,
			task_data TEXT NOT NULL,
			attempts INTEGER DEFAULT 0,
			max_attempts INTEGER DEFAULT 5,
			next_retry_at DATETIME,
			status TEXT DEFAULT 'pending',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			error_msg TEXT
		)
	`)
	if err != nil {
		return err
	}

	// Create bookings table for storing AYO API booking data
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS bookings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			booking_id TEXT NOT NULL UNIQUE,
			order_detail_id INTEGER,
			field_id INTEGER,
			date TEXT,
			start_time TEXT,
			end_time TEXT,
			booking_source TEXT,
			status TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			raw_json TEXT,
			last_sync_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create indexes for pending_tasks
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_pending_tasks_status ON pending_tasks (status)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_pending_tasks_next_retry ON pending_tasks (next_retry_at)
	`)
	if err != nil {
		return err
	}

	// Create indexes for bookings
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_bookings_booking_id ON bookings (booking_id)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_bookings_date ON bookings (date)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_bookings_status ON bookings (status)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_bookings_field_id ON bookings (field_id)
	`)
	if err != nil {
		return err
	}

	return nil
}

// CreateVideo creates a new video record in the database
func (s *SQLiteDB) CreateVideo(metadata VideoMetadata) error {
	// Insert video metadata
	_, err := s.db.Exec(`
		INSERT INTO videos (
			id, camera_name, local_path, hls_path, hls_url,
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url,
			r2_preview_mp4_path, r2_preview_mp4_url, r2_preview_png_path, r2_preview_png_url,
			unique_id, order_detail_id, booking_id, raw_json, status, error, created_at, finished_at, uploaded_at,
			size, duration, resolution, has_request, last_check_file, video_type, storage_disk_id, mp4_full_path, deprecated_hls, start_time, end_time
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		metadata.ID,
		metadata.CameraName,
		metadata.LocalPath,
		metadata.HLSPath,
		metadata.HLSURL,
		metadata.R2HLSPath,
		metadata.R2MP4Path,
		metadata.R2HLSURL,
		metadata.R2MP4URL,
		metadata.R2PreviewMP4Path,
		metadata.R2PreviewMP4URL,
		metadata.R2PreviewPNGPath,
		metadata.R2PreviewPNGURL,
		metadata.UniqueID,
		metadata.OrderDetailID,
		metadata.BookingID,
		metadata.RawJSON,
		metadata.Status,
		metadata.ErrorMessage,
		metadata.CreatedAt,
		metadata.FinishedAt,
		metadata.UploadedAt,
		metadata.Size,
		metadata.Duration,
		metadata.Resolution,
		metadata.HasRequest,
		metadata.LastCheckFile,
		metadata.VideoType,
		metadata.StorageDiskID,
		metadata.MP4FullPath,
		metadata.DeprecatedHLS,
		metadata.StartTime,
		metadata.EndTime,
	)
	return err
}

// GetVideo retrieves a video record by ID
func (s *SQLiteDB) GetVideo(id string) (*VideoMetadata, error) {
	var video VideoMetadata
	var finishedAt, uploadedAt, lastCheckFile, startTime, endTime sql.NullTime
	var cameraName, uniqueID, orderDetailID, bookingID, rawJSON, videoType, requestID, storageDiskID, mp4FullPath sql.NullString
	var deprecatedHLS sql.NullBool

	err := s.db.QueryRow(`
		SELECT id, camera_name, local_path, hls_path, hls_url,
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url,
			r2_preview_mp4_path, r2_preview_mp4_url, r2_preview_png_path, r2_preview_png_url,
			unique_id, order_detail_id, booking_id, raw_json, status, error, created_at, finished_at, uploaded_at,
			size, duration, resolution, has_request, last_check_file, video_type, request_id, storage_disk_id, mp4_full_path, deprecated_hls, start_time, end_time
		FROM videos WHERE id = ?`, id).Scan(
		&video.ID,
		&cameraName,
		&video.LocalPath,
		&video.HLSPath,
		&video.HLSURL,
		&video.R2HLSPath,
		&video.R2MP4Path,
		&video.R2HLSURL,
		&video.R2MP4URL,
		&video.R2PreviewMP4Path,
		&video.R2PreviewMP4URL,
		&video.R2PreviewPNGPath,
		&video.R2PreviewPNGURL,
		&uniqueID,
		&orderDetailID,
		&bookingID,
		&rawJSON,
		&video.Status,
		&video.ErrorMessage,
		&video.CreatedAt,
		&finishedAt,
		&uploadedAt,
		&video.Size,
		&video.Duration,
		&video.Resolution,
		&video.HasRequest,
		&lastCheckFile,
		&videoType,
		&requestID,
		&storageDiskID,
		&mp4FullPath,
		&deprecatedHLS,
		&startTime,
		&endTime,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if cameraName.Valid {
		video.CameraName = cameraName.String
	}
	if finishedAt.Valid {
		video.FinishedAt = &finishedAt.Time
	}

	if uploadedAt.Valid {
		video.UploadedAt = &uploadedAt.Time
	}
	if uniqueID.Valid {
		video.UniqueID = uniqueID.String
	}
	if orderDetailID.Valid {
		video.OrderDetailID = orderDetailID.String
	}
	if bookingID.Valid {
		video.BookingID = bookingID.String
	}
	if rawJSON.Valid {
		video.RawJSON = rawJSON.String
	}

	// Set resolution if valid, otherwise leave as empty string
	// if resolution.Valid {
	// 	video.Resolution = resolution.String
	// } else {
	// 	video.Resolution = "" // Set default empty value for NULL resolution
	// }

	// Set VideoType if valid, otherwise leave as empty string
	if videoType.Valid {
		video.VideoType = videoType.String
	} else {
		video.VideoType = "" // Set default empty value for NULL video_type
	}

	// Set RequestID if valid, otherwise leave as empty string
	if requestID.Valid {
		video.RequestID = requestID.String
	} else {
		video.RequestID = "" // Set default empty value for NULL request_id
	}

	// Set StorageDiskID if valid
	if storageDiskID.Valid {
		video.StorageDiskID = storageDiskID.String
	}

	// Set MP4FullPath if valid
	if mp4FullPath.Valid {
		video.MP4FullPath = mp4FullPath.String
	}

	// Set DeprecatedHLS if valid
	if deprecatedHLS.Valid {
		video.DeprecatedHLS = deprecatedHLS.Bool
	}

	// Set StartTime and EndTime if valid
	if startTime.Valid {
		video.StartTime = &startTime.Time
	}
	if endTime.Valid {
		video.EndTime = &endTime.Time
	}

	return &video, nil
}

func (s *SQLiteDB) UpdateLocalPathVideo(metadata VideoMetadata) error {
	log.Printf("UpdateLocalPathVideo : Updating database entry for uniqueID: %s %s %s", metadata.LocalPath, metadata.Status, metadata.ID)
	_, err := s.db.Exec(`
		UPDATE videos SET
			local_path = ?,
			status = ?
		WHERE id = ?`,
		metadata.LocalPath,
		metadata.Status,
		metadata.ID,
	)
	log.Printf("UpdateLocalPathVideo : Database entry updated for uniqueID: %s", metadata.ID)
	return err
}

// UpdateVideo updates an existing video record
func (s *SQLiteDB) UpdateVideo(metadata VideoMetadata) error {
	_, err := s.db.Exec(`
		UPDATE videos SET
			camera_name = ?,
			local_path = ?,
			hls_path = ?,
			hls_url = ?,
			r2_hls_path = ?,
			r2_mp4_path = ?,
			r2_hls_url = ?,
			r2_mp4_url = ?,
			r2_preview_mp4_path = ?,
			r2_preview_mp4_url = ?,
			r2_preview_png_path = ?,
			r2_preview_png_url = ?,
			unique_id = ?,
			order_detail_id = ?,
			booking_id = ?,
			raw_json = ?,
			status = ?,
			error = ?,
			finished_at = ?,
			uploaded_at = ?,
			size = ?,
			duration = ?,
			resolution = ?,
			has_request = ?,
			last_check_file = ?,
			video_type = ?,
			start_time = ?,
			end_time = ?
		WHERE id = ?`,
		metadata.CameraName,
		metadata.LocalPath,
		metadata.HLSPath,
		metadata.HLSURL,
		metadata.R2HLSPath,
		metadata.R2MP4Path,
		metadata.R2HLSURL,
		metadata.R2MP4URL,
		metadata.R2PreviewMP4Path,
		metadata.R2PreviewMP4URL,
		metadata.R2PreviewPNGPath,
		metadata.R2PreviewPNGURL,
		metadata.UniqueID,
		metadata.OrderDetailID,
		metadata.BookingID,
		metadata.RawJSON,
		metadata.Status,
		metadata.ErrorMessage,
		metadata.FinishedAt,
		metadata.UploadedAt,
		metadata.Size,
		metadata.Duration,
		metadata.Resolution,
		metadata.HasRequest,
		metadata.LastCheckFile,
		metadata.VideoType,
		metadata.StartTime,
		metadata.EndTime,
		metadata.ID,
	)
	return err
}

// UpdateVideoR2Paths updates the R2 paths for a video
func (s *SQLiteDB) UpdateVideoR2Paths(id, hlsPath, mp4Path string) error {
	// Dapatkan data saat ini untuk memastikan field-field lainnya tidak hilang
	currentVideo, err := s.GetVideo(id)
	if err != nil {
		return fmt.Errorf("failed to get video data for R2 path update: %v", err)
	}

	// Update hanya path R2 tanpa mengubah field lain
	currentVideo.R2HLSPath = hlsPath
	currentVideo.R2MP4Path = mp4Path

	// Gunakan UpdateVideo untuk memastikan semua field tetap terjaga
	return s.UpdateVideo(*currentVideo)
}

// UpdateVideoR2URLs updates the R2 URLs for a video
func (s *SQLiteDB) UpdateVideoR2URLs(id, hlsURL, mp4URL string) error {
	// Dapatkan data saat ini untuk memastikan field-field lainnya tidak hilang
	currentVideo, err := s.GetVideo(id)
	if err != nil {
		return fmt.Errorf("failed to get video data for R2 URL update: %v", err)
	}

	// Update URL R2 dan timestamp upload
	now := time.Now()
	currentVideo.R2HLSURL = hlsURL
	currentVideo.R2MP4URL = mp4URL
	currentVideo.UploadedAt = &now

	// Gunakan UpdateVideo untuk memastikan semua field tetap terjaga
	return s.UpdateVideo(*currentVideo)
}

// ListVideos retrieves a list of videos with pagination
func (s *SQLiteDB) ListVideos(limit, offset int) ([]VideoMetadata, error) {
	rows, err := s.db.Query(`
		SELECT 
			id, camera_name, local_path, hls_path, hls_url,
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url,
			r2_preview_mp4_path, r2_preview_mp4_url, r2_preview_png_path, r2_preview_png_url,
			unique_id, order_detail_id, booking_id, raw_json, status, error, created_at, finished_at, uploaded_at,
			size, duration, resolution, has_request, last_check_file, video_type, request_id, storage_disk_id, mp4_full_path, deprecated_hls
		FROM videos 
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)

	if err != nil {
		return nil, fmt.Errorf("failed to list videos: %v", err)
	}
	defer rows.Close()

	var videos []VideoMetadata
	for rows.Next() {
		var video VideoMetadata
		var finishedAt, uploadedAt, lastCheckFile sql.NullTime
		var cameraName, uniqueID, orderDetailID, bookingID, rawJSON, videoType, requestID, storageDiskID, mp4FullPath sql.NullString
		var hasRequest, deprecatedHLS sql.NullBool

		err := rows.Scan(
			&video.ID,
			&cameraName,
			&video.LocalPath,
			&video.HLSPath,
			&video.HLSURL,
			&video.R2HLSPath,
			&video.R2MP4Path,
			&video.R2HLSURL,
			&video.R2MP4URL,
			&video.R2PreviewMP4Path,
			&video.R2PreviewMP4URL,
			&video.R2PreviewPNGPath,
			&video.R2PreviewPNGURL,
			&uniqueID,
			&orderDetailID,
			&bookingID,
			&rawJSON,
			&video.Status,
			&video.ErrorMessage,
			&video.CreatedAt,
			&finishedAt,
			&uploadedAt,
			&video.Size,
			&video.Duration,
			&video.Resolution,
			&hasRequest,
			&lastCheckFile,
			&videoType,
			&requestID,
			&storageDiskID,
			&mp4FullPath,
			&deprecatedHLS,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan video row: %v", err)
		}

		if cameraName.Valid {
			video.CameraName = cameraName.String
		}
		if finishedAt.Valid {
			video.FinishedAt = &finishedAt.Time
		}
		if uploadedAt.Valid {
			video.UploadedAt = &uploadedAt.Time
		}
		if uniqueID.Valid {
			video.UniqueID = uniqueID.String
		}
		if orderDetailID.Valid {
			video.OrderDetailID = orderDetailID.String
		}
		if bookingID.Valid {
			video.BookingID = bookingID.String
		}
		if rawJSON.Valid {
			video.RawJSON = rawJSON.String
		}
		if hasRequest.Valid {
			video.HasRequest = hasRequest.Bool
		}
		if lastCheckFile.Valid {
			video.LastCheckFile = &lastCheckFile.Time
		}
		if videoType.Valid {
			video.VideoType = videoType.String
		}
		if requestID.Valid {
			video.RequestID = requestID.String
		}
		if storageDiskID.Valid {
			video.StorageDiskID = storageDiskID.String
		}
		if mp4FullPath.Valid {
			video.MP4FullPath = mp4FullPath.String
		}
		if deprecatedHLS.Valid {
			video.DeprecatedHLS = deprecatedHLS.Bool
		}

		videos = append(videos, video)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error after scanning rows: %v", err)
	}

	return videos, nil
}

// GetVideosByStatus retrieves videos with a specific status
func (s *SQLiteDB) GetVideosByStatus(status VideoStatus, limit, offset int) ([]VideoMetadata, error) {
	rows, err := s.db.Query(`
		SELECT 
			id, camera_name, local_path, hls_path, hls_url,
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url,
			r2_preview_mp4_path, r2_preview_mp4_url, r2_preview_png_path, r2_preview_png_url,
			unique_id, order_detail_id, booking_id, raw_json, status, error, created_at, finished_at, uploaded_at,
			size, duration, resolution
		FROM videos 
		WHERE status = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, status, limit, offset)

	if err != nil {
		return nil, fmt.Errorf("failed to get videos by status: %v", err)
	}
	defer rows.Close()

	var videos []VideoMetadata
	for rows.Next() {
		var video VideoMetadata
		var finishedAt, uploadedAt sql.NullTime
		var cameraName, uniqueID, orderDetailID, bookingID, rawJSON sql.NullString

		err := rows.Scan(
			&video.ID,
			&cameraName,
			&video.LocalPath,
			&video.HLSPath,
			&video.HLSURL,
			&video.R2HLSPath,
			&video.R2MP4Path,
			&video.R2HLSURL,
			&video.R2MP4URL,
			&video.R2PreviewMP4Path,
			&video.R2PreviewMP4URL,
			&video.R2PreviewPNGPath,
			&video.R2PreviewPNGURL,
			&uniqueID,
			&orderDetailID,
			&bookingID,
			&rawJSON,
			&video.Status,
			&video.ErrorMessage,
			&video.CreatedAt,
			&finishedAt,
			&uploadedAt,
			&video.Size,
			&video.Duration,
			&video.Resolution,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan video row: %v", err)
		}

		if cameraName.Valid {
			video.CameraName = cameraName.String
		}
		if finishedAt.Valid {
			video.FinishedAt = &finishedAt.Time
		}
		if uploadedAt.Valid {
			video.UploadedAt = &uploadedAt.Time
		}
		if uniqueID.Valid {
			video.UniqueID = uniqueID.String
		}
		if orderDetailID.Valid {
			video.OrderDetailID = orderDetailID.String
		}
		if bookingID.Valid {
			video.BookingID = bookingID.String
		}
		if rawJSON.Valid {
			video.RawJSON = rawJSON.String
		}

		videos = append(videos, video)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error after scanning rows: %v", err)
	}

	return videos, nil
}

// UpdateVideoStatus updates the status and optional error message of a video
func (s *SQLiteDB) UpdateVideoStatus(id string, status VideoStatus, errorMsg string) error {
	var updateSQL string
	var args []interface{}

	// If status is ready or failed, also update the finished_at time
	if status == StatusReady || status == StatusFailed {
		now := time.Now()
		updateSQL = `
			UPDATE videos 
			SET 
				status = ?,
				error = ?,
				finished_at = ?
			WHERE id = ?
		`
		args = []interface{}{status, errorMsg, now, id}

		// Log for debugging purposes
		log.Printf("Setting video %s status to %s with finished_at=%s", id, status, now.Format(time.RFC3339))
	} else {
		// For other statuses, just update status and error
		updateSQL = `
			UPDATE videos 
			SET 
				status = ?,
				error = ?
			WHERE id = ?
		`
		args = []interface{}{status, errorMsg, id}
	}

	// Execute the update
	_, err := s.db.Exec(updateSQL, args...)
	if err != nil {
		return fmt.Errorf("failed to update video status: %v", err)
	}

	// Add debug logging to verify the update worked
	updatedVideo, getErr := s.GetVideo(id)
	if getErr == nil && updatedVideo != nil {
		log.Printf("Updated video %s status to %s, VideoType=%s", id, status, updatedVideo.VideoType)
	}

	return nil
}

// DeleteVideo removes a video record by its ID
func (s *SQLiteDB) DeleteVideo(id string) error {
	_, err := s.db.Exec("DELETE FROM videos WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete video: %v", err)
	}

	return nil
}

// GetVideosByBookingID returns all videos for a specific booking ID
func (s *SQLiteDB) GetVideosByBookingID(bookingID string) ([]VideoMetadata, error) {
	rows, err := s.db.Query(`
		SELECT 
			id, camera_name, local_path, hls_path, hls_url, 
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url, 
			r2_preview_mp4_path, r2_preview_mp4_url, r2_preview_png_path, r2_preview_png_url,
			unique_id, order_detail_id, booking_id, raw_json, status, error, created_at, finished_at, uploaded_at,
			size, duration, resolution, has_request, last_check_file, video_type
		FROM videos 
		WHERE booking_id = ?
		ORDER BY created_at DESC
	`, bookingID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var videos []VideoMetadata
	for rows.Next() {
		var video VideoMetadata
		var createdAt, finishedAt, uploadedAt, lastCheckFile sql.NullTime
		var status string
		var orderDetailID, resolution, videoType sql.NullString
		var hasRequest sql.NullBool

		err := rows.Scan(
			&video.ID, &video.CameraName, &video.LocalPath, &video.HLSPath, &video.HLSURL,
			&video.R2HLSPath, &video.R2MP4Path, &video.R2HLSURL, &video.R2MP4URL,
			&video.R2PreviewMP4Path, &video.R2PreviewMP4URL, &video.R2PreviewPNGPath, &video.R2PreviewPNGURL,
			&video.UniqueID, &orderDetailID, &video.BookingID, &video.RawJSON, &status, &video.ErrorMessage,
			&createdAt, &finishedAt, &uploadedAt,
			&video.Size, &video.Duration, &resolution, &hasRequest, &lastCheckFile, &videoType,
		)
		if err != nil {
			return nil, err
		}

		// Convert string status to VideoStatus enum
		switch status {
		case "pending":
			video.Status = StatusPending
		case "processing":
			video.Status = StatusProcessing
		case "uploading":
			video.Status = StatusUploading
		case "ready":
			video.Status = StatusReady
		case "failed":
			video.Status = StatusFailed
		case "initial":
			video.Status = StatusInitial
		default:
			video.Status = StatusPending
		}
		if createdAt.Valid {
			video.CreatedAt = createdAt.Time
		}
		if finishedAt.Valid {
			video.FinishedAt = &finishedAt.Time
		}
		if uploadedAt.Valid {
			video.UploadedAt = &uploadedAt.Time
		}
		if orderDetailID.Valid {
			video.OrderDetailID = orderDetailID.String
		}

		// Set resolution if valid, otherwise leave as empty string
		if resolution.Valid {
			video.Resolution = resolution.String
		} else {
			video.Resolution = "" // Set default empty value for NULL resolution
		}

		// Set HasRequest if valid
		if hasRequest.Valid {
			video.HasRequest = hasRequest.Bool
		}

		// Set LastCheckFile if valid
		if lastCheckFile.Valid {
			video.LastCheckFile = &lastCheckFile.Time
		}

		// Set VideoType if valid
		if videoType.Valid {
			video.VideoType = videoType.String
		} else {
			video.VideoType = "" // Default empty value for NULL video_type
		}

		videos = append(videos, video)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return videos, nil
}

// GetVideoByUniqueID returns a video with the specified unique ID
func (s *SQLiteDB) GetVideoByUniqueID(uniqueID string) (*VideoMetadata, error) {
	var video VideoMetadata
	var createdAt, finishedAt, uploadedAt, lastCheckFile sql.NullTime
	var status string
	var orderDetailID, resolution, videoType, requestID sql.NullString
	var hasRequest sql.NullBool

	err := s.db.QueryRow(`
		SELECT 
			id, camera_name, local_path, hls_path, hls_url, 
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url, 
			r2_preview_mp4_path, r2_preview_mp4_url, r2_preview_png_path, r2_preview_png_url,
			unique_id, order_detail_id, booking_id, raw_json, status, error, created_at, finished_at, uploaded_at,
			size, duration, resolution, has_request, last_check_file, video_type, request_id
		FROM videos 
		WHERE unique_id = ?
	`, uniqueID).Scan(
		&video.ID, &video.CameraName, &video.LocalPath, &video.HLSPath, &video.HLSURL,
		&video.R2HLSPath, &video.R2MP4Path, &video.R2HLSURL, &video.R2MP4URL,
		&video.R2PreviewMP4Path, &video.R2PreviewMP4URL, &video.R2PreviewPNGPath, &video.R2PreviewPNGURL,
		&video.UniqueID, &orderDetailID, &video.BookingID, &video.RawJSON, &status, &video.ErrorMessage,
		&createdAt, &finishedAt, &uploadedAt,
		&video.Size, &video.Duration, &resolution, &hasRequest, &lastCheckFile, &videoType,
		&requestID,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No video found with this unique ID
		}
		return nil, err
	}

	// Convert string status to VideoStatus enum
	switch status {
	case "pending":
		video.Status = StatusPending
	case "processing":
		video.Status = StatusProcessing
	case "uploading":
		video.Status = StatusUploading
	case "ready":
		video.Status = StatusReady
	case "failed":
		video.Status = StatusFailed
	default:
		video.Status = StatusPending
	}

	if createdAt.Valid {
		video.CreatedAt = createdAt.Time
	}
	if finishedAt.Valid {
		video.FinishedAt = &finishedAt.Time
	}
	if uploadedAt.Valid {
		video.UploadedAt = &uploadedAt.Time
	}
	if orderDetailID.Valid {
		video.OrderDetailID = orderDetailID.String
	}

	// Set resolution if valid, otherwise leave as empty string
	if resolution.Valid {
		video.Resolution = resolution.String
	} else {
		video.Resolution = "" // Set default empty value for NULL resolution
	}

	// Set VideoType if valid, otherwise leave as empty string
	if videoType.Valid {
		video.VideoType = videoType.String
	} else {
		video.VideoType = "" // Set default empty value for NULL video_type
	}
	// Set RequestID if valid, otherwise leave as empty string
	if requestID.Valid {
		video.RequestID = requestID.String
	} else {
		video.RequestID = "" // Set default empty value for NULL request_id
	}

	return &video, nil
}

// UpdateLastCheckFile updates the last_check_file timestamp for a video
func (s *SQLiteDB) UpdateLastCheckFile(id string, lastCheckTime time.Time) error {
	_, err := s.db.Exec(`
		UPDATE videos SET
			last_check_file = ?
		WHERE id = ?
	`, lastCheckTime, id)

	return err
}

// UpdateVideoRequestID updates the request_id for a video
func (s *SQLiteDB) UpdateVideoRequestID(id string, requestID string, remove bool) error {
	exists, err := s.GetVideo(id)
	if err != nil {
		return err
	}
	if exists == nil {
		return fmt.Errorf("video with ID %s does not exist", id)
	}

	var finalRequestID string
	if remove {
		// Jika remove true, hapus requestID dari daftar
		requestIDs := strings.Split(exists.RequestID, ",")
		var newRequestIDs []string
		for _, rid := range requestIDs {
			if rid != requestID {
				newRequestIDs = append(newRequestIDs, rid)
			}
		}
		finalRequestID = strings.Join(newRequestIDs, ",")
	} else {
		// Jika remove false, tambahkan requestID ke daftar
		if exists.RequestID != "" {
			finalRequestID = exists.RequestID + "," + requestID
		} else {
			finalRequestID = requestID
		}
	}

	_, err = s.db.Exec(`
		UPDATE videos SET
			request_id = ?
		WHERE id = ?
	`, finalRequestID, id)

	return err
}

// Storage disk operations

// CreateStorageDisk creates a new storage disk record
func (s *SQLiteDB) CreateStorageDisk(disk StorageDisk) error {
	_, err := s.db.Exec(`
		INSERT INTO storage_disks (
			id, path, total_space_gb, available_space_gb, is_active, priority_order, last_scan, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		disk.ID, disk.Path, disk.TotalSpaceGB, disk.AvailableSpaceGB, 
		disk.IsActive, disk.PriorityOrder, disk.LastScan, disk.CreatedAt,
	)
	return err
}

// GetStorageDisks retrieves all storage disks
func (s *SQLiteDB) GetStorageDisks() ([]StorageDisk, error) {
	rows, err := s.db.Query(`
		SELECT id, path, total_space_gb, available_space_gb, is_active, priority_order, last_scan, created_at
		FROM storage_disks
		ORDER BY priority_order ASC, created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var disks []StorageDisk
	for rows.Next() {
		var disk StorageDisk
		err := rows.Scan(
			&disk.ID, &disk.Path, &disk.TotalSpaceGB, &disk.AvailableSpaceGB,
			&disk.IsActive, &disk.PriorityOrder, &disk.LastScan, &disk.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		disks = append(disks, disk)
	}

	return disks, rows.Err()
}

// GetActiveDisk retrieves the currently active disk for recording
func (s *SQLiteDB) GetActiveDisk() (*StorageDisk, error) {
	var disk StorageDisk
	err := s.db.QueryRow(`
		SELECT id, path, total_space_gb, available_space_gb, is_active, priority_order, last_scan, created_at
		FROM storage_disks
		WHERE is_active = 1
		ORDER BY priority_order ASC
		LIMIT 1
	`).Scan(
		&disk.ID, &disk.Path, &disk.TotalSpaceGB, &disk.AvailableSpaceGB,
		&disk.IsActive, &disk.PriorityOrder, &disk.LastScan, &disk.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &disk, nil
}

// UpdateDiskSpace updates the space information for a storage disk
func (s *SQLiteDB) UpdateDiskSpace(id string, totalGB, availableGB int64) error {
	_, err := s.db.Exec(`
		UPDATE storage_disks 
		SET total_space_gb = ?, available_space_gb = ?, last_scan = ?
		WHERE id = ?
	`, totalGB, availableGB, time.Now(), id)

	return err
}

// SetActiveDisk sets a disk as active and deactivates all others
func (s *SQLiteDB) SetActiveDisk(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Deactivate all disks
	_, err = tx.Exec("UPDATE storage_disks SET is_active = 0")
	if err != nil {
		return err
	}

	// Activate the specified disk
	_, err = tx.Exec("UPDATE storage_disks SET is_active = 1 WHERE id = ?", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetStorageDisk retrieves a specific storage disk by ID
func (s *SQLiteDB) GetStorageDisk(id string) (*StorageDisk, error) {
	var disk StorageDisk
	err := s.db.QueryRow(`
		SELECT id, path, total_space_gb, available_space_gb, is_active, priority_order, last_scan, created_at
		FROM storage_disks
		WHERE id = ?
	`, id).Scan(
		&disk.ID, &disk.Path, &disk.TotalSpaceGB, &disk.AvailableSpaceGB,
		&disk.IsActive, &disk.PriorityOrder, &disk.LastScan, &disk.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &disk, nil
}

// Recording segment operations

// CreateRecordingSegment creates a new recording segment record
func (s *SQLiteDB) CreateRecordingSegment(segment RecordingSegment) error {
	_, err := s.db.Exec(`
		INSERT INTO recording_segments (
			id, camera_name, storage_disk_id, mp4_path, segment_start, segment_end, file_size_bytes, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		segment.ID, segment.CameraName, segment.StorageDiskID, segment.MP4Path,
		segment.SegmentStart, segment.SegmentEnd, segment.FileSizeBytes, segment.CreatedAt,
	)
	return err
}

// GetRecordingSegments retrieves recording segments for a camera within a time range
func (s *SQLiteDB) GetRecordingSegments(cameraName string, start, end time.Time) ([]RecordingSegment, error) {
	rows, err := s.db.Query(`
		SELECT rs.id, rs.camera_name, rs.storage_disk_id, rs.mp4_path, 
			   rs.segment_start, rs.segment_end, rs.file_size_bytes, rs.created_at
		FROM recording_segments rs
		WHERE rs.camera_name = ? 
		  AND rs.segment_start <= ? 
		  AND rs.segment_end >= ?
		ORDER BY rs.segment_start ASC
	`, cameraName, end, start)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var segments []RecordingSegment
	for rows.Next() {
		var segment RecordingSegment
		err := rows.Scan(
			&segment.ID, &segment.CameraName, &segment.StorageDiskID, &segment.MP4Path,
			&segment.SegmentStart, &segment.SegmentEnd, &segment.FileSizeBytes, &segment.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}

	return segments, rows.Err()
}

// DeleteRecordingSegment deletes a recording segment record
func (s *SQLiteDB) DeleteRecordingSegment(id string) error {
	_, err := s.db.Exec("DELETE FROM recording_segments WHERE id = ?", id)
	return err
}

// GetRecordingSegmentsByDisk retrieves all recording segments for a specific disk
func (s *SQLiteDB) GetRecordingSegmentsByDisk(diskID string) ([]RecordingSegment, error) {
	rows, err := s.db.Query(`
		SELECT id, camera_name, storage_disk_id, mp4_path, 
			   segment_start, segment_end, file_size_bytes, created_at
		FROM recording_segments
		WHERE storage_disk_id = ?
		ORDER BY created_at DESC
	`, diskID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var segments []RecordingSegment
	for rows.Next() {
		var segment RecordingSegment
		err := rows.Scan(
			&segment.ID, &segment.CameraName, &segment.StorageDiskID, &segment.MP4Path,
			&segment.SegmentStart, &segment.SegmentEnd, &segment.FileSizeBytes, &segment.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}

	return segments, rows.Err()
}

// Close closes the database connection
func (s *SQLiteDB) Close() error {
	return s.db.Close()
}

// GetDB returns the underlying *sql.DB instance
func (s *SQLiteDB) GetDB() *sql.DB {
	return s.db
}

// Offline Queue Methods

// CreatePendingTask creates a new pending task
func (s *SQLiteDB) CreatePendingTask(task PendingTask) error {
	_, err := s.db.Exec(`
		INSERT INTO pending_tasks (
			task_type, task_data, attempts, max_attempts, next_retry_at, 
			status, created_at, updated_at, error_msg
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.TaskType,
		task.TaskData,
		task.Attempts,
		task.MaxAttempts,
		task.NextRetryAt,
		task.Status,
		task.CreatedAt,
		task.UpdatedAt,
		task.ErrorMsg,
	)
	return err
}

// GetPendingTasks retrieves pending tasks ready for execution
func (s *SQLiteDB) GetPendingTasks(limit int) ([]PendingTask, error) {
	rows, err := s.db.Query(`
		SELECT id, task_type, task_data, attempts, max_attempts, next_retry_at,
			   status, created_at, updated_at, error_msg
		FROM pending_tasks 
		WHERE status IN ('pending', 'failed') 
		  AND (next_retry_at IS NULL OR next_retry_at <= datetime('now', 'localtime'))
		ORDER BY created_at ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []PendingTask
	for rows.Next() {
		var task PendingTask
		var nextRetryAt sql.NullTime
		var errorMsg sql.NullString

		err := rows.Scan(
			&task.ID,
			&task.TaskType,
			&task.TaskData,
			&task.Attempts,
			&task.MaxAttempts,
			&nextRetryAt,
			&task.Status,
			&task.CreatedAt,
			&task.UpdatedAt,
			&errorMsg,
		)
		if err != nil {
			return nil, err
		}

		if nextRetryAt.Valid {
			task.NextRetryAt = nextRetryAt.Time
		}
		if errorMsg.Valid {
			task.ErrorMsg = errorMsg.String
		}

		tasks = append(tasks, task)
	}

	return tasks, nil
}

// UpdateTaskStatus updates the status of a task
func (s *SQLiteDB) UpdateTaskStatus(taskID int, status string, errorMsg string) error {
	_, err := s.db.Exec(`
		UPDATE pending_tasks 
		SET status = ?, error_msg = ?, updated_at = datetime('now', 'localtime')
		WHERE id = ?`,
		status, errorMsg, taskID)
	return err
}

// UpdateTaskNextRetry updates the next retry time and attempt count
func (s *SQLiteDB) UpdateTaskNextRetry(taskID int, nextRetryAt time.Time, attempts int) error {
	_, err := s.db.Exec(`
		UPDATE pending_tasks 
		SET next_retry_at = ?, attempts = ?, status = 'failed', updated_at = datetime('now', 'localtime')
		WHERE id = ?`,
		nextRetryAt, attempts, taskID)
	return err
}

// DeleteCompletedTasks removes completed tasks older than specified time
func (s *SQLiteDB) DeleteCompletedTasks(olderThan time.Time) error {
	_, err := s.db.Exec(`
		DELETE FROM pending_tasks 
		WHERE status = 'completed' AND updated_at < ?`,
		olderThan)
	return err
}

// GetTaskByID retrieves a specific task by ID
func (s *SQLiteDB) GetTaskByID(taskID int) (*PendingTask, error) {
	var task PendingTask
	var nextRetryAt sql.NullTime
	var errorMsg sql.NullString

	err := s.db.QueryRow(`
		SELECT id, task_type, task_data, attempts, max_attempts, next_retry_at,
			   status, created_at, updated_at, error_msg
		FROM pending_tasks WHERE id = ?`, taskID).Scan(
		&task.ID,
		&task.TaskType,
		&task.TaskData,
		&task.Attempts,
		&task.MaxAttempts,
		&nextRetryAt,
		&task.Status,
		&task.CreatedAt,
		&task.UpdatedAt,
		&errorMsg,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if nextRetryAt.Valid {
		task.NextRetryAt = nextRetryAt.Time
	}
	if errorMsg.Valid {
		task.ErrorMsg = errorMsg.String
	}

	return &task, nil
}

// CreateOrUpdateBooking creates a new booking or updates existing one
func (s *SQLiteDB) CreateOrUpdateBooking(booking BookingData) error {
	// Check if booking already exists
	existingBooking, err := s.GetBookingByID(booking.BookingID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("error checking existing booking: %v", err)
	}

	now := time.Now()
	booking.LastSyncAt = now

	if existingBooking != nil {
		// Update existing booking
		booking.UpdatedAt = now
		booking.ID = existingBooking.ID // Preserve original ID

		_, err = s.db.Exec(`
			UPDATE bookings SET
				order_detail_id = ?,
				field_id = ?,
				date = ?,
				start_time = ?,
				end_time = ?,
				booking_source = ?,
				status = ?,
				updated_at = ?,
				raw_json = ?,
				last_sync_at = ?
			WHERE booking_id = ?`,
			booking.OrderDetailID,
			booking.FieldID,
			booking.Date,
			booking.StartTime,
			booking.EndTime,
			booking.BookingSource,
			booking.Status,
			booking.UpdatedAt,
			booking.RawJSON,
			booking.LastSyncAt,
			booking.BookingID,
		)
		
		if err != nil {
			return fmt.Errorf("error updating booking: %v", err)
		}
		
		log.Printf("📅 BOOKING: Updated booking %s (status: %s)", booking.BookingID, booking.Status)
	} else {
		// Create new booking
		booking.CreatedAt = now
		booking.UpdatedAt = now

		_, err = s.db.Exec(`
			INSERT INTO bookings (
				booking_id, order_detail_id, field_id, date, start_time, end_time,
				booking_source, status, created_at, updated_at, raw_json, last_sync_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			booking.BookingID,
			booking.OrderDetailID,
			booking.FieldID,
			booking.Date,
			booking.StartTime,
			booking.EndTime,
			booking.BookingSource,
			booking.Status,
			booking.CreatedAt,
			booking.UpdatedAt,
			booking.RawJSON,
			booking.LastSyncAt,
		)
		
		if err != nil {
			return fmt.Errorf("error creating booking: %v", err)
		}
		
		log.Printf("📅 BOOKING: Created new booking %s (status: %s)", booking.BookingID, booking.Status)
	}

	return nil
}

// GetBookingByID retrieves a booking by its booking ID
func (s *SQLiteDB) GetBookingByID(bookingID string) (*BookingData, error) {
	var booking BookingData
	var createdAt, updatedAt, lastSyncAt sql.NullTime
	var orderDetailID, fieldID sql.NullInt64
	var date, startTime, endTime, bookingSource, status, rawJSON sql.NullString

	err := s.db.QueryRow(`
		SELECT id, booking_id, order_detail_id, field_id, date, start_time, end_time,
			booking_source, status, created_at, updated_at, raw_json, last_sync_at
		FROM bookings WHERE booking_id = ?`, bookingID).Scan(
		&booking.ID,
		&booking.BookingID,
		&orderDetailID,
		&fieldID,
		&date,
		&startTime,
		&endTime,
		&bookingSource,
		&status,
		&createdAt,
		&updatedAt,
		&rawJSON,
		&lastSyncAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Handle nullable fields
	if orderDetailID.Valid {
		booking.OrderDetailID = int(orderDetailID.Int64)
	}
	if fieldID.Valid {
		booking.FieldID = int(fieldID.Int64)
	}
	if date.Valid {
		booking.Date = date.String
	}
	if startTime.Valid {
		booking.StartTime = startTime.String
	}
	if endTime.Valid {
		booking.EndTime = endTime.String
	}
	if bookingSource.Valid {
		booking.BookingSource = bookingSource.String
	}
	if status.Valid {
		booking.Status = status.String
	}
	if rawJSON.Valid {
		booking.RawJSON = rawJSON.String
	}
	if createdAt.Valid {
		booking.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		booking.UpdatedAt = updatedAt.Time
	}
	if lastSyncAt.Valid {
		booking.LastSyncAt = lastSyncAt.Time
	}

	return &booking, nil
}

// GetBookingsByDate retrieves all bookings for a specific date
func (s *SQLiteDB) GetBookingsByDate(date string) ([]BookingData, error) {
	rows, err := s.db.Query(`
		SELECT id, booking_id, order_detail_id, field_id, date, start_time, end_time,
			booking_source, status, created_at, updated_at, raw_json, last_sync_at
		FROM bookings 
		WHERE date = ?
		ORDER BY start_time ASC`, date)

	if err != nil {
		return nil, fmt.Errorf("error getting bookings by date: %v", err)
	}
	defer rows.Close()

	return s.scanBookings(rows)
}

// GetBookingsByStatus retrieves all bookings with a specific status
func (s *SQLiteDB) GetBookingsByStatus(status string) ([]BookingData, error) {
	rows, err := s.db.Query(`
		SELECT id, booking_id, order_detail_id, field_id, date, start_time, end_time,
			booking_source, status, created_at, updated_at, raw_json, last_sync_at
		FROM bookings 
		WHERE status = ?
		ORDER BY date DESC, start_time ASC`, status)

	if err != nil {
		return nil, fmt.Errorf("error getting bookings by status: %v", err)
	}
	defer rows.Close()

	return s.scanBookings(rows)
}

// UpdateBookingStatus updates only the status of a booking
func (s *SQLiteDB) UpdateBookingStatus(bookingID string, status string) error {
	now := time.Now()
	
	result, err := s.db.Exec(`
		UPDATE bookings SET 
			status = ?, 
			updated_at = ?,
			last_sync_at = ?
		WHERE booking_id = ?`,
		status, now, now, bookingID)
	
	if err != nil {
		return fmt.Errorf("error updating booking status: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %v", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no booking found with ID: %s", bookingID)
	}

	log.Printf("📅 BOOKING: Updated status for booking %s to %s", bookingID, status)
	return nil
}

// DeleteOldBookings removes bookings older than specified time
func (s *SQLiteDB) DeleteOldBookings(olderThan time.Time) error {
	result, err := s.db.Exec(`
		DELETE FROM bookings 
		WHERE created_at < ?`,
		olderThan)
	
	if err != nil {
		return fmt.Errorf("error deleting old bookings: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %v", err)
	}

	log.Printf("📅 BOOKING: Deleted %d old bookings", rowsAffected)
	return nil
}

// scanBookings is a helper function to scan multiple booking rows
func (s *SQLiteDB) scanBookings(rows *sql.Rows) ([]BookingData, error) {
	var bookings []BookingData

	for rows.Next() {
		var booking BookingData
		var createdAt, updatedAt, lastSyncAt sql.NullTime
		var orderDetailID, fieldID sql.NullInt64
		var date, startTime, endTime, bookingSource, status, rawJSON sql.NullString

		err := rows.Scan(
			&booking.ID,
			&booking.BookingID,
			&orderDetailID,
			&fieldID,
			&date,
			&startTime,
			&endTime,
			&bookingSource,
			&status,
			&createdAt,
			&updatedAt,
			&rawJSON,
			&lastSyncAt,
		)

		if err != nil {
			return nil, fmt.Errorf("error scanning booking row: %v", err)
		}

		// Handle nullable fields
		if orderDetailID.Valid {
			booking.OrderDetailID = int(orderDetailID.Int64)
		}
		if fieldID.Valid {
			booking.FieldID = int(fieldID.Int64)
		}
		if date.Valid {
			booking.Date = date.String
		}
		if startTime.Valid {
			booking.StartTime = startTime.String
		}
		if endTime.Valid {
			booking.EndTime = endTime.String
		}
		if bookingSource.Valid {
			booking.BookingSource = bookingSource.String
		}
		if status.Valid {
			booking.Status = status.String
		}
		if rawJSON.Valid {
			booking.RawJSON = rawJSON.String
		}
		if createdAt.Valid {
			booking.CreatedAt = createdAt.Time
		}
		if updatedAt.Valid {
			booking.UpdatedAt = updatedAt.Time
		}
		if lastSyncAt.Valid {
			booking.LastSyncAt = lastSyncAt.Time
		}

		bookings = append(bookings, booking)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating booking rows: %v", err)
	}

	return bookings, nil
}

// UpdateCameraConfig updates specific configuration fields for a camera
func (s *SQLiteDB) UpdateCameraConfig(cameraName string, resolution string, frameRate int, autoDelete int, width int, height int) error {
	_, err := s.db.Exec(`
		UPDATE cameras SET 
			resolution = ?, 
			frame_rate = ?, 
			auto_delete = ?,
			width = ?,
			height = ?
		WHERE name = ?`,
		resolution, frameRate, autoDelete, width, height, cameraName)
	
	if err != nil {
		return fmt.Errorf("error updating camera config: %v", err)
	}
	
	log.Printf("📹 CAMERA: Updated config for %s - Resolution: %s (%dx%d), FrameRate: %d, AutoDelete: %d", 
		cameraName, resolution, width, height, frameRate, autoDelete)
	return nil
}
