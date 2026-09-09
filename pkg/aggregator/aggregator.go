package aggregator

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"tracker-proxy/pkg/cache"
	"tracker-proxy/pkg/models"
	"tracker-proxy/pkg/tracker"
)

type Aggregator struct {
	trackers   []tracker.Tracker
	resolvers  map[string]tracker.InfoHashResolver
	cacheStore *cache.Store
	timeout    time.Duration
}

func New(trackers []tracker.Tracker, cacheStore *cache.Store, timeout time.Duration) *Aggregator {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	resolvers := make(map[string]tracker.InfoHashResolver)
	for _, tr := range trackers {
		if res, ok := tr.(tracker.InfoHashResolver); ok {
			resolvers[tr.Name()] = res
		}
	}
	return &Aggregator{
		trackers:   trackers,
		resolvers:  resolvers,
		cacheStore: cacheStore,
		timeout:    timeout,
	}
}

// SetCache allows setting or updating the cache store after initialization.
func (a *Aggregator) SetCache(cs *cache.Store) {
	a.cacheStore = cs
}

func (a *Aggregator) Search(ctx context.Context, query models.SearchQuery) []models.TorrentResult {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	var wg sync.WaitGroup
	resultsChan := make(chan []models.TorrentResult, len(a.trackers))

	for _, tr := range a.trackers {
		if !tr.IsEnabled() {
			continue
		}

		wg.Add(1)
		go func(t tracker.Tracker) {
			defer wg.Done()
			results, err := t.Search(ctx, query)
			if err != nil {
				log.Printf("[%s] search error: %v", t.Name(), err)
				return
			}
			resultsChan <- results
		}(tr)
	}

	wg.Wait()
	close(resultsChan)

	var allResults []models.TorrentResult
	for res := range resultsChan {
		allResults = append(allResults, res...)
	}

	// Enrich each torrent result with season and resolution metadata
	for i := range allResults {
		allResults[i].Seasons, allResults[i].IsComplete = tracker.ExtractSeasonInfo(allResults[i].Title)
		allResults[i].Resolution = tracker.ExtractResolution(allResults[i].Title)
	}

	// Cross-tracker deduplication and merging
	allResults = DeduplicateAndMerge(ctx, allResults, a.cacheStore, a.resolvers, 4*time.Second)

	// Sort by seeds descending
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Seeds > allResults[j].Seeds
	})

	if query.Limit > 0 && len(allResults) > query.Limit {
		allResults = allResults[:query.Limit]
	}

	return allResults
}

// ResolveInfoHash resolves the InfoHash for a given tracker and topic ID, checking cache first.
func (a *Aggregator) ResolveInfoHash(ctx context.Context, trackerName, id string) (string, error) {
	if trackerName == "" || id == "" {
		return "", nil
	}

	// 1. Check bbolt cache first
	if a.cacheStore != nil {
		if cachedHash, found, err := a.cacheStore.GetTopicHash(trackerName, id); err == nil && found && cachedHash != "" {
			return strings.ToLower(cachedHash), nil
		}
	}

	// 2. Resolve via tracker InfoHashResolver
	resolver, ok := a.resolvers[strings.ToLower(trackerName)]
	if !ok {
		return "", nil
	}

	hash, err := resolver.FetchInfoHash(ctx, id)
	if err != nil {
		return "", err
	}

	clean := strings.ToLower(strings.TrimSpace(hash))
	if clean != "" && a.cacheStore != nil {
		_ = a.cacheStore.SetTopicHash(trackerName, id, clean)
	}

	return clean, nil
}

