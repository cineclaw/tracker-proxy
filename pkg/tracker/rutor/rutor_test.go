package rutor

import (
	"context"
	"testing"
	"tracker-proxy/pkg/models"
)

func TestRutorSearch(t *testing.T) {
	tracker := New("")
	results, err := tracker.Search(context.Background(), models.SearchQuery{
		Query: "Avatar",
		Limit: 5,
	})
	if err != nil {
		t.Skipf("skipping live rutor search test due to network: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least 1 result, got 0")
	}

	first := results[0]
	t.Logf("First result: [%s] %s | Seeds: %d | Size: %s", first.Tracker, first.Title, first.Seeds, first.SizeHuman)
	if first.Title == "" {
		t.Errorf("expected non-empty title")
	}
	if first.Magnet == "" {
		t.Errorf("expected magnet link to be present")
	}
	if first.Size <= 0 {
		t.Errorf("expected positive size in bytes")
	}
}
