# Deployment Guide - Native Binary

Panduan lengkap deployment aplikasi Ayo-MWR menggunakan native binary tanpa Docker.

## 🚀 Production Deployment

### 1. Automatic Installation

```bash
# Download install script
wget https://raw.githubusercontent.com/username/ayo-mwr/main/scripts/install.sh
chmod +x install.sh

# Run installation dengan root privileges
sudo ./install.sh --repo username/ayo-mwr
```

**Install script akan:**
- Membuat user dan group `ayo-mwr`
- Download binary dari GitHub Releases
- Setup direktori `/opt/ayo-mwr`
- Membuat systemd service
- Konfigurasi environment variables di `.env`

### 2. Manual Installation

#### Create User dan Directories

```bash
# Create system user
sudo useradd -r -s /bin/false ayo-mwr

# Create directories
sudo mkdir -p /opt/ayo-mwr/{recordings,data,backups,logs}
sudo chown -R ayo-mwr:ayo-mwr /opt/ayo-mwr
```

#### Download Binary

```bash
# Navigate to installation directory
cd /opt/ayo-mwr

# Download latest release
wget https://github.com/username/ayo-mwr/releases/latest/download/ayo-mwr-linux-amd64.tar.gz
wget https://github.com/username/ayo-mwr/releases/latest/download/ayo-mwr-linux-amd64.tar.gz.sha256

# Verify checksum
sha256sum -c ayo-mwr-linux-amd64.tar.gz.sha256

# Extract binary
tar -xzf ayo-mwr-linux-amd64.tar.gz
chmod +x ayo-mwr-linux-amd64
```

#### Setup Environment

```bash
# Create .env file
sudo cat > /opt/ayo-mwr/.env << EOF
# OTA Update Configuration
OTA_UPDATE_ENABLED=true
GITHUB_REPO=username/ayo-mwr
UPDATE_INTERVAL=6h
BACKUP_DIR=/opt/ayo-mwr/backups
BINARY_NAME=ayo-mwr-linux-amd64

# Application Configuration
STORAGE_PATH=/opt/ayo-mwr/recordings
DATABASE_PATH=/opt/ayo-mwr/data/videos.db
API_PORT=8080

# Camera Configuration
RTSP_URL_1=rtsp://admin:password@192.168.1.100:554/stream1
RTSP_URL_2=rtsp://admin:password@192.168.1.101:554/stream1

# R2 Storage Configuration
R2_ACCESS_KEY=your_access_key
R2_SECRET_KEY=your_secret_key
R2_ACCOUNT_ID=your_account_id
R2_BUCKET=your_bucket
R2_REGION=auto
R2_ENDPOINT=your_endpoint
R2_BASE_URL=your_base_url

# AYO API Configuration
AYO_API_BASE_URL=https://api.ayoconnect.id
AYO_API_KEY=your_api_key
VENUE_CODE=your_venue_code

# Arduino Configuration
ARDUINO_COM_PORT=/dev/ttyUSB0
ARDUINO_BAUD_RATE=9600

# Other Settings
AUTO_DELETE=true
TZ=UTC
EOF

# Set permissions
sudo chmod 600 /opt/ayo-mwr/.env
sudo chown ayo-mwr:ayo-mwr /opt/ayo-mwr/.env
```

#### Setup Systemd Service

```bash
# Create systemd service file
sudo cat > /etc/systemd/system/ayo-mwr.service << EOF
[Unit]
Description=Ayo-MWR Video Recording Service
Documentation=https://github.com/username/ayo-mwr
After=network.target
Wants=network.target

[Service]
Type=simple
User=ayo-mwr
Group=ayo-mwr
WorkingDirectory=/opt/ayo-mwr
ExecStart=/opt/ayo-mwr/ayo-mwr-linux-amd64
ExecReload=/bin/kill -HUP \$MAINPID
Restart=always
RestartSec=10
LimitNOFILE=65536

# Environment file
EnvironmentFile=/opt/ayo-mwr/.env

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/opt/ayo-mwr
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
SyslogIdentifier=ayo-mwr

# Process management
KillMode=mixed
KillSignal=SIGTERM
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
EOF

# Reload systemd dan enable service
sudo systemctl daemon-reload
sudo systemctl enable ayo-mwr
sudo systemctl start ayo-mwr
```

## 🔧 Configuration Management

### 1. Environment Variables

Environment variables dikonfigurasi di file `/opt/ayo-mwr/.env`:

```bash
# Edit configuration
sudo nano /opt/ayo-mwr/.env

# Restart service setelah perubahan
sudo systemctl restart ayo-mwr
```

### 2. Enable/Disable OTA Updates

```bash
# Enable OTA updates
sudo sed -i 's/OTA_UPDATE_ENABLED=false/OTA_UPDATE_ENABLED=true/' /opt/ayo-mwr/.env

# Disable OTA updates
sudo sed -i 's/OTA_UPDATE_ENABLED=true/OTA_UPDATE_ENABLED=false/' /opt/ayo-mwr/.env

# Restart service
sudo systemctl restart ayo-mwr
```

### 3. Private Repository Configuration

```bash
# Add GitHub token untuk private repository
echo "GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" | sudo tee -a /opt/ayo-mwr/.env

# Restart service
sudo systemctl restart ayo-mwr
```

## 📊 Monitoring dan Management

### 1. Service Status

```bash
# Check service status
sudo systemctl status ayo-mwr

# View logs
sudo journalctl -u ayo-mwr -f

# View recent logs
sudo journalctl -u ayo-mwr -n 50
```

### 2. OTA Update Status

```bash
# Check OTA update logs
sudo journalctl -u ayo-mwr | grep -i "ota\|update"

# Manual update trigger
sudo kill -USR1 $(pgrep -f ayo-mwr-linux-amd64)
```

### 3. Application Health

```bash
# Check if application is running
curl -f http://localhost:8080/health

# Check port
sudo netstat -tulpn | grep :8080

# Check process
ps aux | grep ayo-mwr
```

## 🔄 Update Process

### 1. Automatic Updates (OTA)

OTA updates berjalan otomatis setiap 6 jam jika dikonfigurasi:

```bash
# Check OTA status
sudo journalctl -u ayo-mwr | grep -i "updater initialized"

# Force manual update
sudo kill -USR1 $(pgrep -f ayo-mwr-linux-amd64)
```

### 2. Manual Updates

```bash
# Stop service
sudo systemctl stop ayo-mwr

# Backup current binary
sudo cp /opt/ayo-mwr/ayo-mwr-linux-amd64 /opt/ayo-mwr/backups/ayo-mwr-linux-amd64.bak.$(date +%s)

# Download new version
cd /opt/ayo-mwr
sudo wget https://github.com/username/ayo-mwr/releases/latest/download/ayo-mwr-linux-amd64.tar.gz
sudo tar -xzf ayo-mwr-linux-amd64.tar.gz
sudo chmod +x ayo-mwr-linux-amd64
sudo chown ayo-mwr:ayo-mwr ayo-mwr-linux-amd64

# Start service
sudo systemctl start ayo-mwr
```

## 🛡️ Security

### 1. File Permissions

```bash
# Set proper permissions
sudo chmod 755 /opt/ayo-mwr/ayo-mwr-linux-amd64
sudo chmod 600 /opt/ayo-mwr/.env
sudo chmod 700 /opt/ayo-mwr/backups
sudo chown -R ayo-mwr:ayo-mwr /opt/ayo-mwr
```

### 2. Firewall Configuration

```bash
# Allow application port
sudo ufw allow 8080/tcp

# Allow from specific network (optional)
sudo ufw allow from 192.168.1.0/24 to any port 8080
```

### 3. Log Rotation

```bash
# Create logrotate configuration
sudo cat > /etc/logrotate.d/ayo-mwr << EOF
/var/log/journal/*/ayo-mwr.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    create 0644 ayo-mwr ayo-mwr
}
EOF
```

## 🚨 Troubleshooting

### 1. Service Issues

```bash
# Service won't start
sudo systemctl status ayo-mwr
sudo journalctl -u ayo-mwr -n 50

# Check binary permissions
ls -la /opt/ayo-mwr/ayo-mwr-linux-amd64

# Check .env file
sudo cat /opt/ayo-mwr/.env
```

### 2. OTA Update Issues

```bash
# Check GitHub connectivity
curl -I https://api.github.com/repos/username/ayo-mwr/releases/latest

# Check token (untuk private repo)
curl -H "Authorization: token YOUR_TOKEN" https://api.github.com/user

# Check update logs
sudo journalctl -u ayo-mwr | grep -i "failed\|error" | tail -10
```

### 3. Application Issues

```bash
# Check port binding
sudo ss -tulpn | grep :8080

# Check disk space
df -h /opt/ayo-mwr

# Check memory usage
free -h
```

## 📋 Maintenance

### 1. Regular Tasks

```bash
# Weekly: Check service status
sudo systemctl status ayo-mwr

# Monthly: Clean old backups
sudo find /opt/ayo-mwr/backups -name "*.bak.*" -mtime +30 -delete

# Monthly: Check disk usage
df -h /opt/ayo-mwr
```

### 2. Backup Strategy

```bash
# Backup configuration
sudo cp /opt/ayo-mwr/.env /backup/ayo-mwr-config-$(date +%Y%m%d).env

# Backup database
sudo cp /opt/ayo-mwr/data/videos.db /backup/ayo-mwr-db-$(date +%Y%m%d).db
```

---

**Note**: Deployment ini menggunakan native binary tanpa containerization untuk performa optimal dan akses hardware yang mudah. 