package cron

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	// import your database, api, and playlist packages
	"ayo-mwr/api"
	"ayo-mwr/database"
	"database/sql"
)

// VideoClient is an interface for clients that can mark videos as unavailable
type VideoClient interface {
	MarkVideosUnavailable(uniqueIds []string) (map[string]interface{}, error)
}

func StartVideoCleanupJob(db *sql.DB, client *api.AyoIndoClient, autoDelete int, venueCode string) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			cleanupExpiredVideosWithClient(db, client, autoDelete, venueCode)
			<-ticker.C
		}
	}()
}

// CleanupExpiredVideos is a public wrapper for cleanupExpiredVideos for testing purposes
func CleanupExpiredVideos(db *sql.DB, client VideoClient, autoDelete int, venueCode string) {
	cleanupExpiredVideosWithClient(db, client, autoDelete, venueCode)
}

// CleanupExpiredVideosWithSQLiteDB is a wrapper that accepts a SQLiteDB instance
func CleanupExpiredVideosWithSQLiteDB(sqliteDB *database.SQLiteDB, client VideoClient, autoDelete int, venueCode string) {
	// Access the underlying *sql.DB from the SQLiteDB struct
	db := sqliteDB.GetDB()
	cleanupExpiredVideosWithClient(db, client, autoDelete, venueCode)
}

// cleanupExpiredVideosWithClient is the implementation of cleanupExpiredVideos that accepts the VideoClient interface
func cleanupExpiredVideosWithClient(db *sql.DB, client VideoClient, autoDelete int, venueCode string) {
	log.Printf("Starting video cleanup process. Auto-delete days: %d", autoDelete)

	expiry := time.Now().AddDate(0, 0, -autoDelete)
	log.Printf("Cleaning up videos created before: %s", expiry.Format("2006-01-02"))

	// Debug: Print all videos in database with their statuses
	debugRows, err := db.Query("SELECT id, camera_name, created_at, status FROM videos")
	if err != nil {
		log.Printf("Error querying videos for debug: %v", err)
	} else if debugRows != nil {
		log.Printf("All videos in database:")
		for debugRows.Next() {
			var id, cameraName, status string
			var createdAt time.Time
			debugRows.Scan(&id, &cameraName, &createdAt, &status)
			log.Printf("  - ID=%s, Camera=%s, Created=%s, Status=%s", id, cameraName, createdAt.Format("2006-01-02"), status)
		}
		debugRows.Close()
	}

	// Query for videos older than the expiry date that are not already marked as unavailable
	rows, err := db.Query("SELECT id, unique_id, local_path, hls_path, camera_name FROM videos WHERE status != ? AND created_at < ?",
		database.StatusUnavailable, expiry)
	if err != nil {
		log.Printf("Cleanup query error: %v", err)
		return
	}
	defer rows.Close()

	// Process videos in batches of 10 for the API call
	var batch []string        // for uniqueIds to send to API
	var processedIds []string // for database updates

	for rows.Next() {
		var id, uniqueId, localPath, hlsPath, cameraName string
		if err := rows.Scan(&id, &uniqueId, &localPath, &hlsPath, &cameraName); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}

		// Skip if uniqueId is empty (can't mark as unavailable via API)
		if uniqueId == "" {
			log.Printf("Skipping video %s: empty uniqueId", id)
			continue
		}

		log.Printf("Processing expired video: ID=%s, UniqueID=%s", id, uniqueId)

		// Add to batch for API call
		batch = append(batch, uniqueId)
		processedIds = append(processedIds, id)

		// Delete local video file if it exists
		if localPath != "" {
			if _, err := os.Stat(localPath); err == nil {
				log.Printf("Deleting local video file: %s", localPath)
				if err := os.Remove(localPath); err != nil {
					log.Printf("Error deleting local video file %s: %v", localPath, err)
				}
			}
		}

		// Delete HLS segments for this video
		if hlsPath != "" && cameraName != "" {
			// Extract date from created_at to find HLS segments
			var createdAt time.Time
			err := db.QueryRow("SELECT created_at FROM videos WHERE id = ?", id).Scan(&createdAt)
			if err == nil {
				// Format date as YYYYMMDD for segment filename pattern
				dateStr := createdAt.Format("20060102")

				// Use the HLS path from the database directly
				hlsDir := hlsPath
				log.Printf("Looking for HLS segments in directory: %s", hlsDir)

				// Find and delete HLS segments matching pattern segment_YYYYMMDD*.ts
				segmentPattern := fmt.Sprintf("segment_%s*.ts", dateStr)
				log.Printf("Looking for HLS segments with pattern: %s in directory: %s", segmentPattern, hlsDir)

				// Check if directory exists
				if _, err := os.Stat(hlsDir); os.IsNotExist(err) {
					log.Printf("HLS directory does not exist: %s", hlsDir)
					continue
				}

				// List all files in the directory for debugging
				hlsFiles, hlsErr := os.ReadDir(hlsDir)
				if hlsErr == nil {
					log.Printf("Files in HLS directory %s:", hlsDir)
					for _, file := range hlsFiles {
						log.Printf("  - %s", file.Name())
					}
				} else {
					log.Printf("Error reading HLS directory: %v", hlsErr)
				}

				// Debug the full path pattern
				fullHlsPattern := filepath.Join(hlsDir, segmentPattern)
				log.Printf("Full pattern for HLS segments: %s", fullHlsPattern)

				segments, err := filepath.Glob(fullHlsPattern)

				if err != nil {
					log.Printf("Error finding HLS segments for video %s: %v", id, err)
				} else if len(segments) == 0 {
					log.Printf("No HLS segments found matching pattern %s in %s", segmentPattern, hlsPath)
					// Double check with a directory listing
					files, err := os.ReadDir(hlsPath)
					if err == nil {
						log.Printf("Directory listing for %s:", hlsPath)
						for _, file := range files {
							log.Printf("  - %s", file.Name())
						}
					}
				} else {
					log.Printf("Found %d HLS segments to delete", len(segments))
					for _, segment := range segments {
						log.Printf("Deleting HLS segment: %s", segment)
						if err := os.Remove(segment); err != nil {
							log.Printf("Error deleting HLS segment %s: %v", segment, err)
						} else {
							log.Printf("Successfully deleted HLS segment: %s", segment)
						}
					}
				}
			}
		}

		// Delete MP4 files for this camera and date
		if cameraName != "" {
			// Extract date from created_at to find MP4 files
			var createdAt time.Time
			err := db.QueryRow("SELECT created_at FROM videos WHERE id = ?", id).Scan(&createdAt)
			if err == nil {
				// Format date as YYYYMMDD for MP4 filename pattern
				dateStr := createdAt.Format("20060102")
				log.Printf("Video %s has date: %s", id, dateStr)

				// Extract the camera directory from the HLS path
				// HLS path is typically <storage_path>/recordings/<camera_name>/hls
				// So we need to go up one level to get the camera directory
				cameraDir := filepath.Dir(hlsPath)

				// MP4 directory is at the same level as HLS directory
				mp4Dir := filepath.Join(cameraDir, "mp4")
				log.Printf("Looking for MP4 files in directory: %s", mp4Dir)

				// Check if directory exists
				if _, err := os.Stat(mp4Dir); os.IsNotExist(err) {
					log.Printf("MP4 directory does not exist: %s", mp4Dir)
					continue
				}

				// List all files in the directory for debugging
				files, err := os.ReadDir(mp4Dir)
				if err == nil {
					log.Printf("Files in MP4 directory %s:", mp4Dir)
					for _, file := range files {
						log.Printf("  - %s", file.Name())
					}
				} else {
					log.Printf("Error reading MP4 directory: %v", err)
				}

				// Find and delete MP4 files matching pattern <cameraName>_YYYYMMDD_*.mp4
				mp4Pattern := fmt.Sprintf("%s_%s_*.mp4", cameraName, dateStr)
				log.Printf("Looking for MP4 files with pattern: %s in %s", mp4Pattern, mp4Dir)

				// Debug the full path pattern
				fullPattern := filepath.Join(mp4Dir, mp4Pattern)
				log.Printf("Full pattern for MP4 files: %s", fullPattern)

				mp4Files, err := filepath.Glob(fullPattern)

				if err != nil {
					log.Printf("Error finding MP4 files for %s on %s: %v", cameraName, dateStr, err)
				} else if len(mp4Files) == 0 {
					log.Printf("No MP4 files found matching pattern %s in %s", mp4Pattern, mp4Dir)
				} else {
					log.Printf("Found %d MP4 files to delete", len(mp4Files))
					for _, mp4File := range mp4Files {
						log.Printf("Deleting MP4 file: %s", mp4File)
						if err := os.Remove(mp4File); err != nil {
							log.Printf("Error deleting MP4 file %s: %v", mp4File, err)
						} else {
							log.Printf("Successfully deleted MP4 file: %s", mp4File)
						}
					}
				}
			}
		}

		// Update HLS playlist if it exists
		if hlsPath != "" {
			playlistPath := filepath.Join(hlsPath, "playlist.m3u8")
			if _, err := os.Stat(playlistPath); err == nil {
				log.Printf("Updating HLS playlist: %s", playlistPath)
				updateHLSPlaylist(playlistPath, expiry)
			}
		}

		// Send batch to API if we've reached 10 videos
		if len(batch) >= 10 {
			markVideosUnavailableWithClient(client, batch)
			updateVideoStatusInDatabase(db, processedIds)

			// Reset batch for next group
			batch = []string{}
			processedIds = []string{}
		}
	}

	// Process any remaining videos in the batch
	if len(batch) > 0 {
		markVideosUnavailableWithClient(client, batch)
		updateVideoStatusInDatabase(db, processedIds)
	}

	log.Printf("Video cleanup process completed")
}

// markVideosUnavailableWithClient calls the API to mark videos as unavailable using the VideoClient interface
func markVideosUnavailableWithClient(client VideoClient, uniqueIds []string) {
	if len(uniqueIds) == 0 {
		return
	}

	log.Printf("Marking %d videos as unavailable via API", len(uniqueIds))
	result, err := client.MarkVideosUnavailable(uniqueIds)
	if err != nil {
		log.Printf("Error marking videos as unavailable: %v", err)
		return
	}

	log.Printf("API response for marking videos unavailable: %v", result)
}

// cleanupExpiredVideos is now implemented as cleanupExpiredVideosWithClient

// markVideosUnavailable calls the API to mark videos as unavailable
func markVideosUnavailable(client *api.AyoIndoClient, uniqueIds []string) {
	// Reload configuration from database before API call
	// This ensures we have the latest venue code and secret key
	if err := client.ReloadConfigFromDatabase(); err != nil {
		log.Printf("Warning: Failed to reload config from database: %v", err)
	}
	
	markVideosUnavailableWithClient(client, uniqueIds)
}

// updateVideoStatusInDatabase updates the status of multiple videos in the database
func updateVideoStatusInDatabase(db *sql.DB, ids []string) {
	if len(ids) == 0 {
		return
	}

	// Create placeholders for the SQL query
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids)+1)
	args[0] = database.StatusUnavailable

	for i, id := range ids {
		placeholders[i] = "?"
		args[i+1] = id
	}

	// Build and execute the query
	query := "UPDATE videos SET status = ? WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	result, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("Error updating video status in database: %v", err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	log.Printf("Updated %d videos to status 'unavailable' in database", rowsAffected)
}

// updateHLSPlaylist modifies the HLS playlist to remove segments older than the expiry date
// and deletes the corresponding .ts segment files
func updateHLSPlaylist(playlistPath string, expiry time.Time) {
	// Check if playlist file exists
	if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
		log.Printf("Playlist file not found: %s", playlistPath)
		return
	}

	// Open the playlist file
	file, err := os.Open(playlistPath)
	if err != nil {
		log.Printf("Error opening playlist file %s: %v", playlistPath, err)
		return
	}
	defer file.Close()

	// Read the playlist content
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading playlist file: %v", err)
		return
	}

	// Process the playlist to remove old segments
	var newLines []string
	skipNext := false
	hlsDir := filepath.Dir(playlistPath)
	var segmentsToDelete []string

	for _, line := range lines {
		// Keep header lines and non-segment lines
		if strings.HasPrefix(line, "#EXTM3U") || strings.HasPrefix(line, "#EXT-X-VERSION") ||
			strings.HasPrefix(line, "#EXT-X-TARGETDURATION") || strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE") {
			newLines = append(newLines, line)
			continue
		}

		// Skip segment if flagged
		if skipNext {
			skipNext = false
			continue
		}

		// Check if line is a segment file
		if strings.HasPrefix(line, "segment_") && strings.HasSuffix(line, ".ts") {
			// Parse the segment timestamp
			// Format: segment_20250605_021303.ts
			parts := strings.Split(strings.TrimSuffix(line, ".ts"), "_")
			if len(parts) == 3 {
				dateStr := parts[1]
				timeStr := parts[2]
				segmentTimeStr := dateStr + "_" + timeStr
				segmentTime, err := time.ParseInLocation("20060102_150405", segmentTimeStr, time.Local)

				if err == nil && segmentTime.Before(expiry) {
					// This segment is older than expiry, skip it and its duration line
					skipNext = true
					// Add to list of segments to delete
					segmentsToDelete = append(segmentsToDelete, line)
					continue
				}
			}
		}

		// Add line to new content
		newLines = append(newLines, line)
	}

	// Write the updated playlist back to file
	tempFile := playlistPath + ".tmp"
	outFile, err := os.Create(tempFile)
	if err != nil {
		log.Printf("Error creating temp playlist file: %v", err)
		return
	}

	for _, line := range newLines {
		outFile.WriteString(line + "\n")
	}

	outFile.Close()

	// Replace the original file with the updated one
	if err := os.Rename(tempFile, playlistPath); err != nil {
		log.Printf("Error replacing playlist file: %v", err)
		return
	}

	log.Printf("Updated HLS playlist: %s", playlistPath)

	// Delete the expired .ts segment files
	for _, segment := range segmentsToDelete {
		segmentPath := filepath.Join(hlsDir, segment)
		if err := os.Remove(segmentPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Error deleting segment file %s: %v", segmentPath, err)
		} else {
			log.Printf("Deleted segment file: %s", segmentPath)
		}
	}

	if len(segmentsToDelete) > 0 {
		log.Printf("Deleted %d expired segment files from %s", len(segmentsToDelete), hlsDir)
	}
}
