#!/bin/bash

# test-graceful-shutdown.sh - Test graceful shutdown implementation
# This script validates the zero-downtime restart functionality

set -e

# Configuration
APP_NAME="ayo-mwr"
SERVICE_NAME="ayo-mwr"
HEALTH_CHECK_URL="http://localhost:3000/api/health"
TEST_BINARY="./ayo-mwr-test"
LOG_FILE="shutdown-test-$(date +%Y%m%d_%H%M%S).log"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Graceful Shutdown Test Suite ===${NC}" | tee -a "$LOG_FILE"
echo -e "${BLUE}Testing zero-downtime restart capabilities${NC}" | tee -a "$LOG_FILE"
echo "Test started at: $(date)" | tee -a "$LOG_FILE"
echo "" | tee -a "$LOG_FILE"

# Function to log with timestamp
log_message() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

# Function to check if service is running
is_service_running() {
    systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null
}

# Function to wait for health check
wait_for_health() {
    local timeout=${1:-30}
    local start_time=$(date +%s)
    
    while [ $(($(date +%s) - start_time)) -lt $timeout ]; do
        if curl -sf "$HEALTH_CHECK_URL" >/dev/null 2>&1; then
            local health_response=$(curl -s "$HEALTH_CHECK_URL" 2>/dev/null || echo "{}")
            local status=$(echo "$health_response" | grep -o '"status":"[^"]*' | cut -d'"' -f4)
            if [ "$status" = "healthy" ] || [ "$status" = "degraded" ]; then
                log_message "✓ Health check passed (status: $status)"
                return 0
            fi
        fi
        sleep 1
    done
    
    log_message "✗ Health check failed after $timeout seconds"
    return 1
}

# Function to test signal handling
test_signal_handling() {
    local test_name="$1"
    local signal="$2"
    
    log_message "Testing $test_name (Signal: $signal)..."
    
    # Start test binary in background
    $TEST_BINARY > test_app.log 2>&1 &
    local PID=$!
    
    # Wait for application to start
    sleep 5
    
    if ! kill -0 $PID 2>/dev/null; then
        log_message "✗ Test binary failed to start"
        return 1
    fi
    
    log_message "Test binary started with PID: $PID"
    
    # Send signal
    log_message "Sending $signal to PID $PID..."
    kill -$signal $PID
    
    # Wait for graceful shutdown (max 35 seconds)
    local wait_time=0
    while kill -0 $PID 2>/dev/null && [ $wait_time -lt 35 ]; do
        sleep 1
        wait_time=$((wait_time + 1))
    done
    
    if kill -0 $PID 2>/dev/null; then
        log_message "✗ Process did not terminate gracefully within 35 seconds"
        kill -9 $PID 2>/dev/null
        return 1
    else
        log_message "✓ Process terminated gracefully in ${wait_time} seconds"
        return 0
    fi
}

# Test 1: Basic compilation check
log_message "=== Test 1: Compilation Check ==="
if [ ! -f "$TEST_BINARY" ]; then
    log_message "Building test binary..."
    go build -o "$TEST_BINARY" .
fi

if [ -f "$TEST_BINARY" ]; then
    log_message "✓ Binary compiled successfully"
else
    log_message "✗ Binary compilation failed"
    exit 1
fi

# Test 2: SIGTERM handling
log_message ""
log_message "=== Test 2: SIGTERM Handling ==="
if test_signal_handling "SIGTERM graceful shutdown" "TERM"; then
    log_message "✓ SIGTERM handling test passed"
else
    log_message "✗ SIGTERM handling test failed"
fi

# Test 3: SIGINT handling  
log_message ""
log_message "=== Test 3: SIGINT Handling ==="
if test_signal_handling "SIGINT graceful shutdown" "INT"; then
    log_message "✓ SIGINT handling test passed"
else
    log_message "✗ SIGINT handling test failed"
fi

# Test 4: Service health check endpoint
log_message ""
log_message "=== Test 4: Health Check Endpoint ==="

# Start service if not running
if ! is_service_running; then
    log_message "Service not running, starting for health check test..."
    sudo systemctl start "$SERVICE_NAME"
    sleep 10
fi

if wait_for_health 30; then
    log_message "✓ Health check endpoint test passed"
    
    # Test health check response format
    health_response=$(curl -s "$HEALTH_CHECK_URL" 2>/dev/null || echo "{}")
    
    if echo "$health_response" | grep -q '"status"'; then
        log_message "✓ Health check response contains status field"
    else
        log_message "✗ Health check response missing status field"
    fi
    
    if echo "$health_response" | grep -q '"recording"'; then
        log_message "✓ Health check response contains recording info"
    else
        log_message "✗ Health check response missing recording info"
    fi
else
    log_message "✗ Health check endpoint test failed"
fi

# Test 5: Systemd service graceful restart
log_message ""
log_message "=== Test 5: Systemd Graceful Restart ==="

if is_service_running; then
    log_message "Testing systemd graceful restart..."
    
    # Get current PID
    local old_pid=$(systemctl show "$SERVICE_NAME" --property=MainPID --value)
    log_message "Current service PID: $old_pid"
    
    # Record restart time
    local restart_start=$(date +%s)
    
    # Perform restart
    sudo systemctl restart "$SERVICE_NAME"
    
    # Wait for restart to complete
    sleep 5
    
    # Check if service is running with new PID
    if is_service_running; then
        local new_pid=$(systemctl show "$SERVICE_NAME" --property=MainPID --value)
        local restart_time=$(($(date +%s) - restart_start))
        
        log_message "New service PID: $new_pid"
        log_message "Restart completed in: ${restart_time} seconds"
        
        if [ "$new_pid" != "$old_pid" ]; then
            log_message "✓ Service restarted successfully"
            
            # Verify health after restart
            if wait_for_health 45; then
                log_message "✓ Service is healthy after restart"
            else
                log_message "✗ Service unhealthy after restart"
            fi
        else
            log_message "✗ Service PID unchanged after restart"
        fi
    else
        log_message "✗ Service not running after restart"
    fi
else
    log_message "⚠ Service not running, skipping restart test"
fi

# Test 6: Recording continuity check
log_message ""
log_message "=== Test 6: Recording Continuity Check ==="

if wait_for_health 30; then
    health_response=$(curl -s "$HEALTH_CHECK_URL" 2>/dev/null || echo "{}")
    running_cameras=$(echo "$health_response" | grep -o '"running_cameras":[0-9]*' | cut -d':' -f2)
    enabled_cameras=$(echo "$health_response" | grep -o '"enabled_cameras":[0-9]*' | cut -d':' -f2)
    
    log_message "Enabled cameras: $enabled_cameras"
    log_message "Running cameras: $running_cameras"
    
    if [ "$running_cameras" != "" ] && [ "$running_cameras" -eq "$enabled_cameras" ]; then
        log_message "✓ All enabled cameras are recording"
    elif [ "$running_cameras" != "" ] && [ "$running_cameras" -gt 0 ]; then
        log_message "⚠ Some cameras recording ($running_cameras/$enabled_cameras)"
    else
        log_message "⚠ No cameras currently recording"
    fi
else
    log_message "✗ Cannot check recording status - health check failed"
fi

# Summary
log_message ""
log_message "=== Test Summary ==="
log_message "Test completed at: $(date)"

if [ -f test_app.log ]; then
    log_message ""
    log_message "=== Application Test Log ==="
    cat test_app.log >> "$LOG_FILE"
    rm -f test_app.log
fi

# Cleanup
rm -f "$TEST_BINARY"

log_message ""
log_message "Test log saved to: $LOG_FILE"
echo -e "${GREEN}Graceful shutdown testing completed!${NC}"
echo -e "${BLUE}Check the log file for detailed results: $LOG_FILE${NC}"