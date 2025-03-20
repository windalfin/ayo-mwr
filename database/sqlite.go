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
			camera_name TEXT,
			local_path TEXT,
			hls_path TEXT,
			hls_url TEXT,
			r2_hls_path TEXT,
			r2_mp4_path TEXT,
			r2_hls_url TEXT,
			r2_mp4_url TEXT,
			status TEXT,
			error TEXT,
			created_at DATETIME,
			finished_at DATETIME,
			uploaded_at DATETIME
		)
	`)
	if err != nil {
		return err
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
			status, error, created_at, finished_at, uploaded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		metadata.ID,
		metadata.CameraID,
		metadata.LocalPath,
		metadata.HLSPath,
		metadata.HLSURL,
		metadata.R2HLSPath,
		metadata.R2MP4Path,
		metadata.R2HLSURL,
		metadata.R2MP4URL,
		metadata.Status,
		metadata.ErrorMessage,
		metadata.CreatedAt,
		metadata.FinishedAt,
		nil,
	)
	return err
}

// GetVideo retrieves a video record by ID
func (s *SQLiteDB) GetVideo(id string) (*VideoMetadata, error) {
	var video VideoMetadata
	var finishedAt, uploadedAt sql.NullTime
	var cameraName sql.NullString

	err := s.db.QueryRow(`
		SELECT id, camera_name, local_path, hls_path, hls_url,
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url,
			status, error, created_at, finished_at, uploaded_at
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
		&video.Status,
		&video.ErrorMessage,
		&video.CreatedAt,
		&finishedAt,
		&uploadedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if cameraName.Valid {
		video.CameraID = cameraName.String
	}
	if finishedAt.Valid {
		video.FinishedAt = &finishedAt.Time
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
			status = ?,
			error = ?,
			finished_at = ?
		WHERE id = ?`,
		metadata.CameraID,
		metadata.LocalPath,
		metadata.HLSPath,
		metadata.HLSURL,
		metadata.R2HLSPath,
		metadata.R2MP4Path,
		metadata.R2HLSURL,
		metadata.R2MP4URL,
		metadata.Status,
		metadata.ErrorMessage,
		metadata.FinishedAt,
		metadata.ID,
	)
	return err
}

// UpdateVideoR2Paths updates the R2 paths for a video
func (s *SQLiteDB) UpdateVideoR2Paths(id, hlsPath, mp4Path string) error {
	_, err := s.db.Exec(`
		UPDATE videos 
		SET r2_hls_path = ?, r2_mp4_path = ?
		WHERE id = ?`,
		hlsPath, mp4Path, id)
	return err
}

// UpdateVideoR2URLs updates the R2 URLs for a video
func (s *SQLiteDB) UpdateVideoR2URLs(id, hlsURL, mp4URL string) error {
	_, err := s.db.Exec(`
		UPDATE videos 
		SET r2_hls_url = ?, r2_mp4_url = ?
		WHERE id = ?`,
		hlsURL, mp4URL, id)
	return err
}

// ListVideos retrieves a list of videos with pagination
func (s *SQLiteDB) ListVideos(limit, offset int) ([]VideoMetadata, error) {
	rows, err := s.db.Query(`
		SELECT 
			id, camera_name, local_path, hls_path, hls_url,
			r2_hls_path, r2_mp4_path, r2_hls_url, r2_mp4_url,
			status, error, created_at, finished_at, uploaded_at
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
		var cameraName sql.NullString

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
			&video.Status,
			&video.ErrorMessage,
			&video.CreatedAt,
			&finishedAt,
			&uploadedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan video row: %v", err)
		}

		if cameraName.Valid {
			video.CameraID = cameraName.String
		}
		if finishedAt.Valid {
			video.FinishedAt = &finishedAt.Time
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
			status, error, created_at, finished_at, uploaded_at
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
		var cameraName sql.NullString

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
			&video.Status,
			&video.ErrorMessage,
			&video.CreatedAt,
			&finishedAt,
			&uploadedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan video row: %v", err)
		}

		if cameraName.Valid {
			video.CameraID = cameraName.String
		}
		if finishedAt.Valid {
			video.FinishedAt = &finishedAt.Time
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
			error = ?,
			finished_at = ?
		WHERE id = ?
	`, status, errorMsg, finishedAt, id)

	if err != nil {
		return fmt.Errorf("failed to update video status: %v", err)
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

// Close closes the database connection
func (s *SQLiteDB) Close() error {
	return s.db.Close()
}
