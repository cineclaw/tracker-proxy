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
	s.loadFromDB("new_movie")
	s.loadFromDB("new_tv")
	s.loadFromDB("new_kids")
	s.loadFromDB("kids_popular")
	s.loadFromDB("anime")
	s.loadFromDB("doc")

	// Trigger initial background pre-fetch for any empty categories so cache is always warm
	go func() {
		for _, cat := range []string{"movie", "tv", "new_movie", "new_tv", "new_kids", "kids_popular", "anime", "doc"} {
			s.mu.RLock()
			cached, ok := s.memCache[cat]
			s.mu.RUnlock()
			if !ok || len(cached.Items) == 0 {
				s.triggerRefresh(cat)
			}
		}
	}()

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
	case "new_movie", "fresh_movie", "new":
		validType = "new_movie"
		shelfID = "tracker_fresh"
		shelfTitle = "Новинки кино на трекерах"
	case "new_tv", "fresh_tv":
		validType = "new_tv"
		shelfID = "tracker_fresh"
		shelfTitle = "Новинки сериалов на трекерах"
	case "new_kids", "kids_fresh", "kids_new":
		validType = "new_kids"
		shelfID = "tracker_fresh_kids"
		shelfTitle = "Новинки на трекерах для детей"
	case "kids_popular", "kids_cartoons", "kids_top":
		validType = "kids_popular"
		shelfID = "tracker_hotlist_kids"
		shelfTitle = "Популярно на трекерах (Мультфильмы)"
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

	// Filter out items with low rating (< 6.0) if rating is known
	var cleanItems []Item
	for _, it := range items {
		if it.Rating > 0 && it.Rating < 6.0 {
			continue
		}
		cleanItems = append(cleanItems, it)
	}
	items = cleanItems

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

	// Check if cached items are corrupt or have unacceptable poster missing rate
	isCorrupt := false
	if found && len(cached.Items) > 0 {
		missingPosterCount := 0
		checkCount := len(cached.Items)
		if checkCount > 20 {
			checkCount = 20
		}
		for i := 0; i < checkCount; i++ {
			it := cached.Items[i]
			if strings.HasPrefix(it.PosterPath, "/poster/") {
				isCorrupt = true
				break
			}
			if it.PosterPath == "" && it.Tconst == "" {
				missingPosterCount++
			}
		}
		if float64(missingPosterCount)/float64(checkCount) > 0.35 {
			isCorrupt = true
		}
	}

	if forceRefresh || isCorrupt {
		// Trigger background refresh with independent context
		go s.triggerRefresh(mediaType)

		// If we already have items (even if partial), return them now so we don't block caller
		if found && len(cached.Items) > 0 && !forceRefresh {
			return cached.Items
		}

		// If forced refresh or completely empty, wait with a dedicated 5-minute timeout
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		s.fetchAndSave(bgCtx, mediaType)
		cancel()

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

	// If completely empty, wait synchronously for first fetch with dedicated background context
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	s.fetchAndSave(bgCtx, mediaType)
	cancel()

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

func isKidsContentSafe(it Item) bool {
	titleLower := strings.ToLower(it.Title + " " + it.OriginalTitle + " " + it.Overview)
	for _, badWord := range []string{"18+", "хентай", "hentai", "эротика", "ужасы", "порно", "расчлененка", "эротич"} {
		if strings.Contains(titleLower, badWord) {
			return false
		}
	}
	for _, g := range it.Genres {
		gl := strings.ToLower(g)
		if gl == "horror" || gl == "erotica" {
			return false
		}
	}
	return true
}

func (s *Service) fetchAndSave(ctx context.Context, mediaType string) {
	log.Printf("[hotlist] Fetching releases from RuTor for %s...", mediaType)
	var raw []RawTorrent
	var err error
	matchingMediaType := mediaType

	switch mediaType {
	case "tv":
		raw, err = s.scraper.ScrapeSeries(ctx, 0)
	case "new_movie":
		raw, err = s.scraper.ScrapeFreshMovies(ctx, 0)
		matchingMediaType = "new_movie"
	case "new_tv":
		raw, err = s.scraper.ScrapeFreshSeries(ctx, 0)
		matchingMediaType = "new_tv"
	case "new_kids":
		raw, err = s.scraper.ScrapeFreshKids(ctx, 0)
		matchingMediaType = "new_kids"
	case "kids_popular":
		raw, err = s.scraper.ScrapeTopKids(ctx, 0)
		matchingMediaType = "kids_popular"
	case "anime":
		raw, err = s.scraper.ScrapeAnime(ctx, 0)
		matchingMediaType = "anime"
	case "doc":
		raw, err = s.scraper.ScrapeDocumentaries(ctx, 0)
		matchingMediaType = "doc"
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

	// If cancelled by context, do not save half-baked results to cache/db
	if ctx.Err() != nil {
		log.Printf("[hotlist] Matching %s aborted by context: %v. Discarding incomplete results.", mediaType, ctx.Err())
		return
	}

	// Content safety filter for kids feeds
	if mediaType == "new_kids" || mediaType == "kids_popular" {
		var kidsClean []Item
		for _, it := range items {
			if isKidsContentSafe(it) {
				kidsClean = append(kidsClean, it)
			}
		}
		items = kidsClean
	}

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
			// Validate that restored items have valid matches and do not have unacceptable missing poster rate
			hasValid := false
			hasLegacy := false
			missingPosterCount := 0
			checkCount := len(data.Items)
			if checkCount > 20 {
				checkCount = 20
			}
			for i := 0; i < checkCount; i++ {
				it := data.Items[i]
				if strings.HasPrefix(it.PosterPath, "/poster/") {
					hasLegacy = true
					break
				}
				if it.PosterPath == "" && it.Tconst == "" {
					missingPosterCount++
				}
				if it.Tconst != "" || it.PosterPath != "" {
					hasValid = true
				}
			}
			if hasLegacy {
				log.Printf("[hotlist] Discarding legacy /poster/ cached %s hotlist items from bbolt", mediaType)
				return nil
			}
			if len(data.Items) > 0 && (!hasValid || float64(missingPosterCount)/float64(checkCount) > 0.35) {
				log.Printf("[hotlist] Discarding corrupt cached %s hotlist items from bbolt (missing posters: %d/%d)", mediaType, missingPosterCount, checkCount)
				return nil
			}

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
		log.Printf("[hotlist] Periodic background refresh triggered for movie, tv, new_movie, new_tv, new_kids, kids_popular, anime, doc...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		s.fetchAndSave(ctx, "movie")
		s.fetchAndSave(ctx, "tv")
		s.fetchAndSave(ctx, "new_movie")
		s.fetchAndSave(ctx, "new_tv")
		s.fetchAndSave(ctx, "new_kids")
		s.fetchAndSave(ctx, "kids_popular")
		s.fetchAndSave(ctx, "anime")
		s.fetchAndSave(ctx, "doc")
		cancel()
	}
}
