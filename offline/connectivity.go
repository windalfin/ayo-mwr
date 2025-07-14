package offline

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"
)

// ConnectivityChecker handles internet connectivity detection
type ConnectivityChecker struct {
	testURLs    []string
	timeout     time.Duration
	isConnected bool
}

// NewConnectivityChecker creates a new connectivity checker
func NewConnectivityChecker() *ConnectivityChecker {
	return &ConnectivityChecker{
		testURLs: []string{
			"https://1.1.1.1",      // Cloudflare DNS
			"https://8.8.8.8",      // Google DNS
			"https://www.google.com",
		},
		timeout:     10 * time.Second,
		isConnected: false,
	}
}

// IsOnline checks if internet connection is available
func (c *ConnectivityChecker) IsOnline() bool {
	// Try multiple test URLs
	for _, url := range c.testURLs {
		if c.checkConnection(url) {
			c.isConnected = true
			return true
		}
	}
	
	// Also try DNS resolution as fallback
	if c.checkDNSResolution("www.google.com") {
		c.isConnected = true
		return true
	}
	
	c.isConnected = false
	return false
}

// checkConnection tests HTTP connectivity to a URL
func (c *ConnectivityChecker) checkConnection(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return false
	}

	client := &http.Client{
		Timeout: c.timeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 5 * time.Second,
			}).DialContext,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode < 400
}

// checkDNSResolution tests DNS connectivity
func (c *ConnectivityChecker) checkDNSResolution(hostname string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	resolver := &net.Resolver{}
	_, err := resolver.LookupHost(ctx, hostname)
	return err == nil
}

// GetConnectionStatus returns current connection status
func (c *ConnectivityChecker) GetConnectionStatus() bool {
	return c.isConnected
}

// WaitForConnection waits until internet connection is restored
func (c *ConnectivityChecker) WaitForConnection(maxWait time.Duration) bool {
	log.Printf("🌐 CONNECTIVITY: Menunggu koneksi internet...")
	
	start := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if c.IsOnline() {
				log.Printf("🌐 CONNECTIVITY: ✅ Koneksi internet kembali tersedia!")
				return true
			}
			
			elapsed := time.Since(start)
			log.Printf("🌐 CONNECTIVITY: ⏳ Masih offline... (sudah menunggu %v)", elapsed.Round(time.Second))
			
			if elapsed >= maxWait {
				log.Printf("🌐 CONNECTIVITY: ❌ Timeout menunggu koneksi setelah %v", maxWait)
				return false
			}
		}
	}
}

// StartPeriodicCheck starts periodic connectivity monitoring
func (c *ConnectivityChecker) StartPeriodicCheck(interval time.Duration, onStatusChange func(bool)) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		
		previousStatus := c.isConnected
		
		for range ticker.C {
			currentStatus := c.IsOnline()
			
			if currentStatus != previousStatus {
				log.Printf("🌐 CONNECTIVITY: Status berubah: %v -> %v", 
					connectionStatusString(previousStatus), 
					connectionStatusString(currentStatus))
				
				if onStatusChange != nil {
					onStatusChange(currentStatus)
				}
				
				previousStatus = currentStatus
			}
		}
	}()
}

func connectionStatusString(connected bool) string {
	if connected {
		return "ONLINE ✅"
	}
	return "OFFLINE ❌"
} 