package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "github.com/mattn/go-sqlite3"
)

var (
	tablesInitialized sync.Once
	initTablesError   error
)

// getEnvOrDefault returns environment variable value or default if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

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

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	// Initialize tables only once
	tablesInitialized.Do(func() {
		initTablesError = initTables(db)
	})

	if initTablesError != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize tables: %v", initTablesError)
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
			-- Chunk support columns
			chunk_type TEXT DEFAULT 'segment',
			source_segments_count INTEGER DEFAULT 1,
			chunk_duration_seconds INTEGER,
			processing_status TEXT DEFAULT 'ready',
			FOREIGN KEY (storage_disk_id) REFERENCES storage_disks(id)
		)
	`)
	if err != nil {
		return err
	}

	// Add chunk support columns to existing recording_segments table (migrations)
	migrations := []struct {
		column string
		query  string
	}{
		{"chunk_type", "ALTER TABLE recording_segments ADD COLUMN chunk_type TEXT DEFAULT 'segment'"},
		{"source_segments_count", "ALTER TABLE recording_segments ADD COLUMN source_segments_count INTEGER DEFAULT 1"},
		{"chunk_duration_seconds", "ALTER TABLE recording_segments ADD COLUMN chunk_duration_seconds INTEGER"},
		{"processing_status", "ALTER TABLE recording_segments ADD COLUMN processing_status TEXT DEFAULT 'ready'"},
		{"is_watermarked", "ALTER TABLE recording_segments ADD COLUMN is_watermarked BOOLEAN DEFAULT FALSE"},
	}

	for _, migration := range migrations {
		_, migrationErr := db.Exec(migration.query)
		if migrationErr != nil {
			log.Printf("Info: Migration for %s: %v (ignore if column exists)", migration.column, migrationErr)
		} else {
			log.Printf("Success: Added %s column to recording_segments table", migration.column)
		}
	}

	// Create optimized indexes for chunk queries
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_chunks_type_time ON recording_segments 
			(chunk_type, camera_name, segment_start, segment_end)
	`)
	if err != nil {
		log.Printf("Warning: Failed to create chunk index: %v", err)
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_segments_camera_time ON recording_segments 
			(camera_name, segment_start, segment_end) WHERE chunk_type = 'segment'
	`)
	if err != nil {
		log.Printf("Warning: Failed to create segment index: %v", err)
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



	// Create system_config table for storing system configuration
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS system_config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'string',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_by TEXT DEFAULT 'system'
		)
	`)
	if err != nil {
		return err
	}

	// Create users table for authentication
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Insert default system configurations if they don't exist
	defaultConfigs := []struct {
		key, value, configType string
	}{
		// Worker Concurrency Configuration
		{"booking_worker_concurrency", getEnvOrDefault("BOOKING_WORKER_CONCURRENCY", "2"), "int"},
		{"video_request_worker_concurrency", getEnvOrDefault("VIDEO_REQUEST_WORKER_CONCURRENCY", "3"), "int"},
		{"pending_task_worker_concurrency", getEnvOrDefault("PENDING_TASK_WORKER_CONCURRENCY", "5"), "int"},
		{"enabled_qualities", getEnvOrDefault("ENABLED_QUALITIES", "1080p,720p,480p,360p"), "string"},
		
		// Video Duration Check Configuration
		{"enable_video_duration_check", "false", "boolean"},
		
		// Disk Manager Configuration
		{"minimum_free_space_gb", "100", "int"},
		{"priority_external", "1", "int"},
		{"priority_mounted_storage", "50", "int"},
		{"priority_internal_nvme", "101", "int"},
		{"priority_internal_sata", "201", "int"},
		{"priority_root_filesystem", "500", "int"},
		
		// Arduino Configuration
		{"arduino_com_port", getEnvOrDefault("ARDUINO_COM_PORT", "COM4"), "string"},
		{"arduino_baud_rate", getEnvOrDefault("ARDUINO_BAUD_RATE", "9600"), "int"},
		
		// RTSP Configuration (Legacy single camera)
		{"rtsp_username", getEnvOrDefault("RTSP_USERNAME", "admin"), "string"},
		{"rtsp_password", getEnvOrDefault("RTSP_PASSWORD", "password"), "string"},
		{"rtsp_ip", getEnvOrDefault("RTSP_IP", "192.168.1.100"), "string"},
		{"rtsp_port", getEnvOrDefault("RTSP_PORT", "554"), "int"},
		{"rtsp_path", getEnvOrDefault("RTSP_PATH", "/streaming/channels/101/"), "string"},
		
		// Recording Configuration
		{"segment_duration", getEnvOrDefault("SEGMENT_DURATION", "30"), "int"},
		{"clip_duration", getEnvOrDefault("CLIP_DURATION", "60"), "int"},
		{"width", getEnvOrDefault("WIDTH", "800"), "int"},
		{"height", getEnvOrDefault("HEIGHT", "600"), "int"},
		{"frame_rate", getEnvOrDefault("FRAME_RATE", "30"), "int"},
		{"resolution", getEnvOrDefault("RESOLUTION", "800x600"), "string"},
		{"auto_delete", "30", "int"},
		
		// Storage Configuration
		{"storage_path", getEnvOrDefault("STORAGE_PATH", "./videos"), "string"},
		{"hardware_accel", getEnvOrDefault("HW_ACCEL", ""), "string"},
		{"codec", getEnvOrDefault("CODEC", "avc"), "string"},
		
		// Server Configuration
		{"server_port", getEnvOrDefault("PORT", "3000"), "int"},
		{"base_url", getEnvOrDefault("BASE_URL", "http://localhost:3000"), "string"},
		

		
		// R2 Storage Configuration
		{"r2_access_key", getEnvOrDefault("R2_ACCESS_KEY", "your-r2-access-key"), "string"},
		{"r2_secret_key", getEnvOrDefault("R2_SECRET_KEY", "your-r2-secret-key"), "string"},
		{"r2_account_id", getEnvOrDefault("R2_ACCOUNT_ID", "your-r2-account-id"), "string"},
		{"r2_bucket", getEnvOrDefault("R2_BUCKET", "your-bucket-name"), "string"},
		{"r2_region", getEnvOrDefault("R2_REGION", "auto"), "string"},
		{"r2_endpoint", getEnvOrDefault("R2_ENDPOINT", "https://your-r2-endpoint.com"), "string"},
		{"r2_base_url", getEnvOrDefault("R2_BASE_URL", "https://your-media-domain.com"), "string"},
		{"r2_enabled", getEnvOrDefault("R2_ENABLED", "false"), "boolean"},
		{"r2_token_value", getEnvOrDefault("R2_TOKEN_VALUE", "your-r2-token"), "string"},
		
		// Watermark Configuration
		{"watermark_position", getEnvOrDefault("WATERMARK_POSITION", "top_right"), "string"},
		{"watermark_margin", getEnvOrDefault("WATERMARK_MARGIN", "10"), "int"},
		{"watermark_opacity", getEnvOrDefault("WATERMARK_OPACITY", "0.6"), "float"},
		
		// AYO API Configuration
		{"ayoindo_api_base_endpoint", getEnvOrDefault("AYOINDO_API_BASE_ENDPOINT", "https://api.example.com/v1"), "string"},
		{"ayoindo_api_token", getEnvOrDefault("AYOINDO_API_TOKEN", "your-api-token"), "string"},
		
		// Venue Configuration (no default values - database only)
		{"venue_code", "", "string"},
		{"venue_secret_key", "", "string"},
		
		// Worker Configuration
		{"worker_concurrency", "2", "int"},
		
		// Transcoding Configuration
		{"enable_480p", "true", "boolean"},
		{"enable_720p", "true", "boolean"},
		{"enable_1080p", "false", "boolean"},
	}

	for _, config := range defaultConfigs {
		_, err = db.Exec(`
			INSERT OR IGNORE INTO system_config (key, value, type, updated_at, updated_by)
			VALUES (?, ?, ?, CURRENT_TIMESTAMP, 'system')
		`, config.key, config.value, config.configType)
		if err != nil {
			return fmt.Errorf("failed to insert default config %s: %v", config.key, err)
		}
	}

	return nil
}

// GetSystemConfig retrieves a system configuration by key
func (s *SQLiteDB) GetSystemConfig(key string) (*SystemConfig, error) {
	var config SystemConfig
	var updatedAt sql.NullTime
	var updatedBy sql.NullString

	err := s.db.QueryRow(`
		SELECT key, value, type, updated_at, updated_by
		FROM system_config
		WHERE key = ?
	`, key).Scan(&config.Key, &config.Value, &config.Type, &updatedAt, &updatedBy)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("configuration key '%s' not found", key)
		}
		return nil, fmt.Errorf("error getting system config: %v", err)
	}

	if updatedAt.Valid {
		config.UpdatedAt = updatedAt.Time
	}
	if updatedBy.Valid {
		config.UpdatedBy = updatedBy.String
	}

	return &config, nil
}

// SetSystemConfig creates or updates a system configuration
func (s *SQLiteDB) SetSystemConfig(config SystemConfig) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO system_config (key, value, type, updated_at, updated_by)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, ?)
	`, config.Key, config.Value, config.Type, config.UpdatedBy)

	if err != nil {
		return fmt.Errorf("error setting system config: %v", err)
	}

	log.Printf("⚙️ CONFIG: Updated system config '%s' = '%s' (type: %s) by %s",
		config.Key, config.Value, config.Type, config.UpdatedBy)
	return nil
}

// GetAllSystemConfigs retrieves all system configurations
func (s *SQLiteDB) GetAllSystemConfigs() ([]SystemConfig, error) {
	rows, err := s.db.Query(`
		SELECT key, value, type, updated_at, updated_by
		FROM system_config
		ORDER BY key
	`)
	if err != nil {
		return nil, fmt.Errorf("error querying system configs: %v", err)
	}
	defer rows.Close()

	var configs []SystemConfig
	for rows.Next() {
		var config SystemConfig
		var updatedAt sql.NullTime
		var updatedBy sql.NullString

		err := rows.Scan(&config.Key, &config.Value, &config.Type, &updatedAt, &updatedBy)
		if err != nil {
			return nil, fmt.Errorf("error scanning system config row: %v", err)
		}

		if updatedAt.Valid {
			config.UpdatedAt = updatedAt.Time
		}
		if updatedBy.Valid {
			config.UpdatedBy = updatedBy.String
		}

		configs = append(configs, config)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating system config rows: %v", err)
	}

	return configs, nil
}

// DeleteSystemConfig deletes a system configuration by key
func (s *SQLiteDB) DeleteSystemConfig(key string) error {
	result, err := s.db.Exec(`
		DELETE FROM system_config WHERE key = ?
	`, key)

	if err != nil {
		return fmt.Errorf("error deleting system config: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %v", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("configuration key '%s' not found", key)
	}

	log.Printf("⚙️ CONFIG: Deleted system config '%s'", key)
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
			size, duration, resolution, has_request, last_check_file, video_type, request_id, start_time, end_time
		FROM videos 
		WHERE unique_id = ?
	`, uniqueID).Scan(
		&video.ID, &video.CameraName, &video.LocalPath, &video.HLSPath, &video.HLSURL,
		&video.R2HLSPath, &video.R2MP4Path, &video.R2HLSURL, &video.R2MP4URL,
		&video.R2PreviewMP4Path, &video.R2PreviewMP4URL, &video.R2PreviewPNGPath, &video.R2PreviewPNGURL,
		&video.UniqueID, &orderDetailID, &video.BookingID, &video.RawJSON, &status, &video.ErrorMessage,
		&createdAt, &finishedAt, &uploadedAt,
		&video.Size, &video.Duration, &resolution, &hasRequest, &lastCheckFile, &videoType,
		&requestID, &video.StartTime, &video.EndTime,
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

// UpdateDiskPriority updates the priority_order for a storage disk
func (s *SQLiteDB) UpdateDiskPriority(id string, priority int) error {
	_, err := s.db.Exec(`
        UPDATE storage_disks SET priority_order = ?, last_scan = ? WHERE id = ?
    `, priority, time.Now(), id)
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
			id, camera_name, storage_disk_id, mp4_path, segment_start, segment_end, file_size_bytes, created_at,
			chunk_type, source_segments_count, chunk_duration_seconds, processing_status, is_watermarked
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		segment.ID, segment.CameraName, segment.StorageDiskID, segment.MP4Path,
		segment.SegmentStart, segment.SegmentEnd, segment.FileSizeBytes, segment.CreatedAt,
		segment.ChunkType, segment.SourceSegmentsCount, segment.ChunkDurationSeconds, segment.ProcessingStatus, segment.IsWatermarked,
	)
	return err
}

// GetRecordingSegments retrieves recording segments for a camera within a time range
func (s *SQLiteDB) GetRecordingSegments(cameraName string, start, end time.Time) ([]RecordingSegment, error) {
	rows, err := s.db.Query(`
		SELECT rs.id, rs.camera_name, rs.storage_disk_id, rs.mp4_path, 
			   rs.segment_start, rs.segment_end, rs.file_size_bytes, rs.created_at,
			   COALESCE(rs.chunk_type, 'segment') as chunk_type,
			   COALESCE(rs.source_segments_count, 1) as source_segments_count,
			   rs.chunk_duration_seconds,
			   COALESCE(rs.processing_status, 'ready') as processing_status,
			   COALESCE(rs.is_watermarked, FALSE) as is_watermarked
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
		var chunkDuration sql.NullInt64
		err := rows.Scan(
			&segment.ID, &segment.CameraName, &segment.StorageDiskID, &segment.MP4Path,
			&segment.SegmentStart, &segment.SegmentEnd, &segment.FileSizeBytes, &segment.CreatedAt,
			&segment.ChunkType, &segment.SourceSegmentsCount, &chunkDuration, &segment.ProcessingStatus, &segment.IsWatermarked,
		)
		if err != nil {
			return nil, err
		}
		if chunkDuration.Valid {
			duration := int(chunkDuration.Int64)
			segment.ChunkDurationSeconds = &duration
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
		WHERE status = 'pending' 
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
func (s *SQLiteDB) UpdateCameraConfig(cameraName string, frameRate int, autoDelete int) error {
	_, err := s.db.Exec(`
		UPDATE cameras SET 
		
			frame_rate = ?, 
			auto_delete = ?
			
		WHERE name = ?`,
		frameRate, autoDelete, cameraName)

	if err != nil {
		return fmt.Errorf("error updating camera config: %v", err)
	}

	log.Printf("📹 CAMERA: Updated config for %s - FrameRate: %d, AutoDelete: %d",
		cameraName, frameRate, autoDelete)
	return nil
}

// CleanupStuckVideosOnStartup cleans up video records that might be stuck in intermediate states
// This function is SYNCHRONOUS and MUST complete before other services start
func (s *SQLiteDB) CleanupStuckVideosOnStartup() error {
	startTime := time.Now()
	log.Println("🧹 STARTUP CLEANUP: Starting synchronous cleanup of stuck videos and request_ids...")

	// Part 1: Change stuck videos to failed status using direct SQL
	// Part 2: Clear request_ids for Ready videos using direct SQL

	var totalStuckVideos int

	// === PART 1: Process stuck videos (change status to failed) ===
	log.Println("🧹 STARTUP CLEANUP: Part 1 - Processing stuck videos...")

	// Use direct SQL for efficiency - update all stuck videos to failed status
	result, err := s.db.Exec(`
		UPDATE videos 
		SET status = ?, 
		    error = ?
		WHERE status IN (?, ?, ?, ?, ?)
	`, StatusFailed,
		"Video stuck during service restart - marked as failed",
		StatusPending, StatusProcessing, StatusRecording, StatusInitial, StatusUploading)

	if err != nil {
		log.Printf("❌ STARTUP CLEANUP: Error updating stuck videos: %v", err)
	} else {
		rowsAffected, _ := result.RowsAffected()
		totalStuckVideos = int(rowsAffected)
		if totalStuckVideos > 0 {
			log.Printf("✅ STARTUP CLEANUP: Updated %d stuck videos to failed status", totalStuckVideos)
		} else {
			log.Printf("✅ STARTUP CLEANUP: No stuck videos found")
		}
	}

	// === PART 2: Process stuck request_ids (for Ready videos) ===
	log.Println("🧹 STARTUP CLEANUP: Part 2 - Processing stuck request_ids...")

	var stuckRequestCount int

	// Use direct SQL for efficiency - clear all request_ids for Ready videos
	result, err = s.db.Exec(`
		UPDATE videos 
		SET request_id = '' 
		WHERE status = ? AND request_id != '' AND request_id IS NOT NULL
	`, StatusReady)

	if err != nil {
		log.Printf("❌ STARTUP CLEANUP: Error clearing stuck request_ids: %v", err)
	} else {
		rowsAffected, _ := result.RowsAffected()
		stuckRequestCount = int(rowsAffected)
		if stuckRequestCount > 0 {
			log.Printf("✅ STARTUP CLEANUP: Cleared %d stuck request_ids for Ready videos", stuckRequestCount)
		} else {
			log.Printf("✅ STARTUP CLEANUP: No stuck request_ids found for Ready videos")
		}
	}

	duration := time.Since(startTime)

	if totalStuckVideos > 0 || stuckRequestCount > 0 {
		log.Printf("✅ STARTUP CLEANUP: COMPLETED! Found %d stuck videos, %d stuck request_ids in %v",
			totalStuckVideos, stuckRequestCount, duration)
		log.Printf("🧹 STARTUP CLEANUP: Part 1 - %d stuck videos marked as failed", totalStuckVideos)
		log.Printf("🧹 STARTUP CLEANUP: Part 2 - %d stuck request_ids cleared", stuckRequestCount)
		log.Printf("📤 STARTUP CLEANUP: Videos with uploading status were left untouched (may take hours for large files)")
	} else {
		log.Printf("✅ STARTUP CLEANUP: No stuck videos or request_ids found - system is clean! (completed in %v)", duration)
	}

	return nil
}

// User authentication methods

// CreateUser creates a new user with hashed password
func (s *SQLiteDB) CreateUser(username, password string) error {
	// Hash the password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("error hashing password: %v", err)
	}

	// Insert user into database
	_, err = s.db.Exec(`
		INSERT INTO users (username, password_hash, created_at, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, username, string(hashedPassword))

	if err != nil {
		return fmt.Errorf("error creating user: %v", err)
	}

	log.Printf("👤 AUTH: Created user '%s'", username)
	return nil
}

// GetUserByUsername retrieves a user by username
func (s *SQLiteDB) GetUserByUsername(username string) (*User, error) {
	var user User
	var createdAt, updatedAt time.Time

	err := s.db.QueryRow(`
		SELECT id, username, password_hash, created_at, updated_at
		FROM users
		WHERE username = ?
	`, username).Scan(&user.ID, &user.Username, &user.PasswordHash, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		return nil, nil // User not found
	}
	if err != nil {
		return nil, fmt.Errorf("error getting user: %v", err)
	}

	user.CreatedAt = createdAt
	user.UpdatedAt = updatedAt

	return &user, nil
}

// HasUsers checks if there are any users in the system
func (s *SQLiteDB) HasUsers() (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return false, fmt.Errorf("error counting users: %v", err)
	}
	return count > 0, nil
}

// ValidatePassword checks if the provided password matches the user's hashed password
func ValidatePassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// Chunk operations implementation

// CreateChunk creates a new pre-concatenated chunk record
func (s *SQLiteDB) CreateChunk(chunk RecordingSegment) error {
	return s.CreateRecordingSegment(chunk)
}

// FindChunksInTimeRange finds pre-concatenated chunks that overlap with the given time range
func (s *SQLiteDB) FindChunksInTimeRange(cameraName string, start, end time.Time) ([]ChunkInfo, error) {
	rows, err := s.db.Query(`
		SELECT rs.id, rs.camera_name, rs.segment_start, rs.segment_end, 
			   rs.mp4_path, rs.source_segments_count, rs.chunk_duration_seconds,
			   rs.file_size_bytes, rs.processing_status, rs.storage_disk_id,
			   sd.path as disk_path, COALESCE(rs.is_watermarked, FALSE) as is_watermarked
		FROM recording_segments rs
		JOIN storage_disks sd ON rs.storage_disk_id = sd.id
		WHERE rs.camera_name = ? 
		  AND rs.chunk_type = 'chunk'
		  AND rs.processing_status = 'ready'
		  AND rs.segment_start < ? 
		  AND rs.segment_end > ?
		ORDER BY rs.segment_start ASC
	`, cameraName, end, start)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []ChunkInfo
	for rows.Next() {
		var chunk ChunkInfo
		var chunkDuration sql.NullInt64
		var diskPath string
		var relativePath string
		err := rows.Scan(
			&chunk.ID, &chunk.CameraName, &chunk.StartTime, &chunk.EndTime,
			&relativePath, &chunk.SourceSegmentsCount, &chunkDuration,
			&chunk.FileSizeBytes, &chunk.ProcessingStatus, &chunk.StorageDiskID,
			&diskPath, &chunk.IsWatermarked,
		)
		if err != nil {
			return nil, err
		}
		if chunkDuration.Valid {
			chunk.DurationSeconds = int(chunkDuration.Int64)
		}
		// Construct full path using filepath.Join for proper path handling
		chunk.FilePath = filepath.Join(diskPath, relativePath)
		chunks = append(chunks, chunk)
	}

	return chunks, rows.Err()
}

// GetPendingChunkSegments gets individual segments that need to be combined into a chunk
func (s *SQLiteDB) GetPendingChunkSegments(cameraName string, chunkStart time.Time, chunkDurationMinutes int) ([]RecordingSegment, error) {
	chunkEnd := chunkStart.Add(time.Duration(chunkDurationMinutes) * time.Minute)
	
	rows, err := s.db.Query(`
		SELECT rs.id, rs.camera_name, rs.storage_disk_id, rs.mp4_path, 
			   rs.segment_start, rs.segment_end, rs.file_size_bytes, rs.created_at,
			   COALESCE(rs.chunk_type, 'segment') as chunk_type,
			   COALESCE(rs.source_segments_count, 1) as source_segments_count,
			   rs.chunk_duration_seconds,
			   COALESCE(rs.processing_status, 'ready') as processing_status
		FROM recording_segments rs
		WHERE rs.camera_name = ? 
		  AND rs.chunk_type = 'segment'
		  AND rs.segment_start >= ?
		  AND rs.segment_end <= ?
		  AND rs.processing_status = 'ready'
		ORDER BY rs.segment_start ASC
	`, cameraName, chunkStart, chunkEnd)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var segments []RecordingSegment
	for rows.Next() {
		var segment RecordingSegment
		var chunkDuration sql.NullInt64
		err := rows.Scan(
			&segment.ID, &segment.CameraName, &segment.StorageDiskID, &segment.MP4Path,
			&segment.SegmentStart, &segment.SegmentEnd, &segment.FileSizeBytes, &segment.CreatedAt,
			&segment.ChunkType, &segment.SourceSegmentsCount, &chunkDuration, &segment.ProcessingStatus,
		)
		if err != nil {
			return nil, err
		}
		if chunkDuration.Valid {
			duration := int(chunkDuration.Int64)
			segment.ChunkDurationSeconds = &duration
		}
		segments = append(segments, segment)
	}

	return segments, rows.Err()
}

// UpdateChunkProcessingStatus updates the processing status of a chunk
func (s *SQLiteDB) UpdateChunkProcessingStatus(chunkID string, status ProcessingStatus) error {
	_, err := s.db.Exec(`
		UPDATE recording_segments 
		SET processing_status = ?
		WHERE id = ?
	`, status, chunkID)
	return err
}

// GetChunksByProcessingStatus gets chunks by their processing status
func (s *SQLiteDB) GetChunksByProcessingStatus(status ProcessingStatus) ([]RecordingSegment, error) {
	rows, err := s.db.Query(`
		SELECT rs.id, rs.camera_name, rs.storage_disk_id, rs.mp4_path, 
			   rs.segment_start, rs.segment_end, rs.file_size_bytes, rs.created_at,
			   COALESCE(rs.chunk_type, 'segment') as chunk_type,
			   COALESCE(rs.source_segments_count, 1) as source_segments_count,
			   rs.chunk_duration_seconds,
			   COALESCE(rs.processing_status, 'ready') as processing_status
		FROM recording_segments rs
		WHERE rs.chunk_type = 'chunk'
		  AND rs.processing_status = ?
		ORDER BY rs.segment_start ASC
	`, status)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var segments []RecordingSegment
	for rows.Next() {
		var segment RecordingSegment
		var chunkDuration sql.NullInt64
		err := rows.Scan(
			&segment.ID, &segment.CameraName, &segment.StorageDiskID, &segment.MP4Path,
			&segment.SegmentStart, &segment.SegmentEnd, &segment.FileSizeBytes, &segment.CreatedAt,
			&segment.ChunkType, &segment.SourceSegmentsCount, &chunkDuration, &segment.ProcessingStatus,
		)
		if err != nil {
			return nil, err
		}
		if chunkDuration.Valid {
			duration := int(chunkDuration.Int64)
			segment.ChunkDurationSeconds = &duration
		}
		segments = append(segments, segment)
	}

	return segments, rows.Err()
}

// DeleteOldChunks deletes chunks older than the specified time
func (s *SQLiteDB) DeleteOldChunks(olderThan time.Time) error {
	_, err := s.db.Exec(`
		DELETE FROM recording_segments 
		WHERE chunk_type = 'chunk' 
		  AND created_at < ?
	`, olderThan)
	return err
}

// GetChunkStatistics returns statistics about chunk processing
func (s *SQLiteDB) GetChunkStatistics() (map[string]interface{}, error) {
	stats := make(map[string]interface{})
	
	// Count chunks by status
	rows, err := s.db.Query(`
		SELECT processing_status, COUNT(*) as count
		FROM recording_segments 
		WHERE chunk_type = 'chunk'
		GROUP BY processing_status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	statusStats := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		err := rows.Scan(&status, &count)
		if err != nil {
			return nil, err
		}
		statusStats[status] = count
	}
	stats["by_status"] = statusStats
	
	// Total chunks
	var totalChunks, totalSegments int
	err = s.db.QueryRow(`
		SELECT 
			SUM(CASE WHEN chunk_type = 'chunk' THEN 1 ELSE 0 END) as chunk_count,
			SUM(CASE WHEN chunk_type = 'segment' THEN 1 ELSE 0 END) as segment_count
		FROM recording_segments
	`).Scan(&totalChunks, &totalSegments)
	if err != nil {
		return nil, err
	}
	stats["total_chunks"] = totalChunks
	stats["total_segments"] = totalSegments
	
	// Average chunk duration and size
	var avgDuration sql.NullFloat64
	var avgSize sql.NullFloat64
	err = s.db.QueryRow(`
		SELECT AVG(chunk_duration_seconds), AVG(file_size_bytes)
		FROM recording_segments 
		WHERE chunk_type = 'chunk'
	`).Scan(&avgDuration, &avgSize)
	if err != nil {
		return nil, err
	}
	if avgDuration.Valid {
		stats["avg_duration_seconds"] = avgDuration.Float64
	}
	if avgSize.Valid {
		stats["avg_file_size_bytes"] = avgSize.Float64
	}
	
	return stats, nil
}
