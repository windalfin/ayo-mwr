# Recording Storage & Processing: Design Decision & Functionality

## Extension of Watermarking Design
This document extends the decisions made in `watermarking_frd.md` regarding post-processing and watermarking of recorded videos.

## Key Decisions
1. **Stored Recording = Watermarked Version**
   - After each video is recorded, it will always undergo a post-processing step.
   - If a watermark is specified, it will be applied; if not, the video will still be processed (copied or re-encoded) to ensure a consistent workflow and output format.
   - The stored video in the system will always be the watermarked (or post-processed) version.

2. **Post-Processed Recording as Source for HLS and R2**
   - The watermarked/post-processed recording will be the input for all further conversions (e.g., HLS, DASH) and for uploads to external storage such as Cloudflare R2.
   - This ensures that all distributed and archived content is based on the final, post-processed version, maintaining consistency across formats and storage locations.

## Rationale
- Guarantees all distributed content includes intended branding or watermarking.
- Simplifies pipeline: only one version of each video is used for downstream tasks.
- Ensures even unwatermarked videos are normalized (e.g., re-encoded, validated) before further use.
- Reduces risk of accidental distribution of unprocessed or raw videos.

## Implementation Notes
- The post-processing step should be robust and able to handle cases where no watermark is applied (acts as a pass-through or re-encode).
- Metadata should indicate whether a watermark was present/applied for traceability.
- The pipeline should ensure that only post-processed videos are referenced for HLS conversion and R2 upload.

## Open Questions
- Should the original, raw (pre-watermark) video be preserved for audit or recovery?
  - **Yes. The original raw video will be stored locally (e.g., in `recordings/[camera]/raw/`), but will NOT be uploaded to R2 or any external storage.** This ensures auditability and recovery while preventing accidental distribution.

- What metadata should be attached to each stored video to track watermark presence, processing time, etc.?
  - **Best Practice Metadata Fields:**
    - `video_id`: Unique identifier for the video.
    - `source_filename`: Name/path of the original raw video file.
    - `recorded_at`: Timestamp when the video was recorded.
    - `camera_name`: Source camera identifier.
    - `watermark_applied`: Boolean (true/false).
    - `watermark_image`: Path or identifier of the watermark image used (if any).
    - `watermark_position`: Position (e.g., top-right, bottom-left) and margin used.
    - `watermark_opacity`: Numeric value (e.g., 0.6) if applicable.
    - `processing_started_at`: Timestamp when post-processing began.
    - `processing_completed_at`: Timestamp when post-processing finished.
    - `processing_duration_ms`: How long the post-processing took (milliseconds).
    - `processed_by`: Identifier for the server/process that performed post-processing.
    - `hls_generated`: Boolean (true/false) indicating if HLS output was produced.
    - `hls_path`: Path or URL to the HLS output (if generated).
    - `r2_uploaded`: Boolean (true/false) indicating if the video was uploaded to R2.
    - `r2_url`: URL or identifier in R2 (if uploaded).
    - `error`: Any error message encountered during processing (optional).
  - **Format Suggestion:** Store metadata as a JSON sidecar file (e.g., `video_id.json`) alongside the processed video for easy access and extensibility.

---
See also: [watermarking_decision.md](./watermarking_decision.md)
