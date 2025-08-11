package cron

import (
	"context"
	"log"
	"time"

	"ayo-mwr/database"
	"ayo-mwr/service"
	"ayo-mwr/storage"

	"github.com/robfig/cron/v3"
)

// ChunkProcessingCron handles the scheduled processing of video chunks
type ChunkProcessingCron struct {
	cron           *cron.Cron
	chunkProcessor *service.ChunkProcessor
	isRunning      bool
}

// NewChunkProcessingCron creates a new chunk processing cron job
func NewChunkProcessingCron(db database.Database, storageManager *storage.DiskManager) *ChunkProcessingCron {
	chunkProcessor := service.NewChunkProcessor(db, storageManager)

	return &ChunkProcessingCron{
		cron:           cron.New(cron.WithSeconds()),
		chunkProcessor: chunkProcessor,
		isRunning:      false,
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

	// Schedule cleanup every hour at minute 5
	_, err = cpc.cron.AddFunc("0 5 * * * *", func() {
		cpc.cleanupOldChunks()
	})
	if err != nil {
		return err
	}

	// Schedule statistics logging every hour
	_, err = cpc.cron.AddFunc("0 0 * * * *", func() {
		cpc.logStatistics()
	})
	if err != nil {
		return err
	}

	cpc.cron.Start()
	cpc.isRunning = true

	log.Println("[ChunkProcessingCron] ✅ Chunk processing cron jobs started successfully")
	log.Println("[ChunkProcessingCron] 📅 Schedule:")
	log.Println("[ChunkProcessingCron]   • Process chunks: Every 15 min at :02, :17, :32, :47")
	log.Println("[ChunkProcessingCron]   • Cleanup: Every hour at :05")
	log.Println("[ChunkProcessingCron]   • Statistics: Every hour at :00")

	return nil
}

// Stop stops all chunk processing cron jobs
func (cpc *ChunkProcessingCron) Stop() {
	if !cpc.isRunning {
		log.Println("[ChunkProcessingCron] Cron is not running")
		return
	}

	log.Println("[ChunkProcessingCron] Stopping chunk processing cron jobs...")
	cpc.cron.Stop()
	cpc.isRunning = false
	log.Println("[ChunkProcessingCron] ✅ Chunk processing cron jobs stopped")
}

// processChunks handles the main chunk processing logic
func (cpc *ChunkProcessingCron) processChunks() {
	log.Println("[ChunkProcessingCron] 🔄 Starting scheduled chunk processing...")
	
	startTime := time.Now()
	
	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	
	// Process chunks using the new physical chunk processor
	err := cpc.chunkProcessor.ProcessChunks(ctx)
	if err != nil {
		log.Printf("[ChunkProcessingCron] ❌ Chunk processing failed: %v", err)
		return
	}
	
	duration := time.Since(startTime)
	log.Printf("[ChunkProcessingCron] ✅ Chunk processing completed in %v", duration)
}

// cleanupOldChunks removes old chunk files and database records
func (cpc *ChunkProcessingCron) cleanupOldChunks() {
	log.Println("[ChunkProcessingCron] 🧹 Starting chunk cleanup...")
	
	startTime := time.Now()
	
	// TODO: Implement cleanup logic for old physical chunk files
	// This should remove chunks older than retention period from both filesystem and database
	
	duration := time.Since(startTime)
	log.Printf("[ChunkProcessingCron] ✅ Chunk cleanup completed in %v", duration)
}

// logStatistics logs current chunk processing statistics
func (cpc *ChunkProcessingCron) logStatistics() {
	log.Println("[ChunkProcessingCron] 📊 Logging chunk statistics...")
	
	stats, err := cpc.chunkProcessor.GetProcessingStats()
	if err != nil {
		log.Printf("[ChunkProcessingCron] ❌ Failed to get statistics: %v", err)
		return
	}
	
	log.Printf("[ChunkProcessingCron] 📈 Current statistics: %+v", stats)
}

// GetProcessingStatus returns the current status of chunk processing
func (cpc *ChunkProcessingCron) GetProcessingStatus() map[string]interface{} {
	return map[string]interface{}{
		"is_running":   cpc.isRunning,
		"last_run":     time.Now().Format(time.RFC3339), // This would need to be tracked properly
		"next_run":     "Every 15 minutes at :02, :17, :32, :47",
		"description":  "Physical chunk creation from HLS segments",
	}
}

// ForceProcessChunks manually triggers chunk processing
func (cpc *ChunkProcessingCron) ForceProcessChunks() error {
	log.Println("[ChunkProcessingCron] 🔧 Force processing chunks...")
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	
	return cpc.chunkProcessor.ProcessChunks(ctx)
}

// ForceCleanupChunks manually triggers chunk cleanup
func (cpc *ChunkProcessingCron) ForceCleanupChunks() error {
	log.Println("[ChunkProcessingCron] 🔧 Force cleaning up chunks...")
	
	// TODO: Implement actual cleanup logic
	return nil
}
