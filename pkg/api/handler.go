package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"tracker-proxy/pkg/aggregator"
	"tracker-proxy/pkg/auth"
	"tracker-proxy/pkg/cache"
	"tracker-proxy/pkg/home"
	"tracker-proxy/pkg/hotlist"
	"tracker-proxy/pkg/models"
	"tracker-proxy/pkg/playback"
	"tracker-proxy/pkg/stream"
	"tracker-proxy/pkg/tracker"
	"tracker-proxy/pkg/transcode"
	"tracker-proxy/pkg/version"
)

type Handler struct {
	aggregator *aggregator.Aggregator
	cache      *cache.Store
	mounter    *stream.TorrStreamService
	playback   *playback.Store
	nextUp     *playback.NextUpService
	auth       *auth.Manager
	hotlist    *hotlist.Service
	home       *home.Service
	transcode  *transcode.TranscodeEngine
	sf         singleflight.Group
}

func NewHandler(
	agg *aggregator.Aggregator,
	cacheStore *cache.Store,
	authMgr *auth.Manager,
	hotlistSvc *hotlist.Service,
	streamSvc *stream.TorrStreamService,
	playStore *playback.Store,
	nextUpSvc *playback.NextUpService,
	transcodeEng *transcode.TranscodeEngine,
	homeSvc *home.Service,
) *Handler {
	return &Handler{
		aggregator: agg,
		cache:      cacheStore,
		mounter:    streamSvc,
		playback:   playStore,
		nextUp:     nextUpSvc,
		auth:       authMgr,
		hotlist:    hotlistSvc,
		home:       homeSvc,
		transcode:  transcodeEng,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/torrents/health", h.handleHealth)
	mux.HandleFunc("/api/stream/health", h.handleHealth)
	mux.HandleFunc("/api/system/diagnostic", h.corsMiddleware(h.handleSystemDiagnostic))
	mux.HandleFunc("/torrents/system/diagnostic", h.corsMiddleware(h.handleSystemDiagnostic))

	mux.HandleFunc("/api/search", h.corsMiddleware(h.handleJSONSearch))
	mux.HandleFunc("/api/torrents", h.corsMiddleware(h.handleJSONSearch))
	mux.HandleFunc("/torrents", h.corsMiddleware(h.handleJSONSearch))

	// Home & Hub Unified BFF endpoints
	mux.HandleFunc("/api/home", h.corsMiddleware(h.handleHomeFeed))
	mux.HandleFunc("/home", h.corsMiddleware(h.handleHomeFeed))
	mux.HandleFunc("/api/hub", h.corsMiddleware(h.handleHubFeed))
	mux.HandleFunc("/hub", h.corsMiddleware(h.handleHubFeed))

	// Tracker hotlist / trending
	mux.HandleFunc("/api/stream/hotlist", h.corsMiddleware(h.handleHotlist))
	mux.HandleFunc("/stream/hotlist", h.corsMiddleware(h.handleHotlist))
	mux.HandleFunc("/torrents/hotlist", h.corsMiddleware(h.handleHotlist))

	// Mount / Unmount / Status
	mux.HandleFunc("/api/stream/mount", h.corsMiddleware(h.handleStreamMount))
	mux.HandleFunc("/stream/mount", h.corsMiddleware(h.handleStreamMount))
	mux.HandleFunc("/torrents/mount", h.corsMiddleware(h.handleStreamMount))

	mux.HandleFunc("/api/stream/unmount", h.corsMiddleware(h.handleStreamUnmount))
	mux.HandleFunc("/stream/unmount", h.corsMiddleware(h.handleStreamUnmount))
	mux.HandleFunc("/torrents/unmount", h.corsMiddleware(h.handleStreamUnmount))

	mux.HandleFunc("/api/stream/status", h.corsMiddleware(h.handleStreamStatus))
	mux.HandleFunc("/stream/status", h.corsMiddleware(h.handleStreamStatus))
	mux.HandleFunc("/torrents/status", h.corsMiddleware(h.handleStreamStatus))

	// Video player & progress sync endpoints
	mux.HandleFunc("/api/stream/stats", h.corsMiddleware(h.handleStreamStats))
	mux.HandleFunc("/stream/stats", h.corsMiddleware(h.handleStreamStats))
	mux.HandleFunc("/api/stream/player/info", h.corsMiddleware(h.handlePlayerInfo))
	mux.HandleFunc("/api/stream/player/start", h.corsMiddleware(h.handlePlayerStart))
	mux.HandleFunc("/api/stream/player/progress", h.corsMiddleware(h.handlePlayerProgress))
	mux.HandleFunc("/api/stream/player/stop", h.corsMiddleware(h.handlePlayerStop))
	mux.HandleFunc("/api/stream/resume", h.corsMiddleware(h.handleStreamResume))
	mux.HandleFunc("/stream/resume", h.corsMiddleware(h.handleStreamResume))
	mux.HandleFunc("/api/stream/resume/remove", h.corsMiddleware(h.handleStreamResume))
	mux.HandleFunc("/stream/resume/remove", h.corsMiddleware(h.handleStreamResume))

	// On-the-fly video transcoding endpoints (FFmpeg)
	mux.HandleFunc("/api/stream/transcode/profiles", h.corsMiddleware(h.handleTranscodeProfiles))
	mux.HandleFunc("/api/stream/transcode/stop", h.corsMiddleware(h.handleTranscodeStop))
	mux.HandleFunc("/api/stream/transcode/seg/", h.corsMiddleware(h.handleTranscodeSegment))
	mux.HandleFunc("/api/stream/transcode/", h.corsMiddleware(h.handleTranscodePlaylist))

	// Native Playback & Watch History (SQLite)
	mux.HandleFunc("/api/playback/progress", h.corsMiddleware(h.handlePlaybackProgress))
	mux.HandleFunc("/api/playback/audio", h.corsMiddleware(h.handlePlaybackAudioPreference))
	mux.HandleFunc("/api/playback/resume", h.corsMiddleware(h.handlePlaybackResume))
	mux.HandleFunc("/api/playback/next-up", h.corsMiddleware(h.handlePlaybackNextUp))
	mux.HandleFunc("/api/playback/item", h.corsMiddleware(h.handlePlaybackItem))
	mux.HandleFunc("/api/playback/series-progress", h.corsMiddleware(h.handlePlaybackSeriesProgress))
	mux.HandleFunc("/api/playback/mark-watched", h.corsMiddleware(h.handlePlaybackMarkWatched))
	mux.HandleFunc("/api/playback/delete", h.corsMiddleware(h.handlePlaybackDelete))
	mux.HandleFunc("/api/watchlist", h.corsMiddleware(h.handleWatchlist))
	mux.HandleFunc("/api/watchlist/check", h.corsMiddleware(h.handleWatchlistCheck))
	mux.HandleFunc("/api/playback/watchlist", h.corsMiddleware(h.handleWatchlist))
	mux.HandleFunc("/api/playback/watchlist/check", h.corsMiddleware(h.handleWatchlistCheck))

	// Jellyfin webhook endpoints (no-op backwards compatibility)
	mux.HandleFunc("/api/stream/webhook/deleted", h.handleJellyfinItemDeleted)
	mux.HandleFunc("/webhook/deleted", h.handleJellyfinItemDeleted)

	// Auth endpoints
	mux.HandleFunc("/api/auth/login", h.corsMiddleware(h.handleAuthLogin))
	mux.HandleFunc("/api/auth/verify", h.handleAuthVerify)
	mux.HandleFunc("/api/auth/logout", h.corsMiddleware(h.handleAuthLogout))
	mux.HandleFunc("/api/auth/me", h.corsMiddleware(h.handleAuthMe))

	// Torznab endpoints: supports both Jackett-style and direct paths
	mux.HandleFunc("/api", h.handleTorznab)
	mux.HandleFunc("/api/v2.0/indexers/all/results/torznab/api", h.handleTorznab)
	mux.HandleFunc("/api/v2.0/indexers/all/results/torznab/", h.handleTorznab)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"service": "tracker-proxy",
		"version": version.Version,
	})
}

type ServiceDiagnosticResult struct {
	Status    string                 `json:"status"` // "online" or "offline"
	Version   string                 `json:"version,omitempty"`
	LatencyMs int64                  `json:"latency_ms"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

func (h *Handler) handleSystemDiagnostic(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	torrURL, _, indexerURL := h.mounter.GetServiceURLs()
	if torrURL == "" {
		torrURL = "http://torrserver:8090"
	}
	if indexerURL == "" {
		indexerURL = "http://imdb-indexer:8090"
	}

	flaresolverrURL := os.Getenv("FLARESOLVERR_URL")
	if flaresolverrURL == "" {
		flaresolverrURL = "http://flaresolverr:8191"
	}
	aiURL := os.Getenv("AI_ENGINE_URL")
	if aiURL == "" {
		aiURL = "http://cineclaw-ai:9120"
	}

	results := make(map[string]ServiceDiagnosticResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// 1. tracker-proxy (self)
	results["tracker-proxy"] = ServiceDiagnosticResult{
		Status:    "online",
		Version:   version.Version,
		LatencyMs: 0,
		Details: map[string]interface{}{
			"Трекеры": "RuTracker, RuTor, NNM-Club",
			"Кэш":     "bbolt & sqlite (активен)",
		},
	}

	client := &http.Client{Timeout: 3 * time.Second}

	// Helper for probing HTTP endpoints
	probe := func(id, url string, parser func([]byte) (string, map[string]interface{})) {
		defer wg.Done()
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			mu.Lock()
			results[id] = ServiceDiagnosticResult{
				Status:    "offline",
				LatencyMs: time.Since(start).Milliseconds(),
				Error:     err.Error(),
			}
			mu.Unlock()
			return
		}

		resp, err := client.Do(req)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			mu.Lock()
			results[id] = ServiceDiagnosticResult{
				Status:    "offline",
				LatencyMs: latency,
				Error:     err.Error(),
			}
			mu.Unlock()
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			ver, details := parser(body)
			mu.Lock()
			results[id] = ServiceDiagnosticResult{
				Status:    "online",
				Version:   ver,
				LatencyMs: latency,
				Details:   details,
			}
			mu.Unlock()
		} else {
			mu.Lock()
			results[id] = ServiceDiagnosticResult{
				Status:    "offline",
				LatencyMs: latency,
				Error:     fmt.Sprintf("HTTP %d", resp.StatusCode),
			}
			mu.Unlock()
		}
	}

	// 2. imdb-indexer
	wg.Add(1)
	go probe("imdb-indexer", strings.TrimRight(indexerURL, "/")+"/status", func(b []byte) (string, map[string]interface{}) {
		ver := "1.0.0"
		details := map[string]interface{}{}
		var d struct {
			Version        string `json:"version"`
			TotalDocuments int    `json:"total_documents"`
			IsIndexing     bool   `json:"is_indexing"`
		}
		if err := json.Unmarshal(b, &d); err == nil {
			if d.Version != "" {
				ver = d.Version
			}
			details["Проиндексировано фильмов"] = fmt.Sprintf("%d", d.TotalDocuments)
			if d.IsIndexing {
				details["Фоновая индексация"] = "Активна"
			} else {
				details["Фоновая индексация"] = "Неактивна"
			}
		}
		return ver, details
	})

	// 3. torrserver
	wg.Add(1)
	go probe("torrserver", strings.TrimRight(torrURL, "/")+"/echo", func(b []byte) (string, map[string]interface{}) {
		ver := strings.TrimSpace(string(b))
		if ver == "" {
			ver = "MatriX"
		}
		details := map[string]interface{}{
			"Движок":   "TorrServer MatriX",
			"Стриминг": "Прямой BitTorrent HTTP стрим",
		}
		return ver, details
	})

	// 5. flaresolverr
	wg.Add(1)
	go probe("flaresolverr", strings.TrimRight(flaresolverrURL, "/")+"/", func(b []byte) (string, map[string]interface{}) {
		ver := "3.5.0"
		details := map[string]interface{}{
			"Сессия Turnstile": "Готов",
		}
		var d struct {
			Version string `json:"version"`
			Msg     string `json:"msg"`
		}
		if err := json.Unmarshal(b, &d); err == nil && d.Version != "" {
			ver = d.Version
		}
		return ver, details
	})

	// 6. cineclaw-ai
	wg.Add(1)
	go probe("cineclaw-ai", strings.TrimRight(aiURL, "/")+"/health", func(b []byte) (string, map[string]interface{}) {
		ver := "1.0.0"
		details := map[string]interface{}{
			"Модель":  "Gemini 2.5 Flash",
			"Критики": "RT, Metacritic, IMDb",
		}
		var d struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(b, &d); err == nil && d.Version != "" {
			ver = d.Version
		}
		return ver, details
	})

	wg.Wait()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(results)
}

func filterAndRankBySeason(results []models.TorrentResult, season int) []models.TorrentResult {
	if season <= 0 {
		return results
	}

	var tier1 []models.TorrentResult // exact single season
	var tier2 []models.TorrentResult // multi-season pack or complete collection

	for _, item := range results {
		if item.IsComplete {
			tier2 = append(tier2, item)
			continue
		}
		isMatch := false
		for _, s := range item.Seasons {
			if s == season {
				isMatch = true
				break
			}
		}
		if !isMatch {
			continue
		}

		if len(item.Seasons) == 1 && item.Seasons[0] == season {
			tier1 = append(tier1, item)
		} else {
			tier2 = append(tier2, item)
		}
	}

	return append(tier1, tier2...)
}

func (h *Handler) executeSearch(ctx context.Context, queryStr, imdbID, mediaType string, season int, targetYear int, refreshCache bool, limit int) ([]models.TorrentResult, string) {
	normIMDb := cache.NormalizeIMDbID(imdbID)
	cacheStatus := "BYPASS"

	var meta *stream.IndexerMeta
	if normIMDb != "" && h.mounter != nil {
		meta = h.mounter.FetchIndexerMeta(ctx, normIMDb)
	}

	if targetYear <= 0 && meta != nil && meta.Year > 0 {
		targetYear = meta.Year
	}

	// Auto-resolve title from IMDb indexer if queryStr is empty
	if strings.TrimSpace(queryStr) == "" && meta != nil {
		baseTitle := strings.TrimSpace(meta.Title)
		if baseTitle == "" {
			baseTitle = strings.TrimSpace(meta.OriginalTitle)
		}
		if baseTitle != "" {
			if meta.Year > 0 && (mediaType == "movie" || mediaType == "") {
				queryStr = fmt.Sprintf("%s %d", baseTitle, meta.Year)
			} else {
				queryStr = baseTitle
			}
		}
	}

	if normIMDb != "" && h.cache != nil && !refreshCache {
		if cached, found, err := h.cache.Get(normIMDb); err == nil && found {
			for i := range cached {
				if len(cached[i].Seasons) == 0 && !cached[i].IsComplete {
					cached[i].Seasons, cached[i].IsComplete = tracker.ExtractSeasonInfo(cached[i].Title)
				}
				if cached[i].Resolution == "" {
					cached[i].Resolution = tracker.ExtractResolution(cached[i].Title)
				}
				if len(cached[i].Trackers) == 0 && cached[i].Tracker != "" {
					cached[i].Trackers = []string{cached[i].Tracker}
				}
				if len(cached[i].Sources) == 0 && cached[i].Tracker != "" {
					cached[i].Sources = []models.TorrentSource{
						{
							Tracker:     cached[i].Tracker,
							ID:          cached[i].ID,
							Title:       cached[i].Title,
							Size:        cached[i].Size,
							SizeHuman:   cached[i].SizeHuman,
							Seeds:       cached[i].Seeds,
							Leeches:     cached[i].Leeches,
							Magnet:      cached[i].Magnet,
							DownloadURL: cached[i].DownloadURL,
							DetailsURL:  cached[i].DetailsURL,
						},
					}
				}
			}
			filtered := filterAndRankBySeason(cached, season)
			if meta != nil || targetYear > 0 {
				filtered = stream.FilterCandidates(filtered, meta, season, mediaType, targetYear)
				sort.SliceStable(filtered, func(i, j int) bool {
					return stream.ScoreCandidate(&filtered[i], meta, season) > stream.ScoreCandidate(&filtered[j], meta, season)
				})
			}

			// If general search (season == 0) or season was found in cache
			hasSeasonMatch := false
			if season > 0 {
				for _, it := range filtered {
					if (len(it.Seasons) == 1 && it.Seasons[0] == season) || it.IsComplete {
						hasSeasonMatch = true
						break
					}
				}
			}
			if season == 0 || hasSeasonMatch || len(filtered) >= 2 {
				if limit > 0 && len(filtered) > limit {
					filtered = filtered[:limit]
				}
				return filtered, "HIT"
			}
		}
	}

	if normIMDb != "" {
		if strings.TrimSpace(queryStr) == "" {
			log.Printf("[search] Cannot search trackers for %s: title query could not be resolved", normIMDb)
			return nil, "EMPTY"
		}

		if refreshCache {
			cacheStatus = "REFRESHED"
		} else {
			cacheStatus = "MISS"
		}

		sfKey := normIMDb
		if season > 0 {
			sfKey = fmt.Sprintf("%s:s%d", normIMDb, season)
		}

		v, err, _ := h.sf.Do(sfKey, func() (any, error) {
			res := h.aggregator.Search(ctx, models.SearchQuery{
				Query:        queryStr,
				Type:         mediaType,
				IMDbID:       normIMDb,
				RefreshCache: refreshCache,
				Limit:        limit,
			})
			if len(res) == 0 && meta != nil && strings.TrimSpace(meta.Title) != "" && queryStr != strings.TrimSpace(meta.Title) {
				res = h.aggregator.Search(ctx, models.SearchQuery{
					Query:        strings.TrimSpace(meta.Title),
					Type:         mediaType,
					IMDbID:       normIMDb,
					RefreshCache: refreshCache,
					Limit:        limit,
				})
			}

			// If TV series season requested, also search with season-specific queries
			if season > 0 && meta != nil {
				baseTitle := strings.TrimSpace(meta.Title)
				if baseTitle == "" {
					baseTitle = strings.TrimSpace(meta.OriginalTitle)
				}
				if baseTitle != "" {
					seasonQueries := []string{
						fmt.Sprintf("%s %d сезон", baseTitle, season),
						fmt.Sprintf("%s S%02d", baseTitle, season),
					}
					for _, sq := range seasonQueries {
						seasonRes := h.aggregator.Search(ctx, models.SearchQuery{
							Query:        sq,
							Type:         "tv",
							IMDbID:       normIMDb,
							RefreshCache: refreshCache,
							Limit:        limit,
						})
						if len(seasonRes) > 0 {
							existingIDs := make(map[string]bool)
							for _, r := range res {
								existingIDs[r.ID+"_"+r.Tracker] = true
							}
							for _, sr := range seasonRes {
								key := sr.ID + "_" + sr.Tracker
								if !existingIDs[key] {
									existingIDs[key] = true
									res = append(res, sr)
								}
							}
						}
					}
				}
			}

			if h.cache != nil && len(res) > 0 {
				// Merge with existing cached items only if not doing a forced cache refresh
				if !refreshCache {
					if existing, found, err := h.cache.Get(normIMDb); err == nil && found && len(existing) > 0 {
						existingIDs := make(map[string]bool)
						for _, r := range res {
							existingIDs[r.ID+"_"+r.Tracker] = true
						}
						for _, er := range existing {
							key := er.ID + "_" + er.Tracker
							if !existingIDs[key] {
								existingIDs[key] = true
								res = append(res, er)
							}
						}
					}
				}
				_ = h.cache.Set(normIMDb, queryStr, res)
			}
			return res, nil
		})
		if err == nil && v != nil {
			results := v.([]models.TorrentResult)
			filtered := filterAndRankBySeason(results, season)
			if meta != nil || targetYear > 0 {
				filtered = stream.FilterCandidates(filtered, meta, season, mediaType, targetYear)
				sort.SliceStable(filtered, func(i, j int) bool {
					return stream.ScoreCandidate(&filtered[i], meta, season) > stream.ScoreCandidate(&filtered[j], meta, season)
				})
			}
			if limit > 0 && len(filtered) > limit {
				filtered = filtered[:limit]
			}
			return filtered, cacheStatus
		}
	}

	if strings.TrimSpace(queryStr) == "" {
		return nil, "EMPTY"
	}

	results := h.aggregator.Search(ctx, models.SearchQuery{
		Query: queryStr,
		Type:  mediaType,
		Limit: limit,
	})
	filtered := filterAndRankBySeason(results, season)
	if meta != nil || targetYear > 0 {
		filtered = stream.FilterCandidates(filtered, meta, season, mediaType, targetYear)
		sort.SliceStable(filtered, func(i, j int) bool {
			return stream.ScoreCandidate(&filtered[i], meta, season) > stream.ScoreCandidate(&filtered[j], meta, season)
		})
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, cacheStatus
}


func (h *Handler) handleJSONSearch(w http.ResponseWriter, r *http.Request) {
	queryStr := strings.TrimSpace(r.URL.Query().Get("q"))
	if queryStr == "" {
		queryStr = strings.TrimSpace(r.URL.Query().Get("query"))
	}

	imdbID := strings.TrimSpace(r.URL.Query().Get("imdb_id"))
	if imdbID == "" {
		imdbID = strings.TrimSpace(r.URL.Query().Get("imdbid"))
	}

	refreshStr := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh_cache")))
	if refreshStr == "" {
		refreshStr = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh")))
	}
	refreshCache := refreshStr == "true" || refreshStr == "1" || refreshStr == "yes"

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}

	season := 0
	if s := r.URL.Query().Get("season"); s != "" {
		if val, err := strconv.Atoi(s); err == nil && val > 0 {
			season = val
		}
	}

	mediaType := strings.TrimSpace(r.URL.Query().Get("type"))
	if mediaType == "" {
		mediaType = strings.TrimSpace(r.URL.Query().Get("media_type"))
	}

	targetYear := 0
	if y := r.URL.Query().Get("year"); y != "" {
		if val, err := strconv.Atoi(y); err == nil && val > 0 {
			targetYear = val
		}
	}

	start := time.Now()
	results, cacheStatus := h.executeSearch(r.Context(), queryStr, imdbID, mediaType, season, targetYear, refreshCache, limit)
	duration := time.Since(start)

	log.Printf("[search] query=%q imdb_id=%q type=%q season=%d year=%d cache=%s returned=%d duration=%v", queryStr, imdbID, mediaType, season, targetYear, cacheStatus, len(results), duration)

	w.Header().Set("X-Cache", cacheStatus)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if results == nil {
		results = []models.TorrentResult{}
	}
	_ = json.NewEncoder(w).Encode(results)
}

func (h *Handler) handleTorznab(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("t")

	switch strings.ToLower(t) {
	case "caps":
		RenderCaps(w)
		return

	case "search", "movie", "tvsearch":
		queryStr := strings.TrimSpace(r.URL.Query().Get("q"))
		imdbID := strings.TrimSpace(r.URL.Query().Get("imdbid"))
		if imdbID == "" {
			imdbID = strings.TrimSpace(r.URL.Query().Get("imdb_id"))
		}

		torznabType := ""
		if strings.ToLower(t) == "movie" {
			torznabType = "movie"
		} else if strings.ToLower(t) == "tvsearch" {
			torznabType = "tv"
		}

		refreshStr := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh_cache")))
		if refreshStr == "" {
			refreshStr = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh")))
		}
		refreshCache := refreshStr == "true" || refreshStr == "1" || refreshStr == "yes"

		season := 0
		if s := r.URL.Query().Get("season"); s != "" {
			if val, err := strconv.Atoi(s); err == nil && val > 0 {
				season = val
			}
		}

		limit := 100
		if l := r.URL.Query().Get("limit"); l != "" {
			if val, err := strconv.Atoi(l); err == nil && val > 0 {
				limit = val
			}
		}

		targetYear := 0
		if y := r.URL.Query().Get("year"); y != "" {
			if val, err := strconv.Atoi(y); err == nil && val > 0 {
				targetYear = val
			}
		}

		start := time.Now()
		results, cacheStatus := h.executeSearch(r.Context(), queryStr, imdbID, torznabType, season, targetYear, refreshCache, limit)

		duration := time.Since(start)

		log.Printf("[torznab] t=%s query=%q imdb_id=%q season=%d year=%d cache=%s returned=%d duration=%v", t, queryStr, imdbID, season, targetYear, cacheStatus, len(results), duration)

		w.Header().Set("X-Cache", cacheStatus)
		RenderTorznabFeed(w, results)
		return

	default:
		// Default to caps if no t specified
		if t == "" {
			RenderCaps(w)
			return
		}
		http.Error(w, "Unsupported Torznab mode", http.StatusBadRequest)
	}
}

func (h *Handler) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Expose-Headers", "X-Cache")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func (h *Handler) handleStreamMount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req stream.MountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	res, err := h.mounter.MountTorrent(r.Context(), req)
	if err != nil {
		log.Printf("[mounter] MountTorrent FAILED: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) handleStreamUnmount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req stream.UnmountRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Tconst == "" {
		req.Tconst = r.URL.Query().Get("tconst")
	}
	if req.Type == "" {
		req.Type = r.URL.Query().Get("type")
	}
	if req.FolderName == "" {
		req.FolderName = r.URL.Query().Get("folder_name")
	}

	res, err := h.mounter.UnmountTorrent(r.Context(), req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) handleStreamStatus(w http.ResponseWriter, r *http.Request) {
	tconst := r.URL.Query().Get("tconst")
	if tconst == "" {
		http.Error(w, "Missing tconst query parameter", http.StatusBadRequest)
		return
	}

	status := h.mounter.GetMountedStatus(tconst)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (h *Handler) handleJellyfinItemDeleted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload stream.JellyfinDeletedWebhook
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid webhook body", http.StatusBadRequest)
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.mounter.HandleJellyfinItemDeleted(ctx, payload); err != nil {
			log.Printf("[webhook] Error processing Jellyfin ItemDeleted: %v", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (h *Handler) handleStreamStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hash := r.URL.Query().Get("hash")
	tconst := r.URL.Query().Get("tconst")
	fileIdx, _ := strconv.Atoi(firstNonEmpty(r.URL.Query().Get("file_idx"), r.URL.Query().Get("index"), r.URL.Query().Get("id")))
	season, _ := strconv.Atoi(r.URL.Query().Get("season"))
	episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))
	var clientDuration float64 = 0
	if dStr := r.URL.Query().Get("duration"); dStr != "" {
		if f, err := strconv.ParseFloat(dStr, 64); err == nil {
			clientDuration = f
		}
	}

	if h.mounter == nil {
		http.Error(w, "Stream service unavailable", http.StatusServiceUnavailable)
		return
	}

	stats, err := h.mounter.GetStreamStats(r.Context(), hash, tconst, fileIdx, season, episode, clientDuration)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_ = json.NewEncoder(w).Encode(stats)
}

func (h *Handler) handlePlayerInfo(w http.ResponseWriter, r *http.Request) {
	tconst := r.URL.Query().Get("tconst")
	if tconst == "" {
		http.Error(w, "Missing tconst parameter", http.StatusBadRequest)
		return
	}
	season, _ := strconv.Atoi(r.URL.Query().Get("season"))
	episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))

	info, err := h.mounter.GetPlayerInfo(r.Context(), tconst, season, episode)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

func (h *Handler) handlePlayerStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req stream.PlaybackStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	res, err := h.mounter.ReportPlaybackStart(r.Context(), &req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) handlePlayerProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req stream.PlaybackProgressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	res, err := h.mounter.ReportPlaybackProgress(r.Context(), &req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if h.home != nil {
		h.home.InvalidateWatchState()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) handlePlayerStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req stream.PlaybackStopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if h.transcode != nil && req.MediaSourceId != "" {
		h.transcode.StopSessionsForHash(req.MediaSourceId)
	}

	res, err := h.mounter.ReportPlaybackStop(r.Context(), &req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if h.home != nil {
		h.home.InvalidateWatchState()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) handleStreamResume(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if r.Method == http.MethodDelete || (r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/remove")) {
		var req stream.DeleteResumeRequest
		if r.Header.Get("Content-Type") == "application/json" {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.ItemId == "" {
			req.ItemId = r.URL.Query().Get("item_id")
		}
		if req.Tconst == "" {
			req.Tconst = r.URL.Query().Get("tconst")
		}
		if req.Season == 0 {
			req.Season, _ = strconv.Atoi(r.URL.Query().Get("season"))
		}
		if req.Episode == 0 {
			req.Episode, _ = strconv.Atoi(r.URL.Query().Get("episode"))
		}
		if !req.IsNextUp {
			req.IsNextUp = r.URL.Query().Get("is_next_up") == "true"
		}
		if !req.All {
			req.All = r.URL.Query().Get("all") == "true"
		}

		if err := h.mounter.DeleteResumeItem(ctx, &req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		if h.home != nil {
			h.home.InvalidateWatchState()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Успешно удалено из списка продолжения просмотра",
		})
		return
	}

	items, err := h.mounter.GetResumeItems(ctx)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Failed to fetch resume items: %v", err),
			"items": []interface{}{},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if items == nil {
		items = []stream.ResumeItem{}
	}
	_ = json.NewEncoder(w).Encode(items)
}

type loginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	RememberMe bool   `json:"remember_me"`
}

type loginResponse struct {
	Success   bool   `json:"success"`
	Token     string `json:"token"`
	Username  string `json:"username"`
	ExpiresAt string `json:"expires_at"`
}

func (h *Handler) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request format"})
		return
	}

	token, expiresAt, err := h.auth.Login(req.Username, req.Password, req.RememberMe)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid username or password"})
		return
	}

	http.SetCookie(w, h.auth.BuildCookie(token, expiresAt))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(loginResponse{
		Success:   true,
		Token:     token,
		Username:  req.Username,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

func (h *Handler) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	if !h.auth.ValidateRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"authorized"}`))
}

func (h *Handler) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, h.auth.BuildLogoutCookie())
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true,"message":"logged out"}`))
}

func (h *Handler) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if !h.auth.ValidateRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"authenticated":false}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": true,
		"username":      h.auth.GetUsername(),
	})
}

func (h *Handler) handleHomeFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.home == nil {
		http.Error(w, "Home service not available", http.StatusServiceUnavailable)
		return
	}

	platform := r.URL.Query().Get("platform")
	forceRefresh := r.URL.Query().Get("refresh") == "true" || r.URL.Query().Get("refresh_cache") == "true"

	payload, err := h.home.GetHomeFeed(r.Context(), platform, forceRefresh)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) handleHubFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.home == nil {
		http.Error(w, "Home service not available", http.StatusServiceUnavailable)
		return
	}

	platform := r.URL.Query().Get("platform")
	forceRefresh := r.URL.Query().Get("refresh") == "true"

	payload, err := h.home.GetHomeFeed(r.Context(), platform, forceRefresh)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) handleHotlist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.hotlist == nil {
		http.Error(w, "Hotlist service not available", http.StatusServiceUnavailable)
		return
	}

	mediaType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if mediaType == "" {
		mediaType = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("media_type")))
	}
	quality := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("quality")))
	if quality == "" {
		quality = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("resolution")))
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	refresh := r.URL.Query().Get("refresh") == "true" || r.URL.Query().Get("refresh_cache") == "true"

	resp, err := h.hotlist.GetHotlist(r.Context(), mediaType, quality, page, limit, refresh)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}


	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handlePlaybackProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	var item playback.WatchProgressItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[playback] SaveProgress: imdb_id=%s, type=%s, s=%d, e=%d, pos=%.1fs, dur=%.1fs, pct=%.1f%%, completed=%v",
		item.ImdbID, item.MediaType, item.SeasonNumber, item.EpisodeNumber, item.PositionSeconds, item.DurationSeconds, item.PlaybackPercent, item.IsCompleted)
	if h.playback != nil {
		if err := h.playback.SaveProgress(&item); err != nil {
			log.Printf("[playback] SaveProgress ERROR: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if h.home != nil {
			h.home.InvalidateWatchState()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type AudioPreferenceRequest struct {
	ImdbID     string `json:"imdb_id"`
	AudioTitle string `json:"audio_title"`
	AudioIndex int    `json:"audio_index"`
}

func (h *Handler) handlePlaybackAudioPreference(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req AudioPreferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[audio_pref] Received preference request: imdb_id=%s, audio_title=%q, audio_index=%d", req.ImdbID, req.AudioTitle, req.AudioIndex)
	if req.ImdbID == "" {
		http.Error(w, "imdb_id is required", http.StatusBadRequest)
		return
	}
	if h.playback != nil {
		if err := h.playback.SaveAudioPreference(req.ImdbID, req.AudioTitle, req.AudioIndex); err != nil {
			log.Printf("[audio_pref] Failed to save preference: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[audio_pref] Saved preference for %s: %s (index %d)", req.ImdbID, req.AudioTitle, req.AudioIndex)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) handlePlaybackResume(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil {
			limit = l
		}
	}
	var items []playback.WatchProgressItem
	var err error
	if h.playback != nil {
		items, err = h.playback.GetResumeList(limit)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []playback.WatchProgressItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func (h *Handler) handlePlaybackNextUp(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil {
			limit = l
		}
	}
	var items []playback.NextUpItem
	var err error
	if h.nextUp != nil {
		items, err = h.nextUp.GetNextUpItems(limit)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []playback.NextUpItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func (h *Handler) handlePlaybackItem(w http.ResponseWriter, r *http.Request) {
	imdbId := r.URL.Query().Get("imdb_id")
	season, _ := strconv.Atoi(r.URL.Query().Get("season"))
	episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))

	if imdbId == "" {
		http.Error(w, "imdb_id required", http.StatusBadRequest)
		return
	}

	var item *playback.WatchProgressItem
	var err error
	if h.playback != nil {
		item, err = h.playback.GetItemProgress(imdbId, season, episode)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if item == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"found": false})
		return
	}
	_ = json.NewEncoder(w).Encode(item)
}

type MarkWatchedRequest struct {
	ImdbID       string `json:"imdb_id"`
	Mode         string `json:"mode"` // "episode", "season", "up_to", "series"
	Title        string `json:"title,omitempty"`
	PosterPath   string `json:"poster_path,omitempty"`
	BackdropPath string `json:"backdrop_path,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	EpisodeTitle string `json:"episode_title,omitempty"`
	EpisodeStill string `json:"episode_still,omitempty"`
	UpToSeason   int    `json:"up_to_season,omitempty"`
	UpToEpisode  int    `json:"up_to_episode,omitempty"`
	Completed    bool   `json:"completed"`
}

func (h *Handler) handlePlaybackSeriesProgress(w http.ResponseWriter, r *http.Request) {
	imdbId := r.URL.Query().Get("imdb_id")
	if imdbId == "" {
		http.Error(w, "imdb_id required", http.StatusBadRequest)
		return
	}

	var allEps []playback.TmdbEpisodeItem
	if h.nextUp != nil {
		allEps, _ = h.nextUp.FetchSeriesEpisodes(imdbId)
	}

	if h.playback != nil {
		summary, err := h.playback.GetSeriesDetailedProgress(imdbId, allEps)
		if err == nil && summary != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(summary)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"imdb_id":        imdbId,
		"total_episodes": 0,
		"total_watched":  0,
		"is_completed":   false,
		"seasons":        map[string]interface{}{},
		"episodes":       map[string]interface{}{},
	})
}

func (h *Handler) handlePlaybackMarkWatched(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req MarkWatchedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.ImdbID == "" {
		http.Error(w, "imdb_id required", http.StatusBadRequest)
		return
	}

	if h.playback == nil {
		http.Error(w, "Playback store not initialized", http.StatusInternalServerError)
		return
	}

	var allEps []playback.TmdbEpisodeItem
	if h.nextUp != nil {
		allEps, _ = h.nextUp.FetchSeriesEpisodes(req.ImdbID)
	}

	var err error
	switch req.Mode {
	case "season":
		err = h.playback.MarkSeasonWatched(req.ImdbID, req.Title, req.PosterPath, req.BackdropPath, req.Season, allEps, req.Completed)
	case "up_to":
		err = h.playback.MarkAllUpToEpisodeWatched(req.ImdbID, req.Title, req.PosterPath, req.BackdropPath, req.UpToSeason, req.UpToEpisode, allEps)
	case "series":
		err = h.playback.MarkSeriesWatched(req.ImdbID, req.Title, req.PosterPath, req.BackdropPath, allEps, req.Completed)
	default: // "episode"
		err = h.playback.MarkEpisodeWatched(req.ImdbID, req.Title, req.PosterPath, req.BackdropPath, req.Season, req.Episode, req.EpisodeTitle, req.EpisodeStill, req.Completed)
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	if h.home != nil {
		h.home.InvalidateWatchState()
	}

	summary, _ := h.playback.GetSeriesDetailedProgress(req.ImdbID, allEps)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"progress": summary,
	})
}

func (h *Handler) handlePlaybackDelete(w http.ResponseWriter, r *http.Request) {
	imdbId := r.URL.Query().Get("imdb_id")
	season, _ := strconv.Atoi(r.URL.Query().Get("season"))
	episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))

	if imdbId == "" {
		http.Error(w, "imdb_id required", http.StatusBadRequest)
		return
	}

	if h.playback != nil {
		if season == 0 && episode == 0 {
			_ = h.playback.DeleteShowProgress(imdbId)
		} else {
			_ = h.playback.DeleteItemProgress(imdbId, season, episode)
		}
		if h.home != nil {
			h.home.InvalidateWatchState()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	if h.playback == nil {
		http.Error(w, "Playback store not initialized", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, err := h.playback.GetWatchlist(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []playback.WatchlistItem{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)

	case http.MethodPost:
		var item playback.WatchlistItem
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if err := h.playback.AddToWatchlist(r.Context(), item); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if h.home != nil {
			h.home.InvalidateWatchState()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})

	case http.MethodDelete:
		imdbID := r.URL.Query().Get("imdb_id")
		if imdbID == "" {
			var body struct {
				ImdbID string `json:"imdb_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				imdbID = body.ImdbID
			}
		}
		if imdbID == "" {
			http.Error(w, "imdb_id required", http.StatusBadRequest)
			return
		}
		if err := h.playback.RemoveFromWatchlist(r.Context(), imdbID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if h.home != nil {
			h.home.InvalidateWatchState()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleWatchlistCheck(w http.ResponseWriter, r *http.Request) {
	if h.playback == nil {
		http.Error(w, "Playback store not initialized", http.StatusInternalServerError)
		return
	}

	imdbID := r.URL.Query().Get("imdb_id")
	if imdbID == "" {
		http.Error(w, "imdb_id required", http.StatusBadRequest)
		return
	}

	inList, err := h.playback.IsInWatchlist(r.Context(), imdbID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"in_watchlist": inList})
}

// Transcode Handlers

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (h *Handler) handleTranscodeProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(transcode.AvailableProfiles())
}

func (h *Handler) handleTranscodeStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session")
	hash := r.URL.Query().Get("hash")

	if r.Header.Get("Content-Type") == "application/json" {
		var req struct {
			Session string `json:"session"`
			Hash    string `json:"hash"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			if req.Session != "" {
				sessionID = req.Session
			}
			if req.Hash != "" {
				hash = req.Hash
			}
		}
	}

	if h.transcode != nil {
		if sessionID != "" {
			h.transcode.StopSession(sessionID)
		}
		if hash != "" {
			h.transcode.StopSessionsForHash(hash)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) handleTranscodeSegment(w http.ResponseWriter, r *http.Request) {
	if h.transcode == nil {
		http.Error(w, "Transcode engine unavailable", http.StatusServiceUnavailable)
		return
	}

	// Path: /api/stream/transcode/seg/{sessionID}/{filename}
	path := strings.TrimPrefix(r.URL.Path, "/api/stream/transcode/seg/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	sessionID := parts[0]
	filename := parts[1]

	sess := h.transcode.GetSession(sessionID)
	if sess == nil {
		// Session revival: sessionID format: {hash}_{fileIdx}_{profile}_{audioIdx}[_{customID}]
		idParts := strings.Split(sessionID, "_")
		if len(idParts) >= 4 {
			hash := idParts[0]
			fileIdx, _ := strconv.Atoi(idParts[1])
			profile := transcode.GetProfile(idParts[2])
			audioIdx, _ := strconv.Atoi(idParts[3])
			customID := ""
			if len(idParts) >= 5 {
				customID = strings.Join(idParts[4:], "_")
			}
			segNum := 0
			if matches := regexp.MustCompile(`seg_(\d+)\.ts`).FindStringSubmatch(filename); len(matches) > 1 {
				segNum, _ = strconv.Atoi(matches[1])
			}
			startSec := float64(segNum) * 3.0
			log.Printf("[Transcode] Reviving expired/missing session %s at seg %d (start=%.1fs)", sessionID, segNum, startSec)
			var err error
			sess, err = h.transcode.GetOrCreateSession(hash, fileIdx, profile, audioIdx, startSec, 7200.0, customID)
			if err != nil {
				log.Printf("[Transcode] Failed to revive session %s: %v", sessionID, err)
			}
		}
	}
	if sess == nil {
		http.Error(w, "Transcode session not found or expired", http.StatusNotFound)
		return
	}

	data, err := sess.GetSegment(filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) handleTranscodePlaylist(w http.ResponseWriter, r *http.Request) {
	if h.transcode == nil {
		http.Error(w, "Transcode engine unavailable", http.StatusServiceUnavailable)
		return
	}

	// Path: /api/stream/transcode/{hash}/master.m3u8 or /api/stream/transcode/{hash}/live.m3u8
	path := strings.TrimPrefix(r.URL.Path, "/api/stream/transcode/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	hash := parts[0]
	profileID := r.URL.Query().Get("profile")
	profile := transcode.GetProfile(profileID)

	fileIdx := 0
	if idxStr := firstNonEmpty(r.URL.Query().Get("file_idx"), r.URL.Query().Get("index"), r.URL.Query().Get("id")); idxStr != "" {
		if n, err := strconv.Atoi(idxStr); err == nil {
			fileIdx = n
		}
	}

	audioIdx := 0
	if aStr := r.URL.Query().Get("audio"); aStr != "" {
		if n, err := strconv.Atoi(aStr); err == nil {
			audioIdx = n
		}
	}

	startSec := 0.0
	if sStr := r.URL.Query().Get("start"); sStr != "" {
		if f, err := strconv.ParseFloat(sStr, 64); err == nil {
			startSec = f
		}
	}

	customID := r.URL.Query().Get("s")

	durationSec := 0.0
	if dStr := r.URL.Query().Get("duration"); dStr != "" {
		if f, err := strconv.ParseFloat(dStr, 64); err == nil && f > 0 {
			durationSec = f
		}
	}
	if durationSec <= 0 && h.mounter != nil {
		durationSec = h.mounter.GetStreamDuration(r.Context(), hash, fileIdx)
	}
	if durationSec <= 0 {
		durationSec = 7200.0 // 2 hours default fallback
	}

	sess, err := h.transcode.GetOrCreateSession(hash, fileIdx, profile, audioIdx, startSec, durationSec, customID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Transcode failed: %v", err), http.StatusBadGateway)
		return
	}

	vodPlaylist := transcode.GenerateVODPlaylist(sess.ID, durationSec, startSec)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(vodPlaylist))
}


