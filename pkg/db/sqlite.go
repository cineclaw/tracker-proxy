package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
	}

	// SQLite connection string with WAL mode and busy timeout
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db at %s: %w", dbPath, err)
	}

	// SQLite handles concurrent reads well in WAL mode, but writes must be serialized
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)

	db := &DB{sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

func (db *DB) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS watch_progress (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			imdb_id TEXT NOT NULL,
			media_type TEXT NOT NULL,
			title TEXT NOT NULL,
			poster_path TEXT,
			backdrop_path TEXT,
			season_number INTEGER NOT NULL DEFAULT 0,
			episode_number INTEGER NOT NULL DEFAULT 0,
			episode_title TEXT,
			episode_still_path TEXT,
			position_seconds REAL NOT NULL DEFAULT 0,
			duration_seconds REAL NOT NULL DEFAULT 0,
			playback_percent REAL NOT NULL DEFAULT 0,
			is_completed INTEGER NOT NULL DEFAULT 0,
			torrent_hash TEXT,
			torrent_link TEXT,
			file_index INTEGER DEFAULT -1,
			audio_index INTEGER DEFAULT -1,
			subtitle_index INTEGER DEFAULT -1,
			last_watched_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(imdb_id, season_number, episode_number)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_watch_progress_resume 
		ON watch_progress (is_completed, playback_percent, last_watched_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_watch_progress_show 
		ON watch_progress (imdb_id, season_number, episode_number);`,
		`CREATE TABLE IF NOT EXISTS media_favorites (
			imdb_id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			poster_path TEXT,
			media_type TEXT NOT NULL,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS user_preferences (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migration query error (%s): %w", q, err)
		}
	}

	return nil
}
