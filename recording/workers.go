package recording

import (
    "context"
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
