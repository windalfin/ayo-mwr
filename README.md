# RTSP Capture and Streaming

This application captures video from RTSP cameras, saves the content in segments, and automatically converts them to streaming formats (HLS and DASH). It includes a built-in web server to serve these streams to any device.

## Features

- **RTSP Video Capture**: Capture video from IP cameras using RTSP protocol
- **Segmented Recording**: Creates timed video segments for easier management
- **Streaming Support**: Automatically generates HLS and DASH streams
- **Web Server**: Built-in web server to deliver streaming content
- **Hardware Acceleration**: Support for NVIDIA, Intel, AMD, and macOS hardware acceleration
- **Configurable**: All settings can be adjusted via environment variables

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
   go mod init rtsp-capture
   go get github.com/gin-gonic/gin
   go get github.com/joho/godotenv
   ```

3. Install FFmpeg:
   - **Windows**: Download from [ffmpeg.org](https://ffmpeg.org/download.html) and add to your PATH
   - **macOS**: `brew install ffmpeg`
   - **Ubuntu/Debian**: `sudo apt install ffmpeg`

## Configuration

Create a `.env` file in the project root with the following options:

```
RTSP_USERNAME=your_camera_username
RTSP_PASSWORD=your_camera_password
RTSP_IP=your_camera_ip
RTSP_PORT=554
RTSP_PATH=/your/stream/path
STORAGE_PATH=./videos
HW_ACCEL=nvidia  # Options: nvidia, intel, amd, videotoolbox, or empty for software
CODEC=avc        # Options: avc, hevc, av1
PORT=3000        # Web server port
BASE_URL=http://localhost:3000
```

## Usage

1. Start the application:
   ```bash
   go run main.go
   ```

2. The application will:
   - Begin capturing video from your RTSP camera
   - Save segments to the `videos/uploads` directory
   - Convert segments to HLS and DASH formats
   - Serve streams through the web server

3. Access your streams:
   - HLS: `http://localhost:3000/hls/{video_id}/playlist.m3u8`
   - DASH: `http://localhost:3000/dash/{video_id}/manifest.mpd`
   - List all streams: `http://localhost:3000/api/streams`

## RTSP URL Formats

RTSP URLs can vary by camera manufacturer. Common formats include:

- Hikvision: `rtsp://username:password@ip:port/Streaming/Channels/101/`
- Dahua: `rtsp://username:password@ip:port/cam/realmonitor?channel=1&subtype=0`
- Axis: `rtsp://username:password@ip:port/axis-media/media.amp`
- Generic: `rtsp://username:password@ip:port/live/ch00_0`

## Hardware Acceleration

The application supports hardware acceleration for different GPUs:

- **NVIDIA**: Uses NVENC for hardware-accelerated encoding
- **Intel**: Uses QuickSync (QSV) for hardware-accelerated encoding
- **AMD**: Uses AMF for hardware-accelerated encoding
- **macOS**: Uses VideoToolbox for hardware-accelerated encoding

Set the `HW_ACCEL` environment variable to enable hardware acceleration.

## File Structure

```
videos/
├── uploads/         # Original captured MP4 segments
├── hls/             # HLS streaming files
│   └── {video_id}/  # Each video has its own directory
│       ├── playlist.m3u8
│       └── segment_*.ts
└── dash/            # DASH streaming files
    └── {video_id}/  # Each video has its own directory
        ├── manifest.mpd
        ├── init-stream*.m4s
        └── chunk-stream*.m4s
```

## Troubleshooting

- **RTSP Connection Issues**: Verify your camera's IP, username, password, and RTSP path
- **FFmpeg Errors**: Ensure FFmpeg is installed and in your PATH
- **Playback Issues**: Try different players (VLC, web browsers with HLS.js)
- **Missing Files**: Check the logs for any file creation or permission errors

## License
This app belong to Ayo Indonesia
