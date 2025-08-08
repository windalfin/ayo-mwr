# Date-Based Multi-Root Storage Solution (High-Level Plan)

## Problem Statement
Booking video processing can fail when a disk switch occurs mid-booking because lookups assume a single storage root. After a switch, segments for the same booking window may reside on different disks.

## Key Constraint
Bookings never span midnight. Each booking maps to exactly one calendar day. This creates a safe natural boundary.

## Core Idea (Variant B)
Use date-based directory roots and allow multiple roots per (camera, date) without moving existing data. Each disk switch simply begins writing to a new root for the same date. Segment lookup becomes a bounded merge across the small set of roots for that day.

## Target Structure (Conceptual)
Per disk and per date, store recordings under a date folder, then camera, then media type (e.g. hls). 

## Principles
1. Multi-root per day: A day may accumulate 1–3 roots if switches occur.
2. No data movement during recording: Never copy or migrate active-day folders.
3. Bounded lookup: Booking lookup touches only the small set of roots for (camera, date).
4. Natural day boundary: Simplifies scoping and prevents cross-day complexity.
5. Zero downtime: Switches do not pause FFmpeg or recording flows.
6. Traceability preserved: Original disk association retained for each root.

## High-Level Flow
### Recording
- Determine current active disk when (re)starting a recording writer.
- Derive today’s date folder; ensure it exists.
- If this (camera, date, disk) root not yet tracked, register it.
- Continue writing new segments into that root. Previous roots remain intact.

### Disk Switch
- Triggered by space threshold or health condition.
- Select a new disk (existing priority logic).
- Recording processes begin writing to a new date root on the new disk (implicit on restart or soft signal).
- No attempt to relocate earlier segments.
- A new root record is appended for the same (camera, date).

### Service Restart
- Re-evaluate active disk; reselect if invalid or missing.
- Reconstruct today’s root tracking by scanning expected date folders that already exist on mounted disks (lightweight presence check).
- Start writing into the active disk root (creating root entry if new).
- If active disk differs from previous recorded one, treat as a switch event.

### Booking Segment Lookup
- Derive booking date from start time.
- Retrieve all registered roots for (camera, date).
- For each root, enumerate segments within the time window (using existing filename-based time parsing logic, unmodified conceptually).
- Merge and sort results; optionally deduplicate if overlapping.
- If no roots found, (during migration period) fall back to legacy flat path; otherwise declare no data.

### Root Tracking (Conceptual)
Maintain a lightweight record per (camera, date, disk) capturing:
- Camera name
- Date (YYYYMMDD)
- Disk identifier
- Base path to the day/camera media folder
- First-seen and last-seen timestamps (for auditing / pruning)

### Switch Event Logging (Optional)
Record transitions (previous active disk → new active disk) with a reason (low space, restart, manual) to aid diagnostics and trend analysis.

## Edge Cases & Handling
- Missing root: Log and skip; no fatal error.
- Multiple rapid switches: Additional roots added; lookup still bounded.
- Inaccessible prior disk: Some historical segments unavailable; booking coverage may drop; coverage metrics reflect this.
- Midnight boundary: New date automatically creates a fresh root list (prior day immutable). Bookings spanning midnight are disallowed and should be rejected early.
- Restart near midnight: Only current day reconstruction required (historic day unaffected).

## Observability (Conceptual Metrics)
- Count of roots per (camera, date) to detect abnormal switching.
- Disk switch events over time.
- Booking lookup latency vs number of roots.
- Coverage ratio (segments found vs expected time span).
- Missing root / inaccessible path warnings.

## Reliability & Risk Mitigation
| Risk | Mitigation |
|------|------------|
| Data loss during switch | No in-flight data movement; only additive roots |
| Lookup performance degradation | Bounded root count; single-day scope |
| Complex recovery after failure | Roots are immutable references; no partial migration states |
| Space exhaustion on inactive disks | Standard cleanup policies unaffected by multi-root approach |

## Migration Strategy (Conceptual)
1. Introduce root tracking alongside legacy flat layout (dual-write optional or immediate switch for new days).
2. Enable multi-root lookup with fallback to legacy path if no tracked roots.
3. Monitor metrics (root counts, booking success, lookup latency).
4. After confidence: stop referencing legacy flat path for new bookings.
5. Optional cleanup or archival of legacy structure when obsolete.

## Success Criteria
- Bookings unaffected by mid-day disk switches (no increase in “no video” outcomes).
- Lookup latency remains low and stable (bounded by root count, usually 1–2).
- Zero required downtime during disk transitions.
- Clear observability into switch frequency and coverage quality.

## Out of Scope (Future Enhancements)
- Automatic gap detection and alerting for missing minute ranges.
- Proactive reprocessing when previously inaccessible disk returns.
- Cross-day or multi-day segment collation (not needed due to booking constraint).
- Predictive disk switching based on forecast usage.

## Summary
This plan restructures storage around immutable, date-scoped, multi-root folders. Instead of risky migration, it embraces additive roots per day, ensuring resilience to disk switches, simplicity in lookup, and strong operational safety with minimal code intrusion. All changes remain conceptual: implementation details (data structures, function signatures, schemas) are intentionally omitted to keep the document focused on architecture and behavior.