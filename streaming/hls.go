package streaming

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nareix/joy4/av"
	"github.com/nareix/joy4/format/ts"
)

const (
	hlsSegmentDuration = 2 * time.Second
	hlsMaxSegments     = 6
	hlsTargetDuration  = 2
)

// HLSMuxer handles HLS streaming
type HLSMuxer struct {
	OutputPath  string
	segmentID   int
	muxer       *ts.Muxer
	currentFile *os.File
	segments    []string
	mutex       sync.Mutex
}

// NewHLSMuxer creates a new HLS muxer
func NewHLSMuxer(outputPath string) *HLSMuxer {
	// Create output directory if it doesn't exist
	err := os.MkdirAll(outputPath, 0755)
	if err != nil {
		fmt.Printf("Error creating HLS directory: %v\n", err)
	}

	return &HLSMuxer{
		OutputPath: outputPath,
		segments:   make([]string, 0),
	}
}

// WritePacket writes a packet to the current segment
func (h *HLSMuxer) WritePacket(pkt av.Packet) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	// Create first segment if it doesn't exist
	if h.muxer == nil {
		if err := h.createNewSegment(); err != nil {
			return err
		}
	}

	// Write packet
	if err := h.muxer.WritePacket(pkt); err != nil {
		return err
	}

	// Create new segment if duration exceeded
	if pkt.Time >= time.Duration(hlsTargetDuration*float64(time.Second)) {
		if err := h.createNewSegment(); err != nil {
			return err
		}
	}

	return nil
}

// createNewSegment creates a new TS segment
func (h *HLSMuxer) createNewSegment() error {
	// Close current file if it exists
	if h.currentFile != nil {
		h.currentFile.Close()
	}

	// Create new segment file
	segmentPath := filepath.Join(h.OutputPath, fmt.Sprintf("segment_%d.ts", h.segmentID))
	file, err := os.Create(segmentPath)
	if err != nil {
		return err
	}
	h.currentFile = file

	// Create new muxer
	h.muxer = ts.NewMuxer(file)

	// Add segment to list
	h.segments = append(h.segments, segmentPath)

	// Remove old segments
	if len(h.segments) > hlsMaxSegments {
		oldSegment := h.segments[0]
		h.segments = h.segments[1:]
		os.Remove(oldSegment)
	}

	h.segmentID++
	return nil
}

// GenerateM3U8 generates the HLS playlist
func (h *HLSMuxer) GenerateM3U8() string {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	m3u8 := "#EXTM3U\n"
	m3u8 += "#EXT-X-VERSION:3\n"
	m3u8 += fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", hlsTargetDuration)
	m3u8 += fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", h.segmentID-len(h.segments))

	for i := range h.segments {
		m3u8 += "#EXTINF:2.0,\n"
		m3u8 += fmt.Sprintf("segment_%d.ts\n", i)
	}

	return m3u8
}

// ServeHTTP implements http.Handler
func (h *HLSMuxer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	filename := filepath.Base(r.URL.Path)

	if filename == "stream.m3u8" {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Write([]byte(h.GenerateM3U8()))
		return
	}

	// Serve segment file
	http.ServeFile(w, r, filepath.Join(h.OutputPath, filename))
}
