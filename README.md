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
R2_REGION=auto
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
   go run main.go
   ```

2. The application will:
   - Begin capturing video from your RTSP cameras
   - Save segments to the configured storage path
   - Convert segments to HLS and DASH formats
   - Serve streams through the web server

## API Endpoints

### List Streams
```http
GET /api/streams
```

Lists all available video streams with their status and URLs.

### Get Stream Details
```http
GET /api/streams/:id
```

Get details for a specific video stream.

### Transcode Video
```http
POST /api/transcode
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

## Troubleshooting

- **RTSP Connection Issues**: Verify your camera's IP, username, password, and RTSP path
- **FFmpeg Errors**: Ensure FFmpeg is installed and in your PATH
- **Playback Issues**: Try different players (VLC, web browsers with HLS.js)
- **Missing Files**: Check the logs for any file creation or permission errors

## License
This app belong to Ayo Indonesia
sip ok