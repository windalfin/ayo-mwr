package offline

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/service"
	"ayo-mwr/storage"
)

// QueueManager manages offline task processing
type QueueManager struct {
	db                 database.Database
	connectivityChecker *ConnectivityChecker
	uploadService      *service.UploadService
	r2Storage          *storage.R2Storage
	ayoClient          service.AyoAPIClient
	config             *config.Config
	isRunning          bool
	stopChan           chan struct{}
	
	// Concurrency control
	semaphore        chan struct{} // Semaphore untuk membatasi concurrent tasks
	maxConcurrency   int           // Maximum concurrent tasks
	
	// Metrics for monitoring
	activeTasks      int           // Current active tasks (concurrent processing)
	processedTasks   int           // Total tasks processed since start
	lastProcessTime  time.Time     // Last time tasks were processed
	tasksMutex       sync.RWMutex  // Mutex for thread-safe metrics
}

// NewQueueManager creates a new queue manager
func NewQueueManager(db database.Database, uploadService *service.UploadService, r2Storage *storage.R2Storage, ayoClient service.AyoAPIClient, cfg *config.Config) *QueueManager {
	maxConcurrency := 3 // Process max 3 tasks concurrently
	
	return &QueueManager{
		db:                  db,
		connectivityChecker: NewConnectivityChecker(),
		uploadService:       uploadService,
		r2Storage:           r2Storage,
		ayoClient:           ayoClient,
		config:              cfg,
		isRunning:           false,
		stopChan:            make(chan struct{}),
		maxConcurrency:      maxConcurrency,
		semaphore:           make(chan struct{}, maxConcurrency),
	}
}

// Start begins the queue processing
func (qm *QueueManager) Start() {
	if qm.isRunning {
		log.Printf("📦 QUEUE: Queue manager sudah berjalan")
		return
	}

	qm.isRunning = true
	log.Printf("📦 QUEUE: 🚀 Memulai queue manager...")

	// Start connectivity monitoring
	qm.connectivityChecker.StartPeriodicCheck(30*time.Second, func(isOnline bool) {
		if isOnline {
			log.Printf("📦 QUEUE: 🌐 Koneksi kembali - memproses task yang tertunda...")
			qm.processQueuedTasks()
		}
	})

	// Start main processing loop
	go qm.processingLoop()

	// Start cleanup routine
	go qm.cleanupLoop()
}

// Stop stops the queue processing
func (qm *QueueManager) Stop() {
	if !qm.isRunning {
		return
	}

	log.Printf("📦 QUEUE: 🛑 Menghentikan queue manager...")
	qm.isRunning = false
	close(qm.stopChan)
}

// processingLoop is the main processing loop
func (qm *QueueManager) processingLoop() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds for faster processing
	defer ticker.Stop()

	for {
		select {
		case <-qm.stopChan:
			log.Printf("📦 QUEUE: Processing loop berhenti")
			return
		case <-ticker.C:
			// Process in background to avoid blocking the ticker
			go qm.processQueuedTasks()
		}
	}
}

// cleanupLoop removes old completed tasks
func (qm *QueueManager) cleanupLoop() {
	ticker := time.NewTicker(24 * time.Hour) // Cleanup daily
	defer ticker.Stop()

	for {
		select {
		case <-qm.stopChan:
			log.Printf("📦 QUEUE: Cleanup loop berhenti")
			return
		case <-ticker.C:
			qm.cleanupCompletedTasks()
		}
	}
}

// processQueuedTasks processes all pending tasks
func (qm *QueueManager) processQueuedTasks() {
	if !qm.connectivityChecker.IsOnline() {
		log.Printf("📦 QUEUE: ❌ Tidak ada koneksi internet - melewati pemrosesan task")
		return
	}

	tasks, err := qm.db.GetPendingTasks(10) // Process 10 tasks at a time
	if err != nil {
		log.Printf("📦 QUEUE: ❌ Error mengambil pending tasks: %v", err)
		return
	}

	if len(tasks) == 0 {
		// Don't log this every time - only log every 10 minutes
		return
	}

	log.Printf("📦 QUEUE: 🔄 Memproses %d task yang tertunda (max %d concurrent)...", len(tasks), qm.maxConcurrency)

	// Process tasks without blocking - fire and forget
	tasksProcessed := 0
	for _, task := range tasks {
		// Try to acquire semaphore (non-blocking)
		select {
		case qm.semaphore <- struct{}{}:
			// Got semaphore, proceed with task
			tasksProcessed++
			go func(t database.PendingTask) {
				defer func() { <-qm.semaphore }() // Release semaphore
				
				qm.processTask(t)
			}(task)
		default:
			// Semaphore full, skip this task for now
			log.Printf("📦 QUEUE: ⏸️ Task %d ditunda - semua worker sibuk (%d/%d)", 
				task.ID, len(qm.semaphore), qm.maxConcurrency)
		}
	}
	
	// Update last process time
	qm.tasksMutex.Lock()
	qm.lastProcessTime = time.Now()
	qm.tasksMutex.Unlock()
	
	// Don't wait - let tasks process asynchronously
	if tasksProcessed > 0 {
		log.Printf("📦 QUEUE: 🚀 Memulai %d task async (total %d task tersedia)", tasksProcessed, len(tasks))
	}
}

// processTask processes a single task
func (qm *QueueManager) processTask(task database.PendingTask) {
	// Update metrics
	qm.tasksMutex.Lock()
	qm.activeTasks++
	qm.tasksMutex.Unlock()
	
	defer func() {
		qm.tasksMutex.Lock()
		qm.activeTasks--
		qm.processedTasks++
		qm.tasksMutex.Unlock()
	}()
	
	log.Printf("📦 QUEUE: 🎯 [ASYNC] Memproses task %d (type: %s, attempt: %d/%d, active: %d)", 
		task.ID, task.TaskType, task.Attempts+1, task.MaxAttempts, qm.activeTasks)

	// Check dependencies before processing
	if task.TaskType == database.TaskNotifyAyoAPI {
		canProcess, err := qm.canProcessNotifyTask(task)
		if err != nil {
			log.Printf("📦 QUEUE: ❌ Error checking task dependency: %v", err)
			return
		}
		if !canProcess {
			log.Printf("📦 QUEUE: ⏸️ Task %d ditunda - menunggu upload selesai dulu", task.ID)
			
			// Parse task data to get VideoID
			var taskData database.AyoAPINotifyTaskData
			err := json.Unmarshal([]byte(task.TaskData), &taskData)
			if err != nil {
				log.Printf("📦 QUEUE: ❌ Error parsing task data: %v", err)
				return
			}
			
			video, err := qm.db.GetVideo(taskData.VideoID)
			if err != nil {
				log.Printf("📦 QUEUE: ❌ Error getting video data: %v", err)
				return
			}
			if video.Status == database.StatusFailed {
				err := qm.db.UpdateTaskStatus(task.ID, database.TaskStatusFailed, fmt.Sprintf("video status failed: %s", taskData.VideoID))
				if err != nil {
					log.Printf("📦 QUEUE: ❌ Error updating task status: %v", err)
				}
				return
			}
			return
		}
	}

	// Mark task as processing
	err := qm.db.UpdateTaskStatus(task.ID, database.TaskStatusProcessing, "")
	if err != nil {
		log.Printf("📦 QUEUE: ❌ Error update task status: %v", err)
		return
	}

	var processErr error

	switch task.TaskType {
	case database.TaskUploadR2:
		processErr = qm.processR2UploadTask(task)
	case database.TaskNotifyAyoAPI:
		processErr = qm.processAyoAPINotifyTask(task)
	default:
		processErr = fmt.Errorf("unknown task type: %s", task.TaskType)
	}

	if processErr != nil {
		qm.handleTaskFailure(task, processErr)
	} else {
		qm.handleTaskSuccess(task)
	}
}

// processR2UploadTask processes R2 upload task using existing UploadProcessedVideo function
func (qm *QueueManager) processR2UploadTask(task database.PendingTask) error {
	var taskData database.R2UploadTaskData
	err := json.Unmarshal([]byte(task.TaskData), &taskData)
	if err != nil {
		return fmt.Errorf("error parsing R2 upload task data: %v", err)
	}

	log.Printf("📦 QUEUE: 📤 Upload video %s ke R2 menggunakan UploadProcessedVideo...", taskData.VideoID)

	// Get video data to extract booking information
	video, err := qm.db.GetVideo(taskData.VideoID)
	if err != nil {
		return fmt.Errorf("error getting video data: %v", err)
	}
	
	if video == nil {
		return fmt.Errorf("video not found: %s", taskData.VideoID)
	}
	if video.Status == database.StatusFailed {
		err := qm.db.UpdateTaskStatus(task.ID, database.TaskStatusFailed, fmt.Sprintf("video status failed: %s", taskData.VideoID))
		if err != nil {
			log.Printf("📦 QUEUE: ❌ Error updating task status: %v", err)
		}
		return fmt.Errorf("video status failed: %s", taskData.VideoID)
	}

	// Create BookingVideoService instance dengan dependencies yang diperlukan
	bookingVideoService := service.NewBookingVideoService(qm.db, qm.ayoClient, qm.r2Storage, qm.config)

	// Use the existing UploadProcessedVideo function
	previewURL, thumbnailURL, err := bookingVideoService.UploadProcessedVideo(
		taskData.VideoID,           // uniqueID
		taskData.LocalMP4Path,      // videoPath (watermarked video)
		video.BookingID,            // bookingID
		video.CameraName,           // cameraName
	)
	
	if err != nil {
		return fmt.Errorf("error in UploadProcessedVideo: %v", err)
	}

	log.Printf("📦 QUEUE: ✅ Upload R2 berhasil untuk video %s", taskData.VideoID)
	log.Printf("📦 QUEUE: - Preview URL: %s", previewURL)
	log.Printf("📦 QUEUE: - Thumbnail URL: %s", thumbnailURL)
	
	return nil
}

// canProcessNotifyTask checks if notify task can be processed (upload must be completed first)
func (qm *QueueManager) canProcessNotifyTask(notifyTask database.PendingTask) (bool, error) {
	var taskData database.AyoAPINotifyTaskData
	err := json.Unmarshal([]byte(notifyTask.TaskData), &taskData)
	if err != nil {
		return false, fmt.Errorf("error parsing notify task data: %v", err)
	}

	// Get all pending tasks to check if there's an upload task for the same video
	allTasks, err := qm.db.GetPendingTasks(100) // Get more tasks to check dependencies
	if err != nil {
		return false, fmt.Errorf("error getting pending tasks: %v", err)
	}

	// Check if there's any upload task for the same video that's still pending/processing
	for _, task := range allTasks {
		if task.TaskType == database.TaskUploadR2 && task.Status != database.TaskStatusCompleted {
			var uploadTaskData database.R2UploadTaskData
			err := json.Unmarshal([]byte(task.TaskData), &uploadTaskData)
			if err != nil {
				continue // Skip if can't parse
			}
			
			if uploadTaskData.VideoID == taskData.VideoID {
				log.Printf("📦 QUEUE: 🔗 Task notify %d menunggu upload task %d untuk video %s", 
					notifyTask.ID, task.ID, taskData.VideoID)
				return false, nil
			}
		}
	}

	// Check if video has required URLs in database
	video, err := qm.db.GetVideo(taskData.VideoID)
	if err != nil {
		return false, fmt.Errorf("error getting video data: %v", err)
	}
	
	if video == nil {
		return false, fmt.Errorf("video not found: %s", taskData.VideoID)
	}

	// Check if required URLs are available
	if video.R2PreviewMP4URL == "" || video.R2PreviewPNGURL == "" {
		log.Printf("📦 QUEUE: ⚠️ Video %s belum memiliki URL preview/thumbnail yang diperlukan", taskData.VideoID)
		return false, nil
	}

	return true, nil
}

// processAyoAPINotifyTask processes AYO API notification task
func (qm *QueueManager) processAyoAPINotifyTask(task database.PendingTask) error {
	var taskData database.AyoAPINotifyTaskData
	err := json.Unmarshal([]byte(task.TaskData), &taskData)
	if err != nil {
		return fmt.Errorf("error parsing AYO API notify task data: %v", err)
	}

	// Get latest video data from database to get actual URLs after upload
	video, err := qm.db.GetVideo(taskData.VideoID)
	if err != nil {
		return fmt.Errorf("error getting video data from database: %v", err)
	}
	
	if video == nil {
		return fmt.Errorf("video not found in database: %s", taskData.VideoID)
	}
	if video.Status == database.StatusFailed {
		err := qm.db.UpdateTaskStatus(task.ID, database.TaskStatusFailed, fmt.Sprintf("video status failed: %s", taskData.VideoID))
		if err != nil {
			log.Printf("📦 QUEUE: ❌ Error updating task status: %v", err)
		}
		return fmt.Errorf("video status failed: %s", taskData.VideoID)
	}

	// Use actual URLs from database (updated after upload completed)
	actualPreviewURL := video.R2PreviewMP4URL
	actualThumbnailURL := video.R2PreviewPNGURL
	actualMP4URL := video.R2MP4URL
	
	log.Printf("📦 QUEUE: 📡 Notifikasi AYO API untuk video %s (uniqueID: %s)...", 
		taskData.VideoID, taskData.UniqueID)
	log.Printf("📦 QUEUE: 📡 Using URLs: MP4=%s, Preview=%s, Thumbnail=%s", 
		actualMP4URL, actualPreviewURL, actualThumbnailURL)

	// Use the upload service to notify AYO API with actual URLs
	err = qm.uploadService.NotifyAyoAPI(
		taskData.UniqueID,
		actualMP4URL,
		actualPreviewURL,
		actualThumbnailURL,
		taskData.Duration,
	)
	if err != nil {
		return fmt.Errorf("error notifying AYO API: %v", err)
	}

	log.Printf("📦 QUEUE: ✅ Notifikasi AYO API berhasil untuk video %s", taskData.VideoID)
	return nil
}

// handleTaskSuccess handles successful task completion
func (qm *QueueManager) handleTaskSuccess(task database.PendingTask) {
	log.Printf("📦 QUEUE: ✅ Task %d berhasil diselesaikan", task.ID)
	
	err := qm.db.UpdateTaskStatus(task.ID, database.TaskStatusCompleted, "")
	if err != nil {
		log.Printf("📦 QUEUE: ❌ Error marking task as completed: %v", err)
	}
}

// handleTaskFailure handles task failure and retry logic
func (qm *QueueManager) handleTaskFailure(task database.PendingTask, taskErr error) {
	attempts := task.Attempts + 1
	errorMsg := taskErr.Error()
	
	log.Printf("📦 QUEUE: ❌ Task %d gagal (attempt %d/%d): %v", 
		task.ID, attempts, task.MaxAttempts, taskErr)

	if attempts >= task.MaxAttempts {
		// Mark as permanently failed
		log.Printf("📦 QUEUE: 💀 Task %d gagal permanen setelah %d percobaan", task.ID, attempts)
		err := qm.db.UpdateTaskStatus(task.ID, database.TaskStatusFailed, errorMsg)
		if err != nil {
			log.Printf("📦 QUEUE: ❌ Error marking task as failed: %v", err)
		}
		return
	}

	// Calculate next retry time with exponential backoff
	backoffMinutes := []int{5, 20, 45, 120, 300} // 5min, 20min, 45min, 2h, 5h
	backoffIndex := attempts - 1
	if backoffIndex >= len(backoffMinutes) {
		backoffIndex = len(backoffMinutes) - 1
	}
	
	nextRetry := time.Now().Add(time.Duration(backoffMinutes[backoffIndex]) * time.Minute)
	
	log.Printf("📦 QUEUE: 🔄 Task %d akan dicoba lagi pada %v (dalam %d menit)", 
		task.ID, nextRetry.Format("15:04:05"), backoffMinutes[backoffIndex])

	err := qm.db.UpdateTaskNextRetry(task.ID, nextRetry, attempts)
	if err != nil {
		log.Printf("📦 QUEUE: ❌ Error scheduling task retry: %v", err)
	}
}

// cleanupCompletedTasks removes old completed tasks
func (qm *QueueManager) cleanupCompletedTasks() {
	olderThan := time.Now().Add(-7 * 24 * time.Hour) // Remove tasks older than 7 days
	
	err := qm.db.DeleteCompletedTasks(olderThan)
	if err != nil {
		log.Printf("📦 QUEUE: ❌ Error cleanup completed tasks: %v", err)
	} else {
		log.Printf("📦 QUEUE: 🧹 Cleanup completed tasks selesai")
	}
}

// EnqueueR2Upload adds an R2 upload task to the queue
func (qm *QueueManager) EnqueueR2Upload(videoID, localMP4Path, localPreviewPath, localThumbnailPath, r2Key, r2PreviewKey, r2ThumbnailKey string) error {
	taskData := database.R2UploadTaskData{
		VideoID:            videoID,
		LocalMP4Path:       localMP4Path,
		LocalPreviewPath:   localPreviewPath,
		LocalThumbnailPath: localThumbnailPath,
		R2Key:              r2Key,
		R2PreviewKey:       r2PreviewKey,
		R2ThumbnailKey:     r2ThumbnailKey,
	}

	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		return fmt.Errorf("error marshaling R2 upload task data: %v", err)
	}

	task := database.PendingTask{
		TaskType:    database.TaskUploadR2,
		TaskData:    string(taskDataJSON),
		Attempts:    0,
		MaxAttempts: 5,
		NextRetryAt: time.Now(),
		Status:      database.TaskStatusPending,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	err = qm.db.CreatePendingTask(task)
	if err != nil {
		return fmt.Errorf("error creating R2 upload task: %v", err)
	}

	log.Printf("📦 QUEUE: ➕ Task upload R2 ditambahkan untuk video %s", videoID)
	return nil
}

// EnqueueAyoAPINotify adds an AYO API notification task to the queue
func (qm *QueueManager) EnqueueAyoAPINotify(videoID, uniqueID, mp4URL, previewURL, thumbnailURL string, duration float64) error {
	taskData := database.AyoAPINotifyTaskData{
		VideoID:      videoID,
		UniqueID:     uniqueID,
		MP4URL:       mp4URL,
		PreviewURL:   previewURL,
		ThumbnailURL: thumbnailURL,
		Duration:     duration,
	}

	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		return fmt.Errorf("error marshaling AYO API notify task data: %v", err)
	}

	task := database.PendingTask{
		TaskType:    database.TaskNotifyAyoAPI,
		TaskData:    string(taskDataJSON),
		Attempts:    0,
		MaxAttempts: 3,
		NextRetryAt: time.Now(),
		Status:      database.TaskStatusPending,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	err = qm.db.CreatePendingTask(task)
	if err != nil {
		return fmt.Errorf("error creating AYO API notify task: %v", err)
	}

	log.Printf("📦 QUEUE: ➕ Task notifikasi AYO API ditambahkan untuk video %s (uniqueID: %s)", videoID, uniqueID)
	return nil
}

// GetQueueStats returns statistics about the queue
func (qm *QueueManager) GetQueueStats() (map[string]interface{}, error) {
	qm.tasksMutex.RLock()
	activeTasks := qm.activeTasks
	processedTasks := qm.processedTasks
	lastProcessTime := qm.lastProcessTime
	qm.tasksMutex.RUnlock()
	
	// Get pending tasks count from database
	pendingTasks, err := qm.db.GetPendingTasks(100)
	pendingCount := 0
	if err == nil {
		pendingCount = len(pendingTasks)
	}
	
	stats := map[string]interface{}{
		"is_online":          qm.connectivityChecker.IsOnline(),
		"active_tasks":       activeTasks,
		"processed_tasks":    processedTasks,
		"pending_tasks":      pendingCount,
		"max_concurrency":    qm.maxConcurrency,
		"available_slots":    qm.maxConcurrency - activeTasks,
		"last_process_time":  lastProcessTime.Format("2006-01-02 15:04:05"),
		"is_running":         qm.isRunning,
		"processing_mode":    "async",
	}
	
	return stats, nil
}

 