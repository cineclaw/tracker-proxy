package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/SherClockHolmes/webpush-go"
)

type NotificationPayload struct {
	Title string                 `json:"title"`
	Body  string                 `json:"body"`
	Icon  string                 `json:"icon,omitempty"`
	Badge string                 `json:"badge,omitempty"`
	Image string                 `json:"image,omitempty"`
	Tag   string                 `json:"tag,omitempty"`
	Data  map[string]interface{} `json:"data,omitempty"`
}

type PushSubscription struct {
	ID        int64     `json:"id,omitempty"`
	Endpoint  string    `json:"endpoint"`
	P256dh    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	UserAgent string    `json:"user_agent,omitempty"`
	Profile   string    `json:"profile,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type Service struct {
	db         *sql.DB
	publicKey  string
	privateKey string
	subscriber string
	mu         sync.RWMutex
}

func NewService(db *sql.DB) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("db cannot be nil")
	}

	svc := &Service{
		db:         db,
		subscriber: "mailto:admin@cineclaw.local",
	}

	if sub := os.Getenv("VAPID_SUBSCRIBER"); sub != "" {
		svc.subscriber = sub
	}

	if err := svc.initVAPIDKeys(); err != nil {
		return nil, fmt.Errorf("failed to init VAPID keys: %w", err)
	}

	return svc, nil
}

func (s *Service) initVAPIDKeys() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pub := os.Getenv("VAPID_PUBLIC_KEY")
	priv := os.Getenv("VAPID_PRIVATE_KEY")

	if pub != "" && priv != "" {
		s.publicKey = pub
		s.privateKey = priv
		return nil
	}

	// Try reading from app_settings
	var dbPub, dbPriv string
	_ = s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'vapid_public_key'`).Scan(&dbPub)
	_ = s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'vapid_private_key'`).Scan(&dbPriv)

	if dbPub != "" && dbPriv != "" {
		s.publicKey = dbPub
		s.privateKey = dbPriv
		return nil
	}

	// Generate new VAPID keypair
	privKey, pubKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("failed to generate VAPID keys: %w", err)
	}

	_, _ = s.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('vapid_public_key', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, pubKey)
	_, _ = s.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('vapid_private_key', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, privKey)

	s.publicKey = pubKey
	s.privateKey = privKey
	log.Printf("[notifications] Generated and persisted new VAPID keys (public: %s...)", pubKey[:16])

	return nil
}

func (s *Service) GetVAPIDPublicKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.publicKey
}

func (s *Service) Subscribe(sub PushSubscription) error {
	if sub.Endpoint == "" || sub.P256dh == "" || sub.Auth == "" {
		return fmt.Errorf("invalid subscription fields")
	}

	prof := strings.TrimSpace(sub.Profile)
	if prof == "" {
		prof = "default"
	}

	query := `
	INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_agent, profile, created_at)
	VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(endpoint) DO UPDATE SET
		p256dh = excluded.p256dh,
		auth = excluded.auth,
		user_agent = excluded.user_agent,
		profile = excluded.profile,
		created_at = CURRENT_TIMESTAMP;
	`
	_, err := s.db.Exec(query, sub.Endpoint, sub.P256dh, sub.Auth, sub.UserAgent, prof)
	if err != nil {
		return fmt.Errorf("failed to save push subscription: %w", err)
	}

	log.Printf("[notifications] Push subscription registered (profile=%s, endpoint=%s...)", prof, sub.Endpoint[:min(30, len(sub.Endpoint))])
	return nil
}

func (s *Service) Unsubscribe(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

func (s *Service) SendNotification(ctx context.Context, payload NotificationPayload, targetProfile ...string) (int, error) {
	s.mu.RLock()
	pub := s.publicKey
	priv := s.privateKey
	subEmail := s.subscriber
	s.mu.RUnlock()

	if pub == "" || priv == "" {
		return 0, fmt.Errorf("VAPID keys not configured")
	}

	if payload.Icon == "" {
		payload.Icon = "/icons/icon-192x192.png"
	}
	if payload.Badge == "" {
		payload.Badge = "/icons/icon-192x192.png"
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal push payload: %w", err)
	}

	var query string
	var args []interface{}
	if len(targetProfile) > 0 && targetProfile[0] != "" && targetProfile[0] != "all" {
		query = `SELECT endpoint, p256dh, auth FROM push_subscriptions WHERE profile = ?`
		args = append(args, targetProfile[0])
	} else {
		query = `SELECT endpoint, p256dh, auth FROM push_subscriptions`
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to query push subscriptions: %w", err)
	}
	defer rows.Close()

	sentCount := 0
	var deadEndpoints []string

	for rows.Next() {
		var ep, p256dh, auth string
		if err := rows.Scan(&ep, &p256dh, &auth); err != nil {
			continue
		}

		sObj := &webpush.Subscription{
			Endpoint: ep,
			Keys: webpush.Keys{
				P256dh: p256dh,
				Auth:   auth,
			},
		}

		resp, err := webpush.SendNotification(payloadBytes, sObj, &webpush.Options{
			Subscriber:      subEmail,
			VAPIDPublicKey:  pub,
			VAPIDPrivateKey: priv,
			TTL:             86400,
		})

		if err != nil {
			log.Printf("[notifications] Failed to send push to %s: %v", ep[:min(30, len(ep))], err)
			continue
		}

		if resp != nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 404 || resp.StatusCode == 410 {
				deadEndpoints = append(deadEndpoints, ep)
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				sentCount++
			}
		}
	}

	// Clean up expired/uninstalled subscriptions
	for _, dead := range deadEndpoints {
		_ = s.Unsubscribe(dead)
	}

	log.Printf("[notifications] Push sent to %d devices (title=%q)", sentCount, payload.Title)
	return sentCount, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
