package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		`CREATE TABLE IF NOT EXISTS media_watchlist (
			imdb_id TEXT PRIMARY KEY,
			media_type TEXT NOT NULL,
			title TEXT NOT NULL,
			original_title TEXT,
			year INTEGER,
			rating REAL,
			poster_path TEXT,
			backdrop_path TEXT,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_watchlist_added ON media_watchlist(added_at DESC);`,
		`CREATE TABLE IF NOT EXISTS media_audio_preferences (
			imdb_id TEXT PRIMARY KEY,
			audio_title TEXT NOT NULL,
			audio_index INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS media_skip_segments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			imdb_id TEXT NOT NULL,
			season_number INTEGER NOT NULL DEFAULT 0,
			episode_number INTEGER NOT NULL DEFAULT 0,
			torrent_hash TEXT NOT NULL,
			file_index INTEGER NOT NULL DEFAULT -1,
			intro_start REAL,
			intro_end REAL,
			credits_start REAL,
			credits_end REAL,
			source TEXT NOT NULL DEFAULT 'chapter',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(imdb_id, season_number, episode_number, torrent_hash, file_index)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_skip_segments_lookup 
		ON media_skip_segments (imdb_id, season_number, episode_number, torrent_hash, file_index);`,
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			endpoint TEXT NOT NULL UNIQUE,
			p256dh TEXT NOT NULL,
			auth TEXT NOT NULL,
			user_agent TEXT,
			profile TEXT DEFAULT 'default',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS series_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			imdb_id TEXT NOT NULL,
			profile TEXT DEFAULT 'default',
			preferred_studio TEXT,
			last_notified_season INTEGER DEFAULT 0,
			last_notified_episode INTEGER DEFAULT 0,
			preferred_studio_notified BOOLEAN DEFAULT 0,
			is_active BOOLEAN DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(imdb_id, profile)
		);`,
		`CREATE TABLE IF NOT EXISTS series_detected_releases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			imdb_id TEXT NOT NULL,
			season INTEGER NOT NULL,
			episode INTEGER NOT NULL,
			studio TEXT NOT NULL,
			quality TEXT NOT NULL,
			info_hash TEXT NOT NULL,
			magnet TEXT NOT NULL,
			stage INTEGER NOT NULL,
			notified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(imdb_id, season, episode, studio)
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
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

	if err := db.migrateProfiles(); err != nil {
		return fmt.Errorf("profile migration error: %w", err)
	}

	return nil
}

type UserProfile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon"`
	IsKids    bool      `json:"is_kids"`
	CreatedAt time.Time `json:"created_at"`
}

func (db *DB) migrateProfiles() error {
	// 1. Ensure user_profiles table exists
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_profiles (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		icon TEXT NOT NULL DEFAULT 'adult',
		is_kids INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	INSERT OR IGNORE INTO user_profiles (id, name, icon, is_kids) VALUES ('default', 'Основной', 'adult', 0);
	INSERT OR IGNORE INTO user_profiles (id, name, icon, is_kids) VALUES ('kids', 'Детский (5-8 лет)', 'kids', 1);
	`)
	if err != nil {
		return fmt.Errorf("failed to create user_profiles table: %w", err)
	}

	// 2. Check if watch_progress table has profile column and compound unique
	var sqlWP string
	_ = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='watch_progress'").Scan(&sqlWP)
	if sqlWP != "" && !strings.Contains(sqlWP, "profile") {
		migrationWP := `
		CREATE TABLE IF NOT EXISTS watch_progress_v2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile TEXT NOT NULL DEFAULT 'default',
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
			UNIQUE(profile, imdb_id, season_number, episode_number)
		);
		INSERT OR IGNORE INTO watch_progress_v2 (
			id, profile, imdb_id, media_type, title, poster_path, backdrop_path,
			season_number, episode_number, episode_title, episode_still_path,
			position_seconds, duration_seconds, playback_percent, is_completed,
			torrent_hash, torrent_link, file_index, audio_index, subtitle_index,
			last_watched_at
		)
		SELECT 
			id, 'default', imdb_id, media_type, title, poster_path, backdrop_path,
			season_number, episode_number, episode_title, episode_still_path,
			position_seconds, duration_seconds, playback_percent, is_completed,
			torrent_hash, torrent_link, file_index, audio_index, subtitle_index,
			last_watched_at
		FROM watch_progress;
		DROP TABLE watch_progress;
		ALTER TABLE watch_progress_v2 RENAME TO watch_progress;
		CREATE INDEX IF NOT EXISTS idx_watch_progress_resume 
		ON watch_progress (profile, is_completed, playback_percent, last_watched_at DESC);
		CREATE INDEX IF NOT EXISTS idx_watch_progress_show 
		ON watch_progress (profile, imdb_id, season_number, episode_number);
		`
		if _, err := db.Exec(migrationWP); err != nil {
			return fmt.Errorf("failed to migrate watch_progress to profiles: %w", err)
		}
	}

	// 3. Check if media_watchlist table has profile column and primary key
	var sqlWL string
	_ = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='media_watchlist'").Scan(&sqlWL)
	if sqlWL != "" && !strings.Contains(sqlWL, "profile") {
		migrationWL := `
		CREATE TABLE IF NOT EXISTS media_watchlist_v2 (
			profile TEXT NOT NULL DEFAULT 'default',
			imdb_id TEXT NOT NULL,
			media_type TEXT NOT NULL,
			title TEXT NOT NULL,
			original_title TEXT,
			year INTEGER,
			rating REAL,
			poster_path TEXT,
			backdrop_path TEXT,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(profile, imdb_id)
		);
		INSERT OR IGNORE INTO media_watchlist_v2 (
			profile, imdb_id, media_type, title, original_title, year, rating, poster_path, backdrop_path, added_at
		)
		SELECT 
			'default', imdb_id, media_type, title, original_title, year, rating, poster_path, backdrop_path, added_at
		FROM media_watchlist;
		DROP TABLE media_watchlist;
		ALTER TABLE media_watchlist_v2 RENAME TO media_watchlist;
		CREATE INDEX IF NOT EXISTS idx_watchlist_added ON media_watchlist(profile, added_at DESC);
		`
		if _, err := db.Exec(migrationWL); err != nil {
			return fmt.Errorf("failed to migrate media_watchlist to profiles: %w", err)
		}
	}

	return nil
}

func (db *DB) GetProfiles() ([]UserProfile, error) {
	rows, err := db.Query(`SELECT id, name, icon, is_kids, created_at FROM user_profiles ORDER BY CASE WHEN id = 'default' THEN 0 WHEN id = 'kids' THEN 1 ELSE 2 END, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []UserProfile
	for rows.Next() {
		var p UserProfile
		var isKidsInt int
		var createdStr string
		if err := rows.Scan(&p.ID, &p.Name, &p.Icon, &isKidsInt, &createdStr); err != nil {
			return nil, err
		}
		p.IsKids = isKidsInt == 1
		p.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdStr)
		profiles = append(profiles, p)
	}
	return profiles, nil
}

func (db *DB) CreateProfile(name, icon string, isKids bool) (*UserProfile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("profile name cannot be empty")
	}
	if icon == "" {
		if isKids {
			icon = "kids"
		} else {
			icon = "adult"
		}
	}
	id := fmt.Sprintf("profile_%d", time.Now().UnixNano())
	isKidsInt := 0
	if isKids {
		isKidsInt = 1
	}

	_, err := db.Exec(`INSERT INTO user_profiles (id, name, icon, is_kids) VALUES (?, ?, ?, ?)`, id, name, icon, isKidsInt)
	if err != nil {
		return nil, err
	}

	return &UserProfile{
		ID:        id,
		Name:      name,
		Icon:      icon,
		IsKids:    isKids,
		CreatedAt: time.Now(),
	}, nil
}

func (db *DB) DeleteProfile(id string) error {
	id = strings.TrimSpace(id)
	if id == "default" || id == "kids" {
		return fmt.Errorf("cannot delete default system profile")
	}
	_, err := db.Exec(`DELETE FROM user_profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	// Also clean up watch progress and watchlist for this profile
	_, _ = db.Exec(`DELETE FROM watch_progress WHERE profile = ?`, id)
	_, _ = db.Exec(`DELETE FROM media_watchlist WHERE profile = ?`, id)
	return nil
}
