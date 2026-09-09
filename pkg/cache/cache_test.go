package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tracker-proxy/pkg/models"
)

func TestNormalizeIMDbID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"tt0120338", "tt0120338"},
		{"0120338", "tt0120338"},
		{"TT0120338", "tt0120338"},
		{"  tt9876543  ", "tt9876543"},
		{"", ""},
	}

	for _, tc := range tests {
		got := NormalizeIMDbID(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeIMDbID(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestStoreSetGetDelete(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_cache.db")

	store, err := New(dbPath, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to create cache store: %v", err)
	}
	defer store.Close()

	// 1. Initial Get should be false
	_, found, err := store.Get("tt0120338")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if found {
		t.Errorf("expected found = false for missing key")
	}

	// 2. Set item
	results := []models.TorrentResult{
		{
			Tracker:   "rutor",
			ID:        "123",
			Title:     "Titanic (1997)",
			Seeds:     50,
			SizeHuman: "10 GB",
		},
	}
	err = store.Set("0120338", "титаник", results)
	if err != nil {
		t.Fatalf("Set error: %v", err)
	}

	// 3. Get with "tt0120338" and "0120338"
	cached, found, err := store.Get("tt0120338")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !found || len(cached) != 1 {
		t.Fatalf("expected found = true with 1 result, got found=%v, len=%d", found, len(cached))
	}
	if cached[0].Title != "Titanic (1997)" || cached[0].Seeds != 50 {
		t.Errorf("cached result mismatch: %+v", cached[0])
	}

	// 4. Delete item
	err = store.Delete("tt0120338")
	if err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	_, found, _ = store.Get("tt0120338")
	if found {
		t.Errorf("expected found = false after delete")
	}
}

func TestStoreExpiration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_expire.db")

	// 50ms TTL
	store, err := New(dbPath, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to create cache store: %v", err)
	}
	defer store.Close()

	results := []models.TorrentResult{
		{Tracker: "rutor", Title: "Avatar"},
	}
	_ = store.Set("tt0499549", "avatar", results)

	// Immediate Get should find it
	_, found, _ := store.Get("tt0499549")
	if !found {
		t.Fatalf("expected found = true immediately after set")
	}

	// Wait for TTL to expire
	time.Sleep(70 * time.Millisecond)

	_, found, _ = store.Get("tt0499549")
	if found {
		t.Errorf("expected found = false after TTL expired")
	}

	// Run CleanExpired
	evicted, err := store.CleanExpired()
	if err != nil {
		t.Fatalf("CleanExpired error: %v", err)
	}
	if evicted != 1 {
		t.Errorf("expected 1 evicted entry, got %d", evicted)
	}
}

func TestTopicHashCache(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_hashes.db")

	store, err := New(dbPath, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to create cache store: %v", err)
	}
	defer store.Close()

	// Initial lookup should return found=false
	hash, found, err := store.GetTopicHash("rutracker", "6813955")
	if err != nil {
		t.Fatalf("GetTopicHash error: %v", err)
	}
	if found || hash != "" {
		t.Errorf("expected found=false for new key, got found=%v, hash=%q", found, hash)
	}

	// Set topic hash (uppercase input should be stored lowercase)
	testHash := "EF54BDF0601D6AFD32F995A13C5C3BCE733D95F2"
	if err := store.SetTopicHash("rutracker", "6813955", testHash); err != nil {
		t.Fatalf("SetTopicHash error: %v", err)
	}

	// Lookup should succeed and return lowercase hash
	hash, found, err = store.GetTopicHash("RuTracker", "6813955")
	if err != nil {
		t.Fatalf("GetTopicHash error: %v", err)
	}
	if !found {
		t.Fatalf("expected found=true after SetTopicHash")
	}
	expectedHash := "ef54bdf0601d6afd32f995a13c5c3bce733d95f2"
	if hash != expectedHash {
		t.Errorf("hash = %q, want %q", hash, expectedHash)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
