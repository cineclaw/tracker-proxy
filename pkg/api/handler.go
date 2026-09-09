package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"tracker-proxy/pkg/aggregator"
	"tracker-proxy/pkg/auth"
	"tracker-proxy/pkg/cache"
	"tracker-proxy/pkg/hotlist"
	"tracker-proxy/pkg/models"
	"tracker-proxy/pkg/stream"
	"tracker-proxy/pkg/tracker"
	"tracker-proxy/pkg/version"
)

type Handler struct {
	aggregator *aggregator.Aggregator
	cache      *cache.Store
	mounter    *stream.Mounter
	auth       *auth.Manager
	hotlist    *hotlist.Service
	sf         singleflight.Group
}

func NewHandler(agg *aggregator.Aggregator, cacheStore *cache.Store, authMgr *auth.Manager, hotlistSvc *hotlist.Service) *Handler {
	m := stream.NewMounter(agg)
	m.StartReconciler(context.Background(), 5*time.Minute)
	return &Handler{
		aggregator: agg,
		cache:      cacheStore,
		mounter:    m,
		auth:       authMgr,
		hotlist:    hotlistSvc,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.handleHealth)
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

	// Jellyfin webhook endpoints
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



