package database

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteDB implements the Database interface using SQLite
type SQLiteDB struct {
	db *sql.DB
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
	// Create config table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create config table: %v", err)
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
			last_check_file DATETIME
		)
	`)
	if err != nil {
		return err
	}
	
	// Migrasi: Coba tambahkan kolom-kolom yang mungkin belum ada
	// Ini akan gagal dengan error tetapi tidak kritis jika kolom sudah ada
	_, migrationErr := db.Exec("ALTER TABLE videos ADD COLUMN camera_name TEXT")
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
	// Create index on status
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_videos_status ON videos (status)
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
			size, duration, resolution, has_request, last_check_file
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
	)
	return err
}

// GetVideo retrieves a video record by ID
func (s *SQLiteDB) GetVideo(id string) (*VideoMetadata, error) {
	var video VideoMetadata
	var finishedAt, uploadedAt, lastCheckFile sql.NullTime
	var cameraName, uniqueID, orderDetailID, bookingID, rawJSON, resolution sql.NullString

	err := s.db.QueryRow(`
		SELECT id, camera_name, local_path, hls_path, hls_url,
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url,
			r2_preview_mp4_path, r2_preview_mp4_url, r2_preview_png_path, r2_preview_png_url,
			unique_id, order_detail_id, booking_id, raw_json, status, error, created_at, finished_at, uploaded_at,
			size, duration, resolution, has_request, last_check_file
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
	if resolution.Valid {
		video.Resolution = resolution.String
	} else {
		video.Resolution = "" // Set default empty value for NULL resolution
	}

	return &video, nil
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
			has_request = ?
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
			size, duration, resolution
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
	// Pertama, cek apakah kita perlu mendapatkan data lama untuk dipertahankan
	if status == StatusReady || status == StatusFailed {
		// Dapatkan data video saat ini
		currentVideo, err := s.GetVideo(id)
		if err != nil {
			return fmt.Errorf("failed to get current video data: %v", err)
		}

		// Pastikan field-field penting dari video tetap terisi
		// dengan mempertahankan nilai yang sudah ada
		now := time.Now()
		
		// Update data video yang ada dengan status baru dan finished_at
		currentVideo.Status = status
		currentVideo.ErrorMessage = errorMsg
		currentVideo.FinishedAt = &now
		
		// Periksa apakah field-field penting kosong
		if currentVideo.CameraName == "" || currentVideo.LocalPath == "" || 
		   currentVideo.UniqueID == "" || currentVideo.OrderDetailID == "" || 
		   currentVideo.BookingID == "" {
			log.Printf("Warning: Some important fields are empty for video %s. This might cause issues.", id)
		}
		
		// Update video menggunakan UpdateVideo untuk memastikan semua field terjaga
		return s.UpdateVideo(*currentVideo)
	} else {
		// Untuk status selain ready dan failed, cukup perbarui status dan error
		_, err := s.db.Exec(`
			UPDATE videos 
			SET 
				status = ?,
				error = ?
			WHERE id = ?
		`, status, errorMsg, id)

		if err != nil {
			return fmt.Errorf("failed to update video status: %v", err)
		}
	}

	log.Printf("Updated video %s status to %s", id, status)
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
			size, duration, resolution, has_request, last_check_file
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
		var orderDetailID sql.NullString
		var resolution sql.NullString
		var hasRequest sql.NullBool

		err := rows.Scan(
			&video.ID, &video.CameraName, &video.LocalPath, &video.HLSPath, &video.HLSURL,
			&video.R2HLSPath, &video.R2MP4Path, &video.R2HLSURL, &video.R2MP4URL,
			&video.R2PreviewMP4Path, &video.R2PreviewMP4URL, &video.R2PreviewPNGPath, &video.R2PreviewPNGURL,
			&video.UniqueID, &orderDetailID, &video.BookingID, &video.RawJSON, &status, &video.ErrorMessage,
			&createdAt, &finishedAt, &uploadedAt,
			&video.Size, &video.Duration, &resolution, &hasRequest, &lastCheckFile,
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
	var orderDetailID sql.NullString
	var resolution sql.NullString
	var hasRequest sql.NullBool

	err := s.db.QueryRow(`
		SELECT 
			id, camera_name, local_path, hls_path, hls_url, 
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url, 
			r2_preview_mp4_path, r2_preview_mp4_url, r2_preview_png_path, r2_preview_png_url,
			unique_id, order_detail_id, booking_id, raw_json, status, error, created_at, finished_at, uploaded_at,
			size, duration, resolution, has_request, last_check_file
		FROM videos 
		WHERE unique_id = ?
	`, uniqueID).Scan(
		&video.ID, &video.CameraName, &video.LocalPath, &video.HLSPath, &video.HLSURL,
		&video.R2HLSPath, &video.R2MP4Path, &video.R2HLSURL, &video.R2MP4URL,
		&video.R2PreviewMP4Path, &video.R2PreviewMP4URL, &video.R2PreviewPNGPath, &video.R2PreviewPNGURL,
		&video.UniqueID, &orderDetailID, &video.BookingID, &video.RawJSON, &status, &video.ErrorMessage,
		&createdAt, &finishedAt, &uploadedAt,
		&video.Size, &video.Duration, &resolution, &hasRequest, &lastCheckFile,
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

// Close closes the database connection
func (s *SQLiteDB) Close() error {
	return s.db.Close()
}

// GetDB returns the underlying *sql.DB instance
func (s *SQLiteDB) GetDB() *sql.DB {
	return s.db
}
