package recording

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"ayo-mwr/config"
)

// NetworkResilienceConfig contains configuration for network resilience
type NetworkResilienceConfig struct {
	MaxRetries         int
	BaseRetryDelay     time.Duration
	MaxRetryDelay      time.Duration
	ConnectionTimeout  time.Duration
	ReadTimeout        time.Duration
	ReconnectThreshold time.Duration
}

// DefaultNetworkConfig returns default network resilience configuration
func DefaultNetworkConfig() NetworkResilienceConfig {
	return NetworkResilienceConfig{
		MaxRetries:         10,
		BaseRetryDelay:     2 * time.Second,
		MaxRetryDelay:      30 * time.Second,
		ConnectionTimeout:  10 * time.Second,
		ReadTimeout:        30 * time.Second,
		ReconnectThreshold: 60 * time.Second,
	}
}

// RTSPConnectionManager manages RTSP connections with retry logic
type RTSPConnectionManager struct {
	config NetworkResilienceConfig
}

// NewRTSPConnectionManager creates a new RTSP connection manager
func NewRTSPConnectionManager() *RTSPConnectionManager {
	return &RTSPConnectionManager{
		config: DefaultNetworkConfig(),
	}
}

// TestRTSPConnection tests if RTSP stream is accessible
func (rcm *RTSPConnectionManager) TestRTSPConnection(camera config.CameraConfig) error {
	rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
		camera.Username, camera.Password,
		camera.IP, camera.Port, camera.Path)
	
	log.Printf("[network] Testing RTSP connection for camera %s", camera.Name)
	
	// Test with a simple probe command with timeout
	ctx, cancel := context.WithTimeout(context.Background(), rcm.config.ConnectionTimeout)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-rtsp_transport", "tcp",
		"-rw_timeout", fmt.Sprintf("%d", int(rcm.config.ReadTimeout.Seconds()*1000000)), // microseconds
		"-i", rtspURL,
		"-show_entries", "stream=codec_type",
		"-of", "csv=p=0")
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("RTSP connection test failed: %v", err)
	}
	
	log.Printf("[network] RTSP connection test passed for camera %s", camera.Name)
	return nil
}

// StartResilientRTSPCapture starts RTSP capture with network resilience
func (rcm *RTSPConnectionManager) StartResilientRTSPCapture(ctx context.Context, camera config.CameraConfig, outputDir string) error {
	retryCount := 0
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		
		// Test connection first
		if err := rcm.TestRTSPConnection(camera); err != nil {
			log.Printf("[network] Camera %s connection test failed: %v", camera.Name, err)
			
			if retryCount >= rcm.config.MaxRetries {
				return fmt.Errorf("max retries exceeded for camera %s", camera.Name)
			}
			
			delay := rcm.calculateBackoffDelay(retryCount)
			log.Printf("[network] Retrying camera %s in %v (attempt %d/%d)", 
				camera.Name, delay, retryCount+1, rcm.config.MaxRetries)
			
			select {
			case <-time.After(delay):
				retryCount++
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		
		// Connection test passed, start capture
		log.Printf("[network] Starting resilient capture for camera %s", camera.Name)
		err := rcm.runResilientFFmpeg(ctx, camera, outputDir)
		
		// Check if context was cancelled (intentional stop)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		
		// If we get here, FFmpeg exited unexpectedly
		log.Printf("[network] Camera %s capture failed: %v", camera.Name, err)
		
		if retryCount >= rcm.config.MaxRetries {
			return fmt.Errorf("max retries exceeded for camera %s after FFmpeg failures", camera.Name)
		}
		
		delay := rcm.calculateBackoffDelay(retryCount)
		log.Printf("[network] Restarting camera %s in %v (attempt %d/%d)", 
			camera.Name, delay, retryCount+1, rcm.config.MaxRetries)
		
		select {
		case <-time.After(delay):
			retryCount++
			// Reset retry count if we've been running for a while
			// This prevents permanent failure due to transient issues
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// runResilientFFmpeg runs FFmpeg with network resilience options
func (rcm *RTSPConnectionManager) runResilientFFmpeg(ctx context.Context, camera config.CameraConfig, outputDir string) error {
	rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%s%s",
		camera.Username, camera.Password,
		camera.IP, camera.Port, camera.Path)
	
	segmentPattern := filepath.Join(outputDir, fmt.Sprintf("%s_%%Y%%m%%d_%%H%%M%%S.ts", camera.Name))
	
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-rtsp_transport", "tcp",
		"-rw_timeout", fmt.Sprintf("%d", int(rcm.config.ReadTimeout.Seconds()*1000000)), // microseconds
		"-reconnect", "1",
		"-reconnect_at_eof", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", fmt.Sprintf("%d", int(rcm.config.MaxRetryDelay.Seconds())),
		"-i", rtspURL,
		"-c:v", "copy",
		"-bsf:v", "h264_mp4toannexb",
		"-flags", "+global_header",
		"-c:a", "aac",
		"-f", "segment",
		"-segment_time", "60",
		"-segment_format", "mpegts",
		"-segment_atclocktime", "1",
		"-strftime", "1",
		"-reset_timestamps", "1",
		"-avoid_negative_ts", "make_zero",
		segmentPattern,
	}
	
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	
	log.Printf("[network] Starting FFmpeg for camera %s with network resilience", camera.Name)
	
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start FFmpeg: %v", err)
	}
	
	// Monitor the process
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	
	// Wait for completion or context cancellation
	select {
	case err := <-done:
		if err != nil {
			log.Printf("[network] FFmpeg exited with error for camera %s: %v", camera.Name, err)
		}
		return err
	case <-ctx.Done():
		// Gracefully terminate the process
		if cmd.Process != nil {
			log.Printf("[network] Terminating FFmpeg process for camera %s", camera.Name)
			cmd.Process.Signal(syscall.SIGTERM)
			
			// Wait up to 5 seconds for graceful shutdown
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				// Force kill if it doesn't terminate gracefully
				cmd.Process.Kill()
			}
		}
		return ctx.Err()
	}
}

// calculateBackoffDelay calculates exponential backoff delay with jitter
func (rcm *RTSPConnectionManager) calculateBackoffDelay(retryCount int) time.Duration {
	// Exponential backoff: baseDelay * 2^retryCount
	delay := rcm.config.BaseRetryDelay
	for i := 0; i < retryCount && delay < rcm.config.MaxRetryDelay; i++ {
		delay *= 2
	}
	
	if delay > rcm.config.MaxRetryDelay {
		delay = rcm.config.MaxRetryDelay
	}
	
	// Add jitter (±25% of delay)
	jitter := time.Duration(float64(delay) * 0.25)
	delay += time.Duration(float64(jitter) * (2*rand.Float64() - 1))
	
	if delay < rcm.config.BaseRetryDelay {
		delay = rcm.config.BaseRetryDelay
	}
	
	return delay
}

// TestNetworkConnectivity tests basic network connectivity to camera
func (rcm *RTSPConnectionManager) TestNetworkConnectivity(camera config.CameraConfig) error {
	address := fmt.Sprintf("%s:%s", camera.IP, camera.Port)
	
	conn, err := net.DialTimeout("tcp", address, rcm.config.ConnectionTimeout)
	if err != nil {
		return fmt.Errorf("network connectivity test failed for %s: %v", address, err)
	}
	defer conn.Close()
	
	log.Printf("[network] Network connectivity test passed for camera %s (%s)", camera.Name, address)
	return nil
}

// MonitorNetworkHealth continuously monitors network health
func (rcm *RTSPConnectionManager) MonitorNetworkHealth(ctx context.Context, cameras []config.CameraConfig, callback func(camera config.CameraConfig, healthy bool)) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			for _, camera := range cameras {
				if !camera.Enabled {
					continue
				}
				
				healthy := rcm.TestNetworkConnectivity(camera) == nil
				callback(camera, healthy)
			}
		case <-ctx.Done():
			return
		}
	}
}