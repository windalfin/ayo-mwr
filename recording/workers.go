package recording

import (
    "context"
    "log"
    "sync"

    "ayo-mwr/config"
)

// registry tracks running camera workers.
var (
    mu      sync.Mutex
    workers = map[string]context.CancelFunc{}
)

// StartCamera launches capture goroutine for cam if not running. Returns true if started.
func StartCamera(cfg *config.Config, cam config.CameraConfig, idx int) bool {
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
