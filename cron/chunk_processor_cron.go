package cron

import (
	"context"
	"log"
	"time"

	"ayo-mwr/database"
	"ayo-mwr/service"
	"ayo-mwr/storage"
)

// ChunkProcessorCron handles the 15-minute chunk processing schedule
type ChunkProcessorCron struct {
	db             database.Database
	storageManager *storage.DiskManager
	processor      *service.ChunkProcessor
	ticker         *time.Ticker
	stopChan       chan bool
}

// NewChunkProcessorCron creates a new chunk processor cron
func NewChunkProcessorCron(db database.Database, storageManager *storage.DiskManager) *ChunkProcessorCron {
	processor := service.NewChunkProcessor(db, storageManager)
	
	return &ChunkProcessorCron{
		db:             db,
		storageManager: storageManager,
		processor:      processor,
		stopChan:       make(chan bool),
	}
}

// Start begins the chunk processing cron job (every 15 minutes)
func (cpc *ChunkProcessorCron) Start() {
	log.Println("[ChunkProcessorCron] 🚀 Starting chunk processor cron (every 15 minutes)")
	
	// Calculate time until next 15-minute boundary
	now := time.Now()
	nextRun := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), (now.Minute()/15+1)*15, 0, 0, now.Location())
	if nextRun.Minute() == 60 {
		nextRun = nextRun.Add(-60*time.Minute).Add(1*time.Hour)
	}
	
	initialDelay := nextRun.Sub(now)
	log.Printf("[ChunkProcessorCron] First run scheduled in %v at %s", initialDelay, nextRun.Format("15:04:05"))
	
	// Run immediately for initial processing
	go cpc.runChunkProcessing()
	
	// Schedule to run at 15-minute boundaries (:00, :15, :30, :45)
	go func() {
		// Wait until the next 15-minute boundary
		time.Sleep(initialDelay)
		
		// Run the first scheduled processing
		go cpc.runChunkProcessing()
		
		// Then run every 15 minutes
		cpc.ticker = time.NewTicker(15 * time.Minute)
		
		for {
			select {
			case <-cpc.ticker.C:
				go cpc.runChunkProcessing()
			case <-cpc.stopChan:
				log.Println("[ChunkProcessorCron] 🛑 Stopping chunk processor cron")
				return
			}
		}
	}()
}

// Stop stops the chunk processing cron job
func (cpc *ChunkProcessorCron) Stop() {
	if cpc.ticker != nil {
		cpc.ticker.Stop()
	}
	cpc.stopChan <- true
}

// runChunkProcessing performs the actual chunk processing
func (cpc *ChunkProcessorCron) runChunkProcessing() {
	log.Println("[ChunkProcessorCron] 🔄 Starting chunk processing...")
	
	startTime := time.Now()
	
	// Create context with timeout (max 10 minutes for processing)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	
	// Run the chunk processor
	err := cpc.processor.ProcessChunks(ctx)
	if err != nil {
		log.Printf("[ChunkProcessorCron] ❌ Error during chunk processing: %v", err)
		return
	}
	
	duration := time.Since(startTime)
	log.Printf("[ChunkProcessorCron] ✅ Chunk processing completed in %v", duration)
	
	// Log statistics
	stats, err := cpc.processor.GetProcessingStats()
	if err != nil {
		log.Printf("[ChunkProcessorCron] Warning: Could not get processing stats: %v", err)
		return
	}
	
	log.Printf("[ChunkProcessorCron] 📊 Processing stats: %+v", stats)
}

// RunManualProcessing runs chunk processing manually (for API endpoints)
func (cpc *ChunkProcessorCron) RunManualProcessing() error {
	log.Println("[ChunkProcessorCron] 🔧 Running manual chunk processing...")
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	
	return cpc.processor.ProcessChunks(ctx)
}

// GetProcessingStats returns current processing statistics
func (cpc *ChunkProcessorCron) GetProcessingStats() (map[string]interface{}, error) {
	return cpc.processor.GetProcessingStats()
}
