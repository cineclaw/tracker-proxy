package swarm

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"tracker-proxy/pkg/models"
)

var defaultOpenTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://opentor.net:6969/announce",
}

type cachedEntry struct {
	assessment SwarmAssessment
	expiresAt  time.Time
}

// Service manages real-time BitTorrent swarm discovery, UDP scraping, and health evaluation.
type Service struct {
	openTrackers  []string
	scrapeTimeout time.Duration
	cacheTTL      time.Duration
	cacheMu       sync.RWMutex
	cache         map[string]cachedEntry
}

// NewService instantiates a new Swarm verification service.
func NewService(openTrackers []string, scrapeTimeout, cacheTTL time.Duration) *Service {
	if len(openTrackers) == 0 {
		openTrackers = defaultOpenTrackers
	}
	if scrapeTimeout <= 0 {
		scrapeTimeout = 1200 * time.Millisecond
	}
	if cacheTTL <= 0 {
		cacheTTL = 15 * time.Minute
	}
	return &Service{
		openTrackers:  openTrackers,
		scrapeTimeout: scrapeTimeout,
		cacheTTL:      cacheTTL,
		cache:         make(map[string]cachedEntry),
	}
}

// GetCached returns a cached swarm assessment if available and unexpired.
func (s *Service) GetCached(infoHash string) (SwarmAssessment, bool) {
	clean := strings.ToLower(strings.TrimSpace(infoHash))
	if clean == "" {
		return SwarmAssessment{}, false
	}

	s.cacheMu.RLock()
	entry, found := s.cache[clean]
	s.cacheMu.RUnlock()

	if !found || time.Now().After(entry.expiresAt) {
		return SwarmAssessment{}, false
	}
	return entry.assessment, true
}

// SetCached stores a swarm assessment in the in-memory cache.
func (s *Service) SetCached(infoHash string, assessment SwarmAssessment) {
	clean := strings.ToLower(strings.TrimSpace(infoHash))
	if clean == "" {
		return
	}

	s.cacheMu.Lock()
	s.cache[clean] = cachedEntry{
		assessment: assessment,
		expiresAt:  time.Now().Add(s.cacheTTL),
	}
	// Clean up expired items periodically if map grows large
	if len(s.cache) > 2000 {
		now := time.Now()
		for k, v := range s.cache {
			if now.After(v.expiresAt) {
				delete(s.cache, k)
			}
		}
	}
	s.cacheMu.Unlock()
}

// EnrichResults queries UDP trackers for all valid InfoHashes in parallel and enriches TorrentResults with verified metrics.
func (s *Service) EnrichResults(ctx context.Context, results []models.TorrentResult) []models.TorrentResult {
	if len(results) == 0 {
		return results
	}

	// 1. Check cache and gather uncached hashes
	type uncachedItem struct {
		hashStr   string
		hashBytes [20]byte
		indices   []int
	}

	uncachedMap := make(map[[20]byte]*uncachedItem)
	var uncachedHashes [][20]byte

	for i := range results {
		res := &results[i]
		cleanHash := strings.ToLower(strings.TrimSpace(res.InfoHash))
		if cleanHash == "" {
			// Cannot scrape without info_hash; calculate unverified assessment
			p := AssessmentParams{
				ClaimedSeeds:   res.Seeds,
				ClaimedLeeches: res.Leeches,
				Trackers:       res.Trackers,
				SizeBytes:      res.Size,
				Resolution:     res.Resolution,
				WasScraped:     false,
			}
			applyAssessment(res, CalculateHealthAndPotential(p))
			continue
		}

		if cached, ok := s.GetCached(cleanHash); ok {
			applyAssessment(res, cached)
			continue
		}

		hBytes, err := ParseInfoHash(cleanHash)
		if err != nil {
			p := AssessmentParams{
				ClaimedSeeds:   res.Seeds,
				ClaimedLeeches: res.Leeches,
				Trackers:       res.Trackers,
				SizeBytes:      res.Size,
				Resolution:     res.Resolution,
				WasScraped:     false,
			}
			applyAssessment(res, CalculateHealthAndPotential(p))
			continue
		}

		if existing, exists := uncachedMap[hBytes]; exists {
			existing.indices = append(existing.indices, i)
		} else {
			item := &uncachedItem{
				hashStr:   cleanHash,
				hashBytes: hBytes,
				indices:   []int{i},
			}
			uncachedMap[hBytes] = item
			uncachedHashes = append(uncachedHashes, hBytes)
		}
	}

	if len(uncachedHashes) == 0 {
		return results
	}

	// 2. Perform parallel UDP scrape across open trackers
	scrapeCtx, cancel := context.WithTimeout(ctx, s.scrapeTimeout)
	defer cancel()

	type trackerScrapeResult struct {
		tracker string
		stats   map[[20]byte]ScrapeStats
		err     error
	}

	resChan := make(chan trackerScrapeResult, len(s.openTrackers))
	var wg sync.WaitGroup

	for _, tr := range s.openTrackers {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			stats, err := ScrapeTracker(scrapeCtx, addr, uncachedHashes, s.scrapeTimeout)
			resChan <- trackerScrapeResult{
				tracker: addr,
				stats:   stats,
				err:     err,
			}
		}(tr)
	}

	wg.Wait()
	close(resChan)

	// 3. Aggregate scrape stats across all responsive trackers
	type aggregatedStats struct {
		maxSeeds          int
		maxLeeches        int
		maxCompleted      int
		responsiveCount   int
		zeroResponseCount int
	}

	aggMap := make(map[[20]byte]*aggregatedStats, len(uncachedHashes))
	for _, h := range uncachedHashes {
		aggMap[h] = &aggregatedStats{}
	}

	successfulTrackers := 0
	for r := range resChan {
		if r.err != nil || len(r.stats) == 0 {
			continue
		}
		successfulTrackers++
		for h, stat := range r.stats {
			agg, exists := aggMap[h]
			if !exists {
				continue
			}
			agg.responsiveCount++
			if int(stat.Seeders) > agg.maxSeeds {
				agg.maxSeeds = int(stat.Seeders)
			}
			if int(stat.Leechers) > agg.maxLeeches {
				agg.maxLeeches = int(stat.Leechers)
			}
			if int(stat.Completed) > agg.maxCompleted {
				agg.maxCompleted = int(stat.Completed)
			}
			if stat.Seeders == 0 && stat.Completed == 0 {
				agg.zeroResponseCount++
			}
		}
	}

	wasScraped := successfulTrackers > 0

	// 4. Calculate assessments and enrich TorrentResults
	for hBytes, item := range uncachedMap {
		agg := aggMap[hBytes]
		allZero := agg != nil && agg.responsiveCount > 0 && agg.zeroResponseCount == agg.responsiveCount

		for _, idx := range item.indices {
			res := &results[idx]
			p := AssessmentParams{
				ClaimedSeeds:            res.Seeds,
				ClaimedLeeches:          res.Leeches,
				VerifiedSeeds:           agg.maxSeeds,
				VerifiedLeeches:         agg.maxLeeches,
				VerifiedCompleted:       agg.maxCompleted,
				Trackers:                res.Trackers,
				SizeBytes:               res.Size,
				Resolution:              res.Resolution,
				WasScraped:              wasScraped && agg.responsiveCount > 0,
				AllTrackersReturnedZero: allZero,
			}
			assessment := CalculateHealthAndPotential(p)
			applyAssessment(res, assessment)
			s.SetCached(item.hashStr, assessment)
		}
	}

	log.Printf("[swarm] Enriched %d torrents (%d hashes scraped via %d trackers in %v)",
		len(results), len(uncachedHashes), successfulTrackers, s.scrapeTimeout)

	return results
}

func applyAssessment(r *models.TorrentResult, a SwarmAssessment) {
	r.Seeds = a.EffectiveSeeds
	r.RealSeeds = a.RealSeeds
	r.SeedsVerified = a.SeedsVerified
	r.Completed = a.Completed
	r.Leeches = a.Leeches
	r.SwarmHealth = a.SwarmHealth
	r.SpeedPotential = a.SpeedPotential
}
