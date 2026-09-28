package auth

import (
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrPairingNotFound = errors.New("pairing code not found or expired")
	ErrPairingExpired  = errors.New("pairing code expired")
	ErrInvalidCode     = errors.New("invalid pairing code")
)

const (
	PairingTTL       = 10 * time.Minute
	PairingDeviceTTL = 180 * 24 * time.Hour // 180 days for paired living room devices
)

type PairingRequest struct {
	Code       string    `json:"code"`
	DeviceName string    `json:"device_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Paired     bool      `json:"paired"`
	Token      string    `json:"token,omitempty"`
	Username   string    `json:"username,omitempty"`
}

type PairingManager struct {
	mu       sync.RWMutex
	requests map[string]*PairingRequest
	auth     *Manager
}

func NewPairingManager(auth *Manager) *PairingManager {
	pm := &PairingManager{
		requests: make(map[string]*PairingRequest),
		auth:     auth,
	}

	// Periodic cleanup routine
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			pm.cleanupExpired()
		}
	}()

	return pm
}

func (pm *PairingManager) cleanupExpired() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	for code, req := range pm.requests {
		// Retain paired requests for up to 3 minutes so TV can read the token upon confirmation, then remove
		if req.Paired {
			if now.Sub(req.ExpiresAt) > 3*time.Minute {
				delete(pm.requests, code)
			}
		} else if now.After(req.ExpiresAt) {
			delete(pm.requests, code)
		}
	}
}

// InitPairing registers or updates a pairing session for a 6-digit code from a TV client.
func (pm *PairingManager) InitPairing(code, deviceName string) (*PairingRequest, error) {
	cleanCode := strings.TrimSpace(code)
	if len(cleanCode) < 4 || len(cleanCode) > 10 {
		return nil, ErrInvalidCode
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	req, exists := pm.requests[cleanCode]
	if !exists || time.Now().After(req.ExpiresAt) {
		req = &PairingRequest{
			Code:       cleanCode,
			DeviceName: deviceName,
			CreatedAt:  time.Now(),
			ExpiresAt:  time.Now().Add(PairingTTL),
			Paired:     false,
		}
		pm.requests[cleanCode] = req
	} else if deviceName != "" {
		req.DeviceName = deviceName
	}

	return req, nil
}

// GetStatus checks whether a TV pairing code has been approved.
func (pm *PairingManager) GetStatus(code string) (paired bool, token string, username string, found bool) {
	cleanCode := strings.TrimSpace(code)
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	req, exists := pm.requests[cleanCode]
	if !exists {
		return false, "", "", false
	}

	if time.Now().After(req.ExpiresAt) && !req.Paired {
		return false, "", "", false
	}

	return req.Paired, req.Token, req.Username, true
}

// Approve approves a pairing code, generating a long-lived auth token for the TV device.
func (pm *PairingManager) Approve(code, username string) (string, error) {
	cleanCode := strings.TrimSpace(code)
	if cleanCode == "" {
		return "", ErrInvalidCode
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	req, exists := pm.requests[cleanCode]
	if !exists {
		// Even if the TV hasn't hit /init yet, allow approval by creating the approved entry
		req = &PairingRequest{
			Code:      cleanCode,
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(5 * time.Minute),
		}
		pm.requests[cleanCode] = req
	} else if time.Now().After(req.ExpiresAt) && !req.Paired {
		return "", ErrPairingExpired
	}

	// Generate long-lived device token (180 days)
	expiresAt := time.Now().Add(PairingDeviceTTL)
	token, err := pm.auth.createToken(username, expiresAt)
	if err != nil {
		return "", err
	}

	req.Paired = true
	req.Token = token
	req.Username = username
	req.ExpiresAt = time.Now().Add(3 * time.Minute) // Keep in cache for 3 minutes for TV polling

	return token, nil
}
