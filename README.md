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
# Multi-camera Configuration
# ------------------------------------------
# JSON array of camera configurations
# Example with 2 cameras:
CAMERAS_CONFIG=[{"button_no": "1", "field":"2892","name":"CAMERA_1","ip":"192.168.0.102","port":"554","path":"/streaming/channels/101/","username":"","password":"","enabled":true,"width":1280,"height":720,"frame_rate":30,"resolution":"720", "auto_delete": 30},{"button_no": "2", "field":"2893","name":"CAMERA_2","ip":"192.168.0.103","port":"554","path":"/streaming/channels/101/","username":"","password":"","enabled":true,"width":1280,"height":720,"frame_rate":30,"resolution":"720", "auto_delete": 30}, {"button_no": "3", "field":"3132","name":"CAMERA_3","ip":"192.168.0.105","port":"554","path":"/streaming/channels/101/","username":"","password":"","enabled":true,"width":1280,"height":720,"frame_rate":30,"resolution":"720", "auto_delete": 30},{"button_no": "4", "field":"3133","name":"CAMERA_4","ip":"192.168.0.106","port":"554","path":"/streaming/channels/101/","username":"","password":"","enabled":true,"width":1280,"height":720,"frame_rate":30,"resolution":"720", "auto_delete": 30}]

# Recording Configuration
# ------------------------------------------
# Duration of each video segment in seconds
SEGMENT_DURATION=30
# Default resolution and frame rate for legacy camera
WIDTH=800
HEIGHT=600
FRAME_RATE=30

# Storage Configuration
# ------------------------------------------
# Local path for storing video files
STORAGE_PATH=./videos
# Hardware acceleration (nvidia, intel, amd, videotoolbox, or empty for software encoding)
HW_ACCEL=
# Video codec (avc, hevc, av1)
CODEC=avc

# Server Configuration
# ------------------------------------------
# Port for the web server
PORT=3000
# Base URL for serving videos (update this to your domain when deploying)
BASE_URL=http://localhost:3000

# Database Configuration
# ------------------------------------------
# Path to the SQLite database file
DATABASE_PATH=

# R2 Storage Configuration
# ------------------------------------------
# Set to true to enable R2 cloud storage
R2_ENABLED=true
# Cloudflare R2 credentials
R2_TOKEN_VALUE=
R2_ACCESS_KEY=
R2_SECRET_KEY=
R2_ACCOUNT_ID=
# Bucket name to store video files
R2_BUCKET=ayo-video
# Optional: Custom endpoint (leave empty to use default Cloudflare endpoint)
R2_ENDPOINT=https://dbb5364ca76f3970ec03f80e93fb403f.r2.cloudflarestorage.com
# Region (auto is usually fine)
R2_REGION=auto
R2_BASE_URL=https://ayomatchcam.com
WATERMARK_POSITION=top_right      # or top_left, bottom_left, bottom_right, center
WATERMARK_MARGIN=10               # integer pixels
WATERMARK_OPACITY=0.6

AYOINDO_API_BASE_ENDPOINT=https://gateway-staging.ayo.co.id/api/v1
# API token for AyoIndonesia API
AYOINDO_API_TOKEN=
# Venue code (10-character unique code for each venue)
VENUE_CODE=
# Venue secret key (used to generate HMAC-SHA512 signatures)
VENUE_SECRET_KEY=

CLIP_DURATION=60

# Arduino Configuration
# ------------------------------------------
# COM port for Arduino
ARDUINO_COM_PORT=COM4
# Baud rate for Arduino
ARDUINO_BAUD_RATE=9600
```

## Directory Structure

```
videos/
├── recordings/                    # Source video recordings
│   └── camera_1/                 # Each camera has its own directory
│       └── mp4/                  # Original MP4 files
│           └── camera_1_20250320_172910.mp4
|       └── tmp/                  # Temporary file for watermarked version of videos to be uploaded
|       └── hls/                  # HLS Streaming
|       └── log/                  # ffmpeg log, useful for debugging

```

## Usage

1. Start the application by running autorun.sh:
   ```bash
   ./autorun.sh
   ```

   By default, the script will only create or update the systemd service file if it is missing or its content has changed. If you want to force an update of the systemd service file (for example, after making manual changes or troubleshooting), you can use the `--update-service` flag:

   ```bash
   ./autorun.sh --update-service
   ```
   This will always recreate the systemd service file, even if it is already up to date.
   

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

You can test the API using curl:

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

### Monitoring Worker Status

You can monitor worker activity through the application logs:
```
📊 BOOKING-CRON: Sistem antrian dimulai - maksimal 4 proses booking bersamaan
📊 VIDEO-REQUEST-CRON: Sistem antrian dimulai - maksimal 3 proses video request bersamaan
📦 QUEUE: 🔄 Memproses 8 task yang tertunda (max 5 concurrent)...
```

## Quality Presets Configuration

The application supports configurable video quality presets for transcoding. You can control which quality variants are generated during video processing.
Adding quality to the config will increase processing time and CPU/RAM usage during HLS creation.

Note that the MP4 will choose the highest quality enabled

### Available Quality Presets

| Preset | Resolution | Bitrate | Bandwidth | Use Case |
|--------|------------|---------|-----------|----------|
| 1080p  | 1920x1080  | 5000k   | 5000000   | High quality, good internet |
| 720p   | 1280x720   | 2800k   | 2800000   | Standard quality, balanced |
| 480p   | 854x480    | 1400k   | 1400000   | Lower quality, slower internet |
| 360p   | 640x360    | 800k    | 800000    | Lowest quality, very slow internet |


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

### How It Works

1. **Automatic Discovery**: The system automatically discovers and registers available disks
2. **Priority-Based Selection**: Selects disks based on configured priorities and available space
3. **Health Monitoring**: Continuously monitors disk health and available space
4. **Dynamic Switching**: Automatically switches to alternative disks when current disk becomes full
5. **Size-Based Adjustment**: Larger disks get slightly higher priority within the same type


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

