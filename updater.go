package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Release represents a GitHub release
type Release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// UpdaterConfig holds configuration for the updater
type UpdaterConfig struct {
	GitHubRepo     string
	GitHubToken    string        // GitHub token untuk private repos
	CurrentVersion string
	UpdateInterval time.Duration
	BackupDir      string
	BinaryName     string
}

// Updater handles OTA updates
type Updater struct {
	config *UpdaterConfig
	client *http.Client
}

// NewUpdater creates a new updater instance
func NewUpdater(config *UpdaterConfig) *Updater {
	return &Updater{
		config: config,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CheckForUpdates checks if there's a new version available
func (u *Updater) CheckForUpdates() (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", u.config.GitHubRepo)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("User-Agent", "ayo-mwr-updater")
	
	// Add GitHub token for private repositories
	if u.config.GitHubToken != "" {
		req.Header.Set("Authorization", "token "+u.config.GitHubToken)
	}
	
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest release: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("GitHub authentication failed. Check your token")
	}
	
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("repository not found or access denied. Check repository name and token permissions")
	}
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	
	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	
	return &release, nil
}

// IsUpdateAvailable checks if the latest version is newer than current
func (u *Updater) IsUpdateAvailable(latestVersion string) bool {
	// Remove 'v' prefix if present
	latest := strings.TrimPrefix(latestVersion, "v")
	current := strings.TrimPrefix(u.config.CurrentVersion, "v")
	
	return latest != current && latest != ""
}

// DownloadUpdate downloads the update package
func (u *Updater) DownloadUpdate(release *Release) (string, error) {
	// Find the appropriate asset for current platform
	var downloadURL string
	var checksumURL string
	
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, platform) && strings.HasSuffix(asset.Name, ".tar.gz") {
			downloadURL = asset.BrowserDownloadURL
		}
		if strings.Contains(asset.Name, platform) && strings.HasSuffix(asset.Name, ".tar.gz.sha256") {
			checksumURL = asset.BrowserDownloadURL
		}
	}
	
	if downloadURL == "" {
		return "", fmt.Errorf("no suitable asset found for platform %s", platform)
	}
	
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "ayo-mwr-update")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	
	// Download the update package
	updateFile := filepath.Join(tempDir, "update.tar.gz")
	if err := u.downloadFile(downloadURL, updateFile); err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to download update: %w", err)
	}
	
	// Download and verify checksum if available
	if checksumURL != "" {
		checksumFile := filepath.Join(tempDir, "checksum.sha256")
		if err := u.downloadFile(checksumURL, checksumFile); err != nil {
			log.Printf("Warning: failed to download checksum: %v", err)
		} else {
			if err := u.verifyChecksum(updateFile, checksumFile); err != nil {
				os.RemoveAll(tempDir)
				return "", fmt.Errorf("checksum verification failed: %w", err)
			}
		}
	}
	
	return updateFile, nil
}

// downloadFile downloads a file from URL to local path
func (u *Updater) downloadFile(url, filepath string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	
	req.Header.Set("User-Agent", "ayo-mwr-updater")
	
	// Add GitHub token for private repository assets
	if u.config.GitHubToken != "" {
		req.Header.Set("Authorization", "token "+u.config.GitHubToken)
	}
	
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("authentication failed when downloading file")
	}
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download file: status %d", resp.StatusCode)
	}
	
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()
	
	_, err = io.Copy(out, resp.Body)
	return err
}

// verifyChecksum verifies the downloaded file's checksum
func (u *Updater) verifyChecksum(filePath, checksumPath string) error {
	// Read expected checksum
	checksumData, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}
	
	expectedChecksum := strings.TrimSpace(string(checksumData))
	if idx := strings.Index(expectedChecksum, " "); idx != -1 {
		expectedChecksum = expectedChecksum[:idx]
	}
	
	// Calculate actual checksum
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}
	
	return nil
}

// ExtractUpdate extracts the update package
func (u *Updater) ExtractUpdate(archivePath string) (string, error) {
	// Create extraction directory
	extractDir := filepath.Join(filepath.Dir(archivePath), "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create extraction directory: %w", err)
	}
	
	// Open and extract tar.gz
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzr.Close()
	
	tr := tar.NewReader(gzr)
	
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		
		target := filepath.Join(extractDir, header.Name)
		
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return "", err
			}
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return "", err
			}
			
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return "", err
			}
			f.Close()
		}
	}
	
	return extractDir, nil
}

// BackupCurrentBinary creates a backup of the current binary
func (u *Updater) BackupCurrentBinary() error {
	if err := os.MkdirAll(u.config.BackupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}
	
	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}
	
	backupPath := filepath.Join(u.config.BackupDir, fmt.Sprintf("%s.bak.%d", u.config.BinaryName, time.Now().Unix()))
	
	src, err := os.Open(currentBinary)
	if err != nil {
		return fmt.Errorf("failed to open current binary: %w", err)
	}
	defer src.Close()
	
	dst, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer dst.Close()
	
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy binary: %w", err)
	}
	
	// Make backup executable
	if err := os.Chmod(backupPath, 0755); err != nil {
		return fmt.Errorf("failed to make backup executable: %w", err)
	}
	
	log.Printf("Backup created: %s", backupPath)
	return nil
}

// ApplyUpdate applies the downloaded update
func (u *Updater) ApplyUpdate(extractDir string) error {
	// Find the new binary
	newBinaryPath := filepath.Join(extractDir, u.config.BinaryName)
	if _, err := os.Stat(newBinaryPath); err != nil {
		return fmt.Errorf("new binary not found in update package: %w", err)
	}
	
	// Get current binary path
	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}
	
	// Create backup
	if err := u.BackupCurrentBinary(); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}
	
	// Replace current binary
	if err := u.replaceBinary(newBinaryPath, currentBinary); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}
	
	log.Printf("Update applied successfully")
	return nil
}

// replaceBinary replaces the current binary with the new one
func (u *Updater) replaceBinary(newBinary, currentBinary string) error {
	// Read new binary
	newData, err := os.ReadFile(newBinary)
	if err != nil {
		return err
	}
	
	// Write to temporary file first
	tempFile := currentBinary + ".tmp"
	if err := os.WriteFile(tempFile, newData, 0755); err != nil {
		return err
	}
	
	// Atomic replace
	if err := os.Rename(tempFile, currentBinary); err != nil {
		os.Remove(tempFile)
		return err
	}
	
	return nil
}

// RestartApplication restarts the application
func (u *Updater) RestartApplication() error {
	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}
	
	// Get current process arguments
	args := os.Args[1:]
	
	// Start new process
	cmd := exec.Command(currentBinary, args...)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start new process: %w", err)
	}
	
	log.Printf("New process started with PID: %d", cmd.Process.Pid)
	
	// Exit current process
	os.Exit(0)
	return nil
}

// PerformUpdate performs the complete update process
func (u *Updater) PerformUpdate() error {
	log.Println("Checking for updates...")
	
	release, err := u.CheckForUpdates()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}
	
	if !u.IsUpdateAvailable(release.TagName) {
		log.Printf("No update available. Current: %s, Latest: %s", u.config.CurrentVersion, release.TagName)
		return nil
	}
	
	log.Printf("Update available: %s -> %s", u.config.CurrentVersion, release.TagName)
	
	// Download update
	updateFile, err := u.DownloadUpdate(release)
	if err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}
	defer os.RemoveAll(filepath.Dir(updateFile))
	
	// Extract update
	extractDir, err := u.ExtractUpdate(updateFile)
	if err != nil {
		return fmt.Errorf("failed to extract update: %w", err)
	}
	
	// Apply update
	if err := u.ApplyUpdate(extractDir); err != nil {
		return fmt.Errorf("failed to apply update: %w", err)
	}
	
	// Restart application
	log.Println("Restarting application...")
	return u.RestartApplication()
}

// StartUpdateChecker starts the automatic update checker
func (u *Updater) StartUpdateChecker() {
	ticker := time.NewTicker(u.config.UpdateInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			if err := u.PerformUpdate(); err != nil {
				log.Printf("Update check failed: %v", err)
			}
		}
	}
}

// CleanupOldBackups removes old backup files
func (u *Updater) CleanupOldBackups(maxBackups int) error {
	if _, err := os.Stat(u.config.BackupDir); os.IsNotExist(err) {
		return nil
	}
	
	files, err := os.ReadDir(u.config.BackupDir)
	if err != nil {
		return err
	}
	
	var backupFiles []os.DirEntry
	for _, file := range files {
		if strings.HasPrefix(file.Name(), u.config.BinaryName+".bak.") {
			backupFiles = append(backupFiles, file)
		}
	}
	
	if len(backupFiles) > maxBackups {
		// Sort by modification time (oldest first)
		// Remove excess backups
		for i := 0; i < len(backupFiles)-maxBackups; i++ {
			backupPath := filepath.Join(u.config.BackupDir, backupFiles[i].Name())
			if err := os.Remove(backupPath); err != nil {
				log.Printf("Failed to remove old backup %s: %v", backupPath, err)
			} else {
				log.Printf("Removed old backup: %s", backupPath)
			}
		}
	}
	
	return nil
}

// HandleUpdateSignal handles update signals (SIGUSR1)
func (u *Updater) HandleUpdateSignal() {
	// This function can be called when receiving SIGUSR1 signal
	log.Println("Received update signal, checking for updates...")
	
	if err := u.PerformUpdate(); err != nil {
		log.Printf("Manual update failed: %v", err)
	}
}

// ForceUpdate forces an update check and application
func (u *Updater) ForceUpdate() error {
	log.Println("Forcing update check...")
	return u.PerformUpdate()
}

// GetCurrentVersion returns the current version
func (u *Updater) GetCurrentVersion() string {
	return u.config.CurrentVersion
}

// SetCurrentVersion updates the current version
func (u *Updater) SetCurrentVersion(version string) {
	u.config.CurrentVersion = version
} 