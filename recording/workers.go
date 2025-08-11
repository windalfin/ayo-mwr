package recording

import (
    "context"
    "fmt"
    "log"
    "sync"

    "ayo-mwr/config"
    "ayo-mwr/database"
    "ayo-mwr/storage"
)

// registry tracks running camera workers.
var (
    mu      sync.Mutex
    workers = map[string]context.CancelFunc{}
)

// StartCamera launches capture goroutine for cam if not running. Returns true if started.
func StartCamera(cfg *config.Config, cam config.CameraConfig, idx int) bool {
    // Use basic capture for backward compatibility
    return startCameraBasic(cfg, cam, idx)
}

// StartCameraEnhanced launches enhanced capture with database tracking
func StartCameraEnhanced(cfg *config.Config, cam config.CameraConfig, idx int, db database.Database, diskManager *storage.DiskManager) bool {
    mu.Lock()
    if _, ok := workers[cam.Name]; ok {
        mu.Unlock()
        return false
    }
    ctx, cancel := context.WithCancel(context.Background())
    workers[cam.Name] = cancel
    mu.Unlock()

    go func() {
        defer func() {
            mu.Lock()
            delete(workers, cam.Name)
            mu.Unlock()
        }()
        captureRTSPStreamForCameraEnhanced(ctx, cfg, cam, idx, db, diskManager)
    }()
    log.Printf("[workers] started enhanced camera %s", cam.Name)
    return true
}

// startCameraBasic launches basic capture (original implementation)
func startCameraBasic(cfg *config.Config, cam config.CameraConfig, idx int) bool {
    mu.Lock()
    if _, ok := workers[cam.Name]; ok {
        mu.Unlock()
        return false
    }
    ctx, cancel := context.WithCancel(context.Background())
    workers[cam.Name] = cancel
    mu.Unlock()

    go func() {
        defer func() {
            mu.Lock()
            delete(workers, cam.Name)
            mu.Unlock()
        }()
        captureRTSPStreamForCamera(ctx, cfg, cam, idx)
    }()
    log.Printf("[workers] started camera %s", cam.Name)
    return true
}

// StopCamera cancels running worker.
func StopCamera(name string) {
    mu.Lock()
    cancel, ok := workers[name]
    if ok {
        delete(workers, name)
    }
    mu.Unlock()
    if ok {
        cancel()
        log.Printf("[workers] stopped camera %s", name)
    }
}

// ListRunningWorkers returns camera names running.
func ListRunningWorkers() []string {
    mu.Lock()
    defer mu.Unlock()
    names := make([]string, 0, len(workers))
    for n := range workers {
        names = append(names, n)
    }
    return names
}

// StartAllCameras kicks off workers for all enabled cameras.
func StartAllCameras(cfg *config.Config) {
    for i, cam := range cfg.Cameras {
        if !cam.Enabled {
            continue
        }
        StartCamera(cfg, cam, i)
    }
}

// StartAllCamerasEnhanced kicks off enhanced workers with database tracking
func StartAllCamerasEnhanced(cfg *config.Config, db database.Database, diskManager *storage.DiskManager) {
    for i, cam := range cfg.Cameras {
        if !cam.Enabled {
            continue
        }
        StartCameraEnhanced(cfg, cam, i, db, diskManager)
    }
}

// StartAllCamerasWithContext starts all cameras with graceful shutdown support
func StartAllCamerasWithContext(ctx context.Context, cfg *config.Config) {
    var wg sync.WaitGroup
    
    for i, cam := range cfg.Cameras {
        if !cam.Enabled {
            continue
        }
        
        wg.Add(1)
        go func(camera config.CameraConfig, cameraID int) {
            defer wg.Done()
            startCameraWithContext(ctx, cfg, camera, cameraID)
        }(cam, i)
    }
    
    // Wait for context cancellation
    <-ctx.Done()
    log.Println("[workers] Context canceled, stopping all cameras...")
    
    // Stop all running cameras
    mu.Lock()
    for name, cancel := range workers {
        log.Printf("[workers] Stopping camera %s for graceful shutdown", name)
        cancel()
    }
    mu.Unlock()
    
    // Wait for all workers to finish gracefully
    wg.Wait()
    log.Println("[workers] All cameras stopped gracefully")
}

// startCameraWithContext starts a camera with context support for graceful shutdown
func startCameraWithContext(ctx context.Context, cfg *config.Config, cam config.CameraConfig, idx int) {
    cameraName := cam.Name
    if cameraName == "" {
        cameraName = fmt.Sprintf("camera_%d", idx)
    }
    
    mu.Lock()
    if _, ok := workers[cameraName]; ok {
        mu.Unlock()
        return
    }
    
    // Create camera-specific context that can be canceled individually or by parent
    cameraCtx, cameraCancel := context.WithCancel(ctx)
    workers[cameraName] = cameraCancel
    mu.Unlock()
    
    defer func() {
        mu.Lock()
        delete(workers, cameraName)
        mu.Unlock()
        log.Printf("[workers] Camera %s worker cleaned up", cameraName)
    }()
    
    log.Printf("[workers] Starting camera %s with graceful shutdown support", cameraName)
    
    // Start the camera capture with context
    captureRTSPStreamForCameraWithGracefulShutdown(cameraCtx, cfg, cam, idx)
}
