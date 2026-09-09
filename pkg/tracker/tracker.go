package tracker

import (
	"context"
	"tracker-proxy/pkg/models"
)

// Tracker defines the standard interface that each tracker implementation must satisfy.
type Tracker interface {
	Name() string
	IsEnabled() bool
	Search(ctx context.Context, query models.SearchQuery) ([]models.TorrentResult, error)
}

// InfoHashResolver is an optional interface implemented by trackers that can fetch a torrent's InfoHash given its topic ID.
type InfoHashResolver interface {
	FetchInfoHash(ctx context.Context, id string) (string, error)
}

