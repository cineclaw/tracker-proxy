package cache

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.etcd.io/bbolt"
	"tracker-proxy/pkg/models"
)

var (
	bucketName     = []byte("imdb_cache")
	hashBucketName = []byte("topic_hashes")
	imdbRegex      = regexp.MustCompile(`(?i)^(?:tt)?(\d+)$`)
)

type Store struct {
	db       *bbolt.DB
	ttl      time.Duration
	stopChan chan struct{}
	closeOnce sync.Once
}

type Entry struct {
	IMDbID       string                 `json:"imdb_id"`
	Query        string                 `json:"query"`
	CreatedAt    time.Time              `json:"created_at"`
	ExpiresAt    time.Time              `json:"expires_at"`
	ResultsCount int                    `json:"results_count"`
	Results      []models.TorrentResult `json:"results"`
}

// NormalizeIMDbID normalizes any IMDb ID variation (e.g., "0120338", "tt0120338", "TT0120338") to canonical "tt0120338".
func NormalizeIMDbID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	matches := imdbRegex.FindStringSubmatch(id)
	if len(matches) > 1 {
		return "tt" + matches[1]
	}
	return strings.ToLower(id)
}

// New initializes a bbolt-backed disk cache.
func New(path string, ttl time.Duration) (*Store, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create cache directory: %w", err)
		}
	}

	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open bbolt cache database: %w", err)
	}

	// Ensure buckets exist
	err = db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketName); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(hashBucketName)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create cache buckets: %w", err)
	}

	store := &Store{
		db:       db,
		ttl:      ttl,
		stopChan: make(chan struct{}),
	}

	// Start background cleanup worker every 1 hour
	go store.cleanupWorker(1 * time.Hour)

	return store, nil
}

// DB returns the underlying bbolt database instance.
func (s *Store) DB() *bbolt.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// Get retrieves cached torrent results for the given IMDb ID.
// Returns (results, found, error). If expired or not found, found is false.
func (s *Store) Get(rawID string) ([]models.TorrentResult, bool, error) {
	id := NormalizeIMDbID(rawID)
	if id == "" || s == nil || s.db == nil {
		return nil, false, nil
	}

	var entry Entry
	var found bool

	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return nil
		}
		data := b.Get([]byte(id))
		if data == nil {
			return nil
		}

		if err := json.Unmarshal(data, &entry); err != nil {
			return err
		}

		// Check expiration
		if time.Now().After(entry.ExpiresAt) {
			return nil // expired
		}

		found = true
		return nil
	})

	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}

	return entry.Results, true, nil
}

// Set stores torrent search results under the given IMDb ID.
func (s *Store) Set(rawID, query string, results []models.TorrentResult) error {
	id := NormalizeIMDbID(rawID)
	if id == "" || s == nil || s.db == nil {
		return nil
	}

	now := time.Now()
	entry := Entry{
		IMDbID:       id,
		Query:        query,
		CreatedAt:    now,
		ExpiresAt:    now.Add(s.ttl),
		ResultsCount: len(results),
		Results:      results,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return fmt.Errorf("cache bucket not found")
		}
		return b.Put([]byte(id), data)
	})
}

// Delete removes an entry by IMDb ID.
func (s *Store) Delete(rawID string) error {
	id := NormalizeIMDbID(rawID)
	if id == "" || s == nil || s.db == nil {
		return nil
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return nil
		}
		return b.Delete([]byte(id))
	})
}

// CleanExpired removes all expired cache entries from the database.
func (s *Store) CleanExpired() (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}

	now := time.Now()
	var expiredKeys [][]byte

	// Phase 1: scan expired keys under View lock
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var entry Entry
			if err := json.Unmarshal(v, &entry); err == nil {
				if now.After(entry.ExpiresAt) {
					keyCopy := make([]byte, len(k))
					copy(keyCopy, k)
					expiredKeys = append(expiredKeys, keyCopy)
				}
			}
			return nil
		})
	})
	if err != nil {
		return 0, err
	}

	if len(expiredKeys) == 0 {
		return 0, nil
	}

	// Phase 2: delete expired keys under Update lock
	err = s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return nil
		}
		for _, k := range expiredKeys {
			_ = b.Delete(k)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	return len(expiredKeys), nil
}

// GetTopicHash retrieves the cached InfoHash for a given tracker and topic ID.
// Returns (hash, found, error). Hash is normalized to lowercase.
func (s *Store) GetTopicHash(tracker, topicID string) (string, bool, error) {
	if s == nil || s.db == nil || tracker == "" || topicID == "" {
		return "", false, nil
	}

	key := []byte(fmt.Sprintf("%s:%s", strings.ToLower(tracker), topicID))
	var hash string
	var found bool

	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(hashBucketName)
		if b == nil {
			return nil
		}
		data := b.Get(key)
		if data != nil {
			hash = string(data)
			found = true
		}
		return nil
	})

	return hash, found, err
}

// SetTopicHash caches the InfoHash for a given tracker and topic ID.
func (s *Store) SetTopicHash(tracker, topicID, hash string) error {
	if s == nil || s.db == nil || tracker == "" || topicID == "" || hash == "" {
		return nil
	}

	key := []byte(fmt.Sprintf("%s:%s", strings.ToLower(tracker), topicID))
	val := []byte(strings.ToLower(strings.TrimSpace(hash)))

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(hashBucketName)
		if b == nil {
			return fmt.Errorf("hash bucket not found")
		}
		return b.Put(key, val)
	})
}

func (s *Store) cleanupWorker(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			deleted, err := s.CleanExpired()
			if err != nil {
				log.Printf("[cache] Cleanup error: %v", err)
			} else if deleted > 0 {
				log.Printf("[cache] Evicted %d expired entries", deleted)
			}
		}
	}
}

// Close gracefully closes the cache store.
func (s *Store) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.stopChan)
		if s.db != nil {
			err = s.db.Close()
		}
	})
	return err
}
