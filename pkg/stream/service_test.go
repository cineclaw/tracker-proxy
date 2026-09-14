package stream

import (
	"testing"

	"tracker-proxy/pkg/models"
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

func TestFilterCandidates(t *testing.T) {
	metaRunner2026 := &IndexerMeta{
		Tconst:        "tt31349844",
		Title:         "Курьер",
		OriginalTitle: "Runner",
		Year:          2026,
	}

	candidates := []models.TorrentResult{
		{
			Title: "Курьер (Карен Шахназаров) [1986, СССР, драма, WEB-DL 1080p]",
			Seeds: 50,
		},
		{
			Title: "Курьер / The Courier (Закари Адлер) [2019, США, боевик, BDRip 1080p]",
			Seeds: 15,
		},
		{
			Title: "Курьер / El correo / The Courier (Даниэль Кальпарсоро) [2024, Испания, WEB-DLRip]",
			Seeds: 34,
		},
		{
			Title: "Курьер. Доставить любой ценой (2025) WEB-DL [H.264/1080p]",
			Seeds: 2,
		},
		{
			Title: "Дмитрий Ра, Вова Бо - Запечатанный мир 1, Имперский Курьер. Том 1 (2025) МР3",
			Seeds: 4,
		},
	}

	filtered := FilterCandidates(candidates, metaRunner2026, 0, "movie", 2026)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 candidate for Runner 2026, got %d", len(filtered))
	}
	if filtered[0].Title != "Курьер. Доставить любой ценой (2025) WEB-DL [H.264/1080p]" {
		t.Errorf("unexpected filtered title: %s", filtered[0].Title)
	}

	// TV Series test
	metaBreakingBad := &IndexerMeta{
		Tconst:        "tt0903747",
		Title:         "Во все тяжкие",
		OriginalTitle: "Breaking Bad",
		Year:          2008,
	}

	tvCandidates := []models.TorrentResult{
		{
			Title: "Во все тяжкие / Breaking Bad / Сезон: 1 [2008, США, BDRip-AVC]",
			Seeds: 20,
		},
		{
			Title: "Во все тяжкие / Breaking Bad / Сезоны: 1-5 [2008-2013, США, BDRip 720p]",
			Seeds: 45,
		},
		{
			Title: "Во все тяжкие [1985, драма, BDRip]",
			Seeds: 10,
		},
	}

	filteredTV := FilterCandidates(tvCandidates, metaBreakingBad, 0, "tv", 2008)
	if len(filteredTV) != 2 {
		t.Fatalf("expected 2 candidates for Breaking Bad, got %d", len(filteredTV))
	}
}
