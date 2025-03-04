package service

import (
	"ayo-mwr/database"
)

// mockDatabase implements the Database interface for testing
type mockDatabase struct {
	videos map[string]database.VideoMetadata
}

func newMockDatabase() *mockDatabase {
	return &mockDatabase{
		videos: make(map[string]database.VideoMetadata),
	}
}

func (m *mockDatabase) CreateVideo(metadata database.VideoMetadata) error {
	m.videos[metadata.ID] = metadata
	return nil
}

func (m *mockDatabase) GetVideo(id string) (*database.VideoMetadata, error) {
	if video, exists := m.videos[id]; exists {
		return &video, nil
	}
	return nil, nil
}

func (m *mockDatabase) UpdateVideo(metadata database.VideoMetadata) error {
	m.videos[metadata.ID] = metadata
	return nil
}

func (m *mockDatabase) ListVideos(limit, offset int) ([]database.VideoMetadata, error) {
	var result []database.VideoMetadata
	count := 0

	for _, video := range m.videos {
		if count >= offset && count < offset+limit {
			result = append(result, video)
		}
		count++
		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

func (m *mockDatabase) DeleteVideo(id string) error {
	delete(m.videos, id)
	return nil
}

func (m *mockDatabase) GetVideosByStatus(status database.VideoStatus, limit, offset int) ([]database.VideoMetadata, error) {
	var result []database.VideoMetadata
	count := 0

	for _, video := range m.videos {
		if video.Status == status {
			if count >= offset && count < offset+limit {
				result = append(result, video)
			}
			count++
			if len(result) >= limit {
				break
			}
		}
	}

	return result, nil
}
