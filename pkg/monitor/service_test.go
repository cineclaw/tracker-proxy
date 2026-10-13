package monitor

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestMonitorDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite db: %v", err)
	}

	migrations := []string{
		`CREATE TABLE IF NOT EXISTS series_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			imdb_id TEXT NOT NULL,
			profile TEXT NOT NULL DEFAULT 'default',
			preferred_studio TEXT NOT NULL DEFAULT '',
			last_notified_season INTEGER NOT NULL DEFAULT 0,
			last_notified_episode INTEGER NOT NULL DEFAULT 0,
			preferred_studio_notified INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(imdb_id, profile)
		);`,
		`CREATE TABLE IF NOT EXISTS series_detected_releases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			imdb_id TEXT NOT NULL,
			season INTEGER NOT NULL,
			episode INTEGER NOT NULL,
			studio TEXT NOT NULL,
			quality TEXT NOT NULL DEFAULT '1080p',
			info_hash TEXT NOT NULL,
			magnet TEXT NOT NULL,
			stage INTEGER NOT NULL DEFAULT 1,
			notified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(imdb_id, season, episode, stage)
		);`,
		`CREATE TABLE IF NOT EXISTS watch_progress (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			imdb_id TEXT NOT NULL,
			media_type TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			season_number INTEGER NOT NULL DEFAULT 0,
			episode_number INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS media_audio_preferences (
			imdb_id TEXT PRIMARY KEY,
			audio_title TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("failed to run migration: %v", err)
		}
	}

	return db
}

func TestSeriesSubscriptions(t *testing.T) {
	db := setupTestMonitorDB(t)
	defer db.Close()

	svc := NewService(db, nil, nil, nil, nil, "")

	// 1. Initial get without explicit subscription -> should auto-fill default
	sub, err := svc.GetSubscription("tt1234567", "default")
	if err != nil {
		t.Fatalf("GetSubscription failed: %v", err)
	}
	if sub.ImdbID != "tt1234567" || !sub.IsActive {
		t.Fatalf("unexpected default subscription: %+v", sub)
	}

	// 2. Set explicit preferred studio
	err = svc.SetSubscription(SeriesSubscription{
		ImdbID:          "tt1234567",
		Profile:         "default",
		PreferredStudio: "Red Head Sound",
		IsActive:        true,
	})
	if err != nil {
		t.Fatalf("SetSubscription failed: %v", err)
	}

	sub, err = svc.GetSubscription("tt1234567", "default")
	if err != nil {
		t.Fatalf("GetSubscription after set failed: %v", err)
	}
	if sub.PreferredStudio != "Red Head Sound" || !sub.IsActive {
		t.Fatalf("expected Red Head Sound, got: %+v", sub)
	}

	// 3. Test detected releases tracking
	d := DetectedRelease{
		ImdbID:   "tt1234567",
		Title:    "Mob Land",
		Season:   2,
		Episode:  2,
		Studio:   "LostFilm",
		Quality:  "1080p",
		InfoHash: "hash123",
		Magnet:   "magnet:?xt=urn:btih:hash123",
		Stage:    1,
	}

	if svc.isReleaseNotified("tt1234567", 2, 2, 1) {
		t.Fatal("expected release NOT yet notified")
	}

	svc.recordRelease(d, 1)

	if !svc.isReleaseNotified("tt1234567", 2, 2, 1) {
		t.Fatal("expected release to be notified after recording")
	}

	// Stage 2 shouldn't be notified yet
	if svc.isReleaseNotified("tt1234567", 2, 2, 2) {
		t.Fatal("expected stage 2 release NOT yet notified")
	}
}
