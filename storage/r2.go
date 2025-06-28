package storage

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// Constants for multipart uploads
const (
	// 5MB is the minimum chunk size for multipart uploads in R2/S3
	minPartSize = 5 * 1024 * 1024 // 5MB
	// Files larger than this will use multipart upload (legacy path – no longer used but kept for reference)
	multipartThreshold = 100 * 1024 * 1024 // 100MB
	// Use this number of goroutines for concurrent chunk uploads (legacy path)
	maxUploadConcurrency = 10

	// Number of attempts for UploadFile retry loop
	maxUploadAttempts = 3
)

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

	// Create uploader with single-connection concurrency to respect limited bandwidth
	// We set PartSize to 10 MB (must be ≥ 5 MB) and Concurrency to 1 so that multipart
	// uploads are performed sequentially, ensuring only **one** HTTP connection is
	// active at a time.
	uploader := s3manager.NewUploader(sess, func(u *s3manager.Uploader) {
		u.PartSize = 10 * 1024 * 1024 // 10 MB
		u.Concurrency = 1             // single connection / no parallel parts
	})

	return &R2Storage{
		config:   config,
		session:  sess,
		client:   client,
		uploader: uploader,
	}, nil
}

// UploadFile uploads a file to R2 storage using chunked uploads for large files
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
		contentType = "video/mp2t"
	case ".m3u8":
		contentType = "application/vnd.apple.mpegurl"
	case ".png":
		contentType = "image/png"
	case ".jpg", ".jpeg":
		contentType = "image/jpeg"
	}

	// Common metadata for all upload methods
	metadata := map[string]*string{
		"OriginalFileName": aws.String(filepath.Base(localPath)),
		"UploadedAt":       aws.String(time.Now().Format(time.RFC3339)),
		"FileSize":         aws.String(fmt.Sprintf("%d", fileInfo.Size())),
	}

	// --- Single-connection upload (standard or multipart handled by SDK) ---
	log.Printf("Uploading file (%.2f MB) via single-connection uploader: %s", float64(fileInfo.Size())/1024/1024, localPath)

	// Ensure we read from the beginning
	if _, err := file.Seek(0, 0); err != nil {
		return "", fmt.Errorf("failed to seek to beginning of file: %v", err)
	}

	var lastErr error
	for attempt := 1; attempt <= maxUploadAttempts; attempt++ {
		// Ensure we start reading from the beginning each attempt
		if _, err := file.Seek(0, 0); err != nil {
			return "", fmt.Errorf("failed to seek to beginning of file: %v", err)
		}

		_, lastErr = r.uploader.Upload(&s3manager.UploadInput{
			Bucket:      aws.String(r.config.Bucket),
			Key:         aws.String(remotePath),
			Body:        file,
			ContentType: aws.String(contentType),
			Metadata:    metadata,
		})

		if lastErr == nil {
			break // success
		}

		log.Printf("Upload attempt %d/%d failed for %s: %v", attempt, maxUploadAttempts, localPath, lastErr)
		// Exponential backoff: 2s, 4s, ...
		time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
	}
	if lastErr != nil {
		return "", fmt.Errorf("failed to upload file to R2 after %d attempts: %v", maxUploadAttempts, lastErr)
	}

	// Generate public URL
	publicURL := fmt.Sprintf("%s/%s", r.GetBaseURL(), remotePath)
	log.Printf("File uploaded successfully, public URL: %s", publicURL)
	
	return publicURL, nil
}

// uploadLargeFile handles multipart upload for large files
func (r *R2Storage) uploadLargeFile(file *os.File, remotePath, contentType string, metadata map[string]*string, fileSize int64) (string, error) {
	// Calculate optimal part size (ensure it's at least 5MB)
	// Try to use approximately 10000 parts maximum (R2/S3 limit is 10,000 parts)
	partSize := max(minPartSize, fileSize/10000)
	
	log.Printf("Starting multipart upload with part size: %.2f MB", float64(partSize)/1024/1024)
	
	// Create the multipart upload
	createResp, err := r.client.CreateMultipartUpload(&s3.CreateMultipartUploadInput{
		Bucket:      aws.String(r.config.Bucket),
		Key:         aws.String(remotePath),
		ContentType: aws.String(contentType),
		Metadata:    metadata,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create multipart upload: %v", err)
	}
	
	uploadID := *createResp.UploadId
	log.Printf("Created multipart upload ID: %s", uploadID)
	
	// Calculate total number of parts
	numParts := (fileSize + partSize - 1) / partSize // Ceiling division
	
	// Use a wait group to track completion of all upload parts
	var wg sync.WaitGroup
	
	// Setup a semaphore to limit concurrency
	sem := make(chan struct{}, maxUploadConcurrency)
	
	// Channel to collect results
	completedParts := make([]*s3.CompletedPart, int(numParts))
	errChan := make(chan error, 1)
	
	// Channel closed flag to prevent sending on closed channels
	var errChanClosed bool
	var errChanMutex sync.Mutex
	
	// Function to safely send errors
	sendError := func(err error) {
		errChanMutex.Lock()
		defer errChanMutex.Unlock()
		if !errChanClosed {
			errChan <- err
			close(errChan)
			errChanClosed = true
		}
	}
	
	log.Printf("Uploading %d parts concurrently (max concurrency: %d)", numParts, maxUploadConcurrency)
	
	// Start uploading parts
	for partNum := int64(1); partNum <= numParts; partNum++ {
		wg.Add(1)
		
		// Acquire semaphore slot
		sem <- struct{}{}
		
		go func(partNum int64) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore slot
			
			// Calculate part boundaries
			start := (partNum - 1) * partSize
			end := min(partNum*partSize, fileSize)
			size := end - start
			
			// Create a buffer for this part
			partBuffer := make([]byte, size)
			
			// Position reader at the start of this part
			if _, err := file.Seek(start, 0); err != nil {
				sendError(fmt.Errorf("failed to seek to position %d: %v", start, err))
				return
			}
			
			// Read the part data
			if _, err := io.ReadFull(file, partBuffer); err != nil {
				sendError(fmt.Errorf("failed to read part %d: %v", partNum, err))
				return
			}
			
			// Upload the part
			resp, err := r.client.UploadPart(&s3.UploadPartInput{
				Body:          bytes.NewReader(partBuffer),
				Bucket:        aws.String(r.config.Bucket),
				Key:           aws.String(remotePath),
				PartNumber:    aws.Int64(partNum),
				UploadId:      aws.String(uploadID),
				ContentLength: aws.Int64(size),
			})
			
			if err != nil {
				sendError(fmt.Errorf("failed to upload part %d: %v", partNum, err))
				return
			}
			
			// Store the completed part info
			completedParts[partNum-1] = &s3.CompletedPart{
				ETag:       resp.ETag,
				PartNumber: aws.Int64(partNum),
			}
			
			log.Printf("Uploaded part %d/%d (%.2f MB)", partNum, numParts, float64(size)/1024/1024)
		}(partNum)
	}
	
	// Wait for all part uploads to complete
	wg.Wait()
	close(sem)
	
	// Check if any errors occurred
	select {
	case err := <-errChan:
		// Attempt to abort the upload
		_, abortErr := r.client.AbortMultipartUpload(&s3.AbortMultipartUploadInput{
			Bucket:   aws.String(r.config.Bucket),
			Key:      aws.String(remotePath),
			UploadId: aws.String(uploadID),
		})
		
		if abortErr != nil {
			log.Printf("Failed to abort multipart upload: %v", abortErr)
		}
		
		return "", err
	default:
		// No errors, proceed with completing the upload
	}
	
	// Complete the multipart upload
	completeInput := &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(r.config.Bucket),
		Key:      aws.String(remotePath),
		UploadId: aws.String(uploadID),
		MultipartUpload: &s3.CompletedMultipartUpload{
			Parts: completedParts,
		},
	}
	
	completeResp, err := r.client.CompleteMultipartUpload(completeInput)
	if err != nil {
		return "", fmt.Errorf("failed to complete multipart upload: %v", err)
	}
	
	log.Printf("Multipart upload completed successfully")
	return *completeResp.Location, nil
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
	r2URL := fmt.Sprintf("%s/%s/master.m3u8", r.GetBaseURL(), remotePrefix)
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

// min returns the smaller of two int64 values
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// max returns the larger of two int64 values
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
