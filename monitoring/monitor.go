package monitoring

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

type ResourceUsage struct {
	CPUPercent     float64
	MemoryUsedMB   float64
	MemoryTotalMB  float64
	MemoryPercent  float64
	NumGoroutines  int
}

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

	usage.MemoryUsedMB = float64(procMem.RSS) / 1024 / 1024  // Convert bytes to MB
	usage.MemoryTotalMB = float64(virtualMem.Total) / 1024 / 1024
	usage.MemoryPercent = float64(procMem.RSS) / float64(virtualMem.Total) * 100

	// Get number of goroutines
	usage.NumGoroutines = runtime.NumGoroutine()

	return usage, nil
}
