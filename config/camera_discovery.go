package config

import (
	"fmt"
	"log"
	"net"
	"time"
)

// ScanCamerasInRange scans the given IP range for RTSP cameras and returns a list of CameraConfig.
func ScanCamerasInRange(start, end string, username, password, port, path string, width, height, frameRate int) ([]CameraConfig, []string) {
	var logLines []string
	var cameras []CameraConfig

	startIP := net.ParseIP(start)
	endIP := net.ParseIP(end)
	if startIP == nil || endIP == nil {
		return cameras, logLines
	}

	ip := startIP
	batchSize := 5
	for {
		batchStart := ip.String()
		batchCameras := []CameraConfig{}
		batchEndIP := ip
		for i := 0; i < batchSize && !batchEndIP.Equal(endIP); i++ {
			batchEndIP = incIP(batchEndIP)
		}
		if batchEndIP.To4() == nil {
			batchEndIP = endIP
		}
		logLines = append(logLines, fmt.Sprintf("scanning ip %s to %s for camera...", batchStart, batchEndIP.String()))
		log.Println("[SCAN] scanning ip", batchStart, "to", batchEndIP.String(), "for camera...")

		// Scan this batch
		currIP := net.ParseIP(batchStart)
		for {
			// Format address properly for both IPv4 and IPv6
			var addr string
			if currIP.To4() != nil {
				// IPv4 address
				addr = fmt.Sprintf("%s:%s", currIP.String(), port)
			} else {
				// IPv6 address needs square brackets
				addr = fmt.Sprintf("[%s]:%s", currIP.String(), port)
			}
			conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err == nil {
				c := CameraConfig{
					Name:      fmt.Sprintf("camera_%s", currIP.String()),
					IP:        currIP.String(),
					Port:      port,
					Path:      path,
					Username:  username,
					Password:  password,
					Enabled:   true,
					Width:     width,
					Height:    height,
					FrameRate: frameRate,
				}
				cameras = append(cameras, c)
				batchCameras = append(batchCameras, c)
				log.Println("[SCAN]   Found camera at", addr)
				conn.Close()
			}
			if currIP.Equal(batchEndIP) {
				break
			}
			currIP = incIP(currIP)
		}
		if len(batchCameras) == 1 {
			logLines = append(logLines, fmt.Sprintf("found 1 camera at %s", batchCameras[0].IP))
		} else if len(batchCameras) > 1 {
			for _, c := range batchCameras {
				logLines = append(logLines, fmt.Sprintf("found camera at %s", c.IP))
			}
		}
		if batchEndIP.Equal(endIP) {
			break
		}
		ip = incIP(batchEndIP)
	}
	logLines = append(logLines, fmt.Sprintf("Scan complete. Found %d camera(s).", len(cameras)))
	log.Println("[SCAN] Scan complete. Found", len(cameras), "camera(s).")
	return cameras, logLines
}

// incIP increments an IP address by 1 (IPv4 only).
func incIP(ip net.IP) net.IP {
	res := make(net.IP, len(ip))
	copy(res, ip)
	for j := len(res) - 1; j >= 0; j-- {
		res[j]++
		if res[j] != 0 {
			break
		}
	}
	return res
}
