package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/recording"
	"ayo-mwr/storage"
	// "ayo-mwr/transcode"
)

// AyoAPIClient adalah interface untuk berinteraksi dengan AYO API
type AyoAPIClient interface {
	GetBookings(date string) (map[string]interface{}, error)
	SaveVideoAvailable(bookingID, videoType, previewURL, thumbnailURL, uniqueID string, startTime, endTime time.Time) (map[string]interface{}, error)
	GetWatermark(resolution string) (string, error)
}

// BookingVideoService handles all operations related to booking videos
type BookingVideoService struct {
	db             database.Database
	ayoClient      AyoAPIClient
	r2Client       *storage.R2Storage
	config         *config.Config
	offlineManager *OfflineManager
}

// Tipe file sementara yang disimpan
const (
	TmpTypeHLS       = "hls"
	TmpTypeMerged    = "merged"
	TmpTypeWatermark = "watermark"
	TmpTypePreview   = "preview"
	TmpTypeThumbnail = "thumbnail"
)

// getTempPath mengembalikan path file sementara berdasarkan tipe dan uniqueID
func (s *BookingVideoService) getTempPath(fileType string, uniqueID string, extension string, cameraName string) string {
	// Buat struktur folder untuk tipe
	baseDir := filepath.Join(s.config.StoragePath, "recordings", cameraName)
	folderPath := filepath.Join(baseDir, "tmp", fileType)
	os.MkdirAll(folderPath, 0755)

	// Format nama file dengan benar (extension harus include titik jika diperlukan)
	fileName := fmt.Sprintf("%s%s", uniqueID, extension)
	return filepath.Join(folderPath, fileName)
}

// sanitizeID mengganti karakter yang tidak valid untuk path file (seperti /) dengan -
func sanitizeID(id string) string {
	// Ganti / dengan - untuk menghindari masalah path
	return strings.ReplaceAll(id, "/", "-")
}

// NewBookingVideoService creates a new booking video service
func NewBookingVideoService(db database.Database, ayoClient AyoAPIClient, r2Client *storage.R2Storage, cfg *config.Config) *BookingVideoService {
	return &BookingVideoService{
		db:        db,
		ayoClient: ayoClient,
		r2Client:  r2Client,
		config:    cfg,
	}
}

// NewBookingVideoServiceWithOfflineManager creates a new booking video service with offline manager
func NewBookingVideoServiceWithOfflineManager(db database.Database, ayoClient AyoAPIClient, r2Client *storage.R2Storage, cfg *config.Config, offlineManager *OfflineManager) *BookingVideoService {
	return &BookingVideoService{
		db:             db,
		ayoClient:      ayoClient,
		r2Client:       r2Client,
		config:         cfg,
		offlineManager: offlineManager,
	}
}

// CopyFile copies a file from src to dst
func (s *BookingVideoService) CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// ProcessVideoSegments merges, watermarks, and processes video segments for a booking
func (s *BookingVideoService) ProcessVideoSegments(
	camera config.CameraConfig,
	bookingID string,
	orderDetailIDStr string,
	segments []string,
	startTime, endTime time.Time,
	rawJSON string,
	videoType string, // Added videoType parameter ("clip" or "full")
) (string, error) {
	if len(segments) == 0 {
		return "", fmt.Errorf("no video segments found")
	}

	// Create a unique ID for this video
	// Sanitasi bookingID untuk menghindari masalah path dengan karakter /
	sanitizedBookingID := sanitizeID(bookingID)
	uniqueID := fmt.Sprintf("%s_%s_%s", sanitizedBookingID, camera.Name, time.Now().Format("20060102150405"))

	log.Printf("ProcessVideoSegments : Processing %d video segments for booking %s on camera %s", len(segments), bookingID, camera.Name)

	videoInitialMeta := database.VideoMetadata{
		ID:            uniqueID,
		CreatedAt:     time.Now(),
		Status:        database.StatusInitial, // Start with initial status
		CameraName:    camera.Name,
		UniqueID:      uniqueID,
		OrderDetailID: orderDetailIDStr,
		BookingID:     bookingID,
		RawJSON:       rawJSON,
		Resolution:    camera.Resolution,
		HasRequest:    false,     // Explicitly set default to false
		VideoType:     videoType, // Set from the parameter
	}

	// Create initial database entry
	if err := s.db.CreateVideo(videoInitialMeta); err != nil {
		return "", fmt.Errorf("ProcessVideoSegments: error creating initial database entry: %v", err)
	}

	log.Printf("ProcessVideoSegments: Created initial entry for video %s with status Initial", uniqueID)

	// Tentukan direktori tempat segment berada (ambil dari segment pertama)
	segmentDir := filepath.Dir(segments[0])

	// Gabungkan video segments dan tambahkan watermark dalam satu operasi FFmpeg
	watermarkedVideoPath := s.getTempPath(TmpTypeWatermark, uniqueID, ".mp4", camera.Name)
	log.Printf("ProcessVideoSegments : Merging video segments and adding watermark in one FFmpeg operation, output to: %s", watermarkedVideoPath)

	// Mendapatkan watermark dan pengaturannya
	watermarkPath, watermarkErr := s.ayoClient.GetWatermark(camera.Resolution)
	if watermarkErr != nil {
		log.Printf("ProcessVideoSegments : Warning: Failed to get watermark: %v, continuing with merge only", watermarkErr)
		// Jika gagal mendapatkan watermark, lakukan merge saja
		err := recording.MergeSessionVideos(segmentDir, startTime, endTime, watermarkedVideoPath, camera.Resolution)
		if err != nil {
			s.db.UpdateVideoStatus(uniqueID, database.StatusFailed, err.Error())
			return "", fmt.Errorf("failed to merge video segments: %v", err)
		}
	} else {
		// Dapatkan pengaturan watermark
		pos, margin, opacity := recording.GetWatermarkSettings()

		// Lakukan merge dan tambahkan watermark dalam satu operasi
		err := recording.MergeAndWatermark(segmentDir, startTime, endTime, watermarkedVideoPath,
			watermarkPath, pos, margin, opacity, camera.Resolution)
		if err != nil {
			log.Printf("ProcessVideoSegments : Warning: Failed to merge and add watermark: %v, falling back to merge only", err)
			// Jika gagal, coba lakukan hanya merge saja
			err := recording.MergeSessionVideos(segmentDir, startTime, endTime, watermarkedVideoPath, camera.Resolution)
			if err != nil {
				s.db.UpdateVideoStatus(uniqueID, database.StatusFailed, err.Error())
				return "", fmt.Errorf("failed to merge video segments in fallback mode: %v", err)
			}
		}
	}

	videoPathForNextStep := watermarkedVideoPath
	log.Printf("ProcessVideoSegments : videoPathForNextStep %s", videoPathForNextStep)
	log.Printf("ProcessVideoSegments : camera.Resolution %s", camera.Resolution)
	
	// Determine storage disk ID and full path for the processed video
	storageDiskID, mp4FullPath, err := s.determineStorageInfo(videoPathForNextStep)
	if err != nil {
		log.Printf("ProcessVideoSegments : Warning: Could not determine storage disk info: %v", err)
		// Continue without storage info rather than failing
	}
	
	// Step 3: update video metadata
	videoMeta := database.VideoMetadata{
		ID:            uniqueID,
		Status:        database.StatusUploading,
		LocalPath:     videoPathForNextStep,
		StorageDiskID: storageDiskID,
		MP4FullPath:   mp4FullPath,
	}

	// Update database entry with processing status and local path
	if err := s.db.UpdateLocalPathVideo(videoMeta); err != nil {
		s.db.UpdateVideoStatus(uniqueID, database.StatusFailed, err.Error())
		return "", fmt.Errorf("ProcessVideoSegments: error updating database entry: %v", err)
	}
	log.Printf("ProcessVideoSegments : Database entry created for uniqueID: %s", uniqueID)
	return uniqueID, nil
}

// UploadProcessedVideo uploads the processed video, creates previews and thumbnails
func (s *BookingVideoService) UploadProcessedVideo(
	uniqueID string,
	videoPath string,
	bookingID string,
	cameraName string,
) (string, string, error) {
	// UniqueID sudah berisi booking ID yang aman
	// getVideoMeta := s.db.GetVideo(uniqueID)

	// Create preview video (di folder preview)
	previewVideoPath := s.getTempPath(TmpTypePreview, uniqueID, ".mp4", cameraName)
	log.Printf("Creating preview video at: %s", previewVideoPath)
	err := s.CreateVideoPreview(videoPath, previewVideoPath)
	if err != nil {
		log.Printf("Warning: Failed to create preview video: %v", err)
		previewVideoPath = "" // Don't use preview if creation failed
	}

	// Create thumbnail (di folder thumbnail)
	thumbnailPath := s.getTempPath(TmpTypeThumbnail, uniqueID, ".png", cameraName)
	log.Printf("Creating thumbnail at: %s", thumbnailPath)
	err = s.CreateThumbnail(videoPath, thumbnailPath)
	if err != nil {
		log.Printf("Warning: Failed to create thumbnail: %v", err)
		thumbnailPath = "" // Don't use thumbnail if creation failed
	}

	// Buat direktori HLS untuk video ini di folder hls, bukan di tmp/hls
	// Sesuai dengan konfigurasi server di api/server.go
	// hlsParentDir := filepath.Join(s.config.StoragePath, "hls")
	// os.MkdirAll(hlsParentDir, 0755)
	// hlsDir := filepath.Join(hlsParentDir, uniqueID)
	// hlsURL := ""

	// // Buat HLS stream dari video menggunakan ffmpeg
	// log.Printf("Generating HLS stream in: %s", hlsDir)
	// if err := transcode.GenerateHLS(videoPath, hlsDir, uniqueID, s.config); err != nil {
	// 	log.Printf("Warning: Failed to create HLS stream: %v", err)
	// } else {
	// 	// Format HLS URL untuk server lokal yang sudah di-setup di api/server.go
	// 	// Route: r.Static("/hls", filepath.Join(s.config.StoragePath, "hls"))
	// 	baseURL := "http://localhost:8080" // Gunakan base URL yang sesuai dengan konfigurasi
	// 	hlsURL = fmt.Sprintf("%s/hls/%s/master.m3u8", baseURL, uniqueID)
	// 	log.Printf("HLS stream created at: %s", hlsDir)
	// 	log.Printf("HLS stream can be accessed at: %s", hlsURL)

	// 	// Upload HLS ke R2
	// 	// _, r2HlsURL, err := s.r2Client.UploadHLSStream(hlsDir, uniqueID)
	// 	// if err != nil {
	// 	// 	log.Printf("Warning: Failed to upload HLS stream to R2: %v", err)
	// 	// } else {
	// 	// 	hlsURL = r2HlsURL // ganti dengan URL dari R2 jika berhasil
	// 	// 	log.Printf("HLS stream uploaded to R2: %s", r2HlsURL)
	// 	// }
	// }

	// Upload main video to R2
	mp4Path := fmt.Sprintf("mp4/%s.mp4", uniqueID)
	// _, err = s.r2Client.UploadFile(videoPath, mp4Path)
	// if err != nil {
	// 	return "", "", "", "", "", fmt.Errorf("error uploading video to R2: %v", err)
	// }

	// Gunakan GetBaseURL untuk membuat URL dari custom domain
	// mp4URL := fmt.Sprintf("%s/%s", s.r2Client.GetBaseURL(), mp4Path)
	// log.Printf("Video uploaded to custom URL: %s", mp4URL)

	// Upload preview video if available
	previewPath := fmt.Sprintf("mp4/%s_preview.mp4", uniqueID)
	previewURL := ""
	if previewVideoPath != "" {
		_, err = s.r2Client.UploadFile(previewVideoPath, previewPath)
		if err != nil {
			log.Printf("Warning: Failed to upload preview video: %v", err)
		} else {
			// Gunakan GetBaseURL untuk URL preview
			previewURL = fmt.Sprintf("%s/%s", s.r2Client.GetBaseURL(), previewPath)
			log.Printf("Preview video uploaded to custom URL: %s", previewURL)
		}
	}

	// Upload thumbnail if available
	thumbnailR2Path := fmt.Sprintf("mp4/%s_thumbnail.png", uniqueID)
	thumbnailURL := ""
	if thumbnailPath != "" {
		_, err = s.r2Client.UploadFile(thumbnailPath, thumbnailR2Path)
		if err != nil {
			log.Printf("Warning: Failed to upload thumbnail: %v", err)
		} else {
			// Gunakan GetBaseURL untuk URL thumbnail
			thumbnailURL = fmt.Sprintf("%s/%s", s.r2Client.GetBaseURL(), thumbnailR2Path)
			log.Printf("Thumbnail uploaded to custom URL: %s", thumbnailURL)
		}
	}

	// Get video info (size and duration)
	fileInfo, err := os.Stat(videoPath)
	var fileSize int64
	if err != nil {
		log.Printf("Error getting file info: %v", err)
		fileSize = 0
	} else {
		fileSize = fileInfo.Size()
	}

	// Get video duration using ffprobe
	duration, err := s.GetVideoDuration(videoPath)
	if err != nil {
		log.Printf("Error getting video duration: %v", err)
		duration = 0
	}

	// Dapatkan data video yang sudah ada dari database
	currentVideo, err := s.db.GetVideo(uniqueID)
	if err != nil {
		log.Printf("Error getting current video data from database: %v", err)
	}

	// Update database entry with all information
	// Siapkan nilai HLS untuk update database
	// var r2HLSPath, hlsLocalPath, hlsLocalURL string
	// if hlsDir != "" {
	// Format paths dengan benar
	// r2HLSPath = fmt.Sprintf("hls/%s", uniqueID) // Path di R2 storage
	// hlsLocalPath = hlsDir // Path di lokal filesystem

	// Format URL dengan benar untuk akses lokal
	// Hapus semua referensi ke StoragePath dan pastikan URL mengacu langsung ke folder hls
	// Karena server sudah di-configure untuk menyajikan langsung dari endpoint /hls

	// Path server hls harus persis seperti endpoint yang dikonfigurasi di server.go: /hls/{uniqueID}
	// hlsServerPath := fmt.Sprintf("hls/%s", uniqueID)

	// Gunakan BaseURL dari konfigurasi
	// hlsLocalURL = fmt.Sprintf("%s/%s/playlist.m3u8", s.config.BaseURL, hlsServerPath)

	// Tambahan debug untuk memastikan URL yang dibuat sudah benar
	// log.Printf("HLS Endpoint URL: %s", hlsLocalURL)

	// Log semua nilai untuk debugging
	log.Printf("HLS: Menyimpan ke database:")
	// log.Printf("  - hls_path: %s", hlsLocalPath)
	// log.Printf("  - hls_url: %s", hlsLocalURL)
	// log.Printf("  - r2_hls_path: %s", r2HLSPath)
	// log.Printf("  - r2_hls_url: %s", hlsURL)
	// }

	// Get existing video metadata to preserve all fields
	existingVideo, err := s.db.GetVideo(uniqueID)
	if err != nil {
		log.Printf("Warning: Could not get existing video metadata: %v. Creating a new update struct.", err)
		// Continue with a new struct if we can't get the existing one
		existingVideo = &database.VideoMetadata{
			ID:        uniqueID,
			UniqueID:  uniqueID,
			BookingID: bookingID,
		}
	}

	log.Printf("Current existing video with ID=%s, VideoType=%s", uniqueID, existingVideo.VideoType)

	// Update only the fields that need changing, preserving everything else
	videoUpdate := *existingVideo
	// log.Printf("videoUpdate.resolution: %s", videoUpdate.Resolution)
	videoUpdate.Status = database.StatusReady
	videoUpdate.Size = fileSize
	videoUpdate.Duration = duration
	videoUpdate.R2PreviewMP4Path = previewPath
	videoUpdate.R2PreviewMP4URL = previewURL
	videoUpdate.R2PreviewPNGPath = thumbnailR2Path
	videoUpdate.R2PreviewPNGURL = thumbnailURL
	videoUpdate.CameraName = cameraName // Ensure this is still set
	videoUpdate.LocalPath = videoPath   // Ensure this is still set
	videoUpdate.R2MP4Path = mp4Path
	// videoUpdate.Resolution = "720p"    // Ensure this is still set

	// Double check that VideoType is preserved
	log.Printf("Updating video with ID=%s, VideoType=%s", uniqueID, videoUpdate.VideoType)

	// Salin field-field penting dari data yang sudah ada jika tersedia
	if currentVideo != nil {
		// Salin field yang tidak diset dalam pembaruan tapi ada di database
		if currentVideo.OrderDetailID != "" {
			videoUpdate.OrderDetailID = currentVideo.OrderDetailID
		}
		if currentVideo.RawJSON != "" {
			videoUpdate.RawJSON = currentVideo.RawJSON
		}
		if currentVideo.CreatedAt.Unix() > 0 {
			videoUpdate.CreatedAt = currentVideo.CreatedAt
		}
	}

	// Set CreatedAt jika masih kosong
	if videoUpdate.CreatedAt.IsZero() {
		videoUpdate.CreatedAt = time.Now()
	}

	// Set timestamp upload dan finished
	now := time.Now()
	// videoUpdate.UploadedAt = &now
	videoUpdate.FinishedAt = &now // Set finished time saat status ready

	log.Printf("Updating video metadata with complete fields for %s", uniqueID)
	err = s.db.UpdateVideo(videoUpdate)
	log.Printf("videoUpdate: %v", videoUpdate)
	if err != nil {
		log.Printf("Error updating database with all fields: %v", err)
	}

	// Update status to ready
	s.db.UpdateVideoStatus(uniqueID, database.StatusReady, "")

	log.Printf("Video metadata updated for %s", uniqueID)

	// Isi nilai HLS untuk dikembalikan ke caller
	// hlsPath := ""
	// if hlsDir != "" {
	// 	hlsPath = fmt.Sprintf("hls/%s", uniqueID)
	// 	// log.Printf("HLS path yang dikembalikan: %s", hlsPath)
	// 	// log.Printf("HLS URL yang dikembalikan: %s", hlsURL)
	// }
	return previewURL, thumbnailURL, nil
}

// NotifyAyoAPI notifies the AYO API that a video is available
func (s *BookingVideoService) NotifyAyoAPI(
	bookingID string,
	uniqueID string,
	previewURL string,
	thumbnailURL string,
	startTime time.Time,
	endTime time.Time,
	videoType string,
) error {
	// Use offline manager if available for resilient API calls
	if s.offlineManager != nil {
		return s.offlineManager.TrySaveVideoAvailable(bookingID, videoType, previewURL, thumbnailURL, uniqueID, startTime, endTime)
	}
	
	// Fallback to direct API call
	_, err := s.ayoClient.SaveVideoAvailable(
		bookingID,
		videoType,    // videoType
		previewURL,   // previewPath
		thumbnailURL, // imagePath
		uniqueID,     // uniqueID
		startTime,    // startTime
		endTime,      // endTime
	)
	return err
}

// CleanupTemporaryFiles removes temporary files after successful processing
func (s *BookingVideoService) CleanupTemporaryFiles(mergedPath, watermarkedPath, previewPath, thumbnailPath string) {
	if mergedPath != "" {
		os.Remove(mergedPath)
	}
	if watermarkedPath != "" {
		os.Remove(watermarkedPath)
	}
	if previewPath != "" {
		os.Remove(previewPath)
	}
	if thumbnailPath != "" {
		os.Remove(thumbnailPath)
	}
}

// addWatermarkToVideo adds a watermark to a video
func (s *BookingVideoService) addWatermarkToVideo(inputPath, outputPath string, resolution string) error {
	// Attempt to get watermark from AYO API
	log.Printf("addWatermarkToVideo : Attempting to get watermark from AYO API")
	watermarkPath, err := s.ayoClient.GetWatermark(resolution)
	if err != nil {
		return fmt.Errorf("addWatermarkToVideo : failed to get watermark: %v", err)
	}
	log.Printf("addWatermarkToVideo : Watermark path: %s", watermarkPath)

	// Get watermark position settings
	pos, margin, opacity := recording.GetWatermarkSettings()
	log.Printf("addWatermarkToVideo : Watermark position: %s, margin: %d, opacity: %f", pos, margin, opacity)

	// Add watermark to video
	err = recording.AddWatermarkWithPosition(inputPath, watermarkPath, outputPath, pos, margin, opacity, resolution)
	if err != nil {
		return fmt.Errorf("addWatermarkToVideo : failed to add watermark: %v", err)
	}
	log.Printf("addWatermarkToVideo : Watermark added successfully")

	return nil
}

// CreateVideoPreview creates a preview video using interval-based clipping based on video duration
func (s *BookingVideoService) CreateVideoPreview(inputPath, outputPath string) error {
	// Get video duration to determine which interval pattern to use
	duration, err := s.GetVideoDuration(inputPath)
	if err != nil {
		return fmt.Errorf("failed to get video duration: %v", err)
	}

	// Using duration directly in seconds for more accurate interval selection
	log.Printf("Creating preview for video with duration: %.2f minutes (%v seconds)", duration/60, duration)

	// Define intervals based on video duration directly in seconds
	// Each interval is a time range to extract (start_time, end_time)
	intervals := determineIntervals(int(duration))
	log.Printf("intervals: %v", intervals)
	// Create a temporary directory for clip segments
	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("preview_clips_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir) // Clean up temp directory when done

	// File to store the list of clips for concatenation
	clipListPath := filepath.Join(tmpDir, "clips.txt")
	clipListFile, err := os.Create(clipListPath)
	if err != nil {
		return fmt.Errorf("failed to create clip list file: %v", err)
	}
	defer clipListFile.Close()

	// Extract each interval segment
	for i, interval := range intervals {
		clipPath := filepath.Join(tmpDir, fmt.Sprintf("clip_%d.mp4", i))

		// Extract the clip using ffmpeg (using fast encoding but good quality)
		cmd := exec.Command(
			"ffmpeg", "-y",
			"-i", inputPath,
			"-ss", interval.start, // Start time
			"-to", interval.end, // End time
			"-c:v", "libx264", "-c:a", "aac", // Use consistent codecs across all clips
			"-crf", "22", // Good quality (lower number = better quality)
			"-preset", "ultrafast", // Faster encoding
			clipPath,
		)

		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to extract clip %d: %v, output: %s", i, err, string(out))
		}

		// Add clip to the list for concatenation
		fmt.Fprintf(clipListFile, "file '%s'\n", clipPath)
	}

	// Close the clip list file before using it
	clipListFile.Close()

	// Concatenate all clips into the final preview video
	cmd := exec.Command(
		"ffmpeg", "-y",
		"-f", "concat",
		"-safe", "0",
		"-i", clipListPath,
		"-c:v", "libx264",
		"-c:a", "aac",
		"-crf", "22", // Good quality
		"-preset", "ultrafast",
		outputPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to concatenate clips: %v, output: %s", err, string(out))
	}

	return nil
}

// timeInterval represents a start and end time for video clipping
type timeInterval struct {
	start string
	end   string
}

// determineIntervals returns the appropriate time intervals for clipping based on video duration in seconds
func determineIntervals(durationSeconds int) []timeInterval {
	// Konstanta durasi dalam detik
	const (
		fiveMinutesInSeconds   = 5 * 60      // 5 menit dalam detik
		thirtyMinutesInSeconds = 30 * 60     // 30 menit dalam detik
		oneHourInSeconds       = 60 * 60     // 1 jam dalam detik
		twoHoursInSeconds      = 2 * 60 * 60 // 2 jam dalam detik
		threeHoursInSeconds    = 3 * 60 * 60 // 3 jam dalam detik
		fourHoursInSeconds     = 4 * 60 * 60 // 4 jam dalam detik
		fiveHoursInSeconds     = 5 * 60 * 60 // 5 jam dalam detik
		sixHoursInSeconds      = 6 * 60 * 60 // 6 jam dalam detik
		sevenHoursInSeconds    = 7 * 60 * 60 // 7 jam dalam detik
	)

	// Helper function to create an interval
	makeInterval := func(timeStr string) timeInterval {
		// Parse the time string (format: H:MM:SS)
		parts := strings.Split(timeStr, ":")

		// Create the end time (add 2 seconds)
		minuteStr := parts[1]
		secondStr := parts[2]

		// Calculate seconds for end time
		second, _ := strconv.Atoi(secondStr)
		second += 2

		// Handle overflow
		if second >= 60 {
			second -= 60
			minute, _ := strconv.Atoi(minuteStr)
			minute++
			minuteStr = fmt.Sprintf("%02d", minute)
		}

		endTime := fmt.Sprintf("%s:%s:%02d", parts[0], minuteStr, second)

		return timeInterval{start: timeStr, end: endTime}
	}

	// Determine intervals based on video duration
	intervals := []timeInterval{}

	// Always include the first interval at 0:00:00
	intervals = append(intervals, makeInterval("0:00:00"))
	log.Printf("Duration in seconds: %d", durationSeconds)
	log.Printf("Duration in minutes: %.2f", float64(durationSeconds)/60)
	if durationSeconds < 20 {
		// Videos less than 20 seconds
		return []timeInterval{makeInterval("0:00:00")}
	} else if durationSeconds < 30 {
		// Videos between 20-30 seconds
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("0:00:20"),
		}
	} else if durationSeconds < 60 {
		// Videos between 30-60 seconds
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("0:00:20"),
			makeInterval("0:00:40"),
		}
	} else if durationSeconds >= 60 && durationSeconds < 120 {
		// 1 minute
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("0:00:20"),
			makeInterval("0:00:40"),
		}
	} else if durationSeconds <= thirtyMinutesInSeconds+fiveMinutesInSeconds {
		// 30 menit + 5 menit toleransi
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("0:10:00"),
			makeInterval("0:20:00"),
		}
	} else if durationSeconds <= oneHourInSeconds+fiveMinutesInSeconds {
		// 1 jam + 5 menit toleransi
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("0:12:00"),
			makeInterval("0:24:00"),
			makeInterval("0:36:00"),
			makeInterval("0:48:00"),
		}
	} else if durationSeconds <= twoHoursInSeconds+fiveMinutesInSeconds {
		// 2 jam + 5 menit toleransi
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("0:24:00"),
			makeInterval("0:48:00"),
			makeInterval("1:12:00"),
			makeInterval("1:36:00"),
		}
	} else if durationSeconds <= threeHoursInSeconds+fiveMinutesInSeconds {
		// 3 jam + 5 menit toleransi
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("0:36:00"),
			makeInterval("1:12:00"),
			makeInterval("1:48:00"),
			makeInterval("2:24:00"),
		}
	} else if durationSeconds <= fourHoursInSeconds+fiveMinutesInSeconds {
		// 4 jam + 5 menit toleransi
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("0:48:00"),
			makeInterval("1:36:00"),
			makeInterval("2:24:00"),
			makeInterval("3:12:00"),
		}
	} else if durationSeconds <= fiveHoursInSeconds+fiveMinutesInSeconds {
		// 5 jam + 5 menit toleransi
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("1:00:00"),
			makeInterval("2:00:00"),
			makeInterval("3:00:00"),
			makeInterval("4:00:00"),
		}
	} else if durationSeconds <= sixHoursInSeconds+fiveMinutesInSeconds {
		// 6 jam + 5 menit toleransi
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("1:12:00"),
			makeInterval("2:24:00"),
			makeInterval("3:36:00"),
			makeInterval("4:48:00"),
		}
	} else if durationSeconds <= sevenHoursInSeconds+fiveMinutesInSeconds {
		// 7 jam + 5 menit toleransi
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("1:24:00"),
			makeInterval("2:48:00"),
			makeInterval("4:12:00"),
			makeInterval("5:36:00"),
		}
	} else {
		// 8 hours or more
		return []timeInterval{
			makeInterval("0:00:00"),
			makeInterval("1:36:00"),
			makeInterval("3:12:00"),
			makeInterval("4:48:00"),
			makeInterval("6:24:00"),
		}
	}
}

// CreateThumbnail extracts a frame from the middle of the video as a thumbnail
func (s *BookingVideoService) CreateThumbnail(inputPath, outputPath string) error {
	// Use ffmpeg to extract a thumbnail from the middle of the video
	cmd := exec.Command(
		"ffmpeg", "-y", "-i", inputPath,
		"-ss", "00:00:05", // Take frame at 5 seconds
		"-vframes", "1", // Extract just one frame
		"-vf", "scale=480:-2", // 480p width thumbnail, maintain aspect ratio
		outputPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg thumbnail creation failed: %v, output: %s", err, string(out))
	}

	return nil
}

// directoryExists checks if a directory exists
func (s *BookingVideoService) directoryExists(dirPath string) bool {
	info, err := os.Stat(dirPath)
	if os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}

// CreateHLSStream converts a video file to HLS format
func (s *BookingVideoService) CreateHLSStream(videoPath string, outputDir string) error {
	// Pastikan output direktori ada
	err := os.MkdirAll(outputDir, 0755)
	if err != nil {
		return fmt.Errorf("error creating HLS output directory: %v", err)
	}

	// Buat playlist HLS dengan ffmpeg
	// ffmpeg -i input.mp4 -profile:v baseline -level 3.0 -start_number 0 -hls_time 5 -hls_list_size 0 -f hls output/playlist.m3u8
	cmd := exec.Command(
		"ffmpeg",
		"-i", videoPath,
		"-profile:v", "baseline",
		"-level", "3.0",
		"-start_number", "0",
		"-hls_time", "5", // Tiap segment 5 detik
		"-hls_list_size", "0", // Semua segment dalam playlist
		"-f", "hls",
		"-hls_segment_filename", filepath.Join(outputDir, "%03d.ts"),
		filepath.Join(outputDir, "playlist.m3u8"),
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("error creating stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("error creating stderr pipe: %v", err)
	}

	// Start command
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("error starting ffmpeg: %v", err)
	}

	// Read and log output
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Printf("ffmpeg HLS stdout: %s", scanner.Text())
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// Hanya tampilkan sebagai error jika memang ada kata 'error' dalam pesan
			text := scanner.Text()
			if strings.Contains(strings.ToLower(text), "error") || strings.Contains(strings.ToLower(text), "failed") {
				log.Printf("ffmpeg HLS error: %s", text)
			} else {
				log.Printf("ffmpeg HLS info: %s", text)
			}
		}
	}()

	// Wait for completion
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("error generating HLS stream: %v", err)
	}

	log.Printf("Successfully created HLS stream in: %s", outputDir)
	return nil
}

// GetVideoDuration gets the duration of a video file in seconds
func (s *BookingVideoService) GetVideoDuration(videoPath string) (float64, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %v, output: %s", err, string(out))
	}

	var duration float64
	_, err = fmt.Sscanf(string(out), "%f", &duration)
	if err != nil {
		return 0, fmt.Errorf("failed to parse duration: %v", err)
	}

	return duration, nil
}

// CombineDateTime combines a date and a time string (format: "HH:MM:SS") into a single time.Time
func (s *BookingVideoService) CombineDateTime(date time.Time, timeStr string) (time.Time, error) {
	// Parse the time part
	parts := strings.Split(timeStr, ":")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid time format: %s", timeStr)
	}

	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid hour: %v", err)
	}

	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid minute: %v", err)
	}

	second, err := strconv.Atoi(parts[2])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid second: %v", err)
	}

	// Combine date and time
	return time.Date(
		date.Year(),
		date.Month(),
		date.Day(),
		hour,
		minute,
		second,
		0,
		date.Location(),
	), nil
}

// GetBookingJSON converts a booking map to a JSON string
func (s *BookingVideoService) GetBookingJSON(booking map[string]interface{}) string {
	jsonBytes, err := json.Marshal(booking)
	if err != nil {
		log.Printf("Error marshaling booking to JSON: %v", err)
		return ""
	}
	return string(jsonBytes)
}

// determineStorageInfo determines which storage disk contains the given file path
// and returns the disk ID and full absolute path
func (s *BookingVideoService) determineStorageInfo(filePath string) (string, string, error) {
	// Get absolute path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to get absolute path: %v", err)
	}
	
	// Get all storage disks from database
	disks, err := s.db.GetStorageDisks()
	if err != nil {
		return "", "", fmt.Errorf("failed to get storage disks: %v", err)
	}
	
	// Find which disk contains this file
	for _, disk := range disks {
		// Check if the file path starts with the disk path
		diskPath := filepath.Clean(disk.Path)
		if strings.HasPrefix(absPath, diskPath) {
			log.Printf("determineStorageInfo: File %s found on disk %s (%s)", absPath, disk.ID, diskPath)
			return disk.ID, absPath, nil
		}
	}
	
	// If no disk found, try to determine from current storage path
	// This handles cases where the file is in the primary storage path
	if strings.Contains(absPath, s.config.StoragePath) {
		// Try to find a disk that matches the storage path
		for _, disk := range disks {
			if disk.Path == s.config.StoragePath {
				log.Printf("determineStorageInfo: File %s matched primary storage disk %s", absPath, disk.ID)
				return disk.ID, absPath, nil
			}
		}
	}
	
	log.Printf("determineStorageInfo: Could not determine storage disk for file: %s", absPath)
	return "", absPath, fmt.Errorf("could not determine storage disk for file: %s", absPath)
}
