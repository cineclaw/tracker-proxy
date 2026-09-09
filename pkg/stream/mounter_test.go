package stream

import (
	"testing"
)

func TestParseSeasonEpisode(t *testing.T) {
	tests := []struct {
		path       string
		expectedS  int
		expectedEp int
	}{
		{"The Sopranos S01E01 Pilot.mkv", 1, 1},
		{"The Sopranos/Season 1/01.mkv", 1, 1},
		{"The Sopranos/Season 2/05 - Member Only.mkv", 2, 5},
		{"The Sopranos/Сезон 3/3x04.mkv", 3, 4},
		{"The Sopranos/S04/04x12.mkv", 4, 12},
		{"The Sopranos/Season 5/The Sopranos - 08.mkv", 5, 8},
		{"The Sopranos/Season 6/Серия 15.mkv", 6, 15},
		{"The Sopranos/S02/Episode 03.mkv", 2, 3},
		{"The Sopranos Season 4/02.mkv", 4, 2},
		{"The Sopranos - 1x03 - Denial, Anger, Acceptance.mkv", 1, 3},
		// Codec & resolution false positive tests (must NOT match as episode numbers)
		{"Mayday.2026.HDR.2160p.WEB.H.265.mkv", 0, 0},
		{"Movie.2024.1080p.BluRay.x264.mkv", 0, 0},
		{"Movie.2023.720p.mkv", 0, 0},
		{"Movie.2022.480p.mkv", 0, 0},
	}

	for _, tt := range tests {
		s, ep := parseSeasonEpisode(tt.path)
		if s != tt.expectedS || ep != tt.expectedEp {
			t.Errorf("parseSeasonEpisode(%q) = (%d, %d); want (%d, %d)", tt.path, s, ep, tt.expectedS, tt.expectedEp)
		}
	}
}

func TestIsSampleFile(t *testing.T) {
	tests := []struct {
		path     string
		length   int64
		maxLen   int64
		isSample bool
	}{
		{"Mayday.2026.HDR.2160p.WEB.H.265.mkv", 20 * 1024 * 1024 * 1024, 20 * 1024 * 1024 * 1024, false},
		{"Sample/sample.mkv", 50 * 1024 * 1024, 20 * 1024 * 1024 * 1024, true},
		{"Mayday.sample.mkv", 80 * 1024 * 1024, 20 * 1024 * 1024 * 1024, true},
		{"Trailer/trailer.mp4", 30 * 1024 * 1024, 20 * 1024 * 1024 * 1024, true},
		{"Episode.01.mkv", 300 * 1024 * 1024, 300 * 1024 * 1024, false},
	}

	for _, tt := range tests {
		got := isSampleFile(tt.path, tt.length, tt.maxLen)
		if got != tt.isSample {
			t.Errorf("isSampleFile(%q, %d, %d) = %v; want %v", tt.path, tt.length, tt.maxLen, got, tt.isSample)
		}
	}
}

