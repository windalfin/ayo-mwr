#!/bin/bash

# autorun.sh - Setup and run AYO MWR application as a systemd service
# This script will build the application binary and create a systemd service
# that will automatically start on boot and restart if it crashes.

set -e  # Exit on any error

# Configuration
APP_NAME="ayo-mwr"
SERVICE_NAME="ayo-mwr"
USER=$(whoami)
WORK_DIR=$(pwd)
BINARY_PATH="$WORK_DIR/$APP_NAME"
SERVICE_FILE="/etc/systemd/system/$SERVICE_NAME.service"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== AYO MWR Auto Setup Script ===${NC}"
echo -e "${BLUE}This script will:${NC}"
echo -e "${BLUE}1. Build the Go application${NC}"
echo -e "${BLUE}2. Create a systemd service${NC}"
echo -e "${BLUE}3. Enable auto-start on boot${NC}"
echo -e "${BLUE}4. Start the service${NC}"
echo ""

# Check if running as root for systemd operations
if [[ $EUID -eq 0 ]]; then
    echo -e "${RED}Error: Do not run this script as root!${NC}"
    echo -e "${YELLOW}This script will use sudo when needed for systemd operations.${NC}"
    exit 1
fi

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed or not in PATH${NC}"
    echo -e "${YELLOW}Please install Go first: https://golang.org/doc/install${NC}"
    exit 1
fi

# Check if we're in the right directory (contains main.go)
if [ ! -f "main.go" ]; then
    echo -e "${RED}Error: main.go not found in current directory${NC}"
    echo -e "${YELLOW}Please run this script from the ayo-mwr project root directory${NC}"
    exit 1
fi

# Check if .env file exists
if [ ! -f ".env" ]; then
    echo -e "${YELLOW}Warning: .env file not found${NC}"
    echo -e "${YELLOW}Make sure to create .env file with required environment variables${NC}"
    echo -e "${YELLOW}You can use env.template as a reference${NC}"
    read -p "Continue anyway? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

echo -e "${GREEN}Step 1: Building the application...${NC}"
# Build the Go application
echo "Building binary: $BINARY_PATH"
go build -o "$BINARY_PATH" .

if [ ! -f "$BINARY_PATH" ]; then
    echo -e "${RED}Error: Failed to build binary${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Binary built successfully${NC}"

echo -e "${GREEN}Step 2: Creating systemd service...${NC}"


# Create systemd service file content
SERVICE_CONTENT="[Unit]
Description=AYO MWR Video Recording Service
After=network.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=$USER
Group=$USER
WorkingDirectory=$WORK_DIR
ExecStart=$BINARY_PATH
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=$SERVICE_NAME

# Environment variables (if needed)
Environment=PATH=/usr/local/bin:/usr/bin:/bin
# Add other environment variables here if needed

# Security settings
NoNewPrivileges=true
PrivateTmp=true

# Allow read/write access to project dir, /mnt, /media, and any other mount points as needed
ProtectSystem=strict
ReadWritePaths=$WORK_DIR /mnt /media
# To allow access to other drives, add their mount points here (e.g., /run/media/$USER)

[Install]
WantedBy=multi-user.target"

# Parse arguments
FORCE_UPDATE=false
for arg in "$@"; do
    case $arg in
        --update-service|--update-service=true)
            FORCE_UPDATE=true
            ;;
    esac
done

# Only create/update the service file if it does not exist, content has changed, or --update-service is passed
NEED_UPDATE=false
if [ "$FORCE_UPDATE" = true ]; then
    NEED_UPDATE=true
elif [ ! -f "$SERVICE_FILE" ]; then
    NEED_UPDATE=true
elif ! diff -q <(echo "$SERVICE_CONTENT") "$SERVICE_FILE" > /dev/null; then
    NEED_UPDATE=true
fi

if [ "$NEED_UPDATE" = true ]; then
    echo "Creating or updating service file: $SERVICE_FILE"
    echo "$SERVICE_CONTENT" | sudo tee "$SERVICE_FILE" > /dev/null
    echo -e "${GREEN}✓ Service file created/updated${NC}"
else
    echo -e "${YELLOW}Service file already up to date, skipping creation.${NC}"
fi

echo -e "${GREEN}Step 3: Configuring systemd service...${NC}"

# Reload systemd to pick up the new service
sudo systemctl daemon-reload

# Enable the service to start on boot
sudo systemctl enable "$SERVICE_NAME"

echo -e "${GREEN}✓ Service enabled for auto-start on boot${NC}"

echo -e "${GREEN}Step 4: Starting the service...${NC}"

# Stop the service if it's already running
if systemctl is-active --quiet "$SERVICE_NAME"; then
    echo "Stopping existing service..."
    sudo systemctl stop "$SERVICE_NAME"
fi

# Start the service
sudo systemctl start "$SERVICE_NAME"

# Wait a moment and check status
sleep 2

if systemctl is-active --quiet "$SERVICE_NAME"; then
    echo -e "${GREEN}✓ Service started successfully${NC}"
else
    echo -e "${RED}✗ Service failed to start${NC}"
    echo -e "${YELLOW}Checking service status...${NC}"
    sudo systemctl status "$SERVICE_NAME" --no-pager
    exit 1
fi

echo ""
echo -e "${GREEN}=== Setup Complete! ===${NC}"
echo -e "${GREEN}Your AYO MWR application is now running as a systemd service.${NC}"
echo ""
echo -e "${BLUE}Useful commands:${NC}"
echo -e "${BLUE}• Check status:${NC} sudo systemctl status $SERVICE_NAME"
echo -e "${BLUE}• View logs:${NC} sudo journalctl -u $SERVICE_NAME -f"
echo -e "${BLUE}• Stop service:${NC} sudo systemctl stop $SERVICE_NAME"
echo -e "${BLUE}• Start service:${NC} sudo systemctl start $SERVICE_NAME"
echo -e "${BLUE}• Restart service:${NC} sudo systemctl restart $SERVICE_NAME"
echo -e "${BLUE}• Disable auto-start:${NC} sudo systemctl disable $SERVICE_NAME"
echo ""
echo -e "${GREEN}The service will automatically start when you reboot your PC.${NC}"

# Show current status
echo -e "${BLUE}Current service status:${NC}"
sudo systemctl status "$SERVICE_NAME" --no-pager -l