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
	// Create videos table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS videos (
			id TEXT PRIMARY KEY,
			created_at TIMESTAMP NOT NULL,
			finished_at TIMESTAMP,
			status TEXT NOT NULL,
			duration REAL DEFAULT 0,
			size INTEGER DEFAULT 0,
			local_path TEXT,
			hls_path TEXT,
			dash_path TEXT,
			hls_url TEXT,
			dash_url TEXT,
			r2_hls_path TEXT,
			r2_dash_path TEXT,
			r2_mp4_path TEXT,
			r2_hls_url TEXT,
			r2_dash_url TEXT,
			r2_mp4_url TEXT,
			camera_id TEXT,
			error_message TEXT
		)
	`)
	if err != nil {
		return err
	}

	// Check if r2_mp4_path column exists, if not add it
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('videos') WHERE name='r2_mp4_path'`).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		// Column doesn't exist, add it
		_, err = db.Exec(`ALTER TABLE videos ADD COLUMN r2_mp4_path TEXT`)
		if err != nil {
			return err
		}
		log.Println("Added r2_mp4_path column to videos table")
	}

	// Check if r2_mp4_url column exists, if not add it
	count = 0
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('videos') WHERE name='r2_mp4_url'`).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		// Column doesn't exist, add it
		_, err = db.Exec(`ALTER TABLE videos ADD COLUMN r2_mp4_url TEXT`)
		if err != nil {
			return err
		}
		log.Println("Added r2_mp4_url column to videos table")
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

// CreateVideo inserts a new video record into the database
func (s *SQLiteDB) CreateVideo(metadata VideoMetadata) error {
	_, err := s.db.Exec(`
		INSERT INTO videos (
			id, created_at, finished_at, status, duration, size, 
			local_path, hls_path, dash_path, hls_url, dash_url, 
			r2_hls_path, r2_dash_path, r2_mp4_path, r2_hls_url, r2_dash_url, r2_mp4_url,
			camera_id, error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		metadata.ID,
		metadata.CreatedAt,
		metadata.FinishedAt,
		metadata.Status,
		metadata.Duration,
		metadata.Size,
		metadata.LocalPath,
		metadata.HLSPath,
		metadata.DASHPath,
		metadata.HLSURL,
		metadata.DASHURL,
		metadata.R2HLSPath,
		metadata.R2DASHPath,
		metadata.R2MP4Path,
		metadata.R2HLSURL,
		metadata.R2DASHURL,
		metadata.R2MP4URL,
		metadata.CameraID,
		metadata.ErrorMessage,
	)

	if err != nil {
		return fmt.Errorf("failed to create video: %v", err)
	}

	return nil
}

// GetVideo retrieves a video by its ID
func (s *SQLiteDB) GetVideo(id string) (*VideoMetadata, error) {
	var video VideoMetadata
	var finishedAt sql.NullTime
	var localPath, hlsPath, dashPath, hlsURL, dashURL sql.NullString
	var r2HLSPath, r2DASHPath, r2MP4Path, r2HLSURL, r2DASHURL, r2MP4URL sql.NullString
	var cameraID, errorMessage sql.NullString

	err := s.db.QueryRow(`
		SELECT 
			id, created_at, finished_at, status, duration, size, 
			local_path, hls_path, dash_path, hls_url, dash_url,
			r2_hls_path, r2_dash_path, r2_mp4_path, r2_hls_url, r2_dash_url, r2_mp4_url,
			camera_id, error_message
		FROM videos 
		WHERE id = ?
	`, id).Scan(
		&video.ID,
		&video.CreatedAt,
		&finishedAt,
		&video.Status,
		&video.Duration,
		&video.Size,
		&localPath,
		&hlsPath,
		&dashPath,
		&hlsURL,
		&dashURL,
		&r2HLSPath,
		&r2DASHPath,
		&r2MP4Path,
		&r2HLSURL,
		&r2DASHURL,
		&r2MP4URL,
		&cameraID,
		&errorMessage,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get video: %v", err)
	}

	// Convert SQL nullable types to Go types
	if finishedAt.Valid {
		video.FinishedAt = &finishedAt.Time
	}

	if localPath.Valid {
		video.LocalPath = localPath.String
	}
	if hlsPath.Valid {
		video.HLSPath = hlsPath.String
	}
	if dashPath.Valid {
		video.DASHPath = dashPath.String
	}
	if hlsURL.Valid {
		video.HLSURL = hlsURL.String
	}
	if dashURL.Valid {
		video.DASHURL = dashURL.String
	}
	if r2HLSPath.Valid {
		video.R2HLSPath = r2HLSPath.String
	}
	if r2DASHPath.Valid {
		video.R2DASHPath = r2DASHPath.String
	}
	if r2MP4Path.Valid {
		video.R2MP4Path = r2MP4Path.String
	}
	if r2HLSURL.Valid {
		video.R2HLSURL = r2HLSURL.String
	}
	if r2DASHURL.Valid {
		video.R2DASHURL = r2DASHURL.String
	}
	if r2MP4URL.Valid {
		video.R2MP4URL = r2MP4URL.String
	}
	if cameraID.Valid {
		video.CameraID = cameraID.String
	}
	if errorMessage.Valid {
		video.ErrorMessage = errorMessage.String
	}

	return &video, nil
}

// UpdateVideo updates an existing video record
func (s *SQLiteDB) UpdateVideo(metadata VideoMetadata) error {
	_, err := s.db.Exec(`
		UPDATE videos 
		SET 
			created_at = ?,
			finished_at = ?,
			status = ?,
			duration = ?,
			size = ?,
			local_path = ?,
			hls_path = ?,
			dash_path = ?,
			hls_url = ?,
			dash_url = ?,
			r2_hls_path = ?,
			r2_dash_path = ?,
			r2_mp4_path = ?,
			r2_hls_url = ?,
			r2_dash_url = ?,
			r2_mp4_url = ?,
			camera_id = ?,
			error_message = ?
		WHERE id = ?
	`,
		metadata.CreatedAt,
		metadata.FinishedAt,
		metadata.Status,
		metadata.Duration,
		metadata.Size,
		metadata.LocalPath,
		metadata.HLSPath,
		metadata.DASHPath,
		metadata.HLSURL,
		metadata.DASHURL,
		metadata.R2HLSPath,
		metadata.R2DASHPath,
		metadata.R2MP4Path,
		metadata.R2HLSURL,
		metadata.R2DASHURL,
		metadata.R2MP4URL,
		metadata.CameraID,
		metadata.ErrorMessage,
		metadata.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update video: %v", err)
	}

	return nil
}

// ListVideos retrieves a list of videos with pagination
func (s *SQLiteDB) ListVideos(limit, offset int) ([]VideoMetadata, error) {
	rows, err := s.db.Query(`
		SELECT 
			id, created_at, finished_at, status, duration, size, 
			local_path, hls_path, dash_path, hls_url, dash_url,
			r2_hls_path, r2_dash_path, r2_mp4_path, r2_hls_url, r2_dash_url, r2_mp4_url,
			camera_id, error_message
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
		var finishedAt sql.NullTime
		var localPath, hlsPath, dashPath, hlsURL, dashURL sql.NullString
		var r2HLSPath, r2DASHPath, r2MP4Path, r2HLSURL, r2DASHURL, r2MP4URL sql.NullString
		var cameraID, errorMessage sql.NullString

		err := rows.Scan(
			&video.ID,
			&video.CreatedAt,
			&finishedAt,
			&video.Status,
			&video.Duration,
			&video.Size,
			&localPath,
			&hlsPath,
			&dashPath,
			&hlsURL,
			&dashURL,
			&r2HLSPath,
			&r2DASHPath,
			&r2MP4Path,
			&r2HLSURL,
			&r2DASHURL,
			&r2MP4URL,
			&cameraID,
			&errorMessage,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan video row: %v", err)
		}

		// Convert SQL nullable types to Go types
		if finishedAt.Valid {
			video.FinishedAt = &finishedAt.Time
		}

		if localPath.Valid {
			video.LocalPath = localPath.String
		}
		if hlsPath.Valid {
			video.HLSPath = hlsPath.String
		}
		if dashPath.Valid {
			video.DASHPath = dashPath.String
		}
		if hlsURL.Valid {
			video.HLSURL = hlsURL.String
		}
		if dashURL.Valid {
			video.DASHURL = dashURL.String
		}
		if r2HLSPath.Valid {
			video.R2HLSPath = r2HLSPath.String
		}
		if r2DASHPath.Valid {
			video.R2DASHPath = r2DASHPath.String
		}
		if r2MP4Path.Valid {
			video.R2MP4Path = r2MP4Path.String
		}
		if r2HLSURL.Valid {
			video.R2HLSURL = r2HLSURL.String
		}
		if r2DASHURL.Valid {
			video.R2DASHURL = r2DASHURL.String
		}
		if r2MP4URL.Valid {
			video.R2MP4URL = r2MP4URL.String
		}
		if cameraID.Valid {
			video.CameraID = cameraID.String
		}
		if errorMessage.Valid {
			video.ErrorMessage = errorMessage.String
		}

		videos = append(videos, video)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error after scanning rows: %v", err)
	}

	return videos, nil
}

// DeleteVideo removes a video record by its ID
func (s *SQLiteDB) DeleteVideo(id string) error {
	_, err := s.db.Exec("DELETE FROM videos WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete video: %v", err)
	}

	return nil
}

// GetVideosByStatus retrieves videos with a specific status
func (s *SQLiteDB) GetVideosByStatus(status VideoStatus, limit, offset int) ([]VideoMetadata, error) {
	rows, err := s.db.Query(`
		SELECT 
			id, created_at, finished_at, status, duration, size, 
			local_path, hls_path, dash_path, hls_url, dash_url,
			r2_hls_path, r2_dash_path, r2_mp4_path, r2_hls_url, r2_dash_url, r2_mp4_url,
			camera_id, error_message
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
		var finishedAt sql.NullTime
		var localPath, hlsPath, dashPath, hlsURL, dashURL sql.NullString
		var r2HLSPath, r2DASHPath, r2MP4Path, r2HLSURL, r2DASHURL, r2MP4URL sql.NullString
		var cameraID, errorMessage sql.NullString

		err := rows.Scan(
			&video.ID,
			&video.CreatedAt,
			&finishedAt,
			&video.Status,
			&video.Duration,
			&video.Size,
			&localPath,
			&hlsPath,
			&dashPath,
			&hlsURL,
			&dashURL,
			&r2HLSPath,
			&r2DASHPath,
			&r2MP4Path,
			&r2HLSURL,
			&r2DASHURL,
			&r2MP4URL,
			&cameraID,
			&errorMessage,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan video row: %v", err)
		}

		// Convert SQL nullable types to Go types
		if finishedAt.Valid {
			video.FinishedAt = &finishedAt.Time
		}

		if localPath.Valid {
			video.LocalPath = localPath.String
		}
		if hlsPath.Valid {
			video.HLSPath = hlsPath.String
		}
		if dashPath.Valid {
			video.DASHPath = dashPath.String
		}
		if hlsURL.Valid {
			video.HLSURL = hlsURL.String
		}
		if dashURL.Valid {
			video.DASHURL = dashURL.String
		}
		if r2HLSPath.Valid {
			video.R2HLSPath = r2HLSPath.String
		}
		if r2DASHPath.Valid {
			video.R2DASHPath = r2DASHPath.String
		}
		if r2MP4Path.Valid {
			video.R2MP4Path = r2MP4Path.String
		}
		if r2HLSURL.Valid {
			video.R2HLSURL = r2HLSURL.String
		}
		if r2DASHURL.Valid {
			video.R2DASHURL = r2DASHURL.String
		}
		if r2MP4URL.Valid {
			video.R2MP4URL = r2MP4URL.String
		}
		if cameraID.Valid {
			video.CameraID = cameraID.String
		}
		if errorMessage.Valid {
			video.ErrorMessage = errorMessage.String
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
	var finishedAt *time.Time

	// If status is ready or failed, set finished_at to current time
	if status == StatusReady || status == StatusFailed {
		now := time.Now()
		finishedAt = &now
	}

	_, err := s.db.Exec(`
		UPDATE videos 
		SET 
			status = ?,
			error_message = ?,
			finished_at = ?
		WHERE id = ?
	`, status, errorMsg, finishedAt, id)

	if err != nil {
		return fmt.Errorf("failed to update video status: %v", err)
	}

	log.Printf("Updated video %s status to %s", id, status)
	return nil
}

// UpdateVideoR2Paths updates the R2 storage paths for a video
func (s *SQLiteDB) UpdateVideoR2Paths(id, hlsPath, dashPath, mp4Path string) error {
	_, err := s.db.Exec(`
		UPDATE videos 
		SET 
			r2_hls_path = ?,
			r2_dash_path = ?,
			r2_mp4_path = ?
		WHERE id = ?
	`, hlsPath, dashPath, mp4Path, id)

	if err != nil {
		return fmt.Errorf("failed to update video R2 paths: %v", err)
	}

	return nil
}

// UpdateVideoR2URLs updates the R2 URLs for a video
func (s *SQLiteDB) UpdateVideoR2URLs(id, hlsURL, dashURL, mp4URL string) error {
	_, err := s.db.Exec(`
		UPDATE videos 
		SET 
			r2_hls_url = ?,
			r2_dash_url = ?,
			r2_mp4_url = ?
		WHERE id = ?
	`, hlsURL, dashURL, mp4URL, id)

	if err != nil {
		return fmt.Errorf("failed to update video R2 URLs: %v", err)
	}

	return nil
}

// Close closes the database connection
func (s *SQLiteDB) Close() error {
	return s.db.Close()
}
