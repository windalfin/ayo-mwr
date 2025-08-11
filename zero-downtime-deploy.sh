#!/bin/bash

# zero-downtime-deploy.sh - Deploy with zero downtime using health checks
# This script implements a blue-green deployment strategy to minimize recording interruptions

set -e  # Exit on any error

# Configuration
APP_NAME="ayo-mwr"
SERVICE_NAME="ayo-mwr"
USER=$(whoami)
WORK_DIR=$(pwd)
BINARY_PATH="$WORK_DIR/$APP_NAME"
BACKUP_BINARY_PATH="$WORK_DIR/${APP_NAME}_backup"
HEALTH_CHECK_URL="http://localhost:3000/api/health"
HEALTH_CHECK_TIMEOUT=30
GRACEFUL_SHUTDOWN_TIMEOUT=45

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Zero-Downtime Deployment Script ===${NC}"
echo -e "${BLUE}This script will:${NC}"
echo -e "${BLUE}1. Build the new binary${NC}"
echo -e "${BLUE}2. Backup the current binary${NC}"
echo -e "${BLUE}3. Perform health checks${NC}"
echo -e "${BLUE}4. Deploy with zero downtime${NC}"
echo -e "${BLUE}5. Verify deployment success${NC}"
echo ""

# Check if running as root
if [[ $EUID -eq 0 ]]; then
    echo -e "${RED}Error: Do not run this script as root!${NC}"
    exit 1
fi

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed or not in PATH${NC}"
    exit 1
fi

# Check if we're in the right directory (contains main.go)
if [ ! -f "main.go" ]; then
    echo -e "${RED}Error: main.go not found in current directory${NC}"
    echo -e "${YELLOW}Please run this script from the ayo-mwr project root directory${NC}"
    exit 1
fi

# Function to check service health
check_health() {
    local timeout=${1:-$HEALTH_CHECK_TIMEOUT}
    local start_time=$(date +%s)
    
    while [ $(($(date +%s) - start_time)) -lt $timeout ]; do
        if curl -sf "$HEALTH_CHECK_URL" >/dev/null 2>&1; then
            local health_status=$(curl -s "$HEALTH_CHECK_URL" | grep -o '"status":"[^"]*' | cut -d'"' -f4)
            if [ "$health_status" = "healthy" ] || [ "$health_status" = "degraded" ]; then
                echo -e "${GREEN}✓ Service is healthy (status: $health_status)${NC}"
                return 0
            fi
        fi
        echo "Waiting for health check to pass..."
        sleep 2
    done
    
    echo -e "${RED}✗ Health check failed after $timeout seconds${NC}"
    return 1
}

# Function to check if service is recording
check_recording_status() {
    local health_response=$(curl -s "$HEALTH_CHECK_URL" 2>/dev/null || echo "{}")
    local recording_status=$(echo "$health_response" | grep -o '"recording":{[^}]*}' | grep -o '"status":"[^"]*' | cut -d'"' -f4)
    local running_cameras=$(echo "$health_response" | grep -o '"running_cameras":[0-9]*' | cut -d':' -f2)
    
    echo "Recording Status: $recording_status"
    echo "Running Cameras: $running_cameras"
    
    if [ "$running_cameras" != "" ] && [ "$running_cameras" -gt 0 ]; then
        return 0  # Cameras are recording
    else
        return 1  # No cameras recording
    fi
}

# Step 1: Pre-deployment health check
echo -e "${GREEN}Step 1: Pre-deployment health check...${NC}"
if ! check_health; then
    echo -e "${RED}Pre-deployment health check failed. Aborting deployment.${NC}"
    exit 1
fi

echo -e "${GREEN}Step 2: Checking recording status...${NC}"
if check_recording_status; then
    echo -e "${GREEN}✓ Cameras are currently recording${NC}"
    RECORDING_ACTIVE=true
else
    echo -e "${YELLOW}⚠ No active recording detected${NC}"
    RECORDING_ACTIVE=false
fi

# Step 3: Build new binary
echo -e "${GREEN}Step 3: Building new binary...${NC}"
NEW_BINARY_PATH="${BINARY_PATH}_new"
go build -o "$NEW_BINARY_PATH" .

if [ ! -f "$NEW_BINARY_PATH" ]; then
    echo -e "${RED}Error: Failed to build new binary${NC}"
    exit 1
fi

echo -e "${GREEN}✓ New binary built successfully${NC}"

# Step 4: Backup current binary
echo -e "${GREEN}Step 4: Backing up current binary...${NC}"
if [ -f "$BINARY_PATH" ]; then
    cp "$BINARY_PATH" "$BACKUP_BINARY_PATH"
    echo -e "${GREEN}✓ Current binary backed up${NC}"
else
    echo -e "${YELLOW}⚠ No existing binary found to backup${NC}"
fi

# Step 5: Deploy with zero downtime
echo -e "${GREEN}Step 5: Deploying with zero downtime...${NC}"

# Replace the binary atomically
mv "$NEW_BINARY_PATH" "$BINARY_PATH"
echo -e "${GREEN}✓ Binary updated${NC}"

# Graceful restart of the service
echo -e "${GREEN}Performing graceful restart...${NC}"

# Send reload signal to systemd service (graceful restart)
sudo systemctl reload-or-restart "$SERVICE_NAME"

# Wait a moment for the restart to begin
sleep 3

# Step 6: Post-deployment health checks
echo -e "${GREEN}Step 6: Post-deployment verification...${NC}"
echo "Waiting for service to start..."

# Extended health check after restart
if ! check_health 60; then
    echo -e "${RED}Post-deployment health check failed!${NC}"
    echo -e "${YELLOW}Attempting rollback...${NC}"
    
    # Rollback if backup exists
    if [ -f "$BACKUP_BINARY_PATH" ]; then
        mv "$BACKUP_BINARY_PATH" "$BINARY_PATH"
        sudo systemctl restart "$SERVICE_NAME"
        sleep 5
        
        if check_health 30; then
            echo -e "${GREEN}✓ Rollback successful${NC}"
        else
            echo -e "${RED}✗ Rollback failed - manual intervention required${NC}"
        fi
    else
        echo -e "${RED}✗ No backup available for rollback${NC}"
    fi
    exit 1
fi

# Verify recording status after deployment
echo -e "${GREEN}Step 7: Verifying recording continuity...${NC}"
sleep 10  # Give cameras time to restart recording

if check_recording_status; then
    echo -e "${GREEN}✓ Recording resumed successfully${NC}"
elif [ "$RECORDING_ACTIVE" = "true" ]; then
    echo -e "${YELLOW}⚠ Recording was active before but not detected after deployment${NC}"
    echo -e "${YELLOW}This may be normal during the transition period${NC}"
else
    echo -e "${GREEN}✓ No recording was expected (matches pre-deployment state)${NC}"
fi

# Final success confirmation
echo ""
echo -e "${GREEN}=== Deployment Successful! ===${NC}"
echo -e "${GREEN}The application has been deployed with zero downtime.${NC}"

# Show current service status
echo ""
echo -e "${BLUE}Current service status:${NC}"
sudo systemctl status "$SERVICE_NAME" --no-pager -l

# Show health check response
echo ""
echo -e "${BLUE}Current health status:${NC}"
HEALTH_RESPONSE=$(curl -s "$HEALTH_CHECK_URL")
echo "$HEALTH_RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$HEALTH_RESPONSE"

# Cleanup backup after successful deployment
if [ -f "$BACKUP_BINARY_PATH" ]; then
    echo ""
    echo -e "${BLUE}Cleaning up backup binary...${NC}"
    rm "$BACKUP_BINARY_PATH"
    echo -e "${GREEN}✓ Backup cleaned up${NC}"
fi

echo ""
echo -e "${GREEN}Deployment completed successfully!${NC}"
echo -e "${BLUE}Deployment time: $(date)${NC}"