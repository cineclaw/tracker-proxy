package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidToken       = errors.New("invalid or expired token")
)

const (
	CookieName    = "cineclaw_session"
	DefaultTTL    = 30 * 24 * time.Hour // 30 days
	ShortTTL      = 24 * time.Hour      // 1 day (if not remember me)
)

type Config struct {
	Enabled  bool
	Username string
	Password string
	Secret   []byte
}

type Manager struct {
	cfg Config
}

func NewManager(enabled bool, username, password, secretStr string) *Manager {
	if username == "" {
		username = "admin"
	}
	if password == "" {
		password = "wavemp3"
	}

	var secret []byte
	if secretStr != "" {
		secret = []byte(secretStr)
	} else {
		// Deterministic secret derived from password + static salt so tokens remain valid across restarts
		h := sha256.Sum256([]byte(password + ":cineclaw-auth-salt-v1"))
		secret = h[:]
	}

	return &Manager{
		cfg: Config{
			Enabled:  enabled,
			Username: username,
			Password: password,
			Secret:   secret,
		},
	}
}

func (m *Manager) IsEnabled() bool {
	return m.cfg.Enabled
}

func (m *Manager) GetUsername() string {
	return m.cfg.Username
}

func (m *Manager) Login(username, password string, rememberMe bool) (string, time.Time, error) {
	if !m.cfg.Enabled {
		return "anonymous", time.Now().Add(DefaultTTL), nil
	}

	uMatch := subtle.ConstantTimeCompare([]byte(username), []byte(m.cfg.Username)) == 1
	pMatch := subtle.ConstantTimeCompare([]byte(password), []byte(m.cfg.Password)) == 1

	if !uMatch || !pMatch {
		return "", time.Time{}, ErrInvalidCredentials
	}

	ttl := ShortTTL
	if rememberMe {
		ttl = DefaultTTL
	}
	expiresAt := time.Now().Add(ttl)

	token, err := m.createToken(username, expiresAt)
	if err != nil {
		return "", time.Time{}, err
	}

	return token, expiresAt, nil
}

func (m *Manager) ValidateToken(tokenStr string) bool {
	if !m.cfg.Enabled {
		return true
	}
	if tokenStr == "" {
		return false
	}

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 4 {
		return false
	}

	usernameBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	username := string(usernameBytes)
	if subtle.ConstantTimeCompare([]byte(username), []byte(m.cfg.Username)) != 1 {
		return false
	}

	expUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expUnix {
		return false // Expired
	}

	nonce := parts[2]
	expectedSig := m.sign(fmt.Sprintf("%s:%d:%s", username, expUnix, nonce))
	providedSig, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare(expectedSig, providedSig) == 1
}

func (m *Manager) ValidateBasicAuth(authHeader string) bool {
	if !m.cfg.Enabled {
		return true
	}
	if !strings.HasPrefix(authHeader, "Basic ") {
		return false
	}

	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader, "Basic "))
	if err != nil {
		return false
	}

	pair := strings.SplitN(string(payload), ":", 2)
	if len(pair) != 2 {
		return false
	}

	uMatch := subtle.ConstantTimeCompare([]byte(pair[0]), []byte(m.cfg.Username)) == 1
	pMatch := subtle.ConstantTimeCompare([]byte(pair[1]), []byte(m.cfg.Password)) == 1
	return uMatch && pMatch
}

func (m *Manager) ValidateRequest(r *http.Request) bool {
	if !m.cfg.Enabled {
		return true
	}

	// 1. Check Cookie
	if cookie, err := r.Cookie(CookieName); err == nil && cookie.Value != "" {
		if m.ValidateToken(cookie.Value) {
			return true
		}
	}

	// 2. Check Authorization Header (Bearer or Basic)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			if m.ValidateToken(token) {
				return true
			}
		} else if strings.HasPrefix(authHeader, "Basic ") {
			if m.ValidateBasicAuth(authHeader) {
				return true
			}
		}
	}

	// 3. Optional: check query parameter token for direct streaming links (e.g. ?token=...)
	if queryToken := r.URL.Query().Get("token"); queryToken != "" {
		if m.ValidateToken(queryToken) {
			return true
		}
	}

	return false
}

func (m *Manager) BuildCookie(token string, expiresAt time.Time) *http.Cookie {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false, // Allows HTTP on local network; when proxying via HTTPS, browsers still accept
	}
}

func (m *Manager) BuildLogoutCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func (m *Manager) createToken(username string, expiresAt time.Time) (string, error) {
	nonceBytes := make([]byte, 8)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(nonceBytes)

	uB64 := base64.RawURLEncoding.EncodeToString([]byte(username))
	expUnix := expiresAt.Unix()
	sig := m.sign(fmt.Sprintf("%s:%d:%s", username, expUnix, nonce))

	return fmt.Sprintf("%s.%d.%s.%s", uB64, expUnix, nonce, hex.EncodeToString(sig)), nil
}

func (m *Manager) sign(payload string) []byte {
	mac := hmac.New(sha256.New, m.cfg.Secret)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}
