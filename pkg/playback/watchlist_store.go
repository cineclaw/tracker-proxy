package playback

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type WatchlistItem struct {
	ImdbID        string    `json:"imdb_id"`
	MediaType     string    `json:"media_type"`
	Title         string    `json:"title"`
	OriginalTitle string    `json:"original_title,omitempty"`
	Year          int       `json:"year,omitempty"`
	Rating        float64   `json:"rating,omitempty"`
	PosterPath    string    `json:"poster_path,omitempty"`
	BackdropPath  string    `json:"backdrop_path,omitempty"`
	AddedAt       time.Time `json:"added_at"`
}

func (s *Store) GetWatchlist(ctx context.Context) ([]WatchlistItem, error) {
	query := `SELECT imdb_id, media_type, title, original_title, year, rating, poster_path, backdrop_path, added_at 
	          FROM media_watchlist 
	          ORDER BY added_at DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query watchlist: %w", err)
	}
	defer rows.Close()

	var items []WatchlistItem
	for rows.Next() {
		var item WatchlistItem
		var origTitle, poster, backdrop sql.NullString
		var yr sql.NullInt64
		var rating sql.NullFloat64
		var addedAtStr string

		if err := rows.Scan(
			&item.ImdbID,
			&item.MediaType,
			&item.Title,
			&origTitle,
			&yr,
			&rating,
			&poster,
			&backdrop,
			&addedAtStr,
		); err != nil {
			return nil, fmt.Errorf("failed to scan watchlist item: %w", err)
		}

		if origTitle.Valid {
			item.OriginalTitle = origTitle.String
		}
		if yr.Valid {
			item.Year = int(yr.Int64)
		}
		if rating.Valid {
			item.Rating = rating.Float64
		}
		if poster.Valid {
			item.PosterPath = poster.String
		}
		if backdrop.Valid {
			item.BackdropPath = backdrop.String
		}
		item.AddedAt = parseSQLiteTime(addedAtStr)

		if strings.HasPrefix(item.PosterPath, "/poster/") || item.PosterPath == "" {
			if p, b := s.ResolveTmdbPoster(item.ImdbID); p != "" {
				item.PosterPath = p
				if item.BackdropPath == "" {
					item.BackdropPath = b
				}
				go func(id, posterVal, backdropVal string) {
					_, _ = s.db.Exec(`UPDATE media_watchlist SET poster_path = ?, backdrop_path = ? WHERE imdb_id = ?`, posterVal, backdropVal, id)
				}(item.ImdbID, p, b)
			}
		}

		items = append(items, item)
	}

	return items, nil
}

func (s *Store) AddToWatchlist(ctx context.Context, item WatchlistItem) error {
	if item.ImdbID == "" {
		return fmt.Errorf("imdb_id cannot be empty")
	}
	if item.MediaType == "" {
		item.MediaType = "movie"
	}

	if strings.HasPrefix(item.PosterPath, "/poster/") || item.PosterPath == "" {
		if p, b := s.ResolveTmdbPoster(item.ImdbID); p != "" {
			item.PosterPath = p
			if item.BackdropPath == "" {
				item.BackdropPath = b
			}
		}
	}

	query := `INSERT INTO media_watchlist (imdb_id, media_type, title, original_title, year, rating, poster_path, backdrop_path, added_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	          ON CONFLICT(imdb_id) DO UPDATE SET
	            media_type = excluded.media_type,
	            title = excluded.title,
	            original_title = excluded.original_title,
	            year = excluded.year,
	            rating = excluded.rating,
	            poster_path = excluded.poster_path,
	            backdrop_path = excluded.backdrop_path,
	            added_at = CURRENT_TIMESTAMP`

	_, err := s.db.ExecContext(ctx, query,
		item.ImdbID,
		item.MediaType,
		item.Title,
		item.OriginalTitle,
		item.Year,
		item.Rating,
		item.PosterPath,
		item.BackdropPath,
	)
	if err != nil {
		return fmt.Errorf("failed to add item to watchlist: %w", err)
	}
	return nil
}

func (s *Store) RemoveFromWatchlist(ctx context.Context, imdbID string) error {
	if imdbID == "" {
		return fmt.Errorf("imdb_id cannot be empty")
	}
	query := `DELETE FROM media_watchlist WHERE imdb_id = ?`
	_, err := s.db.ExecContext(ctx, query, imdbID)
	if err != nil {
		return fmt.Errorf("failed to remove from watchlist: %w", err)
	}
	return nil
}

func (s *Store) IsInWatchlist(ctx context.Context, imdbID string) (bool, error) {
	if imdbID == "" {
		return false, nil
	}
	query := `SELECT 1 FROM media_watchlist WHERE imdb_id = ? LIMIT 1`
	var exists int
	err := s.db.QueryRowContext(ctx, query, imdbID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check watchlist: %w", err)
	}
	return true, nil
}
