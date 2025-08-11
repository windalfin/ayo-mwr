package cron

import (
	"context"
	"log"
	"time"

	"ayo-mwr/chunks"
	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/storage"

	"github.com/robfig/cron/v3"
)

// ChunkProcessingCron handles the scheduled processing of video chunks
type ChunkProcessingCron struct {
	cron         *cron.Cron
	chunkManager *chunks.ChunkManager
	isRunning    bool
}

// NewChunkProcessingCron creates a new chunk processing cron job
func NewChunkProcessingCron(db database.Database, cfg *config.Config, storageManager *storage.DiskManager) *ChunkProcessingCron {
	chunkManager := chunks.NewChunkManager(db, cfg, storageManager)

	return &ChunkProcessingCron{
		cron:         cron.New(cron.WithSeconds()),
		chunkManager: chunkManager,
		isRunning:    false,
	}
}

// Start begins the chunk processing cron jobs
func (cpc *ChunkProcessingCron) Start() error {
	if cpc.isRunning {
		log.Println("[ChunkProcessingCron] Cron is already running")
		return nil
	}

	log.Println("[ChunkProcessingCron] Starting chunk processing cron jobs...")

	// Schedule chunk processing every 15 minutes at :02, :17, :32, :47
	// This gives a 2-minute buffer after each 15-minute boundary to ensure segments are available
	_, err := cpc.cron.AddFunc("0 2,17,32,47 * * * *", func() {
		cpc.processChunks()
	})
	if err != nil {
		return err
	}

	// Schedule chunk cleanup daily at 3:30 AM (offset from other cleanup tasks)
	_, err = cpc.cron.AddFunc("0 30 3 * * *", func() {
		cpc.cleanupOldChunks()
	})
	if err != nil {
		return err
	}

	// Schedule processing status cleanup every hour
	_, err = cpc.cron.AddFunc("0 5 * * * *", func() {
		cpc.cleanupStuckProcessing()
	})
	if err != nil {
		return err
	}

	cpc.cron.Start()
	cpc.isRunning = true

	log.Println("[ChunkProcessingCron] ✅ Chunk processing cron jobs started")
	log.Println("[ChunkProcessingCron] • Chunk processing: Every 15 minutes at :02, :17, :32, :47")
	log.Println("[ChunkProcessingCron] • Cleanup: Daily at 03:30")
	log.Println("[ChunkProcessingCron] • Status cleanup: Hourly at :05")

	return nil
}

// Stop stops the chunk processing cron jobs
func (cpc *ChunkProcessingCron) Stop() {
	if !cpc.isRunning {
		return
	}

	log.Println("[ChunkProcessingCron] Stopping chunk processing cron jobs...")
	
	// Get context for graceful shutdown
	ctx := cpc.cron.Stop()
	
	// Wait for current jobs to finish with timeout
	select {
	case <-ctx.Done():
		log.Println("[ChunkProcessingCron] ✅ Chunk processing cron stopped gracefully")
	case <-time.After(30 * time.Second):
		log.Println("[ChunkProcessingCron] ⚠️  Chunk processing cron stopped with timeout")
	}
	
	cpc.isRunning = false
}

// IsRunning returns whether the cron is currently running
func (cpc *ChunkProcessingCron) IsRunning() bool {
	return cpc.isRunning
}

// processChunks handles the scheduled chunk processing
func (cpc *ChunkProcessingCron) processChunks() {
	startTime := time.Now()
	log.Println("[ChunkProcessingCron] 🔄 Starting scheduled chunk processing...")

	// Create context with timeout (10 minutes max processing time)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Process pending chunks
	err := cpc.chunkManager.ProcessPendingChunks(ctx)
	if err != nil {
		log.Printf("[ChunkProcessingCron] ❌ Error during chunk processing: %v", err)
		return
	}

	duration := time.Since(startTime)
	log.Printf("[ChunkProcessingCron] ✅ Chunk processing completed in %v", duration)
}

// cleanupOldChunks handles the scheduled cleanup of old chunks
func (cpc *ChunkProcessingCron) cleanupOldChunks() {
	startTime := time.Now()
	log.Println("[ChunkProcessingCron] 🧹 Starting scheduled chunk cleanup...")

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Cleanup old chunks
	err := cpc.chunkManager.CleanupOldChunks(ctx)
	if err != nil {
		log.Printf("[ChunkProcessingCron] ❌ Error during chunk cleanup: %v", err)
		return
	}

	duration := time.Since(startTime)
	log.Printf("[ChunkProcessingCron] ✅ Chunk cleanup completed in %v", duration)
}

// cleanupStuckProcessing handles cleanup of chunks stuck in "processing" status
func (cpc *ChunkProcessingCron) cleanupStuckProcessing() {
	log.Println("[ChunkProcessingCron] 🔧 Cleaning up stuck chunk processing...")

	// Get chunks stuck in processing status (older than 30 minutes)
	stuckChunks, err := cpc.chunkManager.GetChunksByProcessingStatus(database.ProcessingStatusProcessing)
	if err != nil {
		log.Printf("[ChunkProcessingCron] Error getting stuck chunks: %v", err)
		return
	}

	stuckTimeout := time.Now().Add(-30 * time.Minute)
	cleanedCount := 0

	for _, chunk := range stuckChunks {
		if chunk.CreatedAt.Before(stuckTimeout) {
			// Mark as failed so it can be retried
			err := cpc.chunkManager.UpdateChunkProcessingStatus(chunk.ID, database.ProcessingStatusFailed)
			if err != nil {
				log.Printf("[ChunkProcessingCron] Error updating stuck chunk %s: %v", chunk.ID, err)
				continue
			}
			cleanedCount++
		}
	}

	if cleanedCount > 0 {
		log.Printf("[ChunkProcessingCron] ✅ Cleaned up %d stuck chunk processing jobs", cleanedCount)
	}
}

// GetNextChunkProcessingTime returns the next scheduled chunk processing time
func (cpc *ChunkProcessingCron) GetNextChunkProcessingTime() time.Time {
	if !cpc.isRunning {
		return time.Time{}
	}

	entries := cpc.cron.Entries()
	if len(entries) > 0 {
		return entries[0].Next // First entry is the chunk processing job
	}

	return time.Time{}
}

// GetChunkStatistics returns current chunk processing statistics
func (cpc *ChunkProcessingCron) GetChunkStatistics() (map[string]interface{}, error) {
	stats, err := cpc.chunkManager.GetChunkStatistics()
	if err != nil {
		return nil, err
	}

	// Add cron-specific information
	stats["cron_running"] = cpc.isRunning
	if cpc.isRunning {
		stats["next_processing"] = cpc.GetNextChunkProcessingTime()
		stats["cron_entries"] = len(cpc.cron.Entries())
	}

	return stats, nil
}

// ForceChunkProcessing manually triggers chunk processing (for testing/admin use)
func (cpc *ChunkProcessingCron) ForceChunkProcessing() error {
	log.Println("[ChunkProcessingCron] 🔄 Manual chunk processing triggered...")

	// Use background context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	return cpc.chunkManager.ProcessPendingChunks(ctx)
}

// ForceChunkCleanup manually triggers chunk cleanup (for testing/admin use)
func (cpc *ChunkProcessingCron) ForceChunkCleanup() error {
	log.Println("[ChunkProcessingCron] 🧹 Manual chunk cleanup triggered...")

	// Use background context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	return cpc.chunkManager.CleanupOldChunks(ctx)
}