# Video Processing Metrics Usage

This document shows how to use the video processing metrics system to track timing for video operations.

## How It Works

The application now automatically tracks timing for:

1. **Video Preview Creation** - Time to extract clips and create preview videos
2. **HLS Transcoding** - Time to transcode videos to HLS format
3. **MP4 Transcoding** - Time to convert TS files to MP4 format
4. **Upload Operations** - Time to upload files to R2 storage

## Automatic Metrics Collection

The metrics system is automatically integrated into the video processing pipeline:

### In BookingVideoService

```go
// Metrics are automatically started when processing a video
func (s *BookingVideoService) UploadProcessedVideo(uniqueID, videoPath, bookingID, cameraName string) (string, string, error) {
    // Start metrics tracking for this video
    videoMetrics := s.metricsCollector.StartVideo(uniqueID)
    defer videoMetrics.Finalize() // Logs complete summary at the end
    
    // Create preview with metrics
    err := s.CreateVideoPreviewWithMetrics(videoPath, previewVideoPath, videoMetrics)
    
    // Upload with metrics
    _, err = s.r2Client.UploadFileWithMetrics(previewVideoPath, previewPath, videoMetrics)
    // ... rest of processing
}
```

### In Transcode Package

```go
// HLS transcoding with metrics
videoMetrics.StartHLS()
// ... HLS transcoding happens
videoMetrics.EndHLS()

// MP4 transcoding with metrics
videoMetrics.StartTranscode()
// ... MP4 transcoding happens
videoMetrics.EndTranscode()
```

### In Upload Service

```go
// Upload operations with metrics
videoMetrics.StartUpload()
// ... upload happens
videoMetrics.EndUpload()
```

## Sample Log Output

When processing a video, you'll see logs like this:

```
[Metrics] Video booking123_camera1: Starting preview creation
[Metrics] Video booking123_camera1: Preview creation completed in 15.23s
[Metrics] Video booking123_camera1: Starting upload to R2
[Metrics] Video booking123_camera1: Upload to R2 completed in 45.67s
[Metrics] Video booking123_camera1: Processing completed - Total: 1m12.45s, Preview: 15.23s, Transcode: 0s, HLS: 0s, Upload: 45.67s
```

## Getting Metrics Programmatically

You can also retrieve metrics data:

```go
// Get metrics for a specific video
metrics := metricsCollector.GetMetrics("video_id")
if metrics != nil {
    fmt.Printf("Preview took: %v\n", metrics.PreviewDuration)
    fmt.Printf("Upload took: %v\n", metrics.UploadDuration)
    fmt.Printf("Total time: %v\n", metrics.TotalDuration)
}

// Get all metrics
allMetrics := metricsCollector.GetAllMetrics()
for videoID, metrics := range allMetrics {
    fmt.Printf("Video %s: %s\n", videoID, metrics.GetSummary())
}
```

## Metrics Cleanup

Old metrics are automatically cleaned up to prevent memory leaks:

```go
// Clean up metrics older than 24 hours
metricsCollector.CleanupOldMetrics(24 * time.Hour)
```

## Performance Insights

With these metrics, you can:

1. **Identify Bottlenecks** - See which operation takes the most time
2. **Monitor Performance** - Track if processing times increase over time
3. **Optimize Resources** - Focus optimization efforts on the slowest operations
4. **Debug Issues** - Identify when operations are taking unusually long

## Example Timing Expectations

Typical processing times for a 30-minute video:

- **Preview Creation**: 10-30 seconds (depends on video length and intervals)
- **HLS Transcoding**: 2-10 minutes (depends on quality settings and hardware)
- **MP4 Transcoding**: 30 seconds - 2 minutes (mostly remuxing, very fast)
- **Upload**: 1-5 minutes (depends on file size and internet speed)

These times will vary based on:
- Video resolution and quality
- Hardware acceleration availability
- Internet connection speed
- Storage performance