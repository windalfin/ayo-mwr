# Zero-Downtime Restart Plan for AYO-MWR

## Overview
This document outlines the implementation plan for achieving zero-downtime restarts of the AYO-MWR video recording service, ensuring no gaps in recording during service updates or scheduled restarts.

## Current State Analysis

### Issues with Current Implementation
- **No graceful shutdown**: FFmpeg processes are killed abruptly
- **No signal handling**: Application doesn't respond to SIGTERM/SIGINT
- **Recording gaps**: Service restarts cause 5-10 second gaps in recordings
- **Incomplete segments**: Last recording segments may be corrupted
- **No health checks**: No way to verify service readiness

### Current Service Behavior
- Systemd manages lifecycle with automatic restarts
- Daily restart at 2 AM via systemd timer
- 5-second restart delay on failure
- No coordination between systemd and application

## Implementation Plan

### Phase 1: Graceful Shutdown Implementation

#### 1.1 Signal Handling in main.go
```go
// Add signal handling for graceful shutdown
- Listen for SIGTERM, SIGINT, SIGHUP
- Propagate shutdown signal to all goroutines via context
- Wait for all workers to complete gracefully
- Maximum shutdown timeout: 30 seconds
```

#### 1.2 FFmpeg Process Management
```go
// Graceful FFmpeg termination
- Send SIGTERM to FFmpeg (not SIGKILL)
- Wait for current segment to complete (max 15 seconds)
- Ensure HLS manifest is updated
- Verify MP4 segments are finalized
```

#### 1.3 Worker Shutdown Sequence
1. Stop accepting new recording requests
2. Complete current recording segments
3. Flush any buffered data
4. Update database with final states
5. Close all file handles
6. Signal completion to main

### Phase 2: Blue-Green Deployment Pattern

#### 2.1 Architecture Overview
```
[Load Balancer/Proxy]
    |
    ├── [Instance A - Active] Port 8080
    |   └── Recording to /recordings/
    |
    └── [Instance B - Standby] Port 8081
        └── Ready to take over
```

#### 2.2 Implementation Steps
1. **Dual Instance Support**
   - Configure application to run on configurable ports
   - Add instance identifier to database entries
   - Implement shared storage access

2. **Traffic Switching**
   - Use nginx or haproxy for load balancing
   - Health check based routing
   - Graceful connection draining

3. **State Synchronization**
   - Shared database for recording metadata
   - File-based locking for recording conflicts
   - Instance coordination via Redis/etcd (optional)

### Phase 3: Overlap Recording Strategy

#### 3.1 Recording Timeline
```
Old Instance: |--Recording--|--Graceful Stop (30s)--|
New Instance:              |--Startup--|--Recording--|
Overlap:                              |--30 seconds--|
```

#### 3.2 Implementation Details
1. **Segment Naming Convention**
   ```
   {camera_id}_{timestamp}_{instance_id}_{segment_num}.mp4
   ```

2. **Overlap Handling**
   - Both instances record during transition
   - Post-processor removes duplicates
   - Priority given to complete segments
   - Seamless timeline reconstruction

3. **Database Schema Updates**
   ```sql
   ALTER TABLE video_segments ADD COLUMN instance_id VARCHAR(50);
   ALTER TABLE video_segments ADD COLUMN overlap_status VARCHAR(20);
   ```

### Phase 4: Health Check Implementation

#### 4.1 Health Check Endpoint
```
GET /health
Response: {
  "status": "healthy|degraded|unhealthy",
  "recording": true,
  "cameras": {
    "camera1": "recording",
    "camera2": "recording"
  },
  "uptime": 3600,
  "instance_id": "instance-a",
  "ready_since": "2024-01-15T10:00:00Z"
}
```

#### 4.2 Readiness Criteria
- All configured cameras are recording
- FFmpeg processes are running
- Database connection is active
- Disk space is available
- No critical errors in last 60 seconds

### Phase 5: Enhanced Systemd Configuration

#### 5.1 Updated Service File
```ini
[Unit]
Description=AYO MWR Video Recording Service
After=network.target
Wants=network-online.target

[Service]
Type=notify
User=ayo
Group=ayo
WorkingDirectory=/opt/ayo-mwr
ExecStartPre=/opt/ayo-mwr/pre-start-check.sh
ExecStart=/opt/ayo-mwr/ayo-mwr
ExecReload=/opt/ayo-mwr/zero-downtime-reload.sh
ExecStop=/bin/kill -TERM $MAINPID
TimeoutStopSec=45
Restart=always
RestartSec=5
KillMode=mixed
KillSignal=SIGTERM

# Notify systemd when ready
NotifyAccess=main

[Install]
WantedBy=multi-user.target
```

#### 5.2 Systemd Integration
- Implement sd_notify for readiness signaling
- Use Type=notify for proper startup sequencing
- Extended timeout for graceful shutdown
- Custom reload script for zero-downtime updates

### Phase 6: Zero-Downtime Deployment Script

#### 6.1 Deployment Flow
```bash
#!/bin/bash
# zero-downtime-deploy.sh

1. Build new binary
2. Start new instance on alternate port
3. Wait for health check pass
4. Update load balancer configuration
5. Signal old instance to gracefully stop
6. Monitor overlap period
7. Verify recording continuity
8. Remove old instance
```

#### 6.2 Rollback Capability
- Automatic rollback on health check failure
- Preserve old binary until deployment success
- Log all deployment steps
- Alert on deployment issues

### Phase 7: Recording Continuity Features

#### 7.1 Segment Stitching
- Identify overlap periods
- Merge segments at keyframe boundaries
- Maintain continuous timestamps
- Remove duplicate frames

#### 7.2 Gap Detection
```go
// Detect and report recording gaps
- Monitor segment timestamps
- Alert on gaps > 1 second
- Automatic gap filling from overlap recordings
- Generate continuity reports
```

#### 7.3 Timeline Reconstruction
- Build continuous timeline from segments
- Handle clock drift between instances
- Provide seamless playback experience
- Export tools for verification

### Phase 8: Testing and Validation

#### 8.1 Test Scenarios
1. **Standard Restart Test**
   - Trigger graceful restart
   - Verify no recording gaps
   - Check segment integrity

2. **Load Test**
   - Restart under heavy recording load
   - Verify all cameras continue recording
   - Monitor resource usage

3. **Failure Scenarios**
   - Test ungraceful shutdown
   - Network interruption during restart
   - Disk space exhaustion

4. **Continuity Validation**
   - Frame-by-frame comparison
   - Audio sync verification
   - Timestamp continuity check

#### 8.2 Monitoring and Metrics
- Recording gap duration
- Restart completion time
- Segment overlap percentage
- Failed restart attempts
- Recovery time metrics

## Implementation Timeline

### Week 1-2: Foundation
- [ ] Implement graceful shutdown
- [ ] Add signal handling
- [ ] Update FFmpeg management

### Week 3-4: Blue-Green Setup
- [ ] Dual instance support
- [ ] Load balancer configuration
- [ ] Health check endpoint

### Week 5-6: Overlap Recording
- [ ] Implement overlap strategy
- [ ] Database schema updates
- [ ] Segment deduplication

### Week 7-8: Testing & Refinement
- [ ] Comprehensive testing
- [ ] Performance optimization
- [ ] Documentation updates

## Success Criteria

1. **Zero Recording Gaps**: No missing frames during restarts
2. **Fast Restart Time**: Complete restart in < 45 seconds
3. **Automatic Recovery**: Self-healing on failures
4. **Monitoring Visibility**: Clear metrics on restart impact
5. **Backward Compatibility**: Works with existing recordings

## Rollout Strategy

1. **Development Environment**: Full implementation and testing
2. **Staging Environment**: 1-week validation period
3. **Production Rollout**: Phased deployment with monitoring
4. **Full Adoption**: After 2 weeks of stable operation

## Usage Instructions

### 1. Deploy with Zero Downtime
```bash
# Standard zero-downtime deployment
./zero-downtime-deploy.sh

# The script will:
# - Build new binary
# - Backup current version
# - Health check before deployment
# - Atomic binary replacement
# - Graceful service restart
# - Post-deployment verification
# - Automatic rollback on failure
```

### 2. Manual Graceful Restart
```bash
# Graceful restart using systemd
sudo systemctl reload-or-restart ayo-mwr

# Or send signal directly (if running manually)
kill -TERM <process_id>
```

### 3. Health Check Monitoring
```bash
# Check service health
curl http://localhost:8080/api/health | jq

# Example response:
{
  "status": "healthy",
  "timestamp": "2024-01-15T10:00:00Z",
  "recording": {
    "status": "recording",
    "running_cameras": 2,
    "enabled_cameras": 2
  },
  "database": {"status": "connected"},
  "system": {
    "memory_mb": 45,
    "goroutines": 12
  }
}
```

### 4. Test Graceful Shutdown
```bash
# Run comprehensive tests
./test-graceful-shutdown.sh

# Tests include:
# - SIGTERM/SIGINT handling
# - Health check endpoint
# - Service restart behavior
# - Recording continuity
```

### 5. Monitor Service Status
```bash
# Check systemd service status
sudo systemctl status ayo-mwr

# View real-time logs
sudo journalctl -u ayo-mwr -f

# Check recording workers
curl -s http://localhost:8080/api/health | jq '.recording'
```

## Implementation Status

### ✅ Completed Features

1. **Graceful Shutdown Implementation**
   - Signal handling (SIGTERM, SIGINT, SIGHUP)
   - Context-based cancellation
   - FFmpeg graceful termination (15s timeout)
   - Worker cleanup and synchronization

2. **Health Check Endpoint**
   - `/api/health` endpoint with comprehensive status
   - Database connectivity check
   - Recording status monitoring
   - System resource information

3. **Enhanced Systemd Configuration**
   - 45-second graceful shutdown timeout
   - Proper signal handling (SIGTERM)
   - Mixed kill mode for child processes
   - Automatic restart on failure

4. **Zero-Downtime Deployment Script**
   - Atomic binary replacement
   - Pre/post deployment health checks
   - Automatic rollback on failure
   - Recording continuity verification

5. **Testing Framework**
   - Comprehensive test suite
   - Signal handling validation
   - Service restart testing
   - Recording continuity checks

### 🔄 Next Steps (Optional Enhancements)

1. **Blue-Green Deployment** (Advanced)
   - Dual instance support
   - Load balancer integration
   - Overlap recording strategy

2. **Monitoring Integration**
   - Prometheus metrics
   - Grafana dashboards
   - Alert notifications

3. **Advanced Health Checks**
   - Camera connectivity testing
   - Disk space monitoring
   - FFmpeg process validation

## Maintenance Considerations

- **Regular Testing**: Run `./test-graceful-shutdown.sh` monthly
- **Monitor Metrics**: Track restart times and recording gaps
- **Update Procedures**: Use `./zero-downtime-deploy.sh` for all deployments
- **Backup Strategy**: Automatic rollback is built-in
- **Log Management**: Monitor systemd journals for graceful shutdown behavior

## Troubleshooting

### Common Issues

1. **Health Check Failures**
   ```bash
   # Check if service is responding
   curl -v http://localhost:8080/api/health
   
   # Verify database connectivity
   sudo journalctl -u ayo-mwr -n 50
   ```

2. **Long Shutdown Times**
   ```bash
   # Check FFmpeg processes
   ps aux | grep ffmpeg
   
   # Monitor shutdown logs
   sudo journalctl -u ayo-mwr -f
   ```

3. **Recording Gaps**
   ```bash
   # Verify camera status
   curl -s http://localhost:8080/api/health | jq '.recording'
   
   # Check camera worker logs
   sudo journalctl -u ayo-mwr -f | grep -i camera
   ```

### Configuration

Key configuration files:
- `/etc/systemd/system/ayo-mwr.service` - Service definition
- `.env` - Environment variables
- `ZERO_DOWNTIME_PLAN.md` - Implementation documentation

### Performance Metrics

Expected performance:
- **Graceful Shutdown**: < 30 seconds
- **Service Restart**: < 45 seconds
- **Recording Gap**: 0 seconds (ideal)
- **Health Check Response**: < 1 second