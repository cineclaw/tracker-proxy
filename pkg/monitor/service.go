package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"tracker-proxy/pkg/aggregator"
	"tracker-proxy/pkg/cache"
	"tracker-proxy/pkg/models"
	"tracker-proxy/pkg/notifications"
	"tracker-proxy/pkg/stream"
	"tracker-proxy/pkg/tracker"
)

type SeriesSubscription struct {
	ID                     int64     `json:"id,omitempty"`
	ImdbID                 string    `json:"imdb_id"`
	Profile                string    `json:"profile"`
	PreferredStudio        string    `json:"preferred_studio"`
	LastNotifiedSeason     int       `json:"last_notified_season"`
	LastNotifiedEpisode    int       `json:"last_notified_episode"`
	PreferredStudioNotified bool     `json:"preferred_studio_notified"`
	IsActive               bool      `json:"is_active"`
	UpdatedAt              time.Time `json:"updated_at,omitempty"`
}

type DetectedRelease struct {
	ImdbID    string `json:"imdb_id"`
	Title     string `json:"title"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Studio    string `json:"studio"`
	Quality   string `json:"quality"`
	InfoHash  string `json:"info_hash"`
	Magnet    string `json:"magnet"`
	Stage     int    `json:"stage"` // 1: any release, 2: preferred studio release
}

type Service struct {
	db          *sql.DB
	aggregator  *aggregator.Aggregator
	torrClient  *stream.TorrClient
	pushSvc     *notifications.Service
	cacheStore  *cache.Store
	indexerURL  string
	httpClient  *http.Client
	stopChan    chan struct{}
	mu          sync.Mutex
	isChecking  bool
}

func NewService(
	db *sql.DB,
	agg *aggregator.Aggregator,
	torr *stream.TorrClient,
	push *notifications.Service,
	cs *cache.Store,
	indexerURL string,
) *Service {
	return &Service{
		db:         db,
		aggregator: agg,
		torrClient: torr,
		pushSvc:    push,
		cacheStore: cs,
		indexerURL: strings.TrimRight(indexerURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		stopChan:   make(chan struct{}),
	}
}

func (s *Service) Start(interval time.Duration) {
	if interval < 5*time.Minute {
		interval = 30 * time.Minute
	}
	log.Printf("[monitor] Series monitor daemon started (interval: %v)", interval)

	go func() {
		// Initial check after 30 seconds startup delay
		select {
		case <-time.After(30 * time.Second):
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			_, _ = s.CheckAll(ctx)
			cancel()
		case <-s.stopChan:
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				_, _ = s.CheckAll(ctx)
				cancel()
			case <-s.stopChan:
				return
			}
		}
	}()
}

func (s *Service) Stop() {
	close(s.stopChan)
}

// CheckAll scans all tracked ongoing series and triggers notifications for newly released episodes
func (s *Service) CheckAll(ctx context.Context) ([]DetectedRelease, error) {
	s.mu.Lock()
	if s.isChecking {
		s.mu.Unlock()
		return nil, nil
	}
	s.isChecking = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isChecking = false
		s.mu.Unlock()
	}()

	seriesList, err := s.gatherTrackedSeries(ctx)
	if err != nil {
		log.Printf("[monitor] Failed to gather tracked series: %v", err)
		return nil, err
	}

	if len(seriesList) == 0 {
		return nil, nil
	}

	log.Printf("[monitor] Running check for %d tracked ongoing series...", len(seriesList))
	var allDetected []DetectedRelease

	for _, id := range seriesList {
		select {
		case <-ctx.Done():
			return allDetected, ctx.Err()
		default:
			detected, err := s.CheckSeries(ctx, id)
			if err != nil {
				log.Printf("[monitor] Error checking series %s: %v", id, err)
				continue
			}
			allDetected = append(allDetected, detected...)
		}
	}

	if len(allDetected) > 0 {
		log.Printf("[monitor] Scan completed: %d new episode releases detected and processed", len(allDetected))
	}
	return allDetected, nil
}

// CheckSeries scans tracker releases for a single series and sends two-stage push notifications
func (s *Service) CheckSeries(ctx context.Context, imdbID string) ([]DetectedRelease, error) {
	normIMDb := cache.NormalizeIMDbID(imdbID)
	if normIMDb == "" {
		return nil, fmt.Errorf("invalid imdb_id: %s", imdbID)
	}

	meta := s.fetchMetadata(ctx, normIMDb)
	if meta == nil || meta.Title == "" {
		return nil, fmt.Errorf("metadata not found for %s", normIMDb)
	}

	// 1. Get latest watched/notified state and preferred studio
	season, lastNotifiedEp, preferredStudio, profile := s.getSeriesTrackingState(normIMDb)
	if season <= 0 {
		season = 1
	}

	// 2. Query trackers with fresh search for this season
	searchQuery := fmt.Sprintf("%s %d сезон", meta.Title, season)
	res := s.aggregator.Search(ctx, models.SearchQuery{
		Query:  searchQuery,
		Type:   "tv",
		IMDbID: normIMDb,
		Limit:  30,
	})

	if len(res) == 0 {
		res = s.aggregator.Search(ctx, models.SearchQuery{
			Query:  meta.Title,
			Type:   "tv",
			IMDbID: normIMDb,
			Limit:  30,
		})
	}

	if len(res) == 0 {
		return nil, nil
	}

	var detected []DetectedRelease

	for _, item := range res {
		seasons, _ := tracker.ExtractSeasonInfo(item.Title)
		if len(seasons) > 0 {
			matchesSeason := false
			for _, sNum := range seasons {
				if sNum == season {
					matchesSeason = true
					break
				}
			}
			if !matchesSeason {
				continue // Torrents from another season shouldn't trigger notifications
			}
		}

		maxEp := tracker.ExtractEpisodeMax(item.Title)
		if maxEp <= lastNotifiedEp {
			continue
		}

		studios := tracker.ExtractStudios(item.Title)
		if len(studios) == 0 {
			continue // No identified Russian studio/dub yet
		}

		// Resolve live hash
		hash := item.InfoHash
		if hash == "" && item.Tracker != "" && item.ID != "" {
			freshHash, err := s.aggregator.ResolveInfoHashFresh(ctx, item.Tracker, item.ID)
			if err == nil && freshHash != "" {
				hash = freshHash
				item.InfoHash = freshHash
				item.Magnet = aggregator.BuildMultiTrackerMagnet(freshHash, item.Title, nil, []string{item.Tracker})
			}
		}

		if hash == "" {
			continue
		}

		primaryStudio := studios[0]
		quality := tracker.ExtractResolution(item.Title)
		if quality == "" {
			quality = "1080p"
		}

		// Stage 1: Initial release in any Russian dub
		if !s.isReleaseNotified(normIMDb, season, maxEp, 1) {
			d := DetectedRelease{
				ImdbID:   normIMDb,
				Title:    meta.Title,
				Season:   season,
				Episode:  maxEp,
				Studio:   primaryStudio,
				Quality:  quality,
				InfoHash: hash,
				Magnet:   item.Magnet,
				Stage:    1,
			}
			s.triggerNotification(ctx, d, profile, false)
			s.recordRelease(d, 1)
			s.updateLastNotified(normIMDb, profile, season, maxEp, false)
			lastNotifiedEp = maxEp
			detected = append(detected, d)

			// Pre-warm in TorrServer
			s.prewarmTorrServer(ctx, item.Magnet, item.Title)
		}

		// Stage 2: Release in user's preferred studio (e.g. Red Head Sound)
		if preferredStudio != "" && !strings.EqualFold(preferredStudio, "любая") {
			if tracker.MatchesStudio(studios, preferredStudio) {
				if !s.isReleaseNotified(normIMDb, season, maxEp, 2) {
					d := DetectedRelease{
						ImdbID:   normIMDb,
						Title:    meta.Title,
						Season:   season,
						Episode:  maxEp,
						Studio:   preferredStudio,
						Quality:  quality,
						InfoHash: hash,
						Magnet:   item.Magnet,
						Stage:    2,
					}
					s.triggerNotification(ctx, d, profile, true)
					s.recordRelease(d, 2)
					s.updateLastNotified(normIMDb, profile, season, maxEp, true)
					detected = append(detected, d)

					// Pre-warm in TorrServer
					s.prewarmTorrServer(ctx, item.Magnet, item.Title)
				}
			}
		}
	}

	return detected, nil
}

func (s *Service) triggerNotification(ctx context.Context, d DetectedRelease, profile string, isPreferredStage bool) {
	var title, body string
	if isPreferredStage {
		title = fmt.Sprintf("«%s»: %d серия в озвучке %s!", d.Title, d.Episode, d.Studio)
		body = fmt.Sprintf("Сезон %d, Серия %d уже доступна в вашей любимой озвучке (%s). Нажмите для просмотра!", d.Season, d.Episode, d.Quality)
	} else {
		title = fmt.Sprintf("Вышла %d серия «%s»!", d.Episode, d.Title)
		body = fmt.Sprintf("Сезон %d, Серия %d доступна в озвучке %s (%s). Нажмите для просмотра!", d.Season, d.Episode, d.Studio, d.Quality)
	}

	playURL := fmt.Sprintf("/?play=%s&season=%d&episode=%d", d.ImdbID, d.Season, d.Episode)
	payload := notifications.NotificationPayload{
		Title: title,
		Body:  body,
		Icon:  "/icons/icon-192x192.png",
		Badge: "/icons/icon-192x192.png",
		Tag:   fmt.Sprintf("%s_s%d_e%d_stage%d", d.ImdbID, d.Season, d.Episode, d.Stage),
		Data: map[string]interface{}{
			"url":     playURL,
			"imdb_id": d.ImdbID,
			"season":  d.Season,
			"episode": d.Episode,
		},
	}

	if s.pushSvc != nil {
		_, err := s.pushSvc.SendNotification(ctx, payload, profile)
		if err != nil {
			log.Printf("[monitor] Error sending push for %s S%02dE%02d: %v", d.ImdbID, d.Season, d.Episode, err)
		}
	}
}

func (s *Service) prewarmTorrServer(ctx context.Context, magnet, title string) {
	if s.torrClient != nil && magnet != "" {
		_, _ = s.torrClient.AddTorrent(ctx, magnet, title)
	}
}

func (s *Service) isReleaseNotified(imdbID string, season, episode, stage int) bool {
	var count int
	_ = s.db.QueryRow(`
		SELECT COUNT(1) FROM series_detected_releases 
		WHERE imdb_id = ? AND season = ? AND episode = ? AND stage = ?
	`, imdbID, season, episode, stage).Scan(&count)
	return count > 0
}

func (s *Service) recordRelease(d DetectedRelease, stage int) {
	query := `
	INSERT OR IGNORE INTO series_detected_releases 
	(imdb_id, season, episode, studio, quality, info_hash, magnet, stage, notified_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`
	_, _ = s.db.Exec(query, d.ImdbID, d.Season, d.Episode, d.Studio, d.Quality, d.InfoHash, d.Magnet, stage)
}

func (s *Service) updateLastNotified(imdbID, profile string, season, episode int, prefNotified bool) {
	prefInt := 0
	if prefNotified {
		prefInt = 1
	}
	query := `
	INSERT INTO series_subscriptions (imdb_id, profile, last_notified_season, last_notified_episode, preferred_studio_notified, is_active, updated_at)
	VALUES (?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
	ON CONFLICT(imdb_id, profile) DO UPDATE SET
		last_notified_season = excluded.last_notified_season,
		last_notified_episode = excluded.last_notified_episode,
		preferred_studio_notified = CASE WHEN excluded.preferred_studio_notified = 1 THEN 1 ELSE series_subscriptions.preferred_studio_notified END,
		updated_at = CURRENT_TIMESTAMP;
	`
	_, _ = s.db.Exec(query, imdbID, profile, season, episode, prefInt)
}

func (s *Service) getSeriesTrackingState(imdbID string) (season int, lastNotifiedEp int, preferredStudio string, profile string) {
	profile = "default"

	// 1. Check explicit subscription
	var prefStudio sql.NullString
	var lastS, lastE sql.NullInt64
	err := s.db.QueryRow(`
		SELECT profile, preferred_studio, last_notified_season, last_notified_episode 
		FROM series_subscriptions 
		WHERE imdb_id = ? AND is_active = 1
		LIMIT 1
	`, imdbID).Scan(&profile, &prefStudio, &lastS, &lastE)

	if err == nil {
		if prefStudio.Valid {
			preferredStudio = prefStudio.String
		}
		if lastS.Valid && lastS.Int64 > 0 {
			season = int(lastS.Int64)
		}
		if lastE.Valid {
			lastNotifiedEp = int(lastE.Int64)
		}
	}

	// 2. Fallback preferred studio from media_audio_preferences
	if preferredStudio == "" {
		var audioTitle string
		_ = s.db.QueryRow(`SELECT audio_title FROM media_audio_preferences WHERE imdb_id = ?`, imdbID).Scan(&audioTitle)
		if audioTitle != "" {
			preferredStudio = tracker.NormalizeStudio(audioTitle)
		}
	}

	// 3. Fallback season & episode from watch_progress
	if season == 0 || lastNotifiedEp == 0 {
		var wpSeason, wpEpisode int
		err := s.db.QueryRow(`
			SELECT season_number, episode_number 
			FROM watch_progress 
			WHERE imdb_id = ? AND media_type = 'tv' 
			ORDER BY season_number DESC, episode_number DESC 
			LIMIT 1
		`, imdbID).Scan(&wpSeason, &wpEpisode)
		if err == nil && wpSeason > 0 {
			if season == 0 {
				season = wpSeason
			}
			if lastNotifiedEp < wpEpisode {
				lastNotifiedEp = wpEpisode
			}
		}
	}

	return season, lastNotifiedEp, preferredStudio, profile
}

func (s *Service) gatherTrackedSeries(ctx context.Context) ([]string, error) {
	foundMap := make(map[string]bool)
	var list []string

	// A. Explicit series_subscriptions
	rows, err := s.db.QueryContext(ctx, `SELECT imdb_id FROM series_subscriptions WHERE is_active = 1`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil && id != "" && !foundMap[id] {
				foundMap[id] = true
				list = append(list, id)
			}
		}
	}

	// B. Watchlist TV shows
	rowsWL, err := s.db.QueryContext(ctx, `SELECT imdb_id FROM media_watchlist WHERE media_type = 'tv'`)
	if err == nil {
		defer rowsWL.Close()
		for rowsWL.Next() {
			var id string
			if err := rowsWL.Scan(&id); err == nil && id != "" && !foundMap[id] {
				foundMap[id] = true
				list = append(list, id)
			}
		}
	}

	// C. Actively watched ongoing series
	rowsWP, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT imdb_id 
		FROM watch_progress 
		WHERE media_type = 'tv' AND last_watched_at > datetime('now', '-30 days')
	`)
	if err == nil {
		defer rowsWP.Close()
		for rowsWP.Next() {
			var id string
			if err := rowsWP.Scan(&id); err == nil && id != "" && !foundMap[id] {
				foundMap[id] = true
				list = append(list, id)
			}
		}
	}

	return list, nil
}

type indexerResult struct {
	Tconst string `json:"tconst"`
	Title  string `json:"title"`
	Year   int    `json:"year"`
	Type   string `json:"title_type"`
}

func (s *Service) fetchMetadata(ctx context.Context, imdbID string) *indexerResult {
	if s.indexerURL != "" {
		reqURL := fmt.Sprintf("%s/search?q=%s&limit=1", s.indexerURL, url.QueryEscape(imdbID))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err == nil {
			resp, err := s.httpClient.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				defer resp.Body.Close()
				var data struct {
					Hits []struct {
						Movie struct {
							Tconst       string `json:"tconst"`
							TitleRu      string `json:"title_ru"`
							TitlePrimary string `json:"title_primary"`
							TitleOrig    string `json:"title_orig"`
							Year         int    `json:"year"`
							TitleType    string `json:"title_type"`
						} `json:"movie"`
					} `json:"hits"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&data); err == nil && len(data.Hits) > 0 {
					m := data.Hits[0].Movie
					title := m.TitleRu
					if title == "" {
						title = m.TitlePrimary
					}
					if title == "" {
						title = m.TitleOrig
					}
					return &indexerResult{
						Tconst: m.Tconst,
						Title:  title,
						Year:   m.Year,
						Type:   m.TitleType,
					}
				}
			}
		}
	}

	// Resilient fallback from SQLite local store
	var dbTitle string
	_ = s.db.QueryRowContext(ctx, `SELECT title FROM watch_progress WHERE imdb_id = ? AND title != '' LIMIT 1`, imdbID).Scan(&dbTitle)
	if dbTitle == "" {
		_ = s.db.QueryRowContext(ctx, `SELECT title FROM media_watchlist WHERE imdb_id = ? AND title != '' LIMIT 1`, imdbID).Scan(&dbTitle)
	}
	if dbTitle != "" {
		return &indexerResult{
			Tconst: imdbID,
			Title:  dbTitle,
			Type:   "tvSeries",
		}
	}

	return nil
}

// Subscription CRUD API methods
func (s *Service) GetSubscription(imdbID, profile string) (*SeriesSubscription, error) {
	normIMDb := cache.NormalizeIMDbID(imdbID)
	if profile == "" {
		profile = "default"
	}

	var sub SeriesSubscription
	var prefStudio sql.NullString
	var lastS, lastE sql.NullInt64
	var prefNotified sql.NullBool
	var updatedStr string

	err := s.db.QueryRow(`
		SELECT id, imdb_id, profile, preferred_studio, last_notified_season, last_notified_episode, preferred_studio_notified, is_active, updated_at
		FROM series_subscriptions
		WHERE imdb_id = ? AND profile = ?
	`, normIMDb, profile).Scan(
		&sub.ID, &sub.ImdbID, &sub.Profile, &prefStudio, &lastS, &lastE, &prefNotified, &sub.IsActive, &updatedStr,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			// Auto-fill from media_audio_preferences
			var autoStudio string
			_ = s.db.QueryRow(`SELECT audio_title FROM media_audio_preferences WHERE imdb_id = ?`, normIMDb).Scan(&autoStudio)
			return &SeriesSubscription{
				ImdbID:          normIMDb,
				Profile:         profile,
				PreferredStudio: tracker.NormalizeStudio(autoStudio),
				IsActive:        true,
			}, nil
		}
		return nil, err
	}

	if prefStudio.Valid {
		sub.PreferredStudio = prefStudio.String
	}
	if lastS.Valid {
		sub.LastNotifiedSeason = int(lastS.Int64)
	}
	if lastE.Valid {
		sub.LastNotifiedEpisode = int(lastE.Int64)
	}
	if prefNotified.Valid {
		sub.PreferredStudioNotified = prefNotified.Bool
	}

	return &sub, nil
}

func (s *Service) SetSubscription(sub SeriesSubscription) error {
	normIMDb := cache.NormalizeIMDbID(sub.ImdbID)
	if normIMDb == "" {
		return fmt.Errorf("invalid imdb_id")
	}
	if sub.Profile == "" {
		sub.Profile = "default"
	}

	query := `
	INSERT INTO series_subscriptions (imdb_id, profile, preferred_studio, is_active, updated_at)
	VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(imdb_id, profile) DO UPDATE SET
		preferred_studio = excluded.preferred_studio,
		is_active = excluded.is_active,
		updated_at = CURRENT_TIMESTAMP;
	`
	activeInt := 0
	if sub.IsActive {
		activeInt = 1
	}
	_, err := s.db.Exec(query, normIMDb, sub.Profile, sub.PreferredStudio, activeInt)
	return err
}
