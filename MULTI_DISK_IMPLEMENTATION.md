# Multi-Disk Storage and HLS Cleanup Implementation Guide

This document provides a comprehensive guide for implementing the multiple HDD support and HLS cleanup system.

## Overview

The new system provides:
- **Multiple HDD Support**: Automatic disk selection based on available space
- **MP4-Only Recording**: Direct MP4 segmentation without HLS intermediate files
- **Database-Driven Storage**: Recording segments tracked in database for efficient retrieval
- **Automated Cleanup**: Nightly disk scanning and HLS file cleanup
- **Health Monitoring**: Disk space monitoring and alerting

## Implementation Steps

### 1. Database Migration

The system automatically creates new tables and adds columns when started:

**New Tables:**
- `storage_disks` - Track multiple HDDs with space and status
- `recording_segments` - Individual MP4 segments with disk locations

**New Columns in `videos` table:**
- `storage_disk_id` - Which disk stores the video
- `mp4_full_path` - Complete path including disk
- `deprecated_hls` - Whether HLS files have been cleaned up

### 2. Register Storage Disks

```go
import (
    "ayo-mwr/database"
    "ayo-mwr/storage"
)

// Initialize
db, _ := database.NewSQLiteDB("path/to/database.db")
diskManager := storage.NewDiskManager(db)

// Register HDDs (priority order: lower = higher priority)
diskManager.RegisterDisk("/mnt/hdd1", 1)  // Primary disk
diskManager.RegisterDisk("/mnt/hdd2", 2)  // Secondary disk
diskManager.RegisterDisk("/mnt/hdd3", 3)  // Tertiary disk
```

### 3. Start Cron Jobs

```go
import "ayo-mwr/cron"

// Disk management (runs nightly at 2 AM)
diskCron := cron.NewDiskManagementCron(db, diskManager)
diskCron.Start()

// HLS cleanup (runs nightly at 3 AM)
hlsCron := cron.NewHLSCleanupCron(db)
hlsCron.Start()
```

### 4. Update Recording Logic

Replace existing recording calls with the enhanced version:

```go
import "ayo-mwr/recording"

// Old approach
// captureRTSPStreamForCamera(ctx, cfg, camera, cameraID)

// New approach
recording.captureRTSPStreamForCameraEnhanced(ctx, cfg, camera, cameraID, db, diskManager)
```

### 5. Update Video Processing

Update booking video processing to use database-driven segment discovery:

```go
import "ayo-mwr/recording"

// Enhanced segment discovery (tries database first, falls back to filesystem)
segments, err := recording.FindSegmentsInRangeEnhanced(
    cameraName, 
    fallbackPath,
    startTime, 
    endTime, 
    db, 
    diskManager,
)
```

## Key Features

### Automatic Disk Selection

The system automatically:
1. Scans all registered disks nightly at 2 AM
2. Selects the first disk with >100GB free space (by priority order)
3. Activates the selected disk for new recordings
4. Sends alerts if no disk has sufficient space

### HLS Cleanup Process

The system automatically:
1. Identifies videos with HLS files but valid MP4 files
2. Verifies MP4 files exist before cleaning HLS
3. Removes HLS directories and segments
4. Updates database to mark HLS as deprecated
5. Logs cleanup statistics

### Database-Driven Segment Discovery

Benefits:
- **Performance**: No filesystem scanning required
- **Reliability**: Segments tracked regardless of disk location
- **Scalability**: Works across multiple disks seamlessly
- **Accuracy**: Precise time-based segment matching

## Configuration Options

### Minimum Free Space

```go
// Change minimum required space (default: 100GB)
storage.MinimumFreeSpaceGB = 150 // 150GB minimum
```

### Segment Duration

```go
// Recording segments are 1-minute by default
// This is configured in the FFmpeg arguments:
"-segment_time", "60" // 60 seconds
```

## Monitoring and Maintenance

### Manual Operations

```go
// Manual disk scan
diskCron.RunManualScan()

// Manual HLS cleanup
hlsCron.RunManualCleanup()

// Clean specific video
hlsCron.CleanupSpecificVideo("video_id")

// Check disk health
diskManager.CheckDiskHealth()

// Get usage statistics
stats, _ := diskManager.GetDiskUsageStats()
```

### Health Monitoring

The system monitors:
- **Disk Space**: Alerts when disks are >90% full
- **Disk Accessibility**: Verifies all registered disks are mounted
- **File Integrity**: Validates segment files exist on disk
- **Scan Frequency**: Warns if disks haven't been scanned recently

### Log Monitoring

Key log messages to monitor:
- `"Selected disk X as active"` - Daily disk selection
- `"Disk health warnings"` - Storage issues
- `"HLS cleanup completed"` - Daily cleanup results
- `"Enhanced MP4 segmenter: recorded segment"` - New recordings

## Migration Strategy

### Phase 1: Preparation
1. Add new database schema (automatic)
2. Register existing storage as first disk
3. Test with single disk setup

### Phase 2: Multi-Disk
1. Register additional HDDs
2. Start disk management cron
3. Monitor disk selection and usage

### Phase 3: HLS Deprecation
1. Start HLS cleanup cron
2. Monitor cleanup progress
3. Verify MP4-only workflow

### Phase 4: Enhanced Recording
1. Switch to enhanced recording functions
2. Update video processing pipelines
3. Remove legacy HLS dependencies

## Troubleshooting

### Common Issues

**No active disk found:**
- Check disk registration: `diskManager.ListDisks()`
- Verify disk space: `diskManager.RunManualScan()`
- Check minimum space requirement

**Segments not found:**
- Verify database has segment records
- Check disk paths are correct
- Use `ValidateSegmentPaths()` for verification

**HLS cleanup not working:**
- Ensure MP4 files exist before cleanup
- Check file permissions on HLS directories
- Review cleanup logs for specific errors

**Performance issues:**
- Monitor database query performance
- Consider segment table indexing
- Check disk I/O performance

### Debug Commands

```go
// List all registered disks
disks, _ := diskManager.ListDisks()
for _, disk := range disks {
    fmt.Printf("Disk: %s, Active: %v, Space: %dGB\n", 
        disk.Path, disk.IsActive, disk.AvailableSpaceGB)
}

// Validate segment integrity
err := recording.ValidateSegmentPaths("camera_name", db)
if err != nil {
    fmt.Printf("Validation issues: %v\n", err)
}

// Check cleanup status
stats, _ := hlsCron.GetCleanupStats()
fmt.Printf("Cleanup stats: %+v\n", stats)
```

## Best Practices

1. **Disk Layout**: Use separate mount points for each HDD
2. **Monitoring**: Set up alerts for disk space warnings
3. **Backup**: Regularly backup the database
4. **Testing**: Test disk failover scenarios
5. **Gradual Migration**: Implement in phases with monitoring

## Performance Optimizations

1. **Database Indexing**: Indexes are automatically created for performance
2. **Batch Operations**: Cleanup processes files in batches
3. **Async Processing**: Segment tracking runs asynchronously
4. **Caching**: Disk information is cached between scans

## Security Considerations

1. **Permissions**: Ensure proper file system permissions
2. **Database**: Secure database file access
3. **Logging**: Avoid logging sensitive camera credentials
4. **Network**: Secure RTSP stream credentials