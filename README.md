# RTSP Capture and Streaming

This application captures video from RTSP cameras, saves the content in segments, and automatically converts them to streaming formats (HLS and DASH). It includes a built-in web server to serve these streams to any device.

## Features

- **RTSP Video Capture**: Capture video from IP cameras using RTSP protocol
- **Segmented Recording**: Creates timed video segments for easier management
- **Streaming Support**: Automatically generates HLS and DASH streams
- **Web Server**: Built-in web server to deliver streaming content
- **Hardware Acceleration**: Support for NVIDIA, Intel, AMD, and macOS hardware acceleration
- **Configurable**: All settings can be adjusted via environment variables
- **Cloud Storage**: Optional R2 storage support for video uploads

## Requirements

- Go (1.16 or later)
- FFmpeg (recent version with HLS and DASH support)
- RTSP-capable IP camera

## Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/yourusername/rtsp-capture.git
   cd rtsp-capture
   ```

2. Install dependencies:
   ```bash
   go mod tidy
   ```

3. Install FFmpeg:
   - **Windows**: Download from [ffmpeg.org](https://ffmpeg.org/download.html) and add to your PATH
   - **macOS**: `brew install ffmpeg`
   - **Ubuntu/Debian**: `sudo apt install ffmpeg`

## Hardware Setup

### 1. Modem Configuration

Configure your modem for optimal performance with the AYO Sport Camera system:

#### Initial modem setup
- Default Gateway: 192.168.100.1 (common for most modems)
- Username: admin
- Password: admin 


### 2. Router Configuration

#### Basic Router Configuration

1. Access Router Interface:
- Default Gateway: 192.168.0.1
- Username: admin
- Password: admin (change immediately)

#### WAN Settings:

- Connection Type: DHCP (if modem in bridge mode)




#### LAN Settings:
- Router IP: 192.168.0.1
- Subnet Mask: 255.255.255.0
- DHCP Range: 192.168.1.100-192.168.1.200
- Lease Time: 24 hours

#### DHCP Configuration
Make sure to configure the reserve DHCP IP for each camera

```
# Camera Reserved IPs
Camera 1: 192.168.1.101 (MAC: xx:xx:xx:xx:xx:01)
Camera 2: 192.168.1.102 (MAC: xx:xx:xx:xx:xx:02)
Camera 3: 192.168.1.103 (MAC: xx:xx:xx:xx:xx:03)
Camera 4: 192.168.1.104 (MAC: xx:xx:xx:xx:xx:04)

```

#### WiFi Configuration
Setup the Wifi Password which we will later setup in the Mini-PC

```
Primary Network (Management):
SSID: AYO-MGMT-[VenueCode]
Security: WPA3/WPA2
Password: [Strong Password]
Channel: Auto or 1,6,11 (2.4GHz) / 36,40,44,48 (5GHz)
```

### 3. Camera Configuration

Configure IP cameras for RTSP streaming and integration with the middleware:

#### Activating the Camera
Store purchased Hikvision camera are usually not activated, we will need to activate it for it to be detected in the network. One way to do it is to use SADP in windows machine

- Connect Camera to Network via cable
- Open SADP 
- Activate camera and set the master password
- Note the Camera IP and reserve the Camera IP in DHCP Config
- Make sure the camera config is configured in .env or in Middleware Dashboard
- Test the camera by accessing it via RTSP URL (VLC) or from live-view in Middleware dashboard

### 4. VenueCode and FieldId Configuration

Configure venue-specific identifiers for AYO platform integration:

- Venue code registration
- Field ID mapping
- Camera to field association
- Booking system integration
- Multi-venue setup
- Configuration validation

### 5. Button Setup

*[Details to be added later]*

Configure manual trigger buttons for highlight capture:

- Hardware button installation
- GPIO pin mapping
- Button debouncing
- Multiple button support
- Wireless button configuration
- Button testing procedures

## Configuration

Create a `.env` file in the project root with the following options:

```env
# Camera Configuration
CAMERAS_CONFIG=[{"name":"camera_1","ip":"192.168.1.100","port":"554","path":"/Streaming/Channels/101","username":"admin","password":"password","enabled":true,"width":1920,"height":1080,"frame_rate":30}]

# Storage Configuration
STORAGE_PATH=./videos
HW_ACCEL=nvidia  # Options: nvidia, intel, amd, videotoolbox, or empty for software
CODEC=avc        # Options: avc, hevc
PORT=3000        # Web server port
BASE_URL=http://localhost:3000

# Database Configuration
DATABASE_PATH=./data/videos.db

# R2 Storage Configuration (Optional)
R2_ENABLED=false
R2_ACCESS_KEY=your_access_key
R2_SECRET_KEY=your_secret_key
R2_ACCOUNT_ID=your_account_id
R2_BUCKET=your_bucket_name
R2_ENDPOINT=your_endpoint
R2_BASE_URL=https://your-domain.com
R2_REGION=auto
R2_TOKEN_VALUE=your_token_value

# Worker Concurrency Configuration
# These settings provide the initial values. They can be updated live via the admin dashboard.
BOOKING_WORKER_CONCURRENCY=2      # Max concurrent booking process workers
VIDEO_REQUEST_WORKER_CONCURRENCY=2 # Max concurrent video request workers
PENDING_TASK_WORKER_CONCURRENCY=3  # Max concurrent pending task workers

# Transcoding Quality Configuration
ENABLED_QUALITIES=1080p,720p,480p,360p  # Comma-separated list of enabled quality presets
```

## Directory Structure

```
videos/
├── recordings/                    # Source video recordings
│   └── camera_1/                 # Each camera has its own directory
│       └── mp4/                  # Original MP4 files
│           └── camera_1_20250320_172910.mp4
├── hls/                          # HLS streaming files
│   └── camera_1/                 # Each camera has its own directory
│       └── camera_1_20250320_172910/  # Each video has its own directory
│           ├── 360p/            # Quality variants
│           ├── 480p/
│           ├── 720p/
│           ├── 1080p/
│           └── master.m3u8      # Master playlist
├── dash/                         # DASH streaming files
│   └── camera_1/                 # Each camera has its own directory
│       └── camera_1_20250320_172910/  # Each video has its own directory
│           ├── init-stream*.m4s
│           ├── chunk-stream*.m4s
│           └── manifest.mpd
```

## Usage

1. Start the application:
   ```bash
   ./autorun.sh
   ```

2. The application will:
   - Begin capturing video from your RTSP cameras
   - Save segments to the configured storage path
   - Convert segments to HLS and DASH formats
   - Serve streams through the web server

## Middleware Dashboard

### Dashboard URL & Guides
```
http://localhost:3000/dashboard/admin_dashboard.html
```
### Dashboard Features and Usages

The admin dashboard provides a comprehensive interface for monitoring and managing the middleware.

#### Summary Cards
At a glance, you can see:
- **Arduino Status**: Shows if the hardware controller is connected and its configuration. Click to configure.
- **Camera Configuration**: Total number of configured and active cameras.
- **System Config**: Configuration for video quality, disk management and some other specific behaviour of the middleware
- **System Resources**: Real-time CPU and Memory usage.

#### Tabs

- **Cameras**:
  - View a list of all configured cameras with their status (online/offline), IP address, and other details.
  - Search and filter cameras by name or status.
  - Open a live video stream for any camera directly in the dashboard.

- **Videos/Clips**:
  - Browse all processed videos and their current status (e.g., ready, uploading, processing, failed).
  - Filter videos by status, camera, or date, and search by ID.
  - Retry failed processing jobs or view completed videos.

- **Camera Config**:
  - Add, edit, or delete camera configurations.
  - Modify details such as name, RTSP stream credentials, and hardware button associations.
  - Save changes and hot-reload the camera configurations without restarting the application.

- **System Config**:
  - **Worker Concurrency**: Adjust the number of concurrent workers for video processing, booking sync, and other background tasks to balance performance and resource usage.
  - **Quality Presets**: Enable or disable specific video quality presets (e.g., 1080p, 720p) to control storage and transcoding overhead.
  - **Disk Management**: Configure the disk manager, including setting the minimum required free space and defining priorities for different storage types (e.g., External, NVMe, SATA).
  - All changes are applied via hot-reload.

- **System Health**:
  - Monitor detailed real-time system metrics, including CPU usage, memory consumption, active goroutines, and system uptime.

- **Logs**:
  - View a live feed of recent application logs and errors.
  - Download the full log file for offline analysis.

### API Endpoints

#### Public Endpoints

- `GET /api/cameras`: Lists all configured cameras and their status.
- `GET /api/videos`: Lists all processed videos.
- `GET /api/streams`: Lists all available video streams.
- `GET /api/streams/:id`: Gets detailed information about a specific video stream.
- `POST /api/upload`: Uploads a video for processing. This was previously `/api/transcode`.
  - **Body**: `{"timestamp": "...", "cameraName": "..."}`
- `GET /api/system_health`: Returns system health metrics (CPU, memory, etc.).
- `GET /api/logs`: Returns recent application logs.
- `GET /api/arduino-status`: Returns the status of the Arduino controller.

#### Admin Endpoints

- `GET /api/admin/cameras-config`: Retrieves the current camera configuration.
- `PUT /api/admin/cameras-config`: Updates the camera configuration.
- `POST /api/admin/reload-cameras`: Hot-reloads the camera configuration.
- `GET /api/admin/system-config`: Retrieves the current system configuration.
- `PUT /api/admin/system-config`: Updates the system configuration. This was previously `/api/config/update`.
- `GET /api/admin/disk-manager-config`: Retrieves the disk manager configuration.
- `PUT /api/admin/disk-manager-config`: Updates the disk manager configuration.
- `PUT /api/admin/arduino-config`: Updates the Arduino controller configuration.

### Transcode Video
```http
POST /api/upload
Content-Type: application/json

{
  "timestamp": "2025-03-20T11:58:51+07:00",  # Find video closest to this time
  "cameraName": "camera_2"                    # Camera identifier
}
```

Response:
```json
{
  "urls": {
    "hls": "http://localhost:3000/hls/camera_2_20250320_115851/master.m3u8",
    "mp4": "http://localhost:3000/mp4/camera_2_20250320_115851.mp4"
  },
  "timings": {
    "transcoding": 15.5,
    "total": 16.2
  },
  "videoId": "camera_2_20250320_115851",
  "filename": "camera_2_20250320_115851.mp4"
}
```

## Testing the API

You can test the API using PowerShell or curl:

### PowerShell
```powershell
# List all streams
Invoke-WebRequest -Method Get -Uri 'http://localhost:3000/api/streams'

# Get stream details
Invoke-WebRequest -Method Get -Uri 'http://localhost:3000/api/streams/camera_1_20250320_172910'

# Transcode video
$body = @{
    timestamp = (Get-Date).ToString('yyyy-MM-ddTHH:mm:sszzz')
    cameraName = 'camera_1'
} | ConvertTo-Json

Invoke-WebRequest -Method Post -Uri 'http://localhost:3000/api/transcode' -Body $body -ContentType 'application/json'
```

### curl
```bash
# List all streams
curl http://localhost:3000/api/streams

# Get stream details
curl http://localhost:3000/api/streams/camera_1_20250320_172910

# Transcode video
curl -X POST -H "Content-Type: application/json" \
  -d '{"timestamp":"2025-03-20T17:29:10+07:00","cameraName":"camera_1"}' \
  http://localhost:3000/api/transcode
```

## Hardware Acceleration

The application supports hardware acceleration for different GPUs:

- **NVIDIA**: Uses NVENC for hardware-accelerated encoding
- **Intel**: Uses QuickSync (QSV) for hardware-accelerated encoding
- **AMD**: Uses AMF for hardware-accelerated encoding
- **macOS**: Uses VideoToolbox for hardware-accelerated encoding

Set the `HW_ACCEL` environment variable to enable hardware acceleration.

## Worker Concurrency Configuration

The application uses multiple background workers for different tasks. You can configure the maximum number of concurrent workers for each type:

### Available Worker Types

1. **Booking Process Workers** (`BOOKING_WORKER_CONCURRENCY`)
   - Process booking videos from database
   - Default: 2 concurrent workers
   - Handles video merging, watermarking, and upload

2. **Video Request Workers** (`VIDEO_REQUEST_WORKER_CONCURRENCY`)
   - Process video requests from AYO API
   - Default: 2 concurrent workers
   - Handles video validation and API notifications

3. **Pending Task Workers** (`PENDING_TASK_WORKER_CONCURRENCY`)
   - Process offline queue tasks (uploads, notifications)
   - Default: 3 concurrent workers
   - Handles R2 uploads and API notifications when offline

### Configuration

The recommended way to configure worker concurrency is through the **Admin Dashboard**, which allows for real-time updates without restarting the application. The environment variables below set the initial values on first startup.


### Monitoring Worker Status

You can monitor worker activity through the application logs:
```
📊 BOOKING-CRON: Sistem antrian dimulai - maksimal 4 proses booking bersamaan
📊 VIDEO-REQUEST-CRON: Sistem antrian dimulai - maksimal 3 proses video request bersamaan
📦 QUEUE: 🔄 Memproses 8 task yang tertunda (max 5 concurrent)...
```

### Hot Reload Worker Concurrency

The application supports **hot reload** for worker concurrency settings, allowing you to update the number of concurrent workers without restarting the application.

#### Features

- **Zero Downtime**: Update worker concurrency without stopping the application
- **Instant Effect**: Changes take effect within 2 minutes maximum
- **Thread Safe**: Safe concurrent access to worker settings
- **Automatic Monitoring**: Built-in configuration monitoring and reloading
- **Comprehensive Logging**: Detailed logs for all concurrency changes

#### How It Works

1. **Dynamic Semaphore Management**: Each worker type uses a dynamic semaphore that can be resized at runtime
2. **Configuration Monitoring**: Background process monitors configuration changes every 2 minutes
3. **Safe Updates**: Thread-safe mechanisms ensure no race conditions during updates
4. **Graceful Scaling**: Workers can scale up or down without affecting running tasks

#### Updating Concurrency Settings

You can update worker concurrency through the API:

```bash
# Update worker concurrency via API
curl -X POST -H "Content-Type: application/json" \
  -d '{"booking_worker_concurrency":5,"video_request_worker_concurrency":4,"pending_task_worker_concurrency":6}' \
  http://localhost:3000/api/config/update
```

Or update the database directly and wait for automatic reload (max 2 minutes).

#### Monitoring Hot Reload Activity

Watch for hot reload logs in the application output:

```
🔄 CONFIG: Hot reload - Booking worker concurrency: 2 → 5
🔄 CONFIG: Hot reload - Video request worker concurrency: 2 → 4  
🔄 CONFIG: Hot reload - Pending task worker concurrency: 3 → 6
📊 BOOKING-CRON: Konkurensi diperbarui: 2 → 5 worker
📊 VIDEO-REQUEST-CRON: Konkurensi diperbarui: 2 → 4 worker
📦 QUEUE: Konkurensi diperbarui: 3 → 6 worker
```

#### Benefits

- **Production Ready**: Update settings in production without downtime
- **Performance Tuning**: Adjust worker counts based on real-time load
- **Resource Management**: Scale workers up/down based on system resources
- **Operational Flexibility**: Quick response to changing requirements

#### Technical Details

For detailed technical information about the hot reload implementation, see [HOT_RELOAD_CONCURRENCY.md](HOT_RELOAD_CONCURRENCY.md).

## Quality Presets Configuration

The application supports configurable video quality presets for transcoding. You can control which quality variants are generated during video processing.

### Available Quality Presets

| Preset | Resolution | Bitrate | Bandwidth | Use Case |
|--------|------------|---------|-----------|----------|
| 1080p  | 1920x1080  | 5000k   | 5000000   | High quality, good internet |
| 720p   | 1280x720   | 2800k   | 2800000   | Standard quality, balanced |
| 480p   | 854x480    | 1400k   | 1400000   | Lower quality, slower internet |
| 360p   | 640x360    | 800k    | 800000    | Lowest quality, very slow internet |

### Configuration Examples

```bash
# Enable all quality presets (default)
ENABLED_QUALITIES=1080p,720p,480p,360p

# Enable only high quality presets
ENABLED_QUALITIES=1080p,720p

# Enable only lower quality presets (bandwidth saving)
ENABLED_QUALITIES=480p,360p

# Enable single quality preset
ENABLED_QUALITIES=720p

# Custom combination
ENABLED_QUALITIES=1080p,480p
```

### Benefits

- **Bandwidth Optimization**: Choose only the qualities you need
- **Storage Savings**: Fewer quality variants = less disk space
- **Processing Speed**: Fewer variants = faster transcoding
- **Flexible Deployment**: Different configurations for different environments

### Notes

- If `ENABLED_QUALITIES` is not set, all presets are enabled by default
- Invalid quality names are ignored
- If no valid presets are found, all presets are used as fallback
- The master HLS playlist will only include enabled quality variants

## Disk Manager Configuration

The application includes an intelligent disk management system that automatically selects the best available disk for video storage based on available space and disk priorities.

### Configurable Parameters

#### Minimum Free Space
- **Parameter**: `minimum_free_space_gb`
- **Default**: 100 GB
- **Range**: 1-1000 GB
- **Description**: Minimum free space required on a disk before it's considered full

#### Disk Priorities
The system assigns priorities to different disk types (lower number = higher priority):

| Disk Type | Default Priority | Description |
|-----------|------------------|-------------|
| External | 1 | USB drives, external HDDs/SSDs |
| Mounted Storage | 50 | Network drives, mounted volumes |
| Internal NVMe | 101 | Internal NVMe SSDs |
| Internal SATA | 201 | Internal SATA drives |
| Root Filesystem | 500 | System root partition (fallback) |

### Configuration via API

You can update disk manager settings through the admin API:

```bash
# Get current disk manager configuration
curl http://localhost:3000/api/admin/disk-manager-config

# Update disk manager configuration
curl -X PUT -H "Content-Type: application/json" \
  -d '{
    "minimum_free_space_gb": 150,
    "priority_external": 1,
    "priority_mounted_storage": 50,
    "priority_internal_nvme": 101,
    "priority_internal_sata": 201,
    "priority_root_filesystem": 500
  }' \
  http://localhost:3000/api/admin/disk-manager-config
```

### How It Works

1. **Automatic Discovery**: The system automatically discovers and registers available disks
2. **Priority-Based Selection**: Selects disks based on configured priorities and available space
3. **Health Monitoring**: Continuously monitors disk health and available space
4. **Dynamic Switching**: Automatically switches to alternative disks when current disk becomes full
5. **Size-Based Adjustment**: Larger disks get slightly higher priority within the same type

### Benefits

- **Automatic Management**: No manual disk selection required
- **Space Optimization**: Efficiently utilizes available storage across multiple disks
- **Configurable Priorities**: Customize disk selection based on your setup
- **Hot Configuration**: Update settings without restarting the application
- **Intelligent Fallback**: Gracefully handles disk full scenarios

### Monitoring

Monitor disk manager activity through application logs:
```
💾 DISK: Disk terpilih: /Volumes/ExternalDrive (1.2TB tersisa, prioritas: 1)
💾 DISK: Disk /Volumes/InternalSSD hampir penuh (8GB tersisa), beralih ke disk lain
💾 DISK: Menemukan disk baru: /Volumes/NewDrive (tipe: External, prioritas: 1)
```

## Troubleshooting

- **RTSP Connection Issues**: Verify your camera's IP, username, password, and RTSP path
- **FFmpeg Errors**: Ensure FFmpeg is installed and in your PATH
- **Playback Issues**: Try different players (VLC, web browsers with HLS.js)
- **Missing Files**: Check the logs for any file creation or permission errors

### Hardware Setup Troubleshooting

- **Network Connectivity**: Check modem and router configurations
- **Camera Connection**: Verify IP addresses and network settings
- **Button Response**: Test GPIO connections and button functionality
- **Venue Configuration**: Validate venue codes and field ID mappings

## License
This app belong to Ayo Indonesia