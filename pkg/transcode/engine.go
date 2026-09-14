package transcode

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TranscodeEngine manages active FFmpeg transcoding sessions and background reaping.
type TranscodeEngine struct {
	sessions    map[string]*TranscodeSession
	mu          sync.RWMutex
	torrURL     string
	baseDir     string
	idleTimeout time.Duration
	stopChan    chan struct{}
}

// NewEngine creates a new TranscodeEngine and starts the idle session reaper.
func NewEngine(torrURL, baseDir string) *TranscodeEngine {
	if torrURL == "" {
		torrURL = "http://127.0.0.1:8092"
	}
	torrURL = strings.TrimRight(torrURL, "/")

	if baseDir == "" {
		baseDir = filepath.Join(os.TempDir(), "cineclaw_transcode")
	}
	_ = os.MkdirAll(baseDir, 0755)

	e := &TranscodeEngine{
		sessions:    make(map[string]*TranscodeSession),
		torrURL:     torrURL,
		baseDir:     baseDir,
		idleTimeout: 35 * time.Second,
		stopChan:    make(chan struct{}),
	}

	go e.runReaper()
	return e
}

// BuildSessionID generates a deterministic or unique session identifier.
func BuildSessionID(hash string, fileIdx int, profile Profile, audioIdx int, customID string) string {
	if customID != "" {
		return fmt.Sprintf("%s_%s_%d", customID, profile.ID, audioIdx)
	}
	return fmt.Sprintf("%s_%d_%s_%d", hash, fileIdx, profile.ID, audioIdx)
}

// GetOrCreateSession retrieves an existing running session or starts a new FFmpeg process.
func (e *TranscodeEngine) GetOrCreateSession(
	hash string,
	fileIdx int,
	profile Profile,
	audioIdx int,
	startSec float64,
	durationSec float64,
	customID string,
) (*TranscodeSession, error) {
	sessionID := BuildSessionID(hash, fileIdx, profile, audioIdx, customID)

	e.mu.Lock()
	if existing, found := e.sessions[sessionID]; found {
		e.mu.Unlock()
		existing.Touch(int(startSec / 3.0))
		return existing, nil
	}

	sourceURL := fmt.Sprintf("%s/stream/video.mkv?link=%s&index=%d&play", e.torrURL, hash, fileIdx)

	sess, err := NewSession(sessionID, hash, fileIdx, profile, audioIdx, startSec, durationSec, sourceURL, e.baseDir)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}

	e.sessions[sessionID] = sess
	e.mu.Unlock()

	// Launch FFmpeg and wait for initial segment (45s timeout for cold BitTorrent seeks)
	if err := sess.Start(45 * time.Second); err != nil {
		e.mu.Lock()
		delete(e.sessions, sessionID)
		e.mu.Unlock()
		return nil, fmt.Errorf("failed to start transcode session: %w", err)
	}

	return sess, nil
}

// GetSession looks up a session by ID.
func (e *TranscodeEngine) GetSession(sessionID string) *TranscodeSession {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sessions[sessionID]
}

// StopSession cleanly terminates a specific session by ID.
func (e *TranscodeEngine) StopSession(sessionID string) {
	e.mu.Lock()
	sess, found := e.sessions[sessionID]
	if found {
		delete(e.sessions, sessionID)
	}
	e.mu.Unlock()

	if sess != nil {
		sess.Stop()
	}
}

// StopSessionsForHash stops all active transcode sessions for a specific torrent hash.
func (e *TranscodeEngine) StopSessionsForHash(hash string) {
	e.mu.Lock()
	var toStop []*TranscodeSession
	for id, sess := range e.sessions {
		if sess.Hash == hash {
			toStop = append(toStop, sess)
			delete(e.sessions, id)
		}
	}
	e.mu.Unlock()

	for _, sess := range toStop {
		sess.Stop()
	}
}

// runReaper periodically inspects active sessions and reaps idle ones.
func (e *TranscodeEngine) runReaper() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopChan:
			return
		case <-ticker.C:
			e.mu.Lock()
			var idleSessions []*TranscodeSession
			for id, sess := range e.sessions {
				if sess.IsIdle(e.idleTimeout) {
					idleSessions = append(idleSessions, sess)
					delete(e.sessions, id)
				}
			}
			e.mu.Unlock()

			for _, sess := range idleSessions {
				log.Printf("[Transcode] Reaping idle session %s (idle > %v)", sess.ID, e.idleTimeout)
				sess.Stop()
			}
		}
	}
}

// Close terminates all active sessions and stops the reaper.
func (e *TranscodeEngine) Close() {
	close(e.stopChan)

	e.mu.Lock()
	all := make([]*TranscodeSession, 0, len(e.sessions))
	for _, sess := range e.sessions {
		all = append(all, sess)
	}
	e.sessions = make(map[string]*TranscodeSession)
	e.mu.Unlock()

	for _, sess := range all {
		sess.Stop()
	}
}
