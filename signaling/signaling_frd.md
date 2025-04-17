# Signaling Subsystem Documentation

## Implementation Status

## ✅ Done (Implemented)
- Signal handling via Arduino (ArduinoSignal) and software (FunctionSignal) using the SignalHandler interface.
- /api/transcode endpoint triggers transcoding with timestamp and camera name.
- FindClosestVideo utility selects the closest video file for a given timestamp.
- Directory structure and naming conventions for source, HLS, and DASH outputs.
- Consistent API and response format for transcoding.
- Extensible design for adding new signal sources.
- Integrate R2 upload: trigger upload of transcoded files to Cloudflare R2 after successful transcoding.
- Never upload files from the raw/ directory.


## ⏳ Not Done (Pending/TODO)
- Enforce watermarking: ensure only watermarked (post-processed) videos are uploaded to R2.
- Add metadata check before upload (read video_id.json, confirm watermark_applied is true).
- Integrate watermarking (AddWatermark/AddWatermarkWithPosition) into the main pipeline after recording, before HLS/R2 upload.
- Update metadata sidecar after watermarking and upload steps.
- Ensure robust error handling and logging for all new steps.
- Prevent concurrent requests for the same video/camera (locking)

## Overview
The signaling subsystem manages the reception and processing of signals (both hardware and software) that trigger video transcoding operations. It is designed to be extensible, supporting multiple sources of signals, and to decouple signal handling from transcoding logic.

## Key Responsibilities
- **Signal Reception:** Listens for and processes signals from hardware (e.g., Arduino via serial port) and software sources.
- **Transcoding Trigger:** Initiates video transcoding by calling the `/api/transcode` endpoint with relevant metadata (timestamp and camera name).
- **Utility Functions:** Provides helpers for managing video file lookup and signal processing.

## Main Components

### 1. SignalHandler Interface
Defines the contract for handling signals:
```go
type SignalHandler interface {
    HandleSignal(signal string) error
}
```

### 2. ArduinoSignal
Handles signals from Arduino devices via serial (COM) ports.
- Manages serial port connection with thread safety (mutex).
- Starts a listener goroutine to read signals reliably.
- Invokes a callback function for each received signal.
- On signal, triggers transcoding with the current timestamp and camera name.
- Implements clean shutdown via `Close()`.

### 3. FunctionSignal
Allows software-based signal triggering using the same interface and flow as hardware signals.

### 4. Utility Functions
- **FindClosestVideo:** Locates the video file closest to a given timestamp for a specified camera. 
- Utilities are located in `utils.go` and are used to keep the codebase maintainable and reusable.

## Directory Structure
```
signaling/
├── arduino.go         # ArduinoSignal implementation
├── function.go        # FunctionSignal implementation
├── utils.go           # Utility functions (e.g., FindClosestVideo)
├── interface.go       # SignalHandler interface
├── SIGNALING.md       # This documentation
```

## Signal Processing Flow
1. **Connection Setup:**
   - `ArduinoSignal.Connect()` establishes serial port connection and starts a listener goroutine.
   - Uses mutex for thread-safe access.
2. **Listening:**
   - Continuously reads signals from the serial port using `bufio.Scanner`.
   - Invokes the callback function for each signal.
3. **Processing:**
   - Callback triggers a POST request to `/api/transcode` with the timestamp and camera name.
   - System finds the closest video and starts transcoding.
4. **Shutdown:**
   - `Close()` method safely closes the serial connection and cleans up resources.

## Example Transcode Request
```json
{
  "timestamp": "2025-03-20T11:58:51+07:00",
  "cameraName": "camera_2"
}
```

## Output
- URLs for transcoded content (HLS, MP4)
- Performance metrics
- Unique video ID
- Source filename

## Extensibility
- The `SignalHandler` interface allows for easy addition of new signal sources.
- Both hardware (Arduino) and software signals use the same API and processing flow.

## Planned Feature: R2 Upload Integration

### Overview
After successful video transcoding, the signaling subsystem will trigger an upload of the resulting files to Cloudflare R2 object storage. This ensures that transcoded outputs are reliably backed up and accessible via cloud storage.

### Implementation Plan
1. **Locate R2 Upload Function**
   - Identify and utilize the existing upload function in the `storage` package responsible for handling uploads to R2.
2. **Integration Point**
   - After the transcoding process completes (and before or after responding to the API client, as appropriate), invoke the R2 upload function from within the signaling flow.
3. **Data Flow**
   - Pass the path(s) of the transcoded output (HLS, MP4, etc.) to the R2 upload function.
   - Handle upload results and errors gracefully, logging any issues and ensuring the system remains robust.
4. **Error Handling**
   - If the upload fails, log the error and consider retry or alert mechanisms.
   - Ensure the signaling process does not leave resources in an inconsistent state.
5. **Extensibility**
   - The integration will be designed so that additional storage backends can be added in the future with minimal changes to the signaling logic.

### Benefits
- Provides offsite/cloud backup for transcoded videos.
- Enables scalable, reliable access to video assets.
- Prepares the system for future cloud workflows and integrations.

## Implementation Plan: R2 Upload Integration in Signaling

### 1. Initialization
- Ensure the signaling subsystem can access R2 configuration (credentials, bucket, etc.).
- Instantiate a singleton or shared `R2Storage` object on application start, or as needed.

### 2. Integration Point
- After successful transcoding (when output files/dirs are ready), trigger uploads:
  - For HLS: call `UploadHLSStream(hlsDir, videoID)`
  - For MP4: call `UploadMP4(mp4Path, videoID)`

### 3. Data Flow
- Pass the local output directory/file path and the video ID to the relevant upload function.
- Capture the returned R2 URL(s) for further use (e.g., API response, logging, notifications).

### 4. Error Handling
- If upload fails, log the error with details.
- Optionally, implement retry logic or alerting.
- Ensure upload errors do not crash the signaling process.

### 5. Extensibility
- Abstract the upload logic so that new storage backends can be added with minimal changes.
- Use interfaces or dependency injection if the system is expected to support multiple storage providers in the future.

#### Example Pseudocode
```go
// Initialization (once)
r2Config := storage.R2Config{ ... }
r2Storage, err := storage.NewR2Storage(r2Config)

// In signal processing flow, after transcoding:
hlsURL, err := r2Storage.UploadHLSStream(hlsDir, videoID)
mp4URL, err := r2Storage.UploadMP4(mp4Path, videoID)
```

## TODO: Watermark Enforcement Before Upload
- Ensure that only watermarked (post-processed) videos are uploaded to R2.
- Add a metadata check before upload:
  - Read the metadata JSON sidecar (e.g., `video_id.json`).
  - Confirm `watermark_applied` is `true`.
  - If not, skip upload and log a warning/error.
- Never upload files from the `raw/` directory.
- Always use the canonical post-processed video for any upload or distribution.

## TODO: Integrate Watermarking Into Recording/Post-Processing Pipeline
- Integrate the watermarking step (using `AddWatermarkWithPosition` or `AddWatermark`) into the main video processing flow.
- Ensure watermarking is performed automatically after each video is recorded, before HLS generation or R2 upload.
- Update the metadata sidecar (`video_id.json`) to reflect watermark status and parameters.
- Watermarking should be robust and handle errors gracefully (preserve original if watermarking fails).

---
*Last updated: 2025-04-16*
