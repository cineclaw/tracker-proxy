package notifications

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite db: %v", err)
	}

	migrations := []string{
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			endpoint TEXT NOT NULL UNIQUE,
			p256dh TEXT NOT NULL,
			auth TEXT NOT NULL,
			user_agent TEXT,
			profile TEXT NOT NULL DEFAULT 'default',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("failed to run migration: %v", err)
		}
	}

	return db
}

func TestWebPushService(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	svc, err := NewService(db)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	pubKey := svc.GetVAPIDPublicKey()
	if pubKey == "" {
		t.Fatal("expected non-empty VAPID public key")
	}

	// Verify persistence in app_settings
	var savedPub string
	err = db.QueryRow(`SELECT value FROM app_settings WHERE key = 'vapid_public_key'`).Scan(&savedPub)
	if err != nil || savedPub != pubKey {
		t.Fatalf("expected VAPID public key saved in DB, got: %s, err: %v", savedPub, err)
	}

	// Test subscription
	sub := PushSubscription{
		Endpoint:  "https://fcm.googleapis.com/fcm/send/test-token",
		P256dh:    "test-p256dh-key",
		Auth:      "test-auth-secret",
		UserAgent: "Mozilla/5.0 TestBrowser",
		Profile:   "admin",
	}

	if err := svc.Subscribe(sub); err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	// Test deduplication / upsert
	sub.UserAgent = "Mozilla/5.0 UpdatedBrowser"
	if err := svc.Subscribe(sub); err != nil {
		t.Fatalf("failed to update subscription: %v", err)
	}

	var count int
	_ = db.QueryRow(`SELECT COUNT(1) FROM push_subscriptions WHERE endpoint = ?`, sub.Endpoint).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 subscription record, got %d", count)
	}

	// Test unsubscribe
	if err := svc.Unsubscribe(sub.Endpoint); err != nil {
		t.Fatalf("failed to unsubscribe: %v", err)
	}

	_ = db.QueryRow(`SELECT COUNT(1) FROM push_subscriptions WHERE endpoint = ?`, sub.Endpoint).Scan(&count)
	if count != 0 {
		t.Fatalf("expected 0 subscription records after unsubscribe, got %d", count)
	}
}
