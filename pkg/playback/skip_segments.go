package playback

import (
	"database/sql"
	"log"
)

type SkipSegment struct {
	Type      string  `json:"type"`       // "intro" | "credits"
	StartTime float64 `json:"start_time"` // in seconds
	EndTime   float64 `json:"end_time"`   // in seconds
	Label     string  `json:"label"`      // "Пропустить заставку" | "Следующая серия"
}

// GetSkipSegments retrieves stored intro and credits timestamps from SQLite
func (s *Store) GetSkipSegments(imdbID string, season, episode int, torrentHash string, fileIndex int) ([]SkipSegment, error) {
	if s.db == nil || imdbID == "" {
		return nil, nil
	}

	var introStart, introEnd, creditsStart, creditsEnd sql.NullFloat64
	var source string

	query := `SELECT intro_start, intro_end, credits_start, credits_end, source 
	          FROM media_skip_segments 
	          WHERE imdb_id = ? AND season_number = ? AND episode_number = ? AND torrent_hash = ? AND file_index = ?`

	err := s.db.QueryRow(query, imdbID, season, episode, torrentHash, fileIndex).Scan(
		&introStart, &introEnd, &creditsStart, &creditsEnd, &source,
	)
	if err == sql.ErrNoRows {
		// Fallback: match by imdb_id, season, episode and hash regardless of file_index
		queryFallback := `SELECT intro_start, intro_end, credits_start, credits_end, source 
		                  FROM media_skip_segments 
		                  WHERE imdb_id = ? AND season_number = ? AND episode_number = ? AND torrent_hash = ? 
		                  LIMIT 1`
		err = s.db.QueryRow(queryFallback, imdbID, season, episode, torrentHash).Scan(
			&introStart, &introEnd, &creditsStart, &creditsEnd, &source,
		)
	}

	if err != nil {
		return nil, err
	}

	var segments []SkipSegment
	if introStart.Valid && introEnd.Valid && introEnd.Float64 > introStart.Float64 {
		segments = append(segments, SkipSegment{
			Type:      "intro",
			StartTime: introStart.Float64,
			EndTime:   introEnd.Float64,
			Label:     "Пропустить заставку",
		})
	}

	if creditsStart.Valid && creditsEnd.Valid && creditsEnd.Float64 > creditsStart.Float64 {
		label := "Пропустить титры"
		if season > 0 {
			label = "Следующая серия"
		}
		segments = append(segments, SkipSegment{
			Type:      "credits",
			StartTime: creditsStart.Float64,
			EndTime:   creditsEnd.Float64,
			Label:     label,
		})
	}

	return segments, nil
}

// SaveSkipSegments persists detected intro and credits timestamps in SQLite
func (s *Store) SaveSkipSegments(imdbID string, season, episode int, torrentHash string, fileIndex int, segments []SkipSegment, source string) error {
	if s.db == nil || imdbID == "" || len(segments) == 0 {
		return nil
	}

	var introStart, introEnd, creditsStart, creditsEnd *float64
	for _, seg := range segments {
		if seg.Type == "intro" {
			sStart := seg.StartTime
			sEnd := seg.EndTime
			introStart = &sStart
			introEnd = &sEnd
		} else if seg.Type == "credits" {
			cStart := seg.StartTime
			cEnd := seg.EndTime
			creditsStart = &cStart
			creditsEnd = &cEnd
		}
	}

	if source == "" {
		source = "chapter"
	}

	query := `INSERT INTO media_skip_segments 
	          (imdb_id, season_number, episode_number, torrent_hash, file_index, intro_start, intro_end, credits_start, credits_end, source, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	          ON CONFLICT(imdb_id, season_number, episode_number, torrent_hash, file_index)
	          DO UPDATE SET 
	              intro_start = excluded.intro_start,
	              intro_end = excluded.intro_end,
	              credits_start = excluded.credits_start,
	              credits_end = excluded.credits_end,
	              source = excluded.source,
	              updated_at = CURRENT_TIMESTAMP`

	_, err := s.db.Exec(query, imdbID, season, episode, torrentHash, fileIndex, introStart, introEnd, creditsStart, creditsEnd, source)
	if err != nil {
		log.Printf("[playback] Failed to save skip segments for %s s%de%d: %v", imdbID, season, episode, err)
		return err
	}

	log.Printf("[playback] Saved skip segments for %s s%de%d (intro=%v-%v, credits=%v-%v)",
		imdbID, season, episode, introStart, introEnd, creditsStart, creditsEnd)
	return nil
}
