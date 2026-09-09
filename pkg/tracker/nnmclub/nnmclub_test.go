package nnmclub

import (
	"context"
	"testing"
	"tracker-proxy/pkg/models"
)

func TestNNMClubSearch(t *testing.T) {
	tracker := New("", "", "", "")
	results, err := tracker.Search(context.Background(), models.SearchQuery{
		Query: "Титаник",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("nnmclub search error: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least 1 result from nnmclub, got 0")
	}

	first := results[0]
	t.Logf("First NNM result: [%s] %s | Seeds: %d | Size: %s (%d bytes) | Category: %s",
		first.Tracker, first.Title, first.Seeds, first.SizeHuman, first.Size, first.Category)

	if first.Title == "" {
		t.Errorf("expected non-empty title")
	}
	if first.DetailsURL == "" {
		t.Errorf("expected non-empty details URL")
	}
}
