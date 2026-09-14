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
		t.Fatalf("Expected hit for Mutiny, got nil")
	}
	t.Logf("Matched tconst: %s, title: %s (%s), poster: %v", hit.Movie.Tconst, hit.Movie.TitlePrimary, hit.Movie.TitleOrig, hit.PosterPath)
}
