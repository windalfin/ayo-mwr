#!/bin/bash

# autorun.sh - Setup and run AYO MWR application as a systemd service
# This script will build the application binary and create a systemd service
# that will automatically start on boot and restart if it crashes.

set -e  # Exit on any error

# Configuration
APP_NAME="ayo-mwr"
SERVICE_NAME="ayo-mwr"
TIMER_NAME="restart-ayo-mwr"
USER=$(whoami)
WORK_DIR=$(pwd)
BINARY_PATH="$WORK_DIR/$APP_NAME"
SERVICE_FILE="/etc/systemd/system/$SERVICE_NAME.service"
TIMER_SERVICE_FILE="/etc/systemd/system/$TIMER_NAME.service"
TIMER_FILE="/etc/systemd/system/$TIMER_NAME.timer"

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
echo -e "${BLUE}3. Create a daily restart timer (midnight)${NC}"
echo -e "${BLUE}4. Enable auto-start on boot${NC}"
echo -e "${BLUE}5. Start the service${NC}"
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
ExecStop=/bin/kill -TERM \$MAINPID
Restart=always
RestartSec=5
TimeoutStopSec=45
KillMode=mixed
KillSignal=SIGTERM
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
DISABLE_TIMER=false
for arg in "$@"; do
    case $arg in
        --update-service|--update-service=true)
            FORCE_UPDATE=true
            ;;
        --disable-timer|--disable-timer=true)
            DISABLE_TIMER=true
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

echo -e "${GREEN}Step 3: Creating daily restart timer...${NC}"

if [ "$DISABLE_TIMER" = false ]; then
    # Check if zero-downtime deployment script exists
    ZERO_DOWNTIME_SCRIPT="$WORK_DIR/zero-downtime-deploy.sh"
    if [ ! -f "$ZERO_DOWNTIME_SCRIPT" ]; then
        echo -e "${YELLOW}Warning: zero-downtime-deploy.sh not found at $ZERO_DOWNTIME_SCRIPT${NC}"
        echo -e "${YELLOW}Timer will use regular systemctl restart instead${NC}"
        TIMER_EXEC_START="/bin/systemctl restart $SERVICE_NAME.service"
    else
        # Make sure the script is executable
        chmod +x "$ZERO_DOWNTIME_SCRIPT"
        TIMER_EXEC_START="$ZERO_DOWNTIME_SCRIPT"
        echo -e "${GREEN}✓ Using zero-downtime deployment script for timer${NC}"
    fi
    # Create timer service content
    TIMER_SERVICE_CONTENT="[Unit]
Description=Zero-Downtime Restart AYO MWR Video Recording Service
After=$SERVICE_NAME.service

[Service]
Type=oneshot
User=$USER
WorkingDirectory=$WORK_DIR
ExecStart=$TIMER_EXEC_START
TimeoutStartSec=300
StandardOutput=journal
StandardError=journal"

    # Create timer content
    TIMER_CONTENT="[Unit]
Description=Zero-Downtime Restart AYO MWR Video Recording Service daily at midnight
Requires=$SERVICE_NAME.service

[Timer]
OnCalendar=*-*-* 00:00:00
Persistent=true

[Install]
WantedBy=timers.target"

    # Check if timer files need updating
    TIMER_NEED_UPDATE=false
    if [ "$FORCE_UPDATE" = true ]; then
        TIMER_NEED_UPDATE=true
    elif [ ! -f "$TIMER_SERVICE_FILE" ] || [ ! -f "$TIMER_FILE" ]; then
        TIMER_NEED_UPDATE=true
    elif ! diff -q <(echo "$TIMER_SERVICE_CONTENT") "$TIMER_SERVICE_FILE" > /dev/null || ! diff -q <(echo "$TIMER_CONTENT") "$TIMER_FILE" > /dev/null; then
        TIMER_NEED_UPDATE=true
    fi

    if [ "$TIMER_NEED_UPDATE" = true ]; then
        echo "Creating or updating timer service file: $TIMER_SERVICE_FILE"
        echo "$TIMER_SERVICE_CONTENT" | sudo tee "$TIMER_SERVICE_FILE" > /dev/null
        
        echo "Creating or updating timer file: $TIMER_FILE"
        echo "$TIMER_CONTENT" | sudo tee "$TIMER_FILE" > /dev/null
        
        echo -e "${GREEN}✓ Timer files created/updated${NC}"
    else
        echo -e "${YELLOW}Timer files already up to date, skipping creation.${NC}"
    fi
else
    echo -e "${YELLOW}Timer creation disabled by --disable-timer flag${NC}"
fi

echo -e "${GREEN}Step 4: Configuring systemd service...${NC}"

# Reload systemd to pick up the new service and timer
sudo systemctl daemon-reload

# Enable the service to start on boot
sudo systemctl enable "$SERVICE_NAME"

# Enable and start the timer if not disabled
if [ "$DISABLE_TIMER" = false ]; then
    sudo systemctl enable "$TIMER_NAME.timer"
    sudo systemctl start "$TIMER_NAME.timer"
    echo -e "${GREEN}✓ Daily restart timer enabled (midnight)${NC}"
fi

echo -e "${GREEN}✓ Service enabled for auto-start on boot${NC}"

echo -e "${GREEN}Step 5: Starting the service...${NC}"

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
if [ "$DISABLE_TIMER" = false ]; then
    echo -e "${GREEN}The service will automatically restart every day at midnight.${NC}"
fi
echo ""
echo -e "${BLUE}Useful commands:${NC}"
echo -e "${BLUE}• Check status:${NC} sudo systemctl status $SERVICE_NAME"
echo -e "${BLUE}• View logs:${NC} sudo journalctl -u $SERVICE_NAME -f"
echo -e "${BLUE}• Stop service:${NC} sudo systemctl stop $SERVICE_NAME"
echo -e "${BLUE}• Start service:${NC} sudo systemctl start $SERVICE_NAME"
echo -e "${BLUE}• Restart service:${NC} sudo systemctl restart $SERVICE_NAME"
echo -e "${BLUE}• Disable auto-start:${NC} sudo systemctl disable $SERVICE_NAME"

if [ "$DISABLE_TIMER" = false ]; then
    echo ""
    echo -e "${BLUE}Timer commands:${NC}"
    echo -e "${BLUE}• Check timer status:${NC} sudo systemctl status $TIMER_NAME.timer"
    echo -e "${BLUE}• List all timers:${NC} sudo systemctl list-timers $TIMER_NAME.timer"
    echo -e "${BLUE}• Disable timer:${NC} sudo systemctl disable $TIMER_NAME.timer"
    echo -e "${BLUE}• View timer logs:${NC} sudo journalctl -u $TIMER_NAME.service -f"
fi

echo ""
echo -e "${GREEN}The service will automatically start when you reboot your PC.${NC}"

# Show current status
echo -e "${BLUE}Current service status:${NC}"
sudo systemctl status "$SERVICE_NAME" --no-pager -l

if [ "$DISABLE_TIMER" = false ]; then
    echo ""
    echo -e "${BLUE}Timer status:${NC}"
    sudo systemctl list-timers "$TIMER_NAME.timer" --no-pager
fi
