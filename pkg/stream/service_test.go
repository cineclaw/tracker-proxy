package stream

import (
	"testing"
)

func TestSelectDefaultAudioTrack(t *testing.T) {
	tests := []struct {
		name     string
		tracks   []AudioTrack
		expected int
	}{
		{
			name:     "Empty tracks",
			tracks:   []AudioTrack{},
			expected: 0,
		},
		{
			name: "English first, Russian DUB second, Russian MVO third",
			tracks: []AudioTrack{
				{Index: 0, Title: "English", Language: "en", Channels: 6},
				{Index: 1, Title: "HDrezka Studio (DUB)", Language: "ru", Channels: 2},
				{Index: 2, Title: "HDrezka Studio (MVO)", Language: "ru", Channels: 2},
			},
			expected: 1, // Should select Russian DUB
		},
		{
			name: "Russian MVO first, English second",
			tracks: []AudioTrack{
				{Index: 0, Title: "HDrezka Studio (MVO)", Language: "ru", Channels: 2},
				{Index: 1, Title: "Original English", Language: "en", Channels: 6},
			},
			expected: 0, // Should select Russian MVO
		},
		{
			name: "Russian DUB vs Russian MVO",
			tracks: []AudioTrack{
				{Index: 0, Title: "MVO LostFilm", Language: "ru", Channels: 2},
				{Index: 1, Title: "Дубляж (DUB)", Language: "ru", Channels: 6},
			},
			expected: 1, // DUB has higher score
		},
		{
			name: "Only foreign tracks",
			tracks: []AudioTrack{
				{Index: 0, Title: "English", Language: "en", Channels: 6},
				{Index: 1, Title: "Spanish", Language: "es", Channels: 2},
			},
			expected: 0, // First foreign track
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := selectDefaultAudioTrack(tt.tracks)
			if result != tt.expected {
				t.Errorf("expected index %d, got %d", tt.expected, result)
			}
		})
	}
}
