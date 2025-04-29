package recording

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FindSegmentsInRange returns a sorted list of MP4 files in inputPath whose timestamps fall within [startTime, endTime].
func FindSegmentsInRange(inputPath string, startTime, endTime time.Time) ([]string, error) {
	var matches []struct {
		path string
		ts   time.Time
	}

	entries, err := os.ReadDir(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp4") {
			continue
		}
		base := entry.Name()
		// Expect format: camera_name_YYYYMMDD_HHMMSS.mp4
		parts := strings.Split(base, "_")
		if len(parts) < 3 {
			continue // not a valid segment filename
		}
		dateStr := parts[len(parts)-2]
		timeStr := strings.TrimSuffix(parts[len(parts)-1], ".mp4")
		ts, err := time.Parse("20060102_150405", dateStr+"_"+timeStr)
		if err != nil {
			continue // skip invalid timestamp
		}
		if !ts.Before(startTime) && !ts.After(endTime) {
			matches = append(matches, struct {
				path string
				ts   time.Time
			}{filepath.Join(inputPath, base), ts})
		}
	}

	// Sort by timestamp ascending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].ts.Before(matches[j].ts)
	})

	result := make([]string, len(matches))
	for i, m := range matches {
		result[i] = m.path
	}
	return result, nil
}
