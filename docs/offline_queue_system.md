# Sistem Offline Queue

Sistem offline queue adalah fitur yang memungkinkan aplikasi video processing untuk tetap berfungsi dengan baik meskipun ada gangguan koneksi internet. Sistem ini secara otomatis mendeteksi status koneksi dan mengelola task yang gagal untuk diproses ulang saat koneksi kembali.

## 🚀 Fitur Utama

### 1. **Deteksi Konektivitas Otomatis**
- Monitoring koneksi internet real-time
- Multi-endpoint testing (Cloudflare DNS, Google DNS, dll)
- DNS resolution fallback
- Automatic recovery detection

### 2. **Task Queue Management**
- Persistent SQLite-based task storage
- Exponential backoff retry logic
- Task prioritization by creation time
- Automatic cleanup of completed tasks

### 3. **Dua Jenis Task**
- **Upload R2**: Upload file video, preview, dan thumbnail ke cloud storage
- **Notify AYO API**: Notifikasi ke AYO API setelah video berhasil diproses

### 4. **Smart Processing Logic**
- **Online**: Coba direct operation dulu, fallback ke queue jika gagal
- **Offline**: Langsung masuk ke queue, akan diproses saat online kembali

## 🔧 Komponen Sistem

### Database Schema
```sql
CREATE TABLE pending_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_type TEXT NOT NULL,           -- "upload_r2" | "notify_ayo_api" 
    task_data TEXT NOT NULL,           -- JSON data task-specific
    attempts INTEGER DEFAULT 0,        -- Jumlah percobaan
    max_attempts INTEGER DEFAULT 5,    -- Maksimal percobaan
    next_retry_at DATETIME,            -- Waktu retry berikutnya
    status TEXT DEFAULT 'pending',     -- "pending" | "processing" | "completed" | "failed"
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    error_msg TEXT                     -- Pesan error terakhir
);
```

### Connectivity Checker (`offline/connectivity.go`)
- Multi-endpoint connectivity testing
- DNS resolution testing
- Periodic connectivity monitoring
- Status change callbacks

### Queue Manager (`offline/queue_manager.go`)
- Background task processing
- Retry logic dengan exponential backoff
- Task lifecycle management
- Cleanup old completed tasks

## 📊 Monitoring & Logging

### Log Format dengan Emoji
```
📦 QUEUE: Processing task 123 (type: upload_r2, attempt: 2/5)
🌐 CONNECTIVITY: Status changed: OFFLINE ❌ -> ONLINE ✅
📤 UPLOAD: Uploading MP4: /path/to/video.mp4
🔔 API: Notification sent successfully
```

### API Endpoint untuk Monitoring
```
GET /api/queue-status
```

Response:
```json
{
  "success": true,
  "message": "Queue status retrieved successfully",
  "data": {
    "is_online": true,
    "connectivity_status": "ONLINE ✅",
    "queue_stats": {
      "total_pending": 5,
      "upload_pending": 3,
      "api_notify_pending": 2,
      "failed_tasks": 0
    },
    "queue_system_running": true
  }
}
```

## 🔄 Workflow Integration

### Workflow Normal (Online)
1. **Video Processing** - Merge, watermark, preview, thumbnail
2. **Upload Direct** - Coba upload langsung ke R2
3. **API Notification Direct** - Coba notifikasi langsung ke AYO API
4. **Fallback to Queue** - Jika gagal, masuk ke offline queue

### Workflow Offline
1. **Video Processing** - Tetap berjalan normal (local processing)
2. **Queue Upload Task** - Task upload masuk queue
3. **Queue API Task** - Task notifikasi API masuk queue
4. **Background Processing** - Queue manager proses saat online kembali

## ⚙️ Konfigurasi Retry

### Upload Tasks (R2)
- Max attempts: **5**
- Backoff: 5min, 20min, 45min, 2h, 5h

### API Notification Tasks
- Max attempts: **3**
- Backoff: 5min, 20min, 45min

### Connectivity Check
- Interval: **30 detik**
- Test URLs: Cloudflare DNS, Google DNS, Google.com
- Timeout: **10 detik** per test

## 🧹 Maintenance

### Automatic Cleanup
- **Completed tasks**: Dihapus setelah 7 hari
- **Failed tasks**: Tetap disimpan untuk debugging
- **Cleanup schedule**: Daily pada 3 AM

### Manual Monitoring
```bash
# Check database directly
sqlite3 data/videos.db "SELECT * FROM pending_tasks;"

# Monitor logs
tail -f server.log | grep "QUEUE\|CONNECTIVITY"
```

## 🚨 Error Handling

### Retry-All-Errors Policy
Sistem akan mencoba retry untuk **SEMUA** jenis error, bukan hanya network errors:
- Network timeouts
- Server errors (5xx)
- File access errors
- JSON parsing errors
- Database errors

### Permanent Failure
Task akan dianggap gagal permanen setelah:
- Mencapai max attempts
- Status diubah ke "failed" 
- Error message disimpan untuk debugging

## 📈 Performance

### Resource Usage
- **Memory**: Minimal overhead untuk task queue
- **CPU**: Background processing sesuai schedule
- **Storage**: SQLite database untuk persistence
- **Network**: Smart connectivity detection

### Scalability
- Task processing: 10 tasks per batch
- Queue check interval: 60 detik
- Connectivity check: 30 detik
- Cleanup: Daily

## 🔒 Reliability

### Data Persistence
- Task data disimpan di SQLite
- Survive application restart
- Transaction-based operations

### Failure Recovery
- Graceful degradation saat offline
- Automatic recovery saat online
- No data loss during network outages

### Monitoring Integration
- Comprehensive logging dengan emoji
- API endpoint untuk status checking
- Integration dengan existing health checks

---

## 📝 Penggunaan

Sistem offline queue **otomatis aktif** setelah aplikasi dijalankan. Tidak perlu konfigurasi tambahan.

### Monitoring Status
```bash
curl http://localhost:8080/api/queue-status
```

### Log Monitoring
```bash
# Filter log queue dan connectivity
tail -f server.log | grep -E "(📦 QUEUE|🌐 CONNECTIVITY)"
```

Sistem ini memberikan **resilience** terhadap gangguan network dan memastikan bahwa semua video processing task akan selesai diproses meskipun ada gangguan koneksi internet sementara. 