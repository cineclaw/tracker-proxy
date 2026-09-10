package playback

import (
	"database/sql"
	"fmt"
	"time"

	"tracker-proxy/pkg/db"
)

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
	SubtitleIndex    int       `json:"subtitle_index"`
	LastWatchedAt    time.Time `json:"last_watched_at"`
}

type EpisodeProgressStatus struct {
	SeasonNumber    int     `json:"season_number"`
	EpisodeNumber   int     `json:"episode_number"`
	PositionSeconds float64 `json:"position_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
	PlaybackPercent float64 `json:"playback_percent"`
	IsCompleted     bool    `json:"is_completed"`
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

type Store struct {
	db *db.DB
}

func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

// SaveProgress upserts playback progress, automatically calculating percent and completion status
func (s *Store) SaveProgress(item *WatchProgressItem) error {
	if item.DurationSeconds > 0 {
		item.PlaybackPercent = (item.PositionSeconds / item.DurationSeconds) * 100.0
		if item.PlaybackPercent > 100.0 {
			item.PlaybackPercent = 100.0
		}
	} else {
		item.PlaybackPercent = 0.0
	}

	if item.PlaybackPercent >= 90.0 {
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
		title = excluded.title,
		poster_path = COALESCE(NULLIF(excluded.poster_path, ''), watch_progress.poster_path),
		backdrop_path = COALESCE(NULLIF(excluded.backdrop_path, ''), watch_progress.backdrop_path),
		episode_title = COALESCE(NULLIF(excluded.episode_title, ''), watch_progress.episode_title),
		episode_still_path = COALESCE(NULLIF(excluded.episode_still_path, ''), watch_progress.episode_still_path),
		position_seconds = excluded.position_seconds,
		duration_seconds = CASE WHEN excluded.duration_seconds > 0 THEN excluded.duration_seconds ELSE watch_progress.duration_seconds END,
		playback_percent = CASE 
			WHEN excluded.duration_seconds > 0 THEN excluded.playback_percent
			WHEN watch_progress.duration_seconds > 0 THEN MIN(100.0, (excluded.position_seconds / watch_progress.duration_seconds) * 100.0)
			ELSE watch_progress.playback_percent 
		END,
		is_completed = CASE 
			WHEN excluded.is_completed = 1 THEN 1
			WHEN excluded.duration_seconds > 0 AND excluded.playback_percent >= 90.0 THEN 1
			WHEN watch_progress.duration_seconds > 0 AND (excluded.position_seconds / watch_progress.duration_seconds) >= 0.90 THEN 1
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
	return err
}

// GetResumeList returns in-progress titles (2% <= percent < 90%), deduplicating TV shows
func (s *Store) GetResumeList(limit int) ([]WatchProgressItem, error) {
	if limit <= 0 {
		limit = 20
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
		WHERE is_completed = 0 AND playback_percent >= 2.0 AND playback_percent < 90.0
		GROUP BY imdb_id
	) latest ON wp.imdb_id = latest.imdb_id AND wp.last_watched_at = latest.max_watched
	WHERE wp.is_completed = 0 AND wp.playback_percent >= 2.0 AND wp.playback_percent < 90.0
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
