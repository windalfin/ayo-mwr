package main

import (
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Contoh penggunaan OTA Update system
func main() {
	// Contoh 1: Enable/Disable OTA Update
	enableDisableExample()

	// Contoh 2: Basic OTA Update Setup
	basicOTAExample()

	// Contoh 3: Manual Update Check
	manualUpdateExample()

	// Contoh 4: Version Management
	versionManagementExample()

	// Contoh 5: Signal-based Update
	signalBasedUpdateExample()
}

// enableDisableExample menunjukkan cara mengaktifkan/menonaktifkan OTA update
func enableDisableExample() {
	log.Println("=== Contoh 1: Enable/Disable OTA Update ===")

	// Simulasi environment variable
	os.Setenv("OTA_UPDATE_ENABLED", "true")
	os.Setenv("GITHUB_REPO", "username/ayo-mwr")

	// Cek apakah OTA update enabled
	otaEnabled := strings.ToLower(os.Getenv("OTA_UPDATE_ENABLED")) == "true"
	
	if otaEnabled {
		log.Println("✅ OTA Update ENABLED")
		
		// Konfigurasi normal
		config := &UpdaterConfig{
			GitHubRepo:     os.Getenv("GITHUB_REPO"),
			GitHubToken:    os.Getenv("GITHUB_TOKEN"), // Optional untuk private repo
			CurrentVersion: "v1.0.0",
			UpdateInterval: 6 * time.Hour,
			BackupDir:      "./backups",
			BinaryName:     "ayo-mwr-linux-amd64",
		}
		
		updater := NewUpdater(config)
		log.Printf("   - Repository: %s", config.GitHubRepo)
		log.Printf("   - Update interval: %v", config.UpdateInterval)
		log.Printf("   - Backup dir: %s", config.BackupDir)
		
		// Test configuration
		if config.GitHubToken != "" {
			log.Println("   - Private repository support: YES")
		} else {
			log.Println("   - Private repository support: NO (public repo)")
		}
		
	} else {
		log.Println("❌ OTA Update DISABLED")
		log.Println("   - Reason: OTA_UPDATE_ENABLED=false")
		log.Println("   - To enable: set OTA_UPDATE_ENABLED=true")
	}

	// Contoh disable
	log.Println("\n--- Simulating DISABLE ---")
	os.Setenv("OTA_UPDATE_ENABLED", "false")
	otaEnabled = strings.ToLower(os.Getenv("OTA_UPDATE_ENABLED")) == "true"
	
	if !otaEnabled {
		log.Println("❌ OTA Update DISABLED by configuration")
		log.Println("   - Service will run without OTA capability")
		log.Println("   - Manual updates still possible via script")
	}
}

// basicOTAExample menunjukkan setup dasar OTA update
func basicOTAExample() {
	log.Println("=== Contoh 1: Basic OTA Update Setup ===")

	// Konfigurasi updater
	config := &UpdaterConfig{
		GitHubRepo:     "username/ayo-mwr",
		CurrentVersion: "v1.0.0",
		UpdateInterval: 1 * time.Hour,
		BackupDir:      "./backups",
		BinaryName:     "ayo-mwr-linux-amd64",
	}

	// Inisialisasi updater
	updater := NewUpdater(config)

	// Mulai automatic update checker
	go updater.StartUpdateChecker()

	log.Println("✅ Automatic update checker started")
	log.Println("   - Check interval: 1 hour")
	log.Println("   - GitHub repo: username/ayo-mwr")
	log.Println("   - Current version: v1.0.0")
}

// manualUpdateExample menunjukkan cara melakukan update manual
func manualUpdateExample() {
	log.Println("\n=== Contoh 2: Manual Update Check ===")

	config := &UpdaterConfig{
		GitHubRepo:     "username/ayo-mwr",
		CurrentVersion: "v1.0.0",
		UpdateInterval: 24 * time.Hour,
		BackupDir:      "./backups",
		BinaryName:     "ayo-mwr-linux-amd64",
	}

	updater := NewUpdater(config)

	// Check untuk update secara manual
	log.Println("🔍 Checking for updates manually...")
	if err := updater.ForceUpdate(); err != nil {
		log.Printf("❌ Manual update failed: %v", err)
	} else {
		log.Println("✅ Manual update check completed")
	}

	// Contoh mengecek versi terbaru tanpa update
	release, err := updater.CheckForUpdates()
	if err != nil {
		log.Printf("❌ Failed to check for updates: %v", err)
	} else {
		log.Printf("📦 Latest version available: %s", release.TagName)
		log.Printf("🔄 Current version: %s", updater.GetCurrentVersion())
		
		if updater.IsUpdateAvailable(release.TagName) {
			log.Println("🆕 Update available!")
		} else {
			log.Println("✅ Already running latest version")
		}
	}
}

// versionManagementExample menunjukkan cara mengelola versi
func versionManagementExample() {
	log.Println("\n=== Contoh 3: Version Management ===")

	config := &UpdaterConfig{
		GitHubRepo:     "username/ayo-mwr",
		CurrentVersion: "v1.0.0",
		UpdateInterval: 6 * time.Hour,
		BackupDir:      "./backups",
		BinaryName:     "ayo-mwr-linux-amd64",
	}

	updater := NewUpdater(config)

	// Menampilkan versi saat ini
	log.Printf("📋 Current version: %s", updater.GetCurrentVersion())

	// Update versi secara programmatik
	updater.SetCurrentVersion("v1.1.0")
	log.Printf("📋 Updated version to: %s", updater.GetCurrentVersion())

	// Cleanup backup files lama
	log.Println("🧹 Cleaning up old backups...")
	if err := updater.CleanupOldBackups(3); err != nil {
		log.Printf("❌ Failed to cleanup backups: %v", err)
	} else {
		log.Println("✅ Old backups cleaned up (keeping last 3)")
	}
}

// signalBasedUpdateExample menunjukkan cara menggunakan signal untuk trigger update
func signalBasedUpdateExample() {
	log.Println("\n=== Contoh 4: Signal-based Update ===")

	config := &UpdaterConfig{
		GitHubRepo:     "username/ayo-mwr",
		CurrentVersion: "v1.0.0",
		UpdateInterval: 24 * time.Hour,
		BackupDir:      "./backups",
		BinaryName:     "ayo-mwr-linux-amd64",
	}

	updater := NewUpdater(config)

	// Setup signal handler untuk SIGUSR1
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1)

	log.Println("🎯 Signal handler setup complete")
	log.Println("   - Send SIGUSR1 to trigger manual update")
	log.Println("   - Command: kill -USR1 <PID>")

	// Goroutine untuk handle signals
	go func() {
		for {
			select {
			case <-sigChan:
				log.Println("📡 Received SIGUSR1 signal")
				updater.HandleUpdateSignal()
			}
		}
	}()

	// Simulate aplikasi berjalan
	log.Println("🚀 Application running... (simulate)")
	log.Println("   - Press Ctrl+C to stop")
	
	// Setup handler untuk graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	<-stopChan
	log.Println("👋 Application shutting down...")
}

// Contoh integrasi dengan environment variables
func environmentIntegrationExample() {
	log.Println("\n=== Contoh 5: Environment Integration ===")

	// Baca konfigurasi dari environment variables
	config := &UpdaterConfig{
		GitHubRepo:     getEnvOrDefault("GITHUB_REPO", "username/ayo-mwr"),
		CurrentVersion: getEnvOrDefault("APP_VERSION", "v1.0.0"),
		UpdateInterval: getUpdateInterval(),
		BackupDir:      getEnvOrDefault("BACKUP_DIR", "./backups"),
		BinaryName:     getEnvOrDefault("BINARY_NAME", "ayo-mwr-linux-amd64"),
	}

	updater := NewUpdater(config)

	log.Printf("🔧 Configuration loaded from environment:")
	log.Printf("   - GitHub Repo: %s", config.GitHubRepo)
	log.Printf("   - Current Version: %s", config.CurrentVersion)
	log.Printf("   - Update Interval: %v", config.UpdateInterval)
	log.Printf("   - Backup Directory: %s", config.BackupDir)
	log.Printf("   - Binary Name: %s", config.BinaryName)

	// Start updater
	go updater.StartUpdateChecker()
	log.Println("✅ OTA Updater started with environment configuration")
}

// Helper functions
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getUpdateInterval() time.Duration {
	if interval := os.Getenv("UPDATE_INTERVAL"); interval != "" {
		if duration, err := time.ParseDuration(interval); err == nil {
			return duration
		}
	}
	return 6 * time.Hour // Default 6 hours
}

// Contoh penggunaan dalam production
func productionExample() {
	log.Println("\n=== Contoh 6: Production Usage ===")

	// Production configuration
	config := &UpdaterConfig{
		GitHubRepo:     os.Getenv("GITHUB_REPO"),
		CurrentVersion: os.Getenv("APP_VERSION"),
		UpdateInterval: 12 * time.Hour, // Check setiap 12 jam
		BackupDir:      "/opt/ayo-mwr/backups",
		BinaryName:     "ayo-mwr-linux-amd64",
	}

	// Validasi konfigurasi
	if config.GitHubRepo == "" {
		log.Fatal("❌ GITHUB_REPO environment variable is required")
	}

	if config.CurrentVersion == "" {
		log.Fatal("❌ APP_VERSION environment variable is required")
	}

	updater := NewUpdater(config)

	// Setup signal handlers
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1)

	// Setup graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	// Start update checker
	go updater.StartUpdateChecker()

	// Signal handler goroutine
	go func() {
		for range sigChan {
			log.Println("📡 Manual update triggered via signal")
			updater.HandleUpdateSignal()
		}
	}()

	// Periodic cleanup
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			if err := updater.CleanupOldBackups(10); err != nil {
				log.Printf("❌ Failed to cleanup backups: %v", err)
			}
		}
	}()

	log.Println("🚀 Production OTA updater started")
	log.Println("   - Automatic updates: every 12 hours")
	log.Println("   - Manual updates: kill -USR1 <PID>")
	log.Println("   - Backup cleanup: daily (keep last 10)")

	// Wait for shutdown signal
	<-stopChan
	log.Println("👋 Production updater shutting down...")
} 