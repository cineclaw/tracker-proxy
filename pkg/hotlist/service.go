package hotlist

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)


var (
	hotlistBucketName = []byte("tracker_hotlist")
)

type CachedData struct {
	UpdatedAt time.Time `json:"updated_at"`
	Items     []Item    `json:"items"`
}

type Service struct {
	scraper    *Scraper
	matcher    *Matcher
	db         *bbolt.DB
	ttl        time.Duration
	mu         sync.RWMutex
	memCache   map[string]CachedData
	isFetching map[string]bool
	fetchMu    sync.Mutex
}

func NewService(scraper *Scraper, matcher *Matcher, db *bbolt.DB, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}

	if db != nil {
		_ = db.Update(func(tx *bbolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists(hotlistBucketName)
			return err
		})
	}

	s := &Service{
		scraper:    scraper,
		matcher:    matcher,
		db:         db,
		ttl:        ttl,
		memCache:   make(map[string]CachedData),
		isFetching: make(map[string]bool),
	}

	// Load initial data from bbolt if available
	s.loadFromDB("movie")
	s.loadFromDB("tv")
	s.loadFromDB("anime")
	s.loadFromDB("doc")

	// Start periodic background refresher every 2 hours
	go s.backgroundRefresher()

	return s
}

func (s *Service) GetHotlist(ctx context.Context, mediaType, quality string, page, limit int, forceRefresh bool) (*Response, error) {
	shelfID := "tracker_hotlist"
	shelfTitle := "Популярно на трекерах"
	validType := "movie"

	switch strings.ToLower(mediaType) {
	case "tv":
		validType = "tv"
	case "anime":
		validType = "anime"
		shelfID = "anime_hub"
		shelfTitle = "Аниме & Мультипликация"
	case "doc":
		validType = "doc"
		shelfID = "doc_hub"
		shelfTitle = "Документальное кино"
	default:
		validType = "movie"
	}

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	items := s.getItems(ctx, validType, forceRefresh)

	is4K := strings.EqualFold(quality, "4k") || strings.EqualFold(quality, "uhd")
	if is4K {
		shelfID = "uhd_4k"
		shelfTitle = "4K UHD Кинозал"
		var uhdItems []Item
		for _, it := range items {
			q := strings.ToUpper(it.Quality)
			if it.Resolution == "4k" || strings.Contains(q, "4K") || strings.Contains(q, "UHD") || strings.Contains(q, "2160P") {
				uhdItems = append(uhdItems, it)
			}
		}
		items = uhdItems
	}

	totalResults := len(items)
	totalPages := int(math.Ceil(float64(totalResults) / float64(limit)))
	if totalPages < 1 {
		totalPages = 1
	}

	// Paginate
	start := (page - 1) * limit
	end := start + limit

	if start >= totalResults {
		return &Response{
			ID:           shelfID,
			Title:        shelfTitle,
			MediaType:    validType,
			Page:         page,
			TotalPages:   totalPages,
			TotalResults: totalResults,
			Items:        []Item{},
		}, nil
	}

	if end > totalResults {
		end = totalResults
	}

	return &Response{
		ID:           shelfID,
		Title:        shelfTitle,
		MediaType:    validType,
		Page:         page,
		TotalPages:   totalPages,
		TotalResults: totalResults,
		Items:        items[start:end],
	}, nil
}

func (s *Service) getItems(ctx context.Context, mediaType string, forceRefresh bool) []Item {
	s.mu.RLock()
	cached, found := s.memCache[mediaType]
	s.mu.RUnlock()

	if forceRefresh {
		s.fetchAndSave(ctx, mediaType)
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.memCache[mediaType].Items
	}

	if found && time.Since(cached.UpdatedAt) < s.ttl && len(cached.Items) > 0 {
		return cached.Items
	}

	// If expired or empty, trigger refresh
	go s.triggerRefresh(mediaType)

	// If we have stale items, return them immediately while refresh runs in background
	if found && len(cached.Items) > 0 {
		return cached.Items
	}

	// If completely empty, wait synchronously for first fetch
	s.fetchAndSave(ctx, mediaType)

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memCache[mediaType].Items
}

func (s *Service) triggerRefresh(mediaType string) {
	s.fetchMu.Lock()
	if s.isFetching[mediaType] {
		s.fetchMu.Unlock()
		return
	}
	s.isFetching[mediaType] = true
	s.fetchMu.Unlock()

	defer func() {
		s.fetchMu.Lock()
		s.isFetching[mediaType] = false
		s.fetchMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	s.fetchAndSave(ctx, mediaType)
}

func (s *Service) fetchAndSave(ctx context.Context, mediaType string) {
	log.Printf("[hotlist] Fetching 10 pages per category from RuTor for %s...", mediaType)
	var raw []RawTorrent
	var err error
	matchingMediaType := mediaType

	switch mediaType {
	case "tv":
		raw, err = s.scraper.ScrapeSeries(ctx, 0)
	case "anime":
		raw, err = s.scraper.ScrapeAnime(ctx, 0)
		matchingMediaType = "tv"
	case "doc":
		raw, err = s.scraper.ScrapeDocumentaries(ctx, 0)
		matchingMediaType = "movie"
	default:
		raw, err = s.scraper.ScrapeMovies(ctx, 0)
		matchingMediaType = "movie"
	}

	if err != nil {
		log.Printf("[hotlist] Error scraping %s: %v", mediaType, err)
		return
	}

	log.Printf("[hotlist] Scraped %d raw releases for %s. Matching against Tantivy...", len(raw), mediaType)
	items := s.matcher.MatchAndGroup(ctx, raw, matchingMediaType)
	log.Printf("[hotlist] Grouped into %d unique items for %s", len(items), mediaType)

	data := CachedData{
		UpdatedAt: time.Now(),
		Items:     items,
	}

	// Update memory
	s.mu.Lock()
	s.memCache[mediaType] = data
	s.mu.Unlock()

	// Update bbolt DB
	s.saveToDB(mediaType, data)
}

func (s *Service) saveToDB(mediaType string, data CachedData) {
	if s.db == nil {
		return
	}

	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}

	_ = s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(hotlistBucketName)
		if b == nil {
			return nil
		}
		return b.Put([]byte(mediaType), bytes)
	})
}

func (s *Service) loadFromDB(mediaType string) {
	if s.db == nil {
		return
	}

	_ = s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(hotlistBucketName)
		if b == nil {
			return nil
		}
		val := b.Get([]byte(mediaType))
		if len(val) == 0 {
			return nil
		}
		var data CachedData
		if err := json.Unmarshal(val, &data); err == nil {
			s.mu.Lock()
			s.memCache[mediaType] = data
			s.mu.Unlock()
			log.Printf("[hotlist] Restored %d cached %s hotlist items from bbolt", len(data.Items), mediaType)
		}
		return nil
	})
}

func (s *Service) backgroundRefresher() {
	ticker := time.NewTicker(s.ttl)
	defer ticker.Stop()

	for range ticker.C {
		log.Printf("[hotlist] Periodic background refresh triggered for movie, tv, anime, doc...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		s.fetchAndSave(ctx, "movie")
		s.fetchAndSave(ctx, "tv")
		s.fetchAndSave(ctx, "anime")
		s.fetchAndSave(ctx, "doc")
		cancel()
	}
}
