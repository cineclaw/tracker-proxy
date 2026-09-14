package stream

import (
	"testing"
)

func TestParseSeasonEpisode(t *testing.T) {
	tests := []struct {
		path      string
		expectedS int
		expectedE int
	}{
		{"Silo.S01E02.1080p.mkv", 1, 2},
		{"Silo - 01x02 - Episode Two.mkv", 1, 2},
		{"[LostFilm] Silo.s01.e02.mkv", 1, 2},
		{"Season 1/02.mkv", 1, 2},
		{"Сезон 1/02 серия.mkv", 1, 2},
		{"Сезон 01/Серия 03.mkv", 1, 3},
		{"Silo [02] [1080p].mkv", 0, 2},
		{"Silo_e04.mkv", 0, 4},
		{"Severance.S02E01.HDR.mkv", 2, 1},
		{"1 сезон/04.mkv", 1, 4},
		{"2 сезон/04.mkv", 2, 4},
		{"2-й сезон/04 серия.mkv", 2, 4},
		{"Сезон II/04.mkv", 2, 4},
		{"Season.2/04.mkv", 2, 4},
		{"Season_02/04.mkv", 2, 4},
		{"Show.S02.1080p/04.mkv", 2, 4},
		{"Show.204.Commendatori.mkv", 2, 4},
	}

	for _, tt := range tests {
		s, e := ParseSeasonEpisode(tt.path)
		if s != tt.expectedS || e != tt.expectedE {
			t.Errorf("ParseSeasonEpisode(%q) = (%d, %d), expected (%d, %d)", tt.path, s, e, tt.expectedS, tt.expectedE)
		}
	}
}

func TestParseSeasonEpisodeRange(t *testing.T) {
	tests := []struct {
		path       string
		expectedS  int
		expectedE1 int
		expectedE2 int
	}{
		{"Show.S02E01-E02.mkv", 2, 1, 2},
		{"Show.S02E01-02.mkv", 2, 1, 2},
		{"2 сезон/01-02 серии.mkv", 2, 1, 2},
		{"2 сезон/03.mkv", 2, 3, 3},
	}

	for _, tt := range tests {
		s, e1, e2 := ParseSeasonEpisodeRange(tt.path)
		if s != tt.expectedS || e1 != tt.expectedE1 || e2 != tt.expectedE2 {
			t.Errorf("ParseSeasonEpisodeRange(%q) = (%d, %d, %d), expected (%d, %d, %d)",
				tt.path, s, e1, e2, tt.expectedS, tt.expectedE1, tt.expectedE2)
		}
	}
}

func TestMatchFile(t *testing.T) {
	client := NewTorrClient("http://127.0.0.1:8092")
	rec := &TorrentRecord{
		Title: "Silo Season 1 Complete",
		FileStats: []TorrentFileStat{
			{ID: 1, Path: "Silo/Silo.S01E01.mkv", Length: 1000},
			{ID: 2, Path: "Silo/Silo.S01E02.mkv", Length: 1000},
			{ID: 3, Path: "Silo/Silo.S01E03.mkv", Length: 1000},
			{ID: 4, Path: "Silo/sample.mkv", Length: 100},
		},
	}

	matched, err := client.MatchFile(rec, 1, 2)
	if err != nil {
		t.Fatalf("MatchFile failed: %v", err)
	}
	if matched.ID != 2 {
		t.Errorf("expected file ID 2, got %d", matched.ID)
	}
}

func TestMatchFile_MultiSeasonCollisionAvoidance(t *testing.T) {
	client := NewTorrClient("http://127.0.0.1:8092")
	rec := &TorrentRecord{
		Title: "The Sopranos S01-S06 Complete BDRip",
		FileStats: []TorrentFileStat{
			{ID: 1, Path: "The Sopranos/Season 1/The.Sopranos.S01E01.mkv", Length: 1000},
			{ID: 2, Path: "The Sopranos/Season 1/The.Sopranos.S01E02.mkv", Length: 1000},
			{ID: 3, Path: "The Sopranos/Season 1/The.Sopranos.S01E03.mkv", Length: 1000},
			{ID: 4, Path: "The Sopranos/Season 1/The.Sopranos.S01E04.mkv", Length: 1000},
			{ID: 14, Path: "The Sopranos/Season 2/The.Sopranos.S02E01.mkv", Length: 1000},
			{ID: 15, Path: "The Sopranos/Season 2/The.Sopranos.S02E02.mkv", Length: 1000},
			{ID: 16, Path: "The Sopranos/Season 2/The.Sopranos.S02E03.mkv", Length: 1000},
			{ID: 17, Path: "The Sopranos/Season 2/The.Sopranos.S02E04.mkv", Length: 1000},
		},
	}

	// Season 2 Episode 4 must match ID 17, NEVER ID 4
	matched, err := client.MatchFile(rec, 2, 4)
	if err != nil {
		t.Fatalf("MatchFile(2, 4) failed: %v", err)
	}
	if matched.ID != 17 {
		t.Fatalf("CRITICAL BUG: MatchFile(2, 4) returned ID %d (%s), expected ID 17 (Season 2 Ep 4)", matched.ID, matched.Path)
	}

	// Season 1 Episode 4 must match ID 4
	matchedS1, err := client.MatchFile(rec, 1, 4)
	if err != nil {
		t.Fatalf("MatchFile(1, 4) failed: %v", err)
	}
	if matchedS1.ID != 4 {
		t.Fatalf("MatchFile(1, 4) returned ID %d, expected ID 4", matchedS1.ID)
	}

	// Non-existent season must return an error and NOT fall back to Season 1
	_, errMissing := client.MatchFile(rec, 7, 1)
	if errMissing == nil {
		t.Fatalf("MatchFile(7, 1) should have returned error for missing season in multi-season pack")
	}
}

func TestMatchFile_RussianFolderMultiSeason(t *testing.T) {
	client := NewTorrClient("http://127.0.0.1:8092")
	rec := &TorrentRecord{
		Title: "Клан Сопрано (сезоны 1-6) [BDRip 1080p]",
		FileStats: []TorrentFileStat{
			{ID: 1, Path: "1 сезон/01. Команда Сопрано.avi", Length: 1000},
			{ID: 2, Path: "1 сезон/02. Серая мышь.avi", Length: 1000},
			{ID: 3, Path: "1 сезон/04. Луг.avi", Length: 1000},
			{ID: 4, Path: "2 сезон/01. Парень идет в офис.avi", Length: 1000},
			{ID: 5, Path: "2 сезон/02. Не реанимировать.avi", Length: 1000},
			{ID: 6, Path: "2 сезон/04. Серия 04.avi", Length: 1000},
		},
	}

	matched, err := client.MatchFile(rec, 2, 4)
	if err != nil {
		t.Fatalf("MatchFile(2, 4) failed: %v", err)
	}
	if matched.ID != 6 {
		t.Fatalf("expected ID 6 (2 сезон/04), got ID %d (%s)", matched.ID, matched.Path)
	}
}

func TestMatchFile_DoubleEpisodeRange(t *testing.T) {
	client := NewTorrClient("http://127.0.0.1:8092")
	rec := &TorrentRecord{
		Title: "Show S02 Complete",
		FileStats: []TorrentFileStat{
			{ID: 1, Path: "Show.S02E01-E02.1080p.mkv", Length: 2000},
			{ID: 2, Path: "Show.S02E03-E04.1080p.mkv", Length: 2000},
		},
	}

	// Ep 1 matches file 1
	m1, err := client.MatchFile(rec, 2, 1)
	if err != nil || m1.ID != 1 {
		t.Fatalf("MatchFile(2, 1) expected ID 1, got %v (err: %v)", m1, err)
	}

	// Ep 2 also matches file 1
	m2, err := client.MatchFile(rec, 2, 2)
	if err != nil || m2.ID != 1 {
		t.Fatalf("MatchFile(2, 2) expected ID 1, got %v (err: %v)", m2, err)
	}

	// Ep 4 matches file 2
	m4, err := client.MatchFile(rec, 2, 4)
	if err != nil || m4.ID != 2 {
		t.Fatalf("MatchFile(2, 4) expected ID 2, got %v (err: %v)", m4, err)
	}
}

func TestMatchFile_NaturalSortFallback(t *testing.T) {
	client := NewTorrClient("http://127.0.0.1:8092")
	rec := &TorrentRecord{
		Title: "Multi-Season Show",
		FileStats: []TorrentFileStat{
			{ID: 1, Path: "1 сезон/Первая серия.mkv", Length: 1000},
			{ID: 2, Path: "1 сезон/Вторая серия.mkv", Length: 1000},
			{ID: 3, Path: "2 сезон/01. Начало.mkv", Length: 1000},
			{ID: 4, Path: "2 сезон/02. Развитие.mkv", Length: 1000},
			{ID: 5, Path: "2 сезон/03. Кульминация.mkv", Length: 1000},
			{ID: 6, Path: "2 сезон/04. Финал.mkv", Length: 1000},
		},
	}

	matched, err := client.MatchFile(rec, 2, 4)
	if err != nil {
		t.Fatalf("MatchFile(2, 4) failed: %v", err)
	}
	if matched.ID != 6 {
		t.Fatalf("expected ID 6, got %d (%s)", matched.ID, matched.Path)
	}
}

func TestMatchFile_ContinuousEpisodeNumbering(t *testing.T) {
	client := NewTorrClient("http://127.0.0.1:8092")
	rec := &TorrentRecord{
		Title: "Anime Pack S01-S02",
		FileStats: []TorrentFileStat{
			{ID: 1, Path: "Season 1/01.mkv", Length: 500},
			{ID: 12, Path: "Season 1/12.mkv", Length: 500},
			{ID: 13, Path: "Season 2/13.mkv", Length: 500},
			{ID: 14, Path: "Season 2/14.mkv", Length: 500},
			{ID: 15, Path: "Season 2/15.mkv", Length: 500},
			{ID: 16, Path: "Season 2/16.mkv", Length: 500}, // Season 2 Episode 4 = absolute 16
		},
	}

	// Season 2 Episode 4 should map to 16.mkv (offset from minEp 13 + 4 - 1 = 16)
	matched, err := client.MatchFile(rec, 2, 4)
	if err != nil {
		t.Fatalf("MatchFile(2, 4) failed: %v", err)
	}
	if matched.ID != 16 {
		t.Fatalf("expected ID 16, got %d (%s)", matched.ID, matched.Path)
	}
}
