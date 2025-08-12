package metrics

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// VideoProcessingMetrics tracks timing for various video processing operations
type VideoProcessingMetrics struct {
	VideoID           string
	StartTime         time.Time
	PreviewStartTime  *time.Time
	PreviewEndTime    *time.Time
	PreviewDuration   time.Duration
	TranscodeStartTime *time.Time
	TranscodeEndTime   *time.Time
	TranscodeDuration  time.Duration
	HLSStartTime      *time.Time
	HLSEndTime        *time.Time
	HLSDuration       time.Duration
	UploadStartTime   *time.Time
	UploadEndTime     *time.Time
	UploadDuration    time.Duration
	TotalDuration     time.Duration
	mu                sync.Mutex
}

// NewVideoProcessingMetrics creates a new metrics instance
func NewVideoProcessingMetrics(videoID string) *VideoProcessingMetrics {
	return &VideoProcessingMetrics{
		VideoID:   videoID,
		StartTime: time.Now(),
	}
}

// StartPreview marks the start of preview creation
func (m *VideoProcessingMetrics) StartPreview() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.PreviewStartTime = &now
	log.Printf("[Metrics] Video %s: Starting preview creation", m.VideoID)
}

// EndPreview marks the end of preview creation
func (m *VideoProcessingMetrics) EndPreview() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.PreviewStartTime != nil {
		now := time.Now()
		m.PreviewEndTime = &now
		m.PreviewDuration = now.Sub(*m.PreviewStartTime)
		log.Printf("[Metrics] Video %s: Preview creation completed in %v", m.VideoID, m.PreviewDuration)
	}
}

// StartTranscode marks the start of MP4 transcoding
func (m *VideoProcessingMetrics) StartTranscode() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.TranscodeStartTime = &now
	log.Printf("[Metrics] Video %s: Starting MP4 transcode", m.VideoID)
}

// EndTranscode marks the end of MP4 transcoding
func (m *VideoProcessingMetrics) EndTranscode() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.TranscodeStartTime != nil {
		now := time.Now()
		m.TranscodeEndTime = &now
		m.TranscodeDuration = now.Sub(*m.TranscodeStartTime)
		log.Printf("[Metrics] Video %s: MP4 transcode completed in %v", m.VideoID, m.TranscodeDuration)
	}
}

// StartHLS marks the start of HLS transcoding
func (m *VideoProcessingMetrics) StartHLS() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.HLSStartTime = &now
	log.Printf("[Metrics] Video %s: Starting HLS transcode", m.VideoID)
}

// EndHLS marks the end of HLS transcoding
func (m *VideoProcessingMetrics) EndHLS() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.HLSStartTime != nil {
		now := time.Now()
		m.HLSEndTime = &now
		m.HLSDuration = now.Sub(*m.HLSStartTime)
		log.Printf("[Metrics] Video %s: HLS transcode completed in %v", m.VideoID, m.HLSDuration)
	}
}

// StartUpload marks the start of upload to R2
func (m *VideoProcessingMetrics) StartUpload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.UploadStartTime = &now
	log.Printf("[Metrics] Video %s: Starting upload to R2", m.VideoID)
}

// EndUpload marks the end of upload to R2
func (m *VideoProcessingMetrics) EndUpload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.UploadStartTime != nil {
		now := time.Now()
		m.UploadEndTime = &now
		m.UploadDuration = now.Sub(*m.UploadStartTime)
		log.Printf("[Metrics] Video %s: Upload to R2 completed in %v", m.VideoID, m.UploadDuration)
	}
}

// Finalize calculates total duration and logs summary
func (m *VideoProcessingMetrics) Finalize() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalDuration = time.Since(m.StartTime)
	log.Printf("[Metrics] Video %s: Processing completed - Total: %v, Preview: %v, Transcode: %v, HLS: %v, Upload: %v",
		m.VideoID,
		m.TotalDuration,
		m.PreviewDuration,
		m.TranscodeDuration,
		m.HLSDuration,
		m.UploadDuration)
}

// GetSummary returns a formatted summary of all metrics
func (m *VideoProcessingMetrics) GetSummary() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	summary := fmt.Sprintf("Video Processing Metrics for %s:\n", m.VideoID)
	summary += fmt.Sprintf("  Total Duration: %v\n", m.TotalDuration)
	
	if m.PreviewDuration > 0 {
		summary += fmt.Sprintf("  Preview Creation: %v\n", m.PreviewDuration)
	}
	
	if m.TranscodeDuration > 0 {
		summary += fmt.Sprintf("  MP4 Transcode: %v\n", m.TranscodeDuration)
	}
	
	if m.HLSDuration > 0 {
		summary += fmt.Sprintf("  HLS Transcode: %v\n", m.HLSDuration)
	}
	
	if m.UploadDuration > 0 {
		summary += fmt.Sprintf("  Upload to R2: %v\n", m.UploadDuration)
	}
	
	return summary
}

// MetricsCollector manages metrics for multiple videos
type MetricsCollector struct {
	metrics map[string]*VideoProcessingMetrics
	mu      sync.RWMutex
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		metrics: make(map[string]*VideoProcessingMetrics),
	}
}

// StartVideo creates metrics for a new video
func (c *MetricsCollector) StartVideo(videoID string) *VideoProcessingMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	metrics := NewVideoProcessingMetrics(videoID)
	c.metrics[videoID] = metrics
	return metrics
}

// GetMetrics retrieves metrics for a video
func (c *MetricsCollector) GetMetrics(videoID string) *VideoProcessingMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return c.metrics[videoID]
}

// GetAllMetrics returns all collected metrics
func (c *MetricsCollector) GetAllMetrics() map[string]*VideoProcessingMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	// Create a copy to avoid race conditions
	result := make(map[string]*VideoProcessingMetrics)
	for k, v := range c.metrics {
		result[k] = v
	}
	return result
}

// CleanupOldMetrics removes metrics older than the specified duration
func (c *MetricsCollector) CleanupOldMetrics(maxAge time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	now := time.Now()
	for videoID, metrics := range c.metrics {
		if now.Sub(metrics.StartTime) > maxAge {
			delete(c.metrics, videoID)
			log.Printf("[Metrics] Cleaned up old metrics for video %s", videoID)
		}
	}
}