package streaming

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/nareix/joy4/av"
	"github.com/nareix/joy4/format/rtsp"
)

// StreamManager handles multiple RTSP streams
type StreamManager struct {
	streams map[string]*Stream
	mutex   sync.RWMutex
}

// Stream represents a single RTSP stream
type Stream struct {
	URL        string
	RTSPClient *rtsp.Client
	Codecs     []av.CodecData
	Running    bool
	Error      error
	mutex      sync.RWMutex
	HLSMuxer   *HLSMuxer
}

// NewStreamManager creates a new stream manager
func NewStreamManager() *StreamManager {
	return &StreamManager{
		streams: make(map[string]*Stream),
	}
}

// AddStream adds a new RTSP stream
func (sm *StreamManager) AddStream(id, url string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if _, exists := sm.streams[id]; exists {
		return fmt.Errorf("stream %s already exists", id)
	}

	stream := &Stream{
		URL:      url,
		Running:  false,
		HLSMuxer: NewHLSMuxer(filepath.Join("/Users/windalfinculmen/Projects/ayo-mwr/videos/recordings", id, "hls")),
	}

	sm.streams[id] = stream
	go stream.Start()
	return nil
}

// GetStream returns a stream by ID
func (sm *StreamManager) GetStream(id string) (*Stream, bool) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	stream, ok := sm.streams[id]
	return stream, ok
}

// RemoveStream removes a stream
func (sm *StreamManager) RemoveStream(id string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if stream, ok := sm.streams[id]; ok {
		stream.Stop()
		delete(sm.streams, id)
	}
}

// Start starts the RTSP stream
func (s *Stream) Start() {
	s.mutex.Lock()
	if s.Running {
		s.mutex.Unlock()
		return
	}
	s.Running = true
	s.mutex.Unlock()

	for {
		if !s.Running {
			return
		}

		err := s.connect()
		if err != nil {
			log.Printf("Error connecting to stream: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for s.Running {
			pkt, err := s.RTSPClient.ReadPacket()
			if err != nil {
				log.Printf("Error reading packet: %v", err)
				break
			}

			// Reset packet time for HLS segmentation
			pkt.Time = pkt.Time % hlsSegmentDuration
			err = s.HLSMuxer.WritePacket(pkt)
			if err != nil {
				log.Printf("Error writing to HLS: %v", err)
				break
			}
		}

		s.RTSPClient.Close()
		time.Sleep(5 * time.Second)
	}
}

// Stop stops the RTSP stream
func (s *Stream) Stop() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.Running = false
	if s.RTSPClient != nil {
		s.RTSPClient.Close()
	}
}

// connect establishes the RTSP connection
func (s *Stream) connect() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	client, err := rtsp.Dial(s.URL)
	if err != nil {
		return fmt.Errorf("failed to connect to RTSP: %v", err)
	}

	s.RTSPClient = client
	// In joy4, streams() returns codec data
	streams, err := client.Streams()
	if err != nil {
		return fmt.Errorf("failed to get codec data: %v", err)
	}
	s.Codecs = streams

	return nil
}
