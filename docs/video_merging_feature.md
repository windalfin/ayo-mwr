# Video Segment Merging (Full Session Video)

## Goal
Enable merging of multiple recorded MP4 video segments into a single, continuous full session video using FFmpeg. This is required for users who want to download or review an entire session (e.g., a match or event) as one file.

## Why FFmpeg?
- FFmpeg is robust, widely used, and supports lossless concatenation for compatible video files.
- It can merge segments without re-encoding if all segments have matching codecs, resolution, and encoding parameters.

## Implementation Plan

### 1. Segment Discovery: Find All Segments in Time Range
- **Use** the new utility function `FindSegmentsInRange(inputPath, startTime, endTime) ([]string, error)` to discover segments to merge.
- This function:
  - Lists all MP4 files in the directory (e.g., `/recordings/[camera_name]/mp4/`).
  - Extracts timestamps from filenames (e.g., `camera_1_20250320_115851.mp4`).
  - Selects those that overlap or fall within `[start_time, end_time]`.
  - Orders them chronologically.

- **Reference:** The filename parsing and timestamp logic is based on the approach used in `FindClosestVideo` in `signaling/utils.go`, but generalized to return all segments in a range.

### 2. Generate FFmpeg Concat List
- Create a temporary text file (e.g., `segments.txt`) with the following format:
  ```
  file '/path/to/segment1.mp4'
  file '/path/to/segment2.mp4'
  ...
  ```
- This list will be passed to FFmpeg's concat demuxer.

### 3. Run FFmpeg Concat Command
- Use the following FFmpeg command:
  ```sh
  ffmpeg -f concat -safe 0 -i segments.txt -c copy output_merged.mp4
  ```
- `-c copy` ensures no re-encoding (fast, lossless).
- `-safe 0` allows absolute paths in the list file.
- **If segment codecs do not match,** fallback to re-encoding:
  ```sh
  ffmpeg -f concat -safe 0 -i segments.txt -c:v libx264 -c:a aac output_merged.mp4
  ```

### 4. Output and Cleanup
- Output is a single MP4 at `output_path`.
- Delete the temporary `segments.txt` file after merging.

### 5. Validation & Error Handling
- Validate that all segments exist and are readable.
- Handle missing/corrupt segments gracefully (skip or abort with error).
- Log or return errors clearly.

### 6. (Optional) Partial Segment Trimming
- If session does not align with segment boundaries, optionally trim the first/last segment using FFmpeg's `-ss` and `-to` parameters.
- Otherwise, include the full segment if it overlaps the window.

### 7. (Optional) Metadata Output
- Generate metadata for the merged video (e.g., list of source segments, time range) for traceability.

### 8. Testing
- Add tests for:
  - Normal case (all segments present, no trimming)
  - Missing/corrupt segments
  - Segments with mismatched codecs
  - Edge-aligned and non-edge-aligned time windows

## Example Directory Structure
```
recordings/camera_1/mp4/camera_1_20250429_140000.mp4
recordings/camera_1/mp4/camera_1_20250429_141000.mp4
...
```

## Example Function Signature
```go
// Find all segments in the time range, then merge them
func MergeSessionVideos(inputPath string, startTime, endTime time.Time, outputPath string) error
```

## References
- [FFmpeg concat demuxer documentation](https://ffmpeg.org/ffmpeg-formats.html#concat-1)
- See also: logic in `signaling/utils.go` (`FindClosestVideo`) for timestamp parsing

## Next Steps
- Implement `FindSegmentsInRange` utility.
- Implement the Go merging function.
- Add tests for various session scenarios.
- Integrate with admin dashboard or API as needed.
