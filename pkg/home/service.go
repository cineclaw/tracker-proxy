package home

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tracker-proxy/pkg/hotlist"
	"tracker-proxy/pkg/playback"
	"golang.org/x/sync/errgroup"
)

type FeedItemRaw struct {
	ID            int64    `json:"id"`
	MediaType     string   `json:"media_type"`
	Title         string   `json:"title"`
	OriginalTitle *string  `json:"original_title"`
	Year          *int     `json:"year"`
	Rating        *float64 `json:"rating"`
	VoteCount     int      `json:"vote_count"`
	PosterPath    *string  `json:"poster_path"`
	BackdropPath  *string  `json:"backdrop_path"`
	Overview      *string  `json:"overview"`
}

type FeedShelfRaw struct {
	ID    string        `json:"id"`
	Title string        `json:"title"`
	Icon  string        `json:"icon"`
	Items []FeedItemRaw `json:"items"`
}

type Service struct {
	playStore  *playback.Store
	nextUpSvc  *playback.NextUpService
	hotlistSvc *hotlist.Service
	indexerURL string
	httpClient *http.Client

	mu       sync.RWMutex
	cached   *HomePayload
	cachedAt time.Time
	cacheTTL time.Duration
}

func NewService(
	playStore *playback.Store,
	nextUpSvc *playback.NextUpService,
	hotlistSvc *hotlist.Service,
	indexerURL string,
	cacheTTL time.Duration,
) *Service {
	if cacheTTL <= 0 {
		cacheTTL = 60 * time.Second
	}
	return &Service{
		playStore:  playStore,
		nextUpSvc:  nextUpSvc,
		hotlistSvc: hotlistSvc,
		indexerURL: strings.TrimRight(indexerURL, "/"),
		httpClient: &http.Client{Timeout: 3 * time.Second},
		cacheTTL:   cacheTTL,
	}
}

// InvalidateWatchState purges the in-memory cache when user playback or watchlist mutates
func (s *Service) InvalidateWatchState() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = nil
}

func (s *Service) fetchIndexerFeeds(ctx context.Context) ([]FeedShelfRaw, error) {
	if s.indexerURL == "" {
		return nil, fmt.Errorf("indexer URL not configured")
	}
	reqURL := fmt.Sprintf("%s/api/feeds", s.indexerURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("indexer feeds HTTP status %d", resp.StatusCode)
	}

	var shelves []FeedShelfRaw
	if err := json.NewDecoder(resp.Body).Decode(&shelves); err != nil {
		return nil, err
	}
	return shelves, nil
}

func (s *Service) GetHomeFeed(ctx context.Context, platform string, forceRefresh bool) (*HomePayload, error) {
	s.mu.RLock()
	if !forceRefresh && s.cached != nil && time.Since(s.cachedAt) < s.cacheTTL {
		cachedCopy := s.cached
		s.mu.RUnlock()
		return cachedCopy, nil
	}
	s.mu.RUnlock()

	var (
		continueWatchingItems []playback.WatchProgressItem
		nextUpItems           []playback.NextUpItem
		watchlistItems        []playback.WatchlistItem
		trackerFreshResp      *hotlist.Response
		trackerHotlistResp    *hotlist.Response
		uhd4kResp             *hotlist.Response
		indexerFeeds          []FeedShelfRaw
	)

	// Context with timeout to prevent slow external scrapers from blocking home load
	aggCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	g, gCtx := errgroup.WithContext(aggCtx)

	// 1a. SQLite Continue Watching In-Progress (<1ms)
	if s.playStore != nil {
		g.Go(func() error {
			items, err := s.playStore.GetResumeList(20)
			if err != nil {
				log.Printf("HomeService: GetResumeList: %v", err)
			} else {
				continueWatchingItems = items
			}
			return nil
		})
	}

	// 1b. Next Up Episodes for completed series (<5ms)
	if s.nextUpSvc != nil {
		g.Go(func() error {
			items, err := s.nextUpSvc.GetNextUpItems(10)
			if err != nil {
				log.Printf("HomeService: GetNextUpItems: %v", err)
			} else {
				nextUpItems = items
			}
			return nil
		})
	}

	// 2. SQLite Watchlist (<1ms)
	if s.playStore != nil {
		g.Go(func() error {
			items, err := s.playStore.GetWatchlist(gCtx)
			if err != nil {
				log.Printf("HomeService: GetWatchlist: %v", err)
			} else {
				watchlistItems = items
			}
			return nil
		})
	}

	// 3. Hotlist - New Movies (RuTor fresh in bbolt/RAM)
	if s.hotlistSvc != nil {
		g.Go(func() error {
			resp, err := s.hotlistSvc.GetHotlist(gCtx, "new_movie", "", 1, 20, false)
			if err != nil {
				log.Printf("HomeService: hotlist new_movie: %v", err)
			} else {
				trackerFreshResp = resp
			}
			return nil
		})
	}

	// 4. Hotlist - Popular Swarms (RuTor hotlist in bbolt/RAM)
	if s.hotlistSvc != nil {
		g.Go(func() error {
			resp, err := s.hotlistSvc.GetHotlist(gCtx, "movie", "", 1, 20, false)
			if err != nil {
				log.Printf("HomeService: hotlist popular: %v", err)
			} else {
				trackerHotlistResp = resp
			}
			return nil
		})
	}

	// 5. Hotlist - 4K UHD Pure Swarms (RuTor in bbolt/RAM)
	if s.hotlistSvc != nil {
		g.Go(func() error {
			resp, err := s.hotlistSvc.GetHotlist(gCtx, "movie", "4k", 1, 20, false)
			if err != nil {
				log.Printf("HomeService: hotlist 4k: %v", err)
			} else {
				uhd4kResp = resp
			}
			return nil
		})
	}

	// 6. Curated Shelves & Hero Carousel from indexer/TMDB
	g.Go(func() error {
		shelves, err := s.fetchIndexerFeeds(gCtx)
		if err != nil {
			log.Printf("HomeService: fetchIndexerFeeds: %v", err)
		} else {
			indexerFeeds = shelves
		}
		return nil
	})

	_ = g.Wait()

	// Build Hero Items (Top 5 from trending shelf, rating >= 6.0 or unrated)
	var heroItems []HomeItem
	if len(indexerFeeds) > 0 && len(indexerFeeds[0].Items) > 0 {
		for _, it := range indexerFeeds[0].Items {
			if it.Rating != nil && *it.Rating > 0 && *it.Rating < 6.0 {
				continue
			}
			heroItems = append(heroItems, mapFeedItem(it))
			if len(heroItems) >= 5 {
				break
			}
		}
	} else if trackerHotlistResp != nil && len(trackerHotlistResp.Items) > 0 {
		for _, it := range trackerHotlistResp.Items {
			if it.Rating > 0 && it.Rating < 6.0 {
				continue
			}
			heroItems = append(heroItems, mapHotlistItem(it, ""))
			if len(heroItems) >= 5 {
				break
			}
		}
	}

	// Build Ordered Shelves List
	var shelves []HomeShelf

	// 1. Continue Watching Shelf (In-Progress + Next Up)
	type rankedItem struct {
		item      HomeItem
		watchedAt time.Time
	}
	var ranked []rankedItem

	for _, cw := range continueWatchingItems {
		ranked = append(ranked, rankedItem{
			item:      mapResumeItem(cw),
			watchedAt: cw.LastWatchedAt,
		})
	}
	for _, nu := range nextUpItems {
		ranked = append(ranked, rankedItem{
			item:      mapNextUpItem(nu),
			watchedAt: nu.LastWatchedAt,
		})
	}

	if len(ranked) > 0 {
		sort.Slice(ranked, func(i, j int) bool {
			return ranked[i].watchedAt.After(ranked[j].watchedAt)
		})
		var items []HomeItem
		for _, r := range ranked {
			items = append(items, r.item)
		}
		shelves = append(shelves, HomeShelf{
			ID:    "continue_watching",
			Title: "Продолжить просмотр",
			Type:  "continue_watching",
			Items: items,
		})
	}

	// 2. Watchlist Shelf ("Буду смотреть")
	if len(watchlistItems) > 0 {
		var items []HomeItem
		for _, wl := range watchlistItems {
			items = append(items, mapWatchlistItem(wl))
		}
		shelves = append(shelves, HomeShelf{
			ID:          "watchlist",
			Title:       "Буду смотреть",
			Type:        "poster",
			Badge:       strconv.Itoa(len(watchlistItems)),
			ActionRoute: "watchlist",
			Items:       items,
		})
	}

	// 3. Fresh on Trackers Shelf ("Новинки на трекерах")
	if trackerFreshResp != nil && len(trackerFreshResp.Items) > 0 {
		var items []HomeItem
		for _, item := range trackerFreshResp.Items {
			if item.Rating > 0 && item.Rating < 6.0 {
				continue
			}
			items = append(items, mapHotlistItem(item, "🌱 Свежие"))
		}
		if len(items) > 0 {
			shelves = append(shelves, HomeShelf{
				ID:          "tracker_fresh",
				Title:       "Новинки на трекерах",
				Type:        "poster",
				Badge:       "🌱 Свежие",
				ActionRoute: "shelf/tracker_fresh",
				Items:       items,
			})
		}
	}

	// 4. Popular on Trackers Shelf ("Популярно на трекерах")
	if trackerHotlistResp != nil && len(trackerHotlistResp.Items) > 0 {
		var items []HomeItem
		for _, item := range trackerHotlistResp.Items {
			if item.Rating > 0 && item.Rating < 6.0 {
				continue
			}
			items = append(items, mapHotlistItem(item, "🌱 Топ"))
		}
		if len(items) > 0 {
			shelves = append(shelves, HomeShelf{
				ID:          "tracker_hotlist",
				Title:       "Популярно на трекерах",
				Type:        "poster",
				Badge:       "🌱 Топ",
				ActionRoute: "shelf/tracker_hotlist",
				Items:       items,
			})
		}
	}

	// 5. 4K UHD Кинозал Shelf
	if uhd4kResp != nil && len(uhd4kResp.Items) > 0 {
		var items []HomeItem
		for _, item := range uhd4kResp.Items {
			if item.Rating > 0 && item.Rating < 6.0 {
				continue
			}
			items = append(items, mapHotlistItem(item, "4K UHD"))
		}
		if len(items) > 0 {
			shelves = append(shelves, HomeShelf{
				ID:          "uhd_4k",
				Title:       "4K UHD Кинозал",
				Type:        "poster",
				Badge:       "✨ 4K",
				ActionRoute: "shelf/uhd_4k",
				Items:       items,
			})
		}
	}

	// 6+. TMDB Feeds
	for _, feed := range indexerFeeds {
		if len(feed.Items) == 0 {
			continue
		}
		var items []HomeItem
		for _, it := range feed.Items {
			if it.Rating != nil && *it.Rating > 0 && *it.Rating < 6.0 {
				continue
			}
			items = append(items, mapFeedItem(it))
		}
		if len(items) > 0 {
			shelves = append(shelves, HomeShelf{
				ID:          feed.ID,
				Title:       feed.Title,
				Type:        "poster",
				ActionRoute: "shelf/" + feed.ID,
				Items:       items,
			})
		}
	}

	payload := &HomePayload{
		Hero:    heroItems,
		Shelves: shelves,
	}

	// Cache successful payload in RAM
	s.mu.Lock()
	s.cached = payload
	s.cachedAt = time.Now()
	s.mu.Unlock()

	return payload, nil
}

func mapResumeItem(w playback.WatchProgressItem) HomeItem {
	posSec := int64(w.PositionSeconds)
	durSec := int64(w.DurationSeconds)

	curM := posSec / 60
	curS := posSec % 60
	totH := durSec / 3600
	totM := (durSec % 3600) / 60

	var timecode string
	if totH > 0 {
		timecode = fmt.Sprintf("%02d:%02d / %02d:%02d:00", curM, curS, totH, totM)
	} else {
		timecode = fmt.Sprintf("%02d:%02d / %02d:00", curM, curS, totM)
	}

	return HomeItem{
		ID:              w.ImdbID,
		Tconst:          w.ImdbID,
		MediaType:       w.MediaType,
		Title:           w.Title,
		PosterPath:      w.PosterPath,
		BackdropPath:    w.BackdropPath,
		Season:          w.SeasonNumber,
		Episode:         w.EpisodeNumber,
		EpisodeTitle:    w.EpisodeTitle,
		EpisodeStill:    w.EpisodeStillPath,
		PositionSeconds: posSec,
		DurationSeconds: durSec,
		PlaybackPercent: w.PlaybackPercent,
		Timecode:        timecode,
		IsNextUp:        false,
	}
}

func mapNextUpItem(nu playback.NextUpItem) HomeItem {
	backdrop := nu.EpisodeStillPath
	if backdrop == "" {
		backdrop = nu.BackdropPath
	}
	if backdrop == "" {
		backdrop = nu.PosterPath
	}

	return HomeItem{
		ID:              fmt.Sprintf("%s_s%d_e%d", nu.ImdbID, nu.SeasonNumber, nu.EpisodeNumber),
		Tconst:          nu.ImdbID,
		MediaType:       "tv",
		Title:           nu.Title,
		PosterPath:      nu.PosterPath,
		BackdropPath:    backdrop,
		Season:          nu.SeasonNumber,
		Episode:         nu.EpisodeNumber,
		EpisodeTitle:    nu.EpisodeTitle,
		EpisodeStill:    nu.EpisodeStillPath,
		PositionSeconds: 0,
		DurationSeconds: 3600,
		PlaybackPercent: 0,
		Timecode:        "",
		IsNextUp:        true,
	}
}

func mapWatchlistItem(w playback.WatchlistItem) HomeItem {
	return HomeItem{
		ID:            w.ImdbID,
		Tconst:        w.ImdbID,
		MediaType:     w.MediaType,
		Title:         w.Title,
		OriginalTitle: w.OriginalTitle,
		Year:          w.Year,
		Rating:        w.Rating,
		PosterPath:    w.PosterPath,
		BackdropPath:  w.BackdropPath,
	}
}

func mapHotlistItem(item hotlist.Item, defaultBadge string) HomeItem {
	badge := defaultBadge
	if item.Seeds > 0 {
		badge = fmt.Sprintf("🌱 %d", item.Seeds)
	}

	idStr := strconv.Itoa(item.ID)
	if item.Tconst != "" {
		idStr = item.Tconst
	}

	return HomeItem{
		ID:            idStr,
		Tconst:        item.Tconst,
		MediaType:     item.MediaType,
		Title:         item.Title,
		OriginalTitle: item.OriginalTitle,
		Year:          item.Year,
		Rating:        item.Rating,
		VoteCount:     item.VoteCount,
		PosterPath:    item.PosterPath,
		BackdropPath:  item.BackdropPath,
		Overview:      item.Overview,
		Seeds:         item.Seeds,
		QualityBadge:  badge,
	}
}

func mapFeedItem(f FeedItemRaw) HomeItem {
	var origTitle string
	if f.OriginalTitle != nil {
		origTitle = *f.OriginalTitle
	}
	var yr int
	if f.Year != nil {
		yr = *f.Year
	}
	var rat float64
	if f.Rating != nil {
		rat = *f.Rating
	}
	var poster, backdrop, overview string
	if f.PosterPath != nil {
		poster = *f.PosterPath
	}
	if f.BackdropPath != nil {
		backdrop = *f.BackdropPath
	}
	if f.Overview != nil {
		overview = *f.Overview
	}

	return HomeItem{
		ID:            strconv.FormatInt(f.ID, 10),
		TmdbID:        f.ID,
		MediaType:     f.MediaType,
		Title:         f.Title,
		OriginalTitle: origTitle,
		Year:          yr,
		Rating:        rat,
		VoteCount:     f.VoteCount,
		PosterPath:    poster,
		BackdropPath:  backdrop,
		Overview:      overview,
	}
}
