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
	}

	for _, tt := range tests {
		s, ep := parseSeasonEpisode(tt.path)
		if s != tt.expectedS || ep != tt.expectedEp {
			t.Errorf("parseSeasonEpisode(%q) = (%d, %d); want (%d, %d)", tt.path, s, ep, tt.expectedS, tt.expectedEp)
		}
	}
}
