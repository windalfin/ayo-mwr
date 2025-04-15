# Watermarking Feature: Design Decision & Functionality

## Decision
Watermarking will be performed as a **post-processing** step after video recording is complete.

## Rationale
- Supports dynamic watermark requirements (image and location provided by API).
- Avoids impacting real-time recording performance.
- Allows for batch or asynchronous processing.
- Easier to update or retry watermarking if issues occur.

## Planned Functionality
- The API will provide the watermark image and its position (coordinates) for each video.
- After a video is recorded and saved, a watermarking function will:
    - Load the recorded video.
    - Overlay the watermark image at the specified position.
    - Save the result (as a new file or by overwriting the original; to be finalized).
- The process should:
    - Handle errors gracefully (original video preserved if watermarking fails).
    - Be efficient and scalable for multiple concurrent videos.
    - Support all video formats in use (e.g., MP4, HLS master file, etc.).

## Open Questions (Updated)
- Original non-watermarked videos will be kept in a temporary folder after watermarking.
- Watermarking will run in the background (asynchronous process).
- Watermark images must be in PNG format.

---
This document should be updated as implementation progresses.
