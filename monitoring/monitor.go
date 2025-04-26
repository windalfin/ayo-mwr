package monitoring

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"time"
	"path/filepath"
	"syscall"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

type ResourceUsage struct {
	CPUPercent     float64
	MemoryUsedMB   float64
	MemoryTotalMB  float64
	MemoryPercent  float64
	NumGoroutines  int
	Uptime         string
	Storage        string
}

var startTime = time.Now()

func StartMonitoring(interval time.Duration) {
	go func() {
		proc, err := process.NewProcess(int32(os.Getpid()))
		if err != nil {
			log.Printf("Error getting process: %v", err)
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			usage, err := getResourceUsage(proc)
			if err != nil {
				log.Printf("Error getting resource usage: %v", err)
				continue
			}

			log.Printf("Resource Usage - CPU: %.2f%%, Memory: %.2f/%.2f MB (%.2f%%), Goroutines: %d",
				usage.CPUPercent,
				usage.MemoryUsedMB,
				usage.MemoryTotalMB,
				usage.MemoryPercent,
				usage.NumGoroutines)
		}
	}()
}

func getResourceUsage(proc *process.Process) (ResourceUsage, error) {
	var usage ResourceUsage

	// Get CPU usage
	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		return usage, fmt.Errorf("error getting CPU usage: %v", err)
	}
	usage.CPUPercent = cpuPercent

	// Get memory usage
	virtualMem, err := mem.VirtualMemory()
	if err != nil {
		return usage, fmt.Errorf("error getting memory info: %v", err)
	}

	procMem, err := proc.MemoryInfo()
	if err != nil {
		return usage, fmt.Errorf("error getting process memory: %v", err)
	}

	usage.MemoryUsedMB = float64(procMem.RSS) / 1024 / 1024 // Convert bytes to MB
	usage.MemoryTotalMB = float64(virtualMem.Total) / 1024 / 1024
	usage.MemoryPercent = float64(procMem.RSS) / float64(virtualMem.Total) * 100

	// Get number of goroutines
	usage.NumGoroutines = runtime.NumGoroutine()

	return usage, nil
}

func GetUptime() string {
	dur := time.Since(startTime)
	days := int(dur.Hours()) / 24
	hours := int(dur.Hours()) % 24
	minutes := int(dur.Minutes()) % 60
	return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
}

func GetStorageUsage(path string) (string, error) {
	var totalSize int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return "-", err
	}
	disk := "/"
	if path != "" {
		disk = path
	}
	stat := &syscall.Statfs_t{}
	err = syscall.Statfs(disk, stat)
	if err != nil {
		return fmt.Sprintf("%s / -", formatBytes(totalSize)), nil
	}
	totalDisk := stat.Blocks * uint64(stat.Bsize)
	return fmt.Sprintf("%s / %s", formatBytes(totalSize), formatBytes(int64(totalDisk))), nil
}

func formatBytes(b int64) string {
	if b > 1<<30 {
		return fmt.Sprintf("%.0fGB", float64(b)/(1<<30))
	} else if b > 1<<20 {
		return fmt.Sprintf("%.0fMB", float64(b)/(1<<20))
	} else if b > 1<<10 {
		return fmt.Sprintf("%.0fKB", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%dB", b)
}

func GetCurrentResourceUsage(storagePath string) (ResourceUsage, error) {
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return ResourceUsage{}, err
	}
	r, err := getResourceUsage(proc)
	if err != nil {
		return ResourceUsage{}, err
	}
	r.Uptime = GetUptime()
	stor, _ := GetStorageUsage(storagePath)
	r.Storage = stor
	return r, nil
}
