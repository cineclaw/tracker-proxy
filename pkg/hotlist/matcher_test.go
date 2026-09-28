package hotlist

import (
	"context"
	"testing"
	"time"
)

func TestMatcherLookupMutiny(t *testing.T) {
	m := NewMatcher("http://127.0.0.1:8090")
	item := RawTorrent{
		RussianTitle:  "Мятеж",
		OriginalTitle: "Mutiny",
		Year:          2026,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hit := m.lookup(ctx, item, "movie")
	if hit == nil {
		t.Skip("indexer not running locally at http://127.0.0.1:8090, skipping integration test")
	}
	t.Logf("Matched tconst: %s, title: %s (%s), poster: %v", hit.Movie.Tconst, hit.Movie.TitlePrimary, hit.Movie.TitleOrig, hit.PosterPath)
}

func TestCleanHotlistQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Ну, погоди! Каникулы [04х01-24 из 26]", "Ну погоди Каникулы"},
		{"Despicable Me 4 / Гадкий я 4", "Despicable Me 4 Гадкий я 4"},
		{"Straume (2024) [1080p]", "Straume"},
		{"Spider-Man: Across the Spider-Verse", "Spider-Man Across the Spider-Verse"},
	}

	for _, tt := range tests {
		got := cleanHotlistQuery(tt.input)
		if got != tt.want {
			t.Errorf("cleanHotlistQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSelectBestHit(t *testing.T) {
	poster1 := "/poster1.jpg"
	poster2 := "/poster2.jpg"
	year2024 := 2024

	hits := []IndexerHit{
		{
			// Undesirable: tvEpisode without poster
			Movie: struct {
				Tconst         string   `json:"tconst"`
				TitleRu        *string  `json:"title_ru"`
				TitleOrig      string   `json:"title_orig"`
				TitlePrimary   string   `json:"title_primary"`
				RussianTitles  []string `json:"russian_titles"`
				Year           *int     `json:"year"`
				TitleType      string   `json:"title_type"`
				Rating         *float64 `json:"rating"`
				NumVotes       int      `json:"num_votes"`
				Genres         []string `json:"genres"`
				RuntimeMinutes *int     `json:"runtime_minutes"`
			}{
				Tconst:    "tt1",
				TitleOrig: "FLOW",
				TitleType: "tvEpisode",
				Year:      &year2024,
			},
			PosterPath: nil,
		},
		{
			// Desirable: movie with poster and matching year
			Movie: struct {
				Tconst         string   `json:"tconst"`
				TitleRu        *string  `json:"title_ru"`
				TitleOrig      string   `json:"title_orig"`
				TitlePrimary   string   `json:"title_primary"`
				RussianTitles  []string `json:"russian_titles"`
				Year           *int     `json:"year"`
				TitleType      string   `json:"title_type"`
				Rating         *float64 `json:"rating"`
				NumVotes       int      `json:"num_votes"`
				Genres         []string `json:"genres"`
				RuntimeMinutes *int     `json:"runtime_minutes"`
			}{
				Tconst:    "tt2",
				TitleOrig: "Straume",
				TitleType: "movie",
				Year:      &year2024,
			},
			PosterPath: &poster1,
		},
		{
			// Movie with poster
			Movie: struct {
				Tconst         string   `json:"tconst"`
				TitleRu        *string  `json:"title_ru"`
				TitleOrig      string   `json:"title_orig"`
				TitlePrimary   string   `json:"title_primary"`
				RussianTitles  []string `json:"russian_titles"`
				Year           *int     `json:"year"`
				TitleType      string   `json:"title_type"`
				Rating         *float64 `json:"rating"`
				NumVotes       int      `json:"num_votes"`
				Genres         []string `json:"genres"`
				RuntimeMinutes *int     `json:"runtime_minutes"`
			}{
				Tconst:    "tt3",
				TitleOrig: "Flow",
				TitleType: "movie",
				Year:      &year2024,
			},
			PosterPath: &poster2,
		},
	}

	target := RawTorrent{
		OriginalTitle: "Flow",
		RussianTitle:  "Поток",
		Year:          2024,
	}

	best := selectBestHit(hits, target, "Flow")
	if best == nil || best.Movie.Tconst != "tt3" {
		t.Fatalf("expected tt3 (Flow movie with poster), got %v", best)
	}
}
