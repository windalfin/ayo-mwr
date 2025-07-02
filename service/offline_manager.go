package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
)

// OfflineManager handles offline operations and retry logic
type OfflineManager struct {
	cfg              *config.Config
	db               database.Database
	ayoClient        AyoAPIClient
	retryQueue       []RetryItem
	mu               sync.RWMutex
	isOnline         bool
	lastConnectCheck time.Time
	retryInterval    time.Duration
	maxRetries       int
	queuePath        string
}

// RetryItem represents an item in the retry queue
type RetryItem struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"` // "save_video_available", "save_video_request"
	Data        map[string]interface{} `json:"data"`
	Attempts    int                    `json:"attempts"`
	LastAttempt time.Time              `json:"last_attempt"`
	CreatedAt   time.Time              `json:"created_at"`
	Priority    int                    `json:"priority"` // 1 = high, 2 = medium, 3 = low
}

// NewOfflineManager creates a new offline manager
func NewOfflineManager(cfg *config.Config, db database.Database, ayoClient AyoAPIClient) *OfflineManager {
	queuePath := filepath.Join(cfg.StoragePath, "offline_queue.json")
	
	om := &OfflineManager{
		cfg:           cfg,
		db:            db,
		ayoClient:     ayoClient,
		retryQueue:    []RetryItem{},
		isOnline:      true,
		retryInterval: 30 * time.Second,
		maxRetries:    50, // Allow many retries for network issues
		queuePath:     queuePath,
	}
	
	// Load existing queue from disk
	om.loadRetryQueue()
	
	return om
}

// Start begins the offline manager operations
func (om *OfflineManager) Start() {
	log.Println("[offline] Starting offline manager")
	
	// Start connectivity monitoring
	go om.connectivityMonitor()
	
	// Start retry processor
	go om.retryProcessor()
}

// IsOnline returns current connectivity status
func (om *OfflineManager) IsOnline() bool {
	om.mu.RLock()
	defer om.mu.RUnlock()
	return om.isOnline
}

// AddSaveVideoAvailableRetry adds a SaveVideoAvailable call to retry queue
func (om *OfflineManager) AddSaveVideoAvailableRetry(bookingID, videoType, previewURL, thumbnailURL, uniqueID string, startTime, endTime time.Time) {
	item := RetryItem{
		ID:   fmt.Sprintf("save_video_%s_%d", uniqueID, time.Now().Unix()),
		Type: "save_video_available",
		Data: map[string]interface{}{
			"booking_id":     bookingID,
			"video_type":     videoType,
			"preview_url":    previewURL,
			"thumbnail_url":  thumbnailURL,
			"unique_id":      uniqueID,
			"start_time":     startTime,
			"end_time":       endTime,
		},
		Attempts:  0,
		CreatedAt: time.Now(),
		Priority:  1, // High priority for video availability
	}
	
	om.addToRetryQueue(item)
	log.Printf("[offline] Added SaveVideoAvailable retry for booking %s, uniqueID %s", bookingID, uniqueID)
}

// AddVideoRequestRetry adds a video request processing to retry queue
func (om *OfflineManager) AddVideoRequestRetry(requestData map[string]interface{}) {
	item := RetryItem{
		ID:        fmt.Sprintf("video_request_%d", time.Now().Unix()),
		Type:      "save_video_request",
		Data:      requestData,
		Attempts:  0,
		CreatedAt: time.Now(),
		Priority:  2, // Medium priority for video requests
	}
	
	om.addToRetryQueue(item)
	log.Printf("[offline] Added video request retry for request: %v", requestData)
}

// TrySaveVideoAvailable attempts to call SaveVideoAvailable with offline handling
func (om *OfflineManager) TrySaveVideoAvailable(bookingID, videoType, previewURL, thumbnailURL, uniqueID string, startTime, endTime time.Time) error {
	if !om.IsOnline() {
		log.Printf("[offline] System offline, queuing SaveVideoAvailable for %s", uniqueID)
		om.AddSaveVideoAvailableRetry(bookingID, videoType, previewURL, thumbnailURL, uniqueID, startTime, endTime)
		return fmt.Errorf("system offline, call queued for retry")
	}
	
	// Try the actual call
	_, err := om.ayoClient.SaveVideoAvailable(bookingID, videoType, previewURL, thumbnailURL, uniqueID, startTime, endTime)
	if err != nil {
		// Check if it's a network error
		if isNetworkError(err) {
			log.Printf("[offline] Network error detected, queuing SaveVideoAvailable for %s: %v", uniqueID, err)
			om.AddSaveVideoAvailableRetry(bookingID, videoType, previewURL, thumbnailURL, uniqueID, startTime, endTime)
			om.setOfflineStatus(true)
		}
		return err
	}
	
	log.Printf("[offline] SaveVideoAvailable succeeded for %s", uniqueID)
	return nil
}

// ValidateLocalBooking checks if a booking exists in local database
func (om *OfflineManager) ValidateLocalBooking(fieldID int, requestTime time.Time) (bool, map[string]interface{}, error) {
	log.Printf("[offline] Validating local booking for field %d at %v", fieldID, requestTime)
	
	// In a real implementation, you would:
	// 1. Check for cached booking data from the last successful API call
	// 2. Query a local bookings table if you store booking data locally
	// 3. Check booking time windows against current time
	
	// For now, we'll implement a basic time-based validation
	// Assume bookings are typically during business hours (8 AM - 10 PM)
	hour := requestTime.Hour()
	if hour < 8 || hour > 22 {
		log.Printf("[offline] Request outside business hours (%d), likely no active booking", hour)
		return false, nil, fmt.Errorf("request outside business hours")
	}
	
	// TODO: Implement actual local booking validation based on your database schema
	// You could store the last successful booking response locally
	// and validate against that cache during offline periods
	
	bookingData := map[string]interface{}{
		"field_id":      fieldID,
		"is_active":     true,
		"validated_at":  time.Now(),
		"source":        "local_validation",
		"method":        "time_based",
		"business_hour": true,
	}
	
	log.Printf("[offline] Local validation passed for field %d (business hours check)", fieldID)
	return true, bookingData, nil
}

// ProcessOfflineClipRequest processes a clip request when offline
func (om *OfflineManager) ProcessOfflineClipRequest(fieldID int, cameraName string) error {
	// Validate booking exists locally
	isValid, bookingData, err := om.ValidateLocalBooking(fieldID, time.Now())
	if err != nil {
		return fmt.Errorf("failed to validate local booking: %v", err)
	}
	
	if !isValid {
		log.Printf("[offline] No active booking found for field %d, ignoring clip request", fieldID)
		return fmt.Errorf("no active booking for field %d", fieldID)
	}
	
	log.Printf("[offline] Processing offline clip request for field %d, camera %s", fieldID, cameraName)
	
	// Store the clip request locally with offline flag
	clipRequest := map[string]interface{}{
		"field_id":     fieldID,
		"camera_name":  cameraName,
		"request_time": time.Now(),
		"booking_data": bookingData,
		"offline_mode": true,
		"status":       "pending_online",
	}
	
	// Add to retry queue for when we're back online
	om.AddVideoRequestRetry(clipRequest)
	
	return nil
}

// connectivityMonitor continuously monitors internet connectivity
func (om *OfflineManager) connectivityMonitor() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			online := om.checkConnectivity()
			
			om.mu.Lock()
			wasOffline := !om.isOnline
			om.isOnline = online
			om.lastConnectCheck = time.Now()
			om.mu.Unlock()
			
			if wasOffline && online {
				log.Println("[offline] Internet connectivity restored!")
				// Trigger immediate retry processing
				go om.processRetryQueue()
			} else if !wasOffline && !online {
				log.Println("[offline] Internet connectivity lost!")
			}
		}
	}
}

// checkConnectivity tests internet connectivity
func (om *OfflineManager) checkConnectivity() bool {
	// Try multiple methods to check connectivity
	
	// Method 1: Try to resolve a reliable DNS
	if om.testDNSResolution() {
		return true
	}
	
	// Method 2: Try HTTP request with timeout
	if om.testHTTPConnectivity() {
		return true
	}
	
	return false
}

// testDNSResolution tests DNS resolution
func (om *OfflineManager) testDNSResolution() bool {
	_, err := net.LookupHost("google.com")
	return err == nil
}

// testHTTPConnectivity tests HTTP connectivity
func (om *OfflineManager) testHTTPConnectivity() bool {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	
	resp, err := client.Get("https://www.google.com")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	
	return resp.StatusCode == 200
}

// retryProcessor processes the retry queue periodically
func (om *OfflineManager) retryProcessor() {
	ticker := time.NewTicker(om.retryInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			if om.IsOnline() {
				om.processRetryQueue()
			}
		}
	}
}

// processRetryQueue processes all items in the retry queue
func (om *OfflineManager) processRetryQueue() {
	om.mu.Lock()
	defer om.mu.Unlock()
	
	if len(om.retryQueue) == 0 {
		return
	}
	
	log.Printf("[offline] Processing %d items in retry queue", len(om.retryQueue))
	
	var remainingItems []RetryItem
	processedCount := 0
	
	for _, item := range om.retryQueue {
		// Skip if too recent
		if time.Since(item.LastAttempt) < time.Duration(item.Attempts*30)*time.Second {
			remainingItems = append(remainingItems, item)
			continue
		}
		
		// Skip if too many attempts
		if item.Attempts >= om.maxRetries {
			log.Printf("[offline] Dropping retry item %s after %d attempts", item.ID, item.Attempts)
			continue
		}
		
		success := om.processRetryItem(&item)
		if success {
			log.Printf("[offline] Successfully processed retry item %s", item.ID)
			processedCount++
		} else {
			item.Attempts++
			item.LastAttempt = time.Now()
			remainingItems = append(remainingItems, item)
		}
	}
	
	om.retryQueue = remainingItems
	
	// Save updated queue to disk
	om.saveRetryQueue()
	
	if processedCount > 0 {
		log.Printf("[offline] Successfully processed %d retry items, %d remaining", processedCount, len(remainingItems))
	}
}

// processRetryItem processes a single retry item
func (om *OfflineManager) processRetryItem(item *RetryItem) bool {
	switch item.Type {
	case "save_video_available":
		return om.retrySaveVideoAvailable(item)
	case "save_video_request":
		return om.retryVideoRequest(item)
	default:
		log.Printf("[offline] Unknown retry item type: %s", item.Type)
		return true // Remove unknown types
	}
}

// retrySaveVideoAvailable retries a SaveVideoAvailable call
func (om *OfflineManager) retrySaveVideoAvailable(item *RetryItem) bool {
	data := item.Data
	
	bookingID, _ := data["booking_id"].(string)
	videoType, _ := data["video_type"].(string)
	previewURL, _ := data["preview_url"].(string)
	thumbnailURL, _ := data["thumbnail_url"].(string)
	uniqueID, _ := data["unique_id"].(string)
	
	startTime, _ := time.Parse(time.RFC3339, data["start_time"].(string))
	endTime, _ := time.Parse(time.RFC3339, data["end_time"].(string))
	
	log.Printf("[offline] Retrying SaveVideoAvailable for %s (attempt %d)", uniqueID, item.Attempts+1)
	
	_, err := om.ayoClient.SaveVideoAvailable(bookingID, videoType, previewURL, thumbnailURL, uniqueID, startTime, endTime)
	if err != nil {
		if isNetworkError(err) {
			om.setOfflineStatus(true)
		}
		log.Printf("[offline] SaveVideoAvailable retry failed for %s: %v", uniqueID, err)
		return false
	}
	
	return true
}

// retryVideoRequest retries a video request processing
func (om *OfflineManager) retryVideoRequest(item *RetryItem) bool {
	log.Printf("[offline] Retrying video request processing (attempt %d)", item.Attempts+1)
	
	// Process the video request based on stored data
	// This would involve calling the appropriate video processing functions
	
	return true // Placeholder - implement based on your video request logic
}

// addToRetryQueue adds an item to the retry queue
func (om *OfflineManager) addToRetryQueue(item RetryItem) {
	om.mu.Lock()
	defer om.mu.Unlock()
	
	om.retryQueue = append(om.retryQueue, item)
	om.saveRetryQueue()
}

// setOfflineStatus sets the offline status
func (om *OfflineManager) setOfflineStatus(offline bool) {
	om.mu.Lock()
	defer om.mu.Unlock()
	
	if om.isOnline == !offline {
		return // No change
	}
	
	om.isOnline = !offline
	if offline {
		log.Println("[offline] Marking system as offline due to network error")
	}
}

// saveRetryQueue saves the retry queue to disk
func (om *OfflineManager) saveRetryQueue() {
	data, err := json.MarshalIndent(om.retryQueue, "", "  ")
	if err != nil {
		log.Printf("[offline] Failed to marshal retry queue: %v", err)
		return
	}
	
	// Create directory if it doesn't exist
	dir := filepath.Dir(om.queuePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[offline] Failed to create queue directory: %v", err)
		return
	}
	
	// Write to temporary file first, then rename for atomic operation
	tempPath := om.queuePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		log.Printf("[offline] Failed to write retry queue: %v", err)
		return
	}
	
	if err := os.Rename(tempPath, om.queuePath); err != nil {
		log.Printf("[offline] Failed to rename retry queue file: %v", err)
	}
}

// loadRetryQueue loads the retry queue from disk
func (om *OfflineManager) loadRetryQueue() {
	if _, err := os.Stat(om.queuePath); os.IsNotExist(err) {
		return // No queue file exists
	}
	
	data, err := os.ReadFile(om.queuePath)
	if err != nil {
		log.Printf("[offline] Failed to read retry queue: %v", err)
		return
	}
	
	if err := json.Unmarshal(data, &om.retryQueue); err != nil {
		log.Printf("[offline] Failed to unmarshal retry queue: %v", err)
		om.retryQueue = []RetryItem{} // Reset to empty queue
		return
	}
	
	log.Printf("[offline] Loaded %d items from retry queue", len(om.retryQueue))
}

// GetQueueStatus returns the current status of the retry queue
func (om *OfflineManager) GetQueueStatus() map[string]interface{} {
	om.mu.RLock()
	defer om.mu.RUnlock()
	
	return map[string]interface{}{
		"is_online":       om.isOnline,
		"queue_length":    len(om.retryQueue),
		"last_check":      om.lastConnectCheck,
		"retry_interval":  om.retryInterval,
		"max_retries":     om.maxRetries,
	}
}

// isNetworkError checks if an error is network-related
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	
	errStr := err.Error()
	networkErrors := []string{
		"no such host",
		"connection refused",
		"connection timeout",
		"network is unreachable",
		"temporary failure in name resolution",
		"dial tcp",
		"EOF",
		"context deadline exceeded",
	}
	
	for _, netErr := range networkErrors {
		if strings.Contains(errStr, netErr) {
			return true
		}
	}
	
	return false
}