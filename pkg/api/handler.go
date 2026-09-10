package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"tracker-proxy/pkg/aggregator"
	"tracker-proxy/pkg/auth"
	"tracker-proxy/pkg/cache"
	"tracker-proxy/pkg/hotlist"
	"tracker-proxy/pkg/models"
	"tracker-proxy/pkg/playback"
	"tracker-proxy/pkg/stream"
	"tracker-proxy/pkg/tracker"
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
) *Handler {
	return &Handler{
		aggregator: agg,
		cache:      cacheStore,
		mounter:    streamSvc,
		playback:   playStore,
		nextUp:     nextUpSvc,
		auth:       authMgr,
		hotlist:    hotlistSvc,
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
	mux.HandleFunc("/api/stream/player/info", h.corsMiddleware(h.handlePlayerInfo))
	mux.HandleFunc("/api/stream/player/start", h.corsMiddleware(h.handlePlayerStart))
	mux.HandleFunc("/api/stream/player/progress", h.corsMiddleware(h.handlePlayerProgress))
	mux.HandleFunc("/api/stream/player/stop", h.corsMiddleware(h.handlePlayerStop))
	mux.HandleFunc("/api/stream/resume", h.corsMiddleware(h.handleStreamResume))
	mux.HandleFunc("/stream/resume", h.corsMiddleware(h.handleStreamResume))

	// Native Playback & Watch History (SQLite)
	mux.HandleFunc("/api/playback/progress", h.corsMiddleware(h.handlePlaybackProgress))
	mux.HandleFunc("/api/playback/resume", h.corsMiddleware(h.handlePlaybackResume))
	mux.HandleFunc("/api/playback/next-up", h.corsMiddleware(h.handlePlaybackNextUp))
	mux.HandleFunc("/api/playback/item", h.corsMiddleware(h.handlePlaybackItem))
	mux.HandleFunc("/api/playback/series-progress", h.corsMiddleware(h.handlePlaybackSeriesProgress))
	mux.HandleFunc("/api/playback/delete", h.corsMiddleware(h.handlePlaybackDelete))

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

func (h *Handler) executeSearch(ctx context.Context, queryStr, imdbID, mediaType string, season int, refreshCache bool, limit int) ([]models.TorrentResult, string) {
	normIMDb := cache.NormalizeIMDbID(imdbID)
	cacheStatus := "BYPASS"

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
			if limit > 0 && len(filtered) > limit {
				filtered = filtered[:limit]
			}
			return filtered, "HIT"
		}
	}

	if normIMDb != "" {
		if refreshCache {
			cacheStatus = "REFRESHED"
		} else {
			cacheStatus = "MISS"
		}

		v, err, _ := h.sf.Do(normIMDb, func() (any, error) {
			res := h.aggregator.Search(ctx, models.SearchQuery{
				Query:        queryStr,
				Type:         mediaType,
				IMDbID:       normIMDb,
				RefreshCache: refreshCache,
				Limit:        limit,
			})
			if h.cache != nil && len(res) > 0 {
				_ = h.cache.Set(normIMDb, queryStr, res)
			}
			return res, nil
		})
		if err == nil && v != nil {
			results := v.([]models.TorrentResult)
			filtered := filterAndRankBySeason(results, season)
			if limit > 0 && len(filtered) > limit {
				filtered = filtered[:limit]
			}
			return filtered, cacheStatus
		}
	}

	results := h.aggregator.Search(ctx, models.SearchQuery{
		Query: queryStr,
		Type:  mediaType,
		Limit: limit,
	})
	filtered := filterAndRankBySeason(results, season)
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

	start := time.Now()
	results, cacheStatus := h.executeSearch(r.Context(), queryStr, imdbID, mediaType, season, refreshCache, limit)
	duration := time.Since(start)

	log.Printf("[search] query=%q imdb_id=%q type=%q season=%d cache=%s returned=%d duration=%v", queryStr, imdbID, mediaType, season, cacheStatus, len(results), duration)

	w.Header().Set("X-Cache", cacheStatus)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
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

		start := time.Now()
		results, cacheStatus := h.executeSearch(r.Context(), queryStr, imdbID, torznabType, season, refreshCache, limit)

		duration := time.Since(start)

		log.Printf("[torznab] t=%s query=%q imdb_id=%q season=%d cache=%s returned=%d duration=%v", t, queryStr, imdbID, season, cacheStatus, len(results), duration)

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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) handleStreamResume(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

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
	if h.playback != nil {
		if err := h.playback.SaveProgress(&item); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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

func (h *Handler) handlePlaybackSeriesProgress(w http.ResponseWriter, r *http.Request) {
	imdbId := r.URL.Query().Get("imdb_id")
	if imdbId == "" {
		http.Error(w, "imdb_id required", http.StatusBadRequest)
		return
	}

	var res map[string]playback.EpisodeProgressStatus
	var err error
	if h.playback != nil {
		res, err = h.playback.GetSeriesProgress(imdbId)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if res == nil {
		res = make(map[string]playback.EpisodeProgressStatus)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
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
		_ = h.playback.DeleteItemProgress(imdbId, season, episode)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
