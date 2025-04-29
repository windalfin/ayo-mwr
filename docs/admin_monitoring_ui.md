# Local Admin Monitoring UI - Feature Proposal

This document outlines the features and data sources for the local admin monitoring UI, based on the current middleware codebase. It is designed to ensure that the UI can be implemented without backend bottlenecks, using only information already available.

---

## 1. Camera Monitoring
- **Camera List:** Show all configured cameras (`config.Cameras`), with:
  - Name, IP, Port, RTSP Path, Enabled status
- **Camera Status:** Indicate if each camera is currently recording (based on recent video activity or logs)
- **Last Recording Time:** Last successful segment/clip per camera
- **Error/Offline Indicator:** If a camera has not produced a video in X minutes, flag as offline

## 2. Video/Clip Monitoring
- **Clip List:** List all recorded videos (from DB), with:
  - ID, Camera Name/ID, Status (`recording`, `processing`, `uploading`, `ready`, `failed`)
  - Created At, Finished At, Duration, Size
  - Local Path, HLS/MP4 URLs, Cloud (R2) status
  - Error Message (if any)
- **Status Filtering:** Filter/search videos by status, camera, date, etc.
- **Quick Actions:** (optional) Retry failed processing, re-upload, delete video

## 3. System Health
- **Resource Usage:** Show current CPU, memory, and goroutine count (from monitoring/monitor.go)
- **Uptime:** Show server uptime (can be calculated at runtime)
- **Storage Usage:** (if available) Show disk usage for video storage path

## 4. Upload/Cloud Sync
- **Upload Queue:** List videos currently being uploaded or pending upload (based on status)
- **Cloud Status:** Show which videos are available locally vs. uploaded to R2 (cloud)

## 5. Logs & Alerts
- **Recent Errors:** Display recent error messages (from video processing, uploads, camera connection, etc.)
- **Downloadable Logs:** (optional) Button to download recent logs for troubleshooting

---

### Data Sources (from current code)
- Cameras: `config.Cameras`
- Video metadata: DB (ListVideos, GetVideosByStatus, etc.)
- Video status: VideoStatus (recording, processing, uploading, ready, failed)
- System health: monitoring/monitor.go (CPU, memory, goroutines)
- Upload/cloud status: Video fields R2HLSURL, R2MP4URL
- Error messages: VideoMetadata.ErrorMessage

---

### UI Should Support
- Real-time status (auto-refresh or polling)
- Filtering/sorting (by camera, status, date)
- Clear error/offline indicators
- At-a-glance summary (number of cameras, number of active/inactive, number of failed videos, etc.)

---

This proposal is based strictly on the data and APIs already present in the middleware codebase. If new backend features are added, the UI can be extended accordingly.
