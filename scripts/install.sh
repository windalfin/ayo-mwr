#!/bin/bash

# Ayo-MWR Installation Script with OTA Update Support
# Usage: ./install.sh [OPTIONS]

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
INSTALL_DIR="/opt/ayo-mwr"
USER="ayo-mwr"
GROUP="ayo-mwr"
GITHUB_REPO=""
VERSION="latest"
SERVICE_NAME="ayo-mwr"

# Functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

show_help() {
    cat << EOF
Ayo-MWR Installation Script with OTA Update Support

Usage: $0 [OPTIONS]

Options:
    -d, --dir DIR          Installation directory (default: /opt/ayo-mwr)
    -u, --user USER        Service user (default: ayo-mwr)
    -g, --group GROUP      Service group (default: ayo-mwr)
    -r, --repo REPO        GitHub repository (format: owner/repo)
    -v, --version VERSION  Version to install (default: latest)
    -h, --help             Show this help message

Examples:
    $0 --repo username/ayo-mwr
    $0 --dir /home/user/ayo-mwr --user user --group user
    $0 --repo username/ayo-mwr --version v1.0.0

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -d|--dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
        -u|--user)
            USER="$2"
            shift 2
            ;;
        -g|--group)
            GROUP="$2"
            shift 2
            ;;
        -r|--repo)
            GITHUB_REPO="$2"
            shift 2
            ;;
        -v|--version)
            VERSION="$2"
            shift 2
            ;;
        -h|--help)
            show_help
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

# Validate required parameters
if [[ -z "$GITHUB_REPO" ]]; then
    log_error "GitHub repository is required. Use -r or --repo option."
    show_help
    exit 1
fi

# Check if running as root
if [[ $EUID -ne 0 ]]; then
    log_error "This script must be run as root"
    exit 1
fi

log_info "Starting Ayo-MWR installation..."
log_info "Installation directory: $INSTALL_DIR"
log_info "Service user: $USER"
log_info "Service group: $GROUP"
log_info "GitHub repository: $GITHUB_REPO"
log_info "Version: $VERSION"

# Update system packages
log_info "Updating system packages..."
if command -v apt-get &> /dev/null; then
    apt-get update
    apt-get install -y curl wget jq unzip ca-certificates ffmpeg
elif command -v yum &> /dev/null; then
    yum update -y
    yum install -y curl wget jq unzip ca-certificates ffmpeg
elif command -v dnf &> /dev/null; then
    dnf update -y
    dnf install -y curl wget jq unzip ca-certificates ffmpeg
else
    log_error "Unsupported package manager. Please install curl, wget, jq, unzip, ca-certificates, and ffmpeg manually."
    exit 1
fi

# Create user and group
log_info "Creating user and group..."
if ! getent group "$GROUP" &> /dev/null; then
    groupadd "$GROUP"
    log_success "Created group: $GROUP"
fi

if ! getent passwd "$USER" &> /dev/null; then
    useradd -r -g "$GROUP" -d "$INSTALL_DIR" -s /bin/false "$USER"
    log_success "Created user: $USER"
fi

# Create installation directory
log_info "Creating installation directory..."
mkdir -p "$INSTALL_DIR"
mkdir -p "$INSTALL_DIR"/{recordings,data,backups,logs}

# Download and install binary
log_info "Downloading Ayo-MWR binary..."
if [[ "$VERSION" == "latest" ]]; then
    DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/latest/download/ayo-mwr-linux-amd64.tar.gz"
    CHECKSUM_URL="https://github.com/$GITHUB_REPO/releases/latest/download/ayo-mwr-linux-amd64.tar.gz.sha256"
else
    DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION/ayo-mwr-linux-amd64.tar.gz"
    CHECKSUM_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION/ayo-mwr-linux-amd64.tar.gz.sha256"
fi

# Download files
cd "$INSTALL_DIR"
wget -O ayo-mwr-linux-amd64.tar.gz "$DOWNLOAD_URL"
wget -O ayo-mwr-linux-amd64.tar.gz.sha256 "$CHECKSUM_URL"

# Verify checksum
log_info "Verifying checksum..."
if sha256sum -c ayo-mwr-linux-amd64.tar.gz.sha256; then
    log_success "Checksum verified successfully"
else
    log_error "Checksum verification failed"
    exit 1
fi

# Extract binary
log_info "Extracting binary..."
tar -xzf ayo-mwr-linux-amd64.tar.gz
chmod +x ayo-mwr-linux-amd64

# Clean up downloaded files
rm ayo-mwr-linux-amd64.tar.gz ayo-mwr-linux-amd64.tar.gz.sha256

# Create .env file
log_info "Creating environment configuration..."
cat > "$INSTALL_DIR/.env" << EOF
# GitHub Repository for OTA Updates
GITHUB_REPO=$GITHUB_REPO

# OTA Update Configuration
OTA_UPDATE_ENABLED=true
UPDATE_INTERVAL=6h
BACKUP_DIR=$INSTALL_DIR/backups
BINARY_NAME=ayo-mwr-linux-amd64

# GitHub Personal Access Token (uncomment for private repositories)
# GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

# Application Configuration
STORAGE_PATH=$INSTALL_DIR/recordings
DATABASE_PATH=$INSTALL_DIR/data/videos.db
API_PORT=8080

# RTSP Camera URLs (configure as needed)
RTSP_URL_1=rtsp://admin:password@192.168.1.100:554/stream1
RTSP_URL_2=rtsp://admin:password@192.168.1.101:554/stream1
RTSP_URL_3=rtsp://admin:password@192.168.1.102:554/stream1
RTSP_URL_4=rtsp://admin:password@192.168.1.103:554/stream1

# R2 Storage Configuration (configure as needed)
R2_ACCESS_KEY=your_access_key
R2_SECRET_KEY=your_secret_key
R2_ACCOUNT_ID=your_account_id
R2_BUCKET=your_bucket
R2_REGION=auto
R2_ENDPOINT=your_endpoint
R2_BASE_URL=your_base_url

# AYO API Configuration (configure as needed)
AYO_API_BASE_URL=https://api.ayoconnect.id
AYO_API_KEY=your_api_key
VENUE_CODE=your_venue_code

# Arduino Configuration (configure as needed)
ARDUINO_COM_PORT=/dev/ttyUSB0
ARDUINO_BAUD_RATE=9600

# Other Settings
AUTO_DELETE=true
TZ=UTC
EOF

# Set proper permissions
chown -R "$USER:$GROUP" "$INSTALL_DIR"
chmod 600 "$INSTALL_DIR/.env"

# Create systemd service
log_info "Creating systemd service..."
cat > /etc/systemd/system/$SERVICE_NAME.service << EOF
[Unit]
Description=Ayo-MWR Video Recording Service
Documentation=https://github.com/$GITHUB_REPO
After=network.target
Wants=network.target

[Service]
Type=simple
User=$USER
Group=$GROUP
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/ayo-mwr-linux-amd64
ExecReload=/bin/kill -HUP \$MAINPID
Restart=always
RestartSec=10
LimitNOFILE=65536

# Environment file
EnvironmentFile=$INSTALL_DIR/.env

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=$INSTALL_DIR
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictRealtime=true
RestrictSUIDSGID=true
RemoveIPC=true
LockPersonality=true
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=$SERVICE_NAME

# Process management
KillMode=mixed
KillSignal=SIGTERM
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
EOF

# Enable and start service
systemctl daemon-reload
systemctl enable $SERVICE_NAME

# Create log rotation
log_info "Setting up log rotation..."
cat > /etc/logrotate.d/$SERVICE_NAME << EOF
/var/log/journal/*/$SERVICE_NAME.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    create 0644 $USER $GROUP
}
EOF

# Create update script
log_info "Creating update script..."
cat > "$INSTALL_DIR/update.sh" << 'EOF'
#!/bin/bash

# Manual update script
SERVICE_NAME="ayo-mwr"
PID=$(pgrep -f "ayo-mwr-linux-amd64")

if [[ -n "$PID" ]]; then
    echo "Triggering manual update..."
    kill -USR1 "$PID"
    echo "Update signal sent. Check logs for progress."
else
    echo "Service not running. Starting service..."
    systemctl start $SERVICE_NAME
fi
EOF

chmod +x "$INSTALL_DIR/update.sh"

# Create status script
log_info "Creating status script..."
cat > "$INSTALL_DIR/status.sh" << EOF
#!/bin/bash

echo "=== Ayo-MWR Service Status ==="
systemctl status $SERVICE_NAME

echo -e "\n=== Recent Logs ==="
journalctl -u $SERVICE_NAME -n 20 --no-pager

echo -e "\n=== Update Status ==="
journalctl -u $SERVICE_NAME -n 100 --no-pager | grep -i update | tail -5
EOF

chmod +x "$INSTALL_DIR/status.sh"

# Start service
log_info "Starting service..."
systemctl start $SERVICE_NAME

# Wait for service to start
sleep 5

# Check service status
if systemctl is-active --quiet $SERVICE_NAME; then
    log_success "Service started successfully"
else
    log_error "Service failed to start. Check logs: journalctl -u $SERVICE_NAME"
    exit 1
fi

# Final instructions
log_success "Installation completed successfully!"
echo
echo "=== Next Steps ==="
echo "1. Edit configuration: $INSTALL_DIR/.env"
echo "2. Restart service: systemctl restart $SERVICE_NAME"
echo "3. Check status: $INSTALL_DIR/status.sh"
echo "4. Manual update: $INSTALL_DIR/update.sh"
echo "5. View logs: journalctl -u $SERVICE_NAME -f"
echo
echo "=== Service Commands ==="
echo "Start:   systemctl start $SERVICE_NAME"
echo "Stop:    systemctl stop $SERVICE_NAME"
echo "Restart: systemctl restart $SERVICE_NAME"
echo "Status:  systemctl status $SERVICE_NAME"
echo "Logs:    journalctl -u $SERVICE_NAME -f"
echo
echo "=== OTA Update ==="
echo "Automatic updates will check every 6 hours"
echo "Manual update: $INSTALL_DIR/update.sh"
echo "Signal update: kill -USR1 \$(pgrep -f ayo-mwr-linux-amd64)"
echo
echo "Application should be running at: http://localhost:8080"
echo "Dashboard: http://localhost:8080/dashboard"
echo
log_success "Installation completed!" 