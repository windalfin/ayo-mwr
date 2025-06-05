package main

import (
	"database/sql"
	"log"
	"os"
	"time"

	"ayo-mwr/cron"
	"ayo-mwr/database"
)

// MockVideoClient implements the cron.VideoClient interface for testing
type MockVideoClient struct{}

// MarkVideosUnavailable mocks the API call to mark videos as unavailable
func (m *MockVideoClient) MarkVideosUnavailable(uniqueIds []string) (map[string]interface{}, error) {
	log.Printf("MOCK: Marking videos as unavailable: %v", uniqueIds)
	return map[string]interface{}{"success": true}, nil
}

func main() {
	// Load database path from environment or use default
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "./data/videos.db"
	}

	// Initialize database connection
	sqliteDB, err := database.NewSQLiteDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer sqliteDB.Close()

	// Get the underlying *sql.DB from SQLiteDB
	db := sqliteDB.GetDB()

	// Create a mock video client
	mockClient := &MockVideoClient{}

	// Set auto-delete threshold to 7 days
	autoDelete := 7
	venueCode := "TEST"

	log.Printf("Starting test cleanup with auto-delete threshold of %d days", autoDelete)

	// Query database to see what videos exist before cleanup
	listVideosBeforeCleanup(db)

	// Run the cleanup function
	log.Println("Running video cleanup function...")
	cron.CleanupExpiredVideos(db, mockClient, autoDelete, venueCode)

	// Query database again to see what changed
	log.Println("\nAfter cleanup:")
	listVideosBeforeCleanup(db)
}

// Helper function to list all videos in the database
func listVideosBeforeCleanup(db *sql.DB) {
	rows, err := db.Query("SELECT id, unique_id, camera_name, created_at, status FROM videos")
	if err != nil {
		log.Printf("Error querying videos: %v", err)
		return
	}
	defer rows.Close()

	log.Println("Current videos in database:")
	for rows.Next() {
		var id, uniqueId, cameraName, status string
		var createdAt time.Time
		if err := rows.Scan(&id, &uniqueId, &cameraName, &createdAt, &status); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		log.Printf("  - ID=%s, UniqueID=%s, Camera=%s, Created=%s, Status=%s", 
			id, uniqueId, cameraName, createdAt.Format("2006-01-02"), status)
	}
}
