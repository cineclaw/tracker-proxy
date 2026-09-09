package aggregator

import (
	"context"
	"strings"
	"testing"
	"time"

	"tracker-proxy/pkg/models"
	"tracker-proxy/pkg/tracker"
)

type mockResolver struct {
	hashes map[string]string
}

func (m *mockResolver) FetchInfoHash(ctx context.Context, id string) (string, error) {
	if hash, ok := m.hashes[id]; ok {
		return hash, nil
	}
	return "", nil
}

func TestCandidateIndicesTolerance(t *testing.T) {
	results := []models.TorrentResult{
		{Tracker: "rutracker", ID: "1", Size: 12401718067}, // 11.55 GB
		{Tracker: "rutor", ID: "2", Size: 12348030976},     // 11.50 GB (diff ~0.05 GB -> match)
		{Tracker: "nnmclub", ID: "3", Size: 16106127360},   // 15.00 GB (diff > 0.1 GB -> no match)
	}

	candidates := findCandidateIndices(results)
	if !candidates[0] || !candidates[1] {
		t.Errorf("expected items 0 and 1 to be candidates, got %v", candidates)
	}
	if candidates[2] {
		t.Errorf("expected item 2 NOT to be a candidate, got %v", candidates)
	}
}

func TestDeduplicateAndMerge_RuTrackerAndRuTor(t *testing.T) {
	sharedHash := "ef54bdf0601d6afd32f995a13c5c3bce733d95f2"

	results := []models.TorrentResult{
		{
			Tracker:     "rutracker",
			ID:          "6813955",
			Title:       "Титаник / Titanic (1997) BDRip 1080p",
			Size:        12401718067,
			SizeHuman:   "11.55 GB",
			Seeds:       20,
			Leeches:     5,
			DetailsURL:  "https://rutracker.org/forum/viewtopic.php?t=6813955",
			DownloadURL: "https://rutracker.org/forum/dl.php?t=6813955",
			Resolution:  "1080p",
		},
		{
			Tracker:    "rutor",
			ID:         "99999",
			Title:      "Титаник / Titanic (1997) BDRip 1080p КПК",
			Size:       12348030976, // 11.50 GB
			SizeHuman:  "11.50 GB",
			Seeds:      35,
			Leeches:    10,
			InfoHash:   sharedHash,
			Magnet:     "magnet:?xt=urn:btih:" + sharedHash + "&tr=udp://opentor.net:6969",
			DetailsURL: "https://rutor.info/torrent/99999",
			Resolution: "1080p",
		},
	}

	resolvers := map[string]tracker.InfoHashResolver{
		"rutracker": &mockResolver{
			hashes: map[string]string{
				"6813955": sharedHash,
			},
		},
	}

	merged := DeduplicateAndMerge(context.Background(), results, nil, resolvers, 1*time.Second)

	if len(merged) != 1 {
		t.Fatalf("expected exactly 1 merged result, got %d", len(merged))
	}

	res := merged[0]
	if len(res.Trackers) != 2 {
		t.Errorf("expected 2 trackers, got %v", res.Trackers)
	}
	if len(res.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(res.Sources))
	}
	if res.Seeds != 55 {
		t.Errorf("expected combined seeds 55, got %d", res.Seeds)
	}
	if res.Leeches != 15 {
		t.Errorf("expected combined leeches 15, got %d", res.Leeches)
	}
	if res.InfoHash != sharedHash {
		t.Errorf("expected InfoHash %s, got %s", sharedHash, res.InfoHash)
	}
	if !strings.Contains(res.Magnet, "opentor.net") {
		t.Errorf("expected magnet to include announce tracker from rutor, got %s", res.Magnet)
	}
}

func TestDeduplicateAndMerge_AllThreeTrackers(t *testing.T) {
	sharedHash := "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

	results := []models.TorrentResult{
		{
			Tracker:    "rutor",
			ID:         "111",
			Title:      "Breaking Bad S01 1080p",
			Size:       10737418240, // 10.0 GB
			Seeds:      10,
			InfoHash:   sharedHash,
			Magnet:     "magnet:?xt=urn:btih:" + sharedHash + "&tr=udp://opentor.net:6969",
			Resolution: "1080p",
			Seasons:    []int{1},
		},
		{
			Tracker:    "nnmclub",
			ID:         "222",
			Title:      "Breaking Bad (Сезон 1) WEB-DL 1080p",
			Size:       10740000000,
			Seeds:      15,
			Magnet:     "magnet:?xt=urn:btih:" + sharedHash + "&tr=http://nnmclub.to/forum/tracker.php",
			Resolution: "1080p",
			Seasons:    []int{1},
		},
		{
			Tracker:    "rutracker",
			ID:         "333",
			Title:      "Breaking Bad / Во все тяжкие / Сезон 1 [1080p]",
			Size:       10738000000,
			Seeds:      25,
			Resolution: "1080p",
			Seasons:    []int{1},
		},
	}

	resolvers := map[string]tracker.InfoHashResolver{
		"nnmclub": &mockResolver{hashes: map[string]string{"222": sharedHash}},
		"rutracker": &mockResolver{hashes: map[string]string{"333": sharedHash}},
	}

	merged := DeduplicateAndMerge(context.Background(), results, nil, resolvers, 1*time.Second)

	if len(merged) != 1 {
		t.Fatalf("expected 1 merged card for 3 trackers, got %d", len(merged))
	}

	res := merged[0]
	if len(res.Trackers) != 3 {
		t.Errorf("expected 3 trackers, got %v", res.Trackers)
	}
	if res.Seeds != 50 {
		t.Errorf("expected combined seeds 50 (10+15+25), got %d", res.Seeds)
	}
	if !strings.Contains(res.Magnet, "opentor.net") || !strings.Contains(res.Magnet, "nnmclub.to") {
		t.Errorf("expected magnet to contain both announce trackers, got %s", res.Magnet)
	}
}

func TestDeduplicateAndMerge_DifferentHashNoMerge(t *testing.T) {
	results := []models.TorrentResult{
		{
			Tracker:    "rutracker",
			ID:         "111",
			Title:      "Release A",
			Size:       5000000000,
			Seeds:      10,
			Resolution: "1080p",
		},
		{
			Tracker:    "rutor",
			ID:         "222",
			Title:      "Release B",
			Size:       5010000000,
			Seeds:      20,
			InfoHash:   "ffffffffffffffffffffffffffffffffffffffff",
			Resolution: "1080p",
		},
	}

	resolvers := map[string]tracker.InfoHashResolver{
		"rutracker": &mockResolver{hashes: map[string]string{"111": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	}

	merged := DeduplicateAndMerge(context.Background(), results, nil, resolvers, 1*time.Second)

	if len(merged) != 2 {
		t.Fatalf("expected 2 separate results because hashes differ, got %d", len(merged))
	}
}
