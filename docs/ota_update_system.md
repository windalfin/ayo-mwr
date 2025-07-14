# OTA Update System

Sistem OTA (Over-The-Air) Update untuk aplikasi Ayo-MWR yang memungkinkan update otomatis aplikasi dari GitHub Releases.

## Fitur Utama

- ✅ **Automatic Updates**: Cek versi terbaru secara otomatis
- ✅ **Manual Updates**: Trigger update manual via signal
- ✅ **Checksum Verification**: Verifikasi integritas file download
- ✅ **Backup System**: Backup otomatis versi sebelumnya
- ✅ **Graceful Restart**: Restart aplikasi setelah update
- ✅ **Cleanup Management**: Cleanup backup files lama
- ✅ **GitHub Integration**: Integrasi dengan GitHub Releases

## Instalasi dan Konfigurasi

### 1. Environment Variables

Tambahkan environment variables berikut ke file `.env`:

```env
# Enable/Disable OTA Updates (true/false)
OTA_UPDATE_ENABLED=true

# GitHub Repository (format: owner/repo)
GITHUB_REPO=username/ayo-mwr

# GitHub Personal Access Token (required for private repositories)
# GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

# Update interval (optional, default: 6h)
UPDATE_INTERVAL=6h

# Backup directory (optional, default: ./backups)
BACKUP_DIR=./backups

# Binary name (optional, default: ayo-mwr-linux-amd64)
BINARY_NAME=ayo-mwr-linux-amd64
```

**Konfigurasi Detail:**
- `OTA_UPDATE_ENABLED`: Mengaktifkan/menonaktifkan sistem OTA update
- `GITHUB_REPO`: Repository GitHub dalam format `owner/repo`
- `GITHUB_TOKEN`: Token akses untuk private repository (optional untuk public repo)
- `UPDATE_INTERVAL`: Interval pengecekan update (30m, 1h, 6h, 12h, 24h)
- `BACKUP_DIR`: Directory untuk menyimpan backup binary lama
- `BINARY_NAME`: Nama file binary untuk update

### 2. Build dengan Version Info

Update perintah build di GitHub Actions atau manual build:

```bash
# Build dengan version info
go build -ldflags="-s -w -X main.version=v1.0.0 -X main.gitCommit=$(git rev-parse HEAD) -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o ayo-mwr-linux-amd64 .
```

## Cara Penggunaan

### 1. Enable/Disable OTA Updates

#### Mengaktifkan OTA Updates

```bash
# Set di environment file
echo "OTA_UPDATE_ENABLED=true" >> .env

# Atau set environment variable
export OTA_UPDATE_ENABLED=true

# Restart aplikasi
systemctl restart ayo-mwr
```

#### Menonaktifkan OTA Updates

```bash
# Set di environment file
sed -i 's/OTA_UPDATE_ENABLED=true/OTA_UPDATE_ENABLED=false/' .env

# Atau set environment variable
export OTA_UPDATE_ENABLED=false

# Restart aplikasi
systemctl restart ayo-mwr
```

#### Status OTA Updates

```bash
# Cek status OTA update dari logs
journalctl -u ayo-mwr | grep -i "ota\|update"

# Expected outputs:
# - "OTA Updater initialized - Current version: v1.0.0" (enabled)
# - "OTA updates disabled by configuration" (disabled)
# - "GITHUB_REPO not configured, OTA updates disabled" (not configured)
```

### 2. Automatic Updates

Update otomatis akan berjalan setiap 6 jam (default):

```go
// Sudah terintegrasi di main.go
// Tidak perlu konfigurasi tambahan
```

### 3. Manual Updates

#### Via Signal (Recommended)

```bash
# Cari PID aplikasi
ps aux | grep ayo-mwr

# Kirim signal SIGUSR1 untuk trigger update
kill -USR1 <PID>
```

#### Via API (Jika ada endpoint)

```bash
# Contoh endpoint untuk trigger update
curl -X POST http://localhost:8080/api/update
```

### 4. Cek Status Update

```bash
# Cek log untuk melihat status update
tail -f server.log | grep -i update
```

## Struktur File

```
ayo-mwr/
├── .github/workflows/
│   └── release.yml              # GitHub Actions workflow
├── updater.go                   # OTA update implementation
├── main.go                      # Main application dengan OTA integration
├── examples/
│   └── ota_example.go          # Contoh penggunaan
├── docs/
│   └── ota_update_system.md    # Dokumentasi ini
└── backups/                    # Directory untuk backup files
    ├── ayo-mwr-linux-amd64.bak.1672531200
    └── ayo-mwr-linux-amd64.bak.1672531300
```

## Workflow Update

### 1. Proses Update Otomatis

```mermaid
graph TD
    A[Check Update Timer] --> B[Query GitHub API]
    B --> C{New Version Available?}
    C -->|Yes| D[Download Update Package]
    C -->|No| E[Continue Running]
    D --> F[Verify Checksum]
    F --> G[Extract Package]
    G --> H[Backup Current Binary]
    H --> I[Replace Binary]
    I --> J[Restart Application]
    J --> K[Cleanup Old Backups]
```

### 2. Proses Manual Update

```mermaid
graph TD
    A[Receive SIGUSR1] --> B[Force Update Check]
    B --> C[Follow Same Process]
    C --> D[Complete Update]
```

## API Reference

### UpdaterConfig

```go
type UpdaterConfig struct {
    GitHubRepo     string        // Format: "owner/repo"
    CurrentVersion string        // Current version (e.g., "v1.0.0")
    UpdateInterval time.Duration // Check interval (e.g., 6*time.Hour)
    BackupDir      string        // Backup directory path
    BinaryName     string        // Binary filename
}
```

### Key Methods

#### NewUpdater
```go
func NewUpdater(config *UpdaterConfig) *Updater
```
Membuat instance updater baru.

#### CheckForUpdates
```go
func (u *Updater) CheckForUpdates() (*Release, error)
```
Mengecek versi terbaru dari GitHub.

#### ForceUpdate
```go
func (u *Updater) ForceUpdate() error
```
Melakukan update secara manual.

#### StartUpdateChecker
```go
func (u *Updater) StartUpdateChecker()
```
Memulai automatic update checker.

#### CleanupOldBackups
```go
func (u *Updater) CleanupOldBackups(maxBackups int) error
```
Membersihkan backup files lama.

## Troubleshooting

### 1. Update Gagal

```bash
# Cek log untuk error details
tail -f server.log | grep -i error

# Cek koneksi ke GitHub
curl -I https://api.github.com/repos/username/ayo-mwr/releases/latest
```

### 2. Checksum Mismatch

```bash
# Verifikasi manual checksum
sha256sum ayo-mwr-linux-amd64.tar.gz
cat ayo-mwr-linux-amd64.tar.gz.sha256
```

### 3. Backup Restore

```bash
# Restore dari backup jika diperlukan
cp backups/ayo-mwr-linux-amd64.bak.1672531200 ayo-mwr-linux-amd64
chmod +x ayo-mwr-linux-amd64
```

## Best Practices

### 1. Production Setup

```bash
# Gunakan systemd service
sudo systemctl enable ayo-mwr
sudo systemctl start ayo-mwr

# Setup log rotation
sudo logrotate -f /etc/logrotate.d/ayo-mwr
```

### 2. Monitoring

```bash
# Monitor update status
journalctl -u ayo-mwr -f | grep -i update

# Setup alerts untuk update failures
# (gunakan monitoring tools seperti Prometheus/Grafana)
```

### 3. Security

```bash
# Pastikan binary dan backup directory secure
chmod 755 ayo-mwr-linux-amd64
chmod 700 backups/

# Validasi signature jika menggunakan signed releases
gpg --verify ayo-mwr-linux-amd64.sig ayo-mwr-linux-amd64
```

## Konfigurasi Lanjutan

### 1. Custom Update Interval

```go
// Update setiap 30 menit
UpdateInterval: 30 * time.Minute

// Update setiap 12 jam
UpdateInterval: 12 * time.Hour
```

### 2. Multiple Backup Retention

```go
// Simpan 10 backup terakhir
updater.CleanupOldBackups(10)
```

### 3. Custom GitHub API Token

```go
// Untuk private repositories
config.GitHubToken = os.Getenv("GITHUB_TOKEN")
```

## Contoh Deployment

### 1. Systemd Service

```ini
[Unit]
Description=Ayo-MWR Service
After=network.target

[Service]
Type=simple
User=ayo-mwr
WorkingDirectory=/opt/ayo-mwr
ExecStart=/opt/ayo-mwr/ayo-mwr-linux-amd64
Restart=always
RestartSec=10

# Environment file
EnvironmentFile=/opt/ayo-mwr/.env

[Install]
WantedBy=multi-user.target
```

### 2. Cron-based Update Check

```bash
# Tambahkan ke crontab untuk backup update check
0 2 * * * /usr/bin/pkill -USR1 -f ayo-mwr-linux-amd64
```

## Changelog

### v1.0.0
- ✅ Initial OTA update implementation
- ✅ GitHub integration
- ✅ Automatic and manual updates
- ✅ Checksum verification
- ✅ Backup system

### Roadmap
- 🔄 Delta updates untuk file besar
- 🔄 Rollback capability
- 🔄 Update scheduling
- 🔄 Web UI untuk update management
- 🔄 Notifikasi update status

## Support

Jika ada masalah atau pertanyaan:

1. Cek dokumentasi ini
2. Lihat log aplikasi
3. Buat issue di GitHub repository
4. Hubungi tim development

---

**Note**: Pastikan untuk test update system di environment development sebelum deploy ke production. 