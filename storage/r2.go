package storage

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

// R2Config holds configuration for Cloudflare R2 storage
type R2Config struct {
	AccessKey string
	SecretKey string
	AccountID string
	Bucket    string
	Endpoint  string
	Region    string
	BaseURL   string // URL publik untuk akses file, contoh: https://media.beligem.com
}

// R2Storage handles operations with Cloudflare R2
type R2Storage struct {
	config   R2Config
	session  *session.Session
	client   *s3.S3
	uploader *s3manager.Uploader
}

// NewR2Storage creates a new R2Storage instance
func NewR2Storage(config R2Config) (*R2Storage, error) {
	// Set default region if not provided
	if config.Region == "" {
		config.Region = "auto"
	}

	// Create endpoint URL if AccountID is provided but full endpoint isn't
	if config.Endpoint == "" && config.AccountID != "" {
		config.Endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", config.AccountID)
	}

	// Create AWS session
	sess, err := session.NewSession(&aws.Config{
		Credentials: credentials.NewStaticCredentials(config.AccessKey, config.SecretKey, ""),
		Endpoint:    aws.String(config.Endpoint),
		Region:      aws.String(config.Region),
		// Force path style addressing for compatibility with S3 API
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %v", err)
	}

	// Create S3 client
	client := s3.New(sess)

	// Create uploader
	uploader := s3manager.NewUploader(sess)

	return &R2Storage{
		config:   config,
		session:  sess,
		client:   client,
		uploader: uploader,
	}, nil
}

// UploadFile uploads a file to R2 storage
func (r *R2Storage) UploadFile(localPath, remotePath string) (string, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %v", localPath, err)
	}
	defer file.Close()

	// Get file info for metadata
	fileInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to get file info: %v", err)
	}

	// Determine content type based on file extension
	contentType := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(localPath)) {
	case ".mp4":
		contentType = "video/mp4"
	case ".ts":
		contentType = "video/MP2T"
	case ".m3u8":
		contentType = "application/vnd.apple.mpegurl"
	}

	// Upload file
	log.Printf("Uploading %s to R2 bucket %s with key %s", localPath, r.config.Bucket, remotePath)

	result, err := r.uploader.Upload(&s3manager.UploadInput{
		Bucket:      aws.String(r.config.Bucket),
		Key:         aws.String(remotePath),
		Body:        file,
		ContentType: aws.String(contentType),
		Metadata: map[string]*string{
			"OriginalFileName": aws.String(filepath.Base(localPath)),
			"UploadedAt":       aws.String(time.Now().Format(time.RFC3339)),
			"FileSize":         aws.String(fmt.Sprintf("%d", fileInfo.Size())),
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to upload file to R2: %v", err)
	}

	// Generate public URL using the configured BaseURL instead of AWS S3 location
	publicURL := fmt.Sprintf("%s/%s", r.GetBaseURL(), remotePath)

	// Log the URL difference for debugging
	log.Printf("AWS S3 URL: %s, Custom URL: %s", result.Location, publicURL)
	
	return publicURL, nil
}

// UploadDirectory uploads all files in a directory to R2
func (r *R2Storage) UploadDirectory(localDir, remotePrefix string) ([]string, error) {
	var uploadedFiles []string

	err := filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Calculate remote path
		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return fmt.Errorf("failed to determine relative path: %v", err)
		}

		remotePath := filepath.Join(remotePrefix, relPath)
		// Ensure forward slashes for S3 keys
		remotePath = strings.ReplaceAll(remotePath, "\\", "/")

		// Upload file
		location, err := r.UploadFile(path, remotePath)
		if err != nil {
			return fmt.Errorf("failed to upload %s: %v", path, err)
		}

		uploadedFiles = append(uploadedFiles, location)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("directory upload failed: %v", err)
	}

	return uploadedFiles, nil
}

// UploadHLSStream uploads an HLS stream directory to R2
func (r *R2Storage) UploadHLSStream(hlsDir, videoID string) (string, string, error) {
	remotePrefix := fmt.Sprintf("hls/%s", videoID)
	_, err := r.UploadDirectory(hlsDir, remotePrefix)
	if err != nil {
		return "", "", fmt.Errorf("failed to upload HLS stream: %v", err)
	}
	// Kembalikan path dan URL
	r2Path := remotePrefix
	r2URL := fmt.Sprintf("%s/%s/playlist.m3u8", r.GetBaseURL(), remotePrefix)
	return r2Path, r2URL, nil
}

// Upload MP4 to R2
func (r *R2Storage) UploadMP4(mp4Dir, videoID string) (string, error) {
	remotePrefix := fmt.Sprintf("mp4/%s%s", videoID, filepath.Ext(mp4Dir))

	log.Printf("Uploading MP4 %s to R2 bucket %s with key %s", mp4Dir, r.config.Bucket, remotePrefix)

	// Use UploadFile instead of UploadDirectory since we're uploading a single file
	_, err := r.UploadFile(mp4Dir, remotePrefix)
	if err != nil {
		return "", fmt.Errorf("failed to upload MP4: %v", err)
	}

	// Gunakan GetBaseURL() untuk mendapatkan URL publik yang benar
	publicURL := fmt.Sprintf("%s/%s", r.GetBaseURL(), remotePrefix)
	log.Printf("MP4 URL: %s", publicURL)
	return publicURL, nil
}

// ListObjects lists objects in the R2 bucket with a given prefix
func (r *R2Storage) ListObjects(prefix string) ([]*s3.Object, error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(r.config.Bucket),
		Prefix: aws.String(prefix),
	}

	result, err := r.client.ListObjectsV2(input)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %v", err)
	}

	return result.Contents, nil
}

// DeleteObject deletes an object from the R2 bucket
func (r *R2Storage) DeleteObject(key string) error {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(r.config.Bucket),
		Key:    aws.String(key),
	}

	_, err := r.client.DeleteObject(input)
	if err != nil {
		return fmt.Errorf("failed to delete object: %v", err)
	}

	return nil
}

// GetBaseURL returns the base URL for the R2 bucket
func (r *R2Storage) GetBaseURL() string {
	// Gunakan BaseURL jika ada, jika tidak gunakan endpoint + bucket
	if r.config.BaseURL != "" {
		return r.config.BaseURL
	}
	
	// Fallback ke endpoint/bucket jika BaseURL tidak tersedia
	return fmt.Sprintf("%s/%s", r.config.Endpoint, r.config.Bucket)
}



