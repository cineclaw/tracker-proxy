package playback

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tracker-proxy/pkg/db"
)

type Store struct {
	db         *db.DB
	indexerURL string
	httpClient *http.Client
}

func NewStore(database *db.DB, indexerURL string) *Store {
	s := &Store{
		db:         database,
		indexerURL: strings.TrimRight(indexerURL, "/"),
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}
	s.AutoHealLegacyPosters()
	return s
}

// ResolveTmdbPoster resolves pure relative poster and backdrop paths from imdb-indexer
func (s *Store) ResolveTmdbPoster(imdbID string) (posterPath, backdropPath string) {
	if s.indexerURL == "" || imdbID == "" {
		return "", ""
	}
	reqURL := fmt.Sprintf("%s/search?q=%s&limit=1", s.indexerURL, url.QueryEscape(imdbID))
	resp, err := s.httpClient.Get(reqURL)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	var data struct {
		Hits []struct {
			PosterPath   *string `json:"poster_path"`
			BackdropPath *string `json:"backdrop_path"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err == nil && len(data.Hits) > 0 {
		if data.Hits[0].PosterPath != nil {
			posterPath = *data.Hits[0].PosterPath
		}
		if data.Hits[0].BackdropPath != nil {
			backdropPath = *data.Hits[0].BackdropPath
		}
	}
	return posterPath, backdropPath
}

// AutoHealLegacyPosters scans SQLite on startup and repairs any legacy /poster/ or empty paths
func (s *Store) AutoHealLegacyPosters() {
	if s.indexerURL == "" {
		return
	}
	go func() {
		// 1. Repair media_watchlist
		rows, err := s.db.Query(`SELECT imdb_id FROM media_watchlist WHERE poster_path LIKE '%/poster/%' OR poster_path = '' OR poster_path IS NULL`)
		if err == nil {
			var ids []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil && id != "" {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				p, b := s.ResolveTmdbPoster(id)
				if p != "" {
					_, _ = s.db.Exec(`UPDATE media_watchlist SET poster_path = ?, backdrop_path = ? WHERE imdb_id = ?`, p, b, id)
				}
			}
		}

		// 2. Repair watch_progress
		rowsWp, err := s.db.Query(`SELECT DISTINCT imdb_id FROM watch_progress WHERE (poster_path LIKE '%/poster/%' OR poster_path = '' OR poster_path IS NULL) AND imdb_id != ''`)
		if err == nil {
			var wpIds []string
			for rowsWp.Next() {
				var id string
				if err := rowsWp.Scan(&id); err == nil && id != "" {
					wpIds = append(wpIds, id)
				}
			}
			rowsWp.Close()
			for _, id := range wpIds {
				p, b := s.ResolveTmdbPoster(id)
				if p != "" {
					_, _ = s.db.Exec(`UPDATE watch_progress SET poster_path = ?, backdrop_path = ? WHERE imdb_id = ?`, p, b, id)
				}
			}
		}
	}()
}

type WatchProgressItem struct {
	ID               int64     `json:"id"`
	ImdbID           string    `json:"imdb_id"`
	MediaType        string    `json:"media_type"` // "movie" or "tv"
	Title            string    `json:"title"`
	PosterPath       string    `json:"poster_path"`
	BackdropPath     string    `json:"backdrop_path"`
	SeasonNumber     int       `json:"season_number"`
	EpisodeNumber    int       `json:"episode_number"`
	EpisodeTitle     string    `json:"episode_title"`
	EpisodeStillPath string    `json:"episode_still_path"`
	PositionSeconds  float64   `json:"position_seconds"`
	DurationSeconds  float64   `json:"duration_seconds"`
	PlaybackPercent  float64   `json:"playback_percent"`
	IsCompleted      bool      `json:"is_completed"`
	TorrentHash      string    `json:"torrent_hash"`
	TorrentLink      string    `json:"torrent_link"`
	FileIndex        int       `json:"file_index"`
	AudioIndex       int       `json:"audio_index"`
	AudioTitle       string    `json:"audio_title,omitempty"`
	SubtitleIndex    int       `json:"subtitle_index"`
	LastWatchedAt    time.Time `json:"last_watched_at"`
}

type AudioPreference struct {
	ImdbID     string    `json:"imdb_id"`
	AudioTitle string    `json:"audio_title"`
	AudioIndex int       `json:"audio_index"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type EpisodeProgressStatus struct {
	SeasonNumber    int     `json:"season_number"`
	EpisodeNumber   int     `json:"episode_number"`
	PositionSeconds float64 `json:"position_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
	PlaybackPercent float64 `json:"playback_percent"`
	IsCompleted     bool    `json:"is_completed"`
}

type SeasonProgressSummary struct {
	SeasonNumber    int  `json:"season_number"`
	TotalEpisodes   int  `json:"total_episodes"`
	WatchedEpisodes int  `json:"watched_episodes"`
	IsCompleted     bool `json:"is_completed"`
}

type SeriesProgressResponse struct {
	ImdbID               string                           `json:"imdb_id"`
	TotalEpisodes        int                              `json:"total_episodes"`
	TotalWatched         int                              `json:"total_watched"`
	IsCompleted          bool                             `json:"is_completed"`
	HasUnwatchedPrior    bool                             `json:"has_unwatched_prior"`
	LatestWatchedSeason  int                              `json:"latest_watched_season,omitempty"`
	LatestWatchedEpisode int                              `json:"latest_watched_episode,omitempty"`
	Seasons              map[string]SeasonProgressSummary `json:"seasons"`
	Episodes             map[string]EpisodeProgressStatus `json:"episodes"`
}

func parseSQLiteTime(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now()
}

// SaveProgress upserts playback progress, automatically calculating percent and completion status
func (s *Store) SaveProgress(item *WatchProgressItem) error {
	if strings.HasPrefix(item.PosterPath, "/poster/") || item.PosterPath == "" {
		if p, b := s.ResolveTmdbPoster(item.ImdbID); p != "" {
			item.PosterPath = p
			if item.BackdropPath == "" {
				item.BackdropPath = b
			}
		}
	}

	if item.DurationSeconds > 0 {
		item.PlaybackPercent = (item.PositionSeconds / item.DurationSeconds) * 100.0
		if item.PlaybackPercent > 100.0 {
			item.PlaybackPercent = 100.0
		}
	} else {
		item.PlaybackPercent = 0.0
	}

	// Smart credits and completion detection: >= 88% or <= 240s remaining with >= 80%
	isCreditsOrEnd := item.PlaybackPercent >= 88.0 ||
		(item.DurationSeconds > 300 && (item.DurationSeconds-item.PositionSeconds) <= 240 && item.PlaybackPercent >= 80.0)

	if item.IsCompleted || isCreditsOrEnd {
		item.IsCompleted = true
	} else {
		item.IsCompleted = false
	}

	query := `
	INSERT INTO watch_progress (
		imdb_id, media_type, title, poster_path, backdrop_path,
		season_number, episode_number, episode_title, episode_still_path,
		position_seconds, duration_seconds, playback_percent, is_completed,
		torrent_hash, torrent_link, file_index, audio_index, subtitle_index,
		last_watched_at
	) VALUES (
		?, ?, ?, ?, ?,
		?, ?, ?, ?,
		?, ?, ?, ?,
		?, ?, ?, ?, ?,
		CURRENT_TIMESTAMP
	)
	ON CONFLICT(imdb_id, season_number, episode_number) DO UPDATE SET
		title = COALESCE(NULLIF(excluded.title, ''), watch_progress.title),
		poster_path = COALESCE(NULLIF(excluded.poster_path, ''), watch_progress.poster_path),
		backdrop_path = COALESCE(NULLIF(excluded.backdrop_path, ''), watch_progress.backdrop_path),
		episode_title = COALESCE(NULLIF(excluded.episode_title, ''), watch_progress.episode_title),
		episode_still_path = COALESCE(NULLIF(excluded.episode_still_path, ''), watch_progress.episode_still_path),
		position_seconds = CASE 
			WHEN excluded.position_seconds > 0 THEN excluded.position_seconds 
			ELSE watch_progress.position_seconds 
		END,
		duration_seconds = CASE WHEN excluded.duration_seconds > 0 THEN excluded.duration_seconds ELSE watch_progress.duration_seconds END,
		playback_percent = CASE 
			WHEN excluded.duration_seconds > 0 AND excluded.position_seconds > 0 THEN excluded.playback_percent
			WHEN excluded.position_seconds > 0 AND watch_progress.duration_seconds > 0 THEN MIN(100.0, (excluded.position_seconds / watch_progress.duration_seconds) * 100.0)
			WHEN watch_progress.duration_seconds > 0 AND watch_progress.position_seconds > 0 THEN MIN(100.0, (watch_progress.position_seconds / watch_progress.duration_seconds) * 100.0)
			ELSE watch_progress.playback_percent 
		END,
		is_completed = CASE 
			WHEN excluded.is_completed = 1 THEN 1
			WHEN excluded.duration_seconds > 0 AND excluded.playback_percent >= 88.0 THEN 1
			WHEN watch_progress.duration_seconds > 0 AND (CASE WHEN excluded.position_seconds > 0 THEN excluded.position_seconds ELSE watch_progress.position_seconds END / watch_progress.duration_seconds) >= 0.88 THEN 1
			ELSE excluded.is_completed 
		END,
		torrent_hash = COALESCE(NULLIF(excluded.torrent_hash, ''), watch_progress.torrent_hash),
		torrent_link = COALESCE(NULLIF(excluded.torrent_link, ''), watch_progress.torrent_link),
		file_index = CASE WHEN excluded.file_index >= 0 THEN excluded.file_index ELSE watch_progress.file_index END,
		audio_index = CASE WHEN excluded.audio_index >= 0 THEN excluded.audio_index ELSE watch_progress.audio_index END,
		subtitle_index = CASE WHEN excluded.subtitle_index >= 0 THEN excluded.subtitle_index ELSE watch_progress.subtitle_index END,
		last_watched_at = CURRENT_TIMESTAMP;
	`

	completedInt := 0
	if item.IsCompleted {
		completedInt = 1
	}

	_, err := s.db.Exec(query,
		item.ImdbID, item.MediaType, item.Title, item.PosterPath, item.BackdropPath,
		item.SeasonNumber, item.EpisodeNumber, item.EpisodeTitle, item.EpisodeStillPath,
		item.PositionSeconds, item.DurationSeconds, item.PlaybackPercent, completedInt,
		item.TorrentHash, item.TorrentLink, item.FileIndex, item.AudioIndex, item.SubtitleIndex,
	)
	if err == nil && item.ImdbID != "" && item.AudioTitle != "" {
		_ = s.SaveAudioPreference(item.ImdbID, item.AudioTitle, item.AudioIndex)
	}

	// Auto-complete earlier episodes if a later episode was completed
	if err == nil && item.MediaType == "tv" && item.IsCompleted && item.SeasonNumber > 0 && item.EpisodeNumber > 0 {
		autoCompQuery := `
		UPDATE watch_progress 
		SET is_completed = 1, playback_percent = 100.0 
		WHERE imdb_id = ? AND media_type = 'tv' 
		  AND (season_number < ? OR (season_number = ? AND episode_number < ?))
		  AND is_completed = 0;
		`
		_, _ = s.db.Exec(autoCompQuery, item.ImdbID, item.SeasonNumber, item.SeasonNumber, item.EpisodeNumber)
	}

	return err
}

// GetResumeList returns in-progress titles (0.5% <= percent < 88%), deduplicating TV shows by latest watched event
func (s *Store) GetResumeList(limit int) ([]WatchProgressItem, error) {
	if limit <= 0 {
		limit = 20
	}

	// Subquery groups by imdb_id across ALL watched items.
	// If the latest watched episode of a show was completed (is_completed = 1),
	// the outer WHERE filters it out so no ghost older episode is resurrected.
	query := `
	SELECT 
		wp.id, wp.imdb_id, wp.media_type, wp.title, wp.poster_path, wp.backdrop_path,
		wp.season_number, wp.episode_number, wp.episode_title, wp.episode_still_path,
		wp.position_seconds, wp.duration_seconds, wp.playback_percent, wp.is_completed,
		wp.torrent_hash, wp.torrent_link, wp.file_index, wp.audio_index, wp.subtitle_index,
		wp.last_watched_at
	FROM watch_progress wp
	INNER JOIN (
		SELECT imdb_id, MAX(last_watched_at) as max_watched
		FROM watch_progress
		GROUP BY imdb_id
	) latest ON wp.imdb_id = latest.imdb_id AND wp.last_watched_at = latest.max_watched
	WHERE wp.is_completed = 0 AND (wp.playback_percent >= 0.5 OR wp.position_seconds >= 10.0) AND wp.playback_percent < 88.0
	ORDER BY wp.last_watched_at DESC
	LIMIT ?;
	`

	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query resume list: %w", err)
	}
	defer rows.Close()

	var items []WatchProgressItem
	for rows.Next() {
		var item WatchProgressItem
		var completedInt int
		var epTitle, epStill, poster, backdrop, tHash, tLink sql.NullString
		var watchedAtStr string

		err := rows.Scan(
			&item.ID, &item.ImdbID, &item.MediaType, &item.Title, &poster, &backdrop,
			&item.SeasonNumber, &item.EpisodeNumber, &epTitle, &epStill,
			&item.PositionSeconds, &item.DurationSeconds, &item.PlaybackPercent, &completedInt,
			&tHash, &tLink, &item.FileIndex, &item.AudioIndex, &item.SubtitleIndex,
			&watchedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan resume item: %w", err)
		}

		item.PosterPath = poster.String
		item.BackdropPath = backdrop.String
		item.EpisodeTitle = epTitle.String
		item.EpisodeStillPath = epStill.String
		item.TorrentHash = tHash.String
		item.TorrentLink = tLink.String
		item.IsCompleted = completedInt == 1
		item.LastWatchedAt = parseSQLiteTime(watchedAtStr)

		if strings.HasPrefix(item.PosterPath, "/poster/") || item.PosterPath == "" {
			if p, b := s.ResolveTmdbPoster(item.ImdbID); p != "" {
				item.PosterPath = p
				if item.BackdropPath == "" {
					item.BackdropPath = b
				}
				go func(id, posterVal, backdropVal string) {
					_, _ = s.db.Exec(`UPDATE watch_progress SET poster_path = ?, backdrop_path = ? WHERE imdb_id = ?`, posterVal, backdropVal, id)
				}(item.ImdbID, p, b)
			}
		}

		items = append(items, item)
	}

	return items, nil
}

// GetItemProgress returns progress for a specific movie or episode
func (s *Store) GetItemProgress(imdbId string, season, episode int) (*WatchProgressItem, error) {
	query := `
	SELECT 
		id, imdb_id, media_type, title, poster_path, backdrop_path,
		season_number, episode_number, episode_title, episode_still_path,
		position_seconds, duration_seconds, playback_percent, is_completed,
		torrent_hash, torrent_link, file_index, audio_index, subtitle_index,
		last_watched_at
	FROM watch_progress
	WHERE imdb_id = ? AND season_number = ? AND episode_number = ?
	LIMIT 1;
	`

	row := s.db.QueryRow(query, imdbId, season, episode)

	var item WatchProgressItem
	var completedInt int
	var epTitle, epStill, poster, backdrop, tHash, tLink sql.NullString
	var watchedAtStr string

	err := row.Scan(
		&item.ID, &item.ImdbID, &item.MediaType, &item.Title, &poster, &backdrop,
		&item.SeasonNumber, &item.EpisodeNumber, &epTitle, &epStill,
		&item.PositionSeconds, &item.DurationSeconds, &item.PlaybackPercent, &completedInt,
		&tHash, &tLink, &item.FileIndex, &item.AudioIndex, &item.SubtitleIndex,
		&watchedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query item progress: %w", err)
	}

	item.PosterPath = poster.String
	item.BackdropPath = backdrop.String
	item.EpisodeTitle = epTitle.String
	item.EpisodeStillPath = epStill.String
	item.TorrentHash = tHash.String
	item.TorrentLink = tLink.String
	item.IsCompleted = completedInt == 1
	item.LastWatchedAt = parseSQLiteTime(watchedAtStr)

	return &item, nil
}

// GetSeriesProgress returns map of all watched episodes for a series
func (s *Store) GetSeriesProgress(imdbId string) (map[string]EpisodeProgressStatus, error) {
	query := `
	SELECT 
		season_number, episode_number, position_seconds, duration_seconds, 
		playback_percent, is_completed
	FROM watch_progress
	WHERE imdb_id = ? AND media_type = 'tv';
	`

	rows, err := s.db.Query(query, imdbId)
	if err != nil {
		return nil, fmt.Errorf("failed to query series progress: %w", err)
	}
	defer rows.Close()

	result := make(map[string]EpisodeProgressStatus)
	for rows.Next() {
		var st EpisodeProgressStatus
		var completedInt int

		if err := rows.Scan(&st.SeasonNumber, &st.EpisodeNumber, &st.PositionSeconds, &st.DurationSeconds, &st.PlaybackPercent, &completedInt); err != nil {
			return nil, fmt.Errorf("failed to scan series episode progress: %w", err)
		}
		st.IsCompleted = completedInt == 1
		key := fmt.Sprintf("%dx%d", st.SeasonNumber, st.EpisodeNumber)
		result[key] = st
	}

	return result, nil
}

// GetLatestWatchedEpisode returns the most recently watched episode for a series
func (s *Store) GetLatestWatchedEpisode(imdbId string) (*WatchProgressItem, error) {
	query := `
	SELECT 
		id, imdb_id, media_type, title, poster_path, backdrop_path,
		season_number, episode_number, episode_title, episode_still_path,
		position_seconds, duration_seconds, playback_percent, is_completed,
		torrent_hash, torrent_link, file_index, audio_index, subtitle_index,
		last_watched_at
	FROM watch_progress
	WHERE imdb_id = ? AND media_type = 'tv'
	ORDER BY last_watched_at DESC
	LIMIT 1;
	`

	row := s.db.QueryRow(query, imdbId)

	var item WatchProgressItem
	var completedInt int
	var epTitle, epStill, poster, backdrop, tHash, tLink sql.NullString
	var watchedAtStr string

	err := row.Scan(
		&item.ID, &item.ImdbID, &item.MediaType, &item.Title, &poster, &backdrop,
		&item.SeasonNumber, &item.EpisodeNumber, &epTitle, &epStill,
		&item.PositionSeconds, &item.DurationSeconds, &item.PlaybackPercent, &completedInt,
		&tHash, &tLink, &item.FileIndex, &item.AudioIndex, &item.SubtitleIndex,
		&watchedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest watched episode: %w", err)
	}

	item.PosterPath = poster.String
	item.BackdropPath = backdrop.String
	item.EpisodeTitle = epTitle.String
	item.EpisodeStillPath = epStill.String
	item.TorrentHash = tHash.String
	item.TorrentLink = tLink.String
	item.IsCompleted = completedInt == 1
	item.LastWatchedAt = parseSQLiteTime(watchedAtStr)

	return &item, nil
}

// GetLatestWatchedEpisodeForSeason returns the most recently watched episode for a specific season of a series
func (s *Store) GetLatestWatchedEpisodeForSeason(imdbId string, season int) (*WatchProgressItem, error) {
	query := `
	SELECT 
		id, imdb_id, media_type, title, poster_path, backdrop_path,
		season_number, episode_number, episode_title, episode_still_path,
		position_seconds, duration_seconds, playback_percent, is_completed,
		torrent_hash, torrent_link, file_index, audio_index, subtitle_index,
		last_watched_at
	FROM watch_progress
	WHERE imdb_id = ? AND media_type = 'tv' AND season_number = ?
	ORDER BY last_watched_at DESC
	LIMIT 1;
	`

	row := s.db.QueryRow(query, imdbId, season)

	var item WatchProgressItem
	var completedInt int
	var epTitle, epStill, poster, backdrop, tHash, tLink sql.NullString
	var watchedAtStr string

	err := row.Scan(
		&item.ID, &item.ImdbID, &item.MediaType, &item.Title, &poster, &backdrop,
		&item.SeasonNumber, &item.EpisodeNumber, &epTitle, &epStill,
		&item.PositionSeconds, &item.DurationSeconds, &item.PlaybackPercent, &completedInt,
		&tHash, &tLink, &item.FileIndex, &item.AudioIndex, &item.SubtitleIndex,
		&watchedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest watched episode for season: %w", err)
	}

	item.PosterPath = poster.String
	item.BackdropPath = backdrop.String
	item.EpisodeTitle = epTitle.String
	item.EpisodeStillPath = epStill.String
	item.TorrentHash = tHash.String
	item.TorrentLink = tLink.String
	item.IsCompleted = completedInt == 1
	item.LastWatchedAt = parseSQLiteTime(watchedAtStr)

	return &item, nil
}

// GetWatchedSeasons returns list of distinct watched season numbers for a TV show
func (s *Store) GetWatchedSeasons(imdbId string) ([]int, error) {
	query := `
	SELECT DISTINCT season_number
	FROM watch_progress
	WHERE imdb_id = ? AND media_type = 'tv' AND season_number > 0
	ORDER BY season_number ASC;
	`

	rows, err := s.db.Query(query, imdbId)
	if err != nil {
		return nil, fmt.Errorf("failed to query watched seasons: %w", err)
	}
	defer rows.Close()

	var seasons []int
	for rows.Next() {
		var sNum int
		if err := rows.Scan(&sNum); err == nil {
			seasons = append(seasons, sNum)
		}
	}
	return seasons, nil
}

// GetAllCompletedShows returns list of distinct TV show imdb_ids where at least one episode is completed
func (s *Store) GetRecentlyCompletedSeries(limit int) ([]WatchProgressItem, error) {
	if limit <= 0 {
		limit = 10
	}

	query := `
	SELECT 
		wp.id, wp.imdb_id, wp.media_type, wp.title, wp.poster_path, wp.backdrop_path,
		wp.season_number, wp.episode_number, wp.episode_title, wp.episode_still_path,
		wp.position_seconds, wp.duration_seconds, wp.playback_percent, wp.is_completed,
		wp.torrent_hash, wp.torrent_link, wp.file_index, wp.audio_index, wp.subtitle_index,
		wp.last_watched_at
	FROM watch_progress wp
	INNER JOIN (
		SELECT imdb_id, MAX(last_watched_at) as max_watched
		FROM watch_progress
		WHERE media_type = 'tv' AND is_completed = 1
		GROUP BY imdb_id
	) latest ON wp.imdb_id = latest.imdb_id AND wp.last_watched_at = latest.max_watched
	WHERE wp.media_type = 'tv' AND wp.is_completed = 1
	ORDER BY wp.last_watched_at DESC
	LIMIT ?;
	`

	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query completed series: %w", err)
	}
	defer rows.Close()

	var items []WatchProgressItem
	for rows.Next() {
		var item WatchProgressItem
		var epTitle, epStill, poster, backdrop, tHash, tLink sql.NullString
		var watchedAtStr string

		err := rows.Scan(
			&item.ID, &item.ImdbID, &item.MediaType, &item.Title, &poster, &backdrop,
			&item.SeasonNumber, &item.EpisodeNumber, &epTitle, &epStill,
			&item.PositionSeconds, &item.DurationSeconds, &item.PlaybackPercent, &item.IsCompleted,
			&tHash, &tLink, &item.FileIndex, &item.AudioIndex, &item.SubtitleIndex,
			&watchedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan item: %w", err)
		}

		item.PosterPath = poster.String
		item.BackdropPath = backdrop.String
		item.EpisodeTitle = epTitle.String
		item.EpisodeStillPath = epStill.String
		item.TorrentHash = tHash.String
		item.TorrentLink = tLink.String
		item.LastWatchedAt = parseSQLiteTime(watchedAtStr)

		items = append(items, item)
	}

	return items, nil
}

// DeleteItemProgress removes progress for movie or episode
func (s *Store) DeleteItemProgress(imdbId string, season, episode int) error {
	query := `DELETE FROM watch_progress WHERE imdb_id = ? AND season_number = ? AND episode_number = ?;`
	_, err := s.db.Exec(query, imdbId, season, episode)
	return err
}

// DeleteShowProgress removes all progress records for a given imdb_id
func (s *Store) DeleteShowProgress(imdbId string) error {
	query := `DELETE FROM watch_progress WHERE imdb_id = ?;`
	_, err := s.db.Exec(query, imdbId)
	return err
}

// SetUserPreference saves a preference key-value pair
func (s *Store) SetUserPreference(key, value string) error {
	query := `
	INSERT INTO user_preferences (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP;
	`
	_, err := s.db.Exec(query, key, value)
	return err
}

// GetUserPreference retrieves a preference key-value pair
func (s *Store) GetUserPreference(key string) (string, error) {
	query := `SELECT value FROM user_preferences WHERE key = ? LIMIT 1;`
	var val string
	err := s.db.QueryRow(query, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// SaveAudioPreference saves the user's preferred audio track for a show or movie
func (s *Store) SaveAudioPreference(imdbID, audioTitle string, audioIndex int) error {
	if imdbID == "" {
		return fmt.Errorf("imdb_id cannot be empty")
	}
	if audioTitle == "" {
		return nil
	}
	query := `
	INSERT INTO media_audio_preferences (imdb_id, audio_title, audio_index, updated_at)
	VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(imdb_id) DO UPDATE SET
		audio_title = excluded.audio_title,
		audio_index = excluded.audio_index,
		updated_at = CURRENT_TIMESTAMP;
	`
	_, err := s.db.Exec(query, imdbID, audioTitle, audioIndex)
	return err
}

// GetAudioPreference retrieves the user's preferred audio track for a show or movie
func (s *Store) GetAudioPreference(imdbID string) (*AudioPreference, error) {
	if imdbID == "" {
		return nil, nil
	}
	query := `SELECT imdb_id, audio_title, audio_index, updated_at FROM media_audio_preferences WHERE imdb_id = ?`
	var pref AudioPreference
	var updatedStr string
	err := s.db.QueryRow(query, imdbID).Scan(&pref.ImdbID, &pref.AudioTitle, &pref.AudioIndex, &updatedStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pref.UpdatedAt = parseSQLiteTime(updatedStr)
	return &pref, nil
}

// MarkEpisodeWatched marks a single episode as watched or unwatched
func (s *Store) MarkEpisodeWatched(imdbId, title, poster, backdrop string, season, episode int, epTitle, epStill string, completed bool) error {
	if imdbId == "" || season <= 0 || episode <= 0 {
		return fmt.Errorf("invalid episode parameters")
	}

	completedInt := 0
	percent := 0.0
	posSec := 0.0
	durSec := 3600.0
	if completed {
		completedInt = 1
		percent = 100.0
		posSec = 3600.0
	}

	query := `
	INSERT INTO watch_progress (
		imdb_id, media_type, title, poster_path, backdrop_path,
		season_number, episode_number, episode_title, episode_still_path,
		position_seconds, duration_seconds, playback_percent, is_completed,
		last_watched_at
	) VALUES (
		?, 'tv', ?, ?, ?,
		?, ?, ?, ?,
		?, ?, ?, ?,
		CURRENT_TIMESTAMP
	)
	ON CONFLICT(imdb_id, season_number, episode_number) DO UPDATE SET
		is_completed = excluded.is_completed,
		playback_percent = excluded.playback_percent,
		position_seconds = excluded.position_seconds,
		last_watched_at = CURRENT_TIMESTAMP;
	`
	_, err := s.db.Exec(query,
		imdbId, title, poster, backdrop,
		season, episode, epTitle, epStill,
		posSec, durSec, percent, completedInt,
	)
	return err
}

// MarkSeasonWatched marks all episodes of a season as watched or unwatched
func (s *Store) MarkSeasonWatched(imdbId, title, poster, backdrop string, season int, episodes []TmdbEpisodeItem, completed bool) error {
	if imdbId == "" || season <= 0 {
		return fmt.Errorf("invalid season parameters")
	}

	for _, ep := range episodes {
		if ep.SeasonNumber != season {
			continue
		}
		_ = s.MarkEpisodeWatched(imdbId, title, poster, backdrop, season, ep.EpisodeNumber, ep.Name, ep.StillPath, completed)
	}
	return nil
}

// MarkAllUpToEpisodeWatched marks all episodes in all seasons up to the specified episode as watched
func (s *Store) MarkAllUpToEpisodeWatched(imdbId, title, poster, backdrop string, upToSeason, upToEpisode int, episodes []TmdbEpisodeItem) error {
	if imdbId == "" || upToSeason <= 0 || upToEpisode <= 0 {
		return fmt.Errorf("invalid up_to parameters")
	}

	for _, ep := range episodes {
		if ep.SeasonNumber < upToSeason || (ep.SeasonNumber == upToSeason && ep.EpisodeNumber <= upToEpisode) {
			_ = s.MarkEpisodeWatched(imdbId, title, poster, backdrop, ep.SeasonNumber, ep.EpisodeNumber, ep.Name, ep.StillPath, true)
		}
	}
	return nil
}

// MarkSeriesWatched marks all episodes of the entire series as watched or unwatched
func (s *Store) MarkSeriesWatched(imdbId, title, poster, backdrop string, episodes []TmdbEpisodeItem, completed bool) error {
	if imdbId == "" {
		return fmt.Errorf("imdb_id cannot be empty")
	}

	for _, ep := range episodes {
		_ = s.MarkEpisodeWatched(imdbId, title, poster, backdrop, ep.SeasonNumber, ep.EpisodeNumber, ep.Name, ep.StillPath, completed)
	}
	return nil
}

// GetSeriesDetailedProgress aggregates watched episode statuses into seasons and overall series progress
func (s *Store) GetSeriesDetailedProgress(imdbId string, allEps []TmdbEpisodeItem) (*SeriesProgressResponse, error) {
	epMap, err := s.GetSeriesProgress(imdbId)
	if err != nil {
		return nil, err
	}

	resp := &SeriesProgressResponse{
		ImdbID:   imdbId,
		Seasons:  make(map[string]SeasonProgressSummary),
		Episodes: epMap,
	}

	if len(allEps) == 0 {
		for _, st := range epMap {
			sKey := fmt.Sprintf("%d", st.SeasonNumber)
			sm, exists := resp.Seasons[sKey]
			if !exists {
				sm = SeasonProgressSummary{SeasonNumber: st.SeasonNumber}
			}
			sm.TotalEpisodes++
			if st.IsCompleted {
				sm.WatchedEpisodes++
				resp.TotalWatched++
			}
			sm.IsCompleted = (sm.WatchedEpisodes >= sm.TotalEpisodes && sm.TotalEpisodes > 0)
			resp.Seasons[sKey] = sm
			resp.TotalEpisodes++
		}
		resp.IsCompleted = (resp.TotalWatched >= resp.TotalEpisodes && resp.TotalEpisodes > 0)
		return resp, nil
	}

	seasonTotals := make(map[int]int)
	seasonWatched := make(map[int]int)
	maxWatchedSeason := 0
	maxWatchedEpisode := 0

	for _, ep := range allEps {
		if ep.SeasonNumber <= 0 {
			continue
		}
		seasonTotals[ep.SeasonNumber]++
		resp.TotalEpisodes++

		key := fmt.Sprintf("%dx%d", ep.SeasonNumber, ep.EpisodeNumber)
		if st, found := epMap[key]; found && st.IsCompleted {
			seasonWatched[ep.SeasonNumber]++
			resp.TotalWatched++

			if ep.SeasonNumber > maxWatchedSeason || (ep.SeasonNumber == maxWatchedSeason && ep.EpisodeNumber > maxWatchedEpisode) {
				maxWatchedSeason = ep.SeasonNumber
				maxWatchedEpisode = ep.EpisodeNumber
			}
		}
	}

	resp.LatestWatchedSeason = maxWatchedSeason
	resp.LatestWatchedEpisode = maxWatchedEpisode
	resp.IsCompleted = (resp.TotalWatched >= resp.TotalEpisodes && resp.TotalEpisodes > 0)

	// Check if there are any unwatched prior episodes before max watched
	if maxWatchedSeason > 0 && maxWatchedEpisode > 0 {
		for _, ep := range allEps {
			if ep.SeasonNumber <= 0 {
				continue
			}
			if ep.SeasonNumber < maxWatchedSeason || (ep.SeasonNumber == maxWatchedSeason && ep.EpisodeNumber < maxWatchedEpisode) {
				key := fmt.Sprintf("%dx%d", ep.SeasonNumber, ep.EpisodeNumber)
				st, found := epMap[key]
				if !found || !st.IsCompleted {
					resp.HasUnwatchedPrior = true
					break
				}
			}
		}
	}

	for sNum, total := range seasonTotals {
		sKey := fmt.Sprintf("%d", sNum)
		watched := seasonWatched[sNum]
		resp.Seasons[sKey] = SeasonProgressSummary{
			SeasonNumber:    sNum,
			TotalEpisodes:   total,
			WatchedEpisodes: watched,
			IsCompleted:     watched >= total && total > 0,
		}
	}

	return resp, nil
}

