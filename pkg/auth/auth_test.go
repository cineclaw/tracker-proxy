package auth

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthManager_LoginAndValidate(t *testing.T) {
	mgr := NewManager(true, "admin", "wavemp3", "testsecret")

	// 1. Invalid password
	_, _, err := mgr.Login("admin", "wrong", true)
	if err == nil {
		t.Fatalf("expected error for invalid password")
	}

	// 2. Valid login
	token, exp, err := mgr.Login("admin", "wavemp3", true)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if !mgr.ValidateToken(token) {
		t.Fatalf("token validation failed for valid token")
	}
	if exp.Before(time.Now()) {
		t.Fatalf("expected future expiry")
	}

	// 3. Tampered token
	tampered := token + "xyz"
	if mgr.ValidateToken(tampered) {
		t.Fatalf("tampered token should not validate")
	}

	// 4. Basic Auth
	basicHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:wavemp3"))
	if !mgr.ValidateBasicAuth(basicHeader) {
		t.Fatalf("basic auth validation failed for valid credentials")
	}

	badBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:wrong"))
	if mgr.ValidateBasicAuth(badBasic) {
		t.Fatalf("bad basic auth should fail")
	}

	// 5. Request validation via Cookie
	req := httptest.NewRequest("GET", "/api/search", nil)
	req.AddCookie(mgr.BuildCookie(token, exp))
	if !mgr.ValidateRequest(req) {
		t.Fatalf("request validation via cookie failed")
	}

	// 6. Request validation via Bearer
	reqBearer := httptest.NewRequest("GET", "/api/search", nil)
	reqBearer.Header.Set("Authorization", "Bearer "+token)
	if !mgr.ValidateRequest(reqBearer) {
		t.Fatalf("request validation via bearer failed")
	}

	// 7. Request validation via Basic
	reqBasic := httptest.NewRequest("GET", "/api/search", nil)
	reqBasic.Header.Set("Authorization", basicHeader)
	if !mgr.ValidateRequest(reqBasic) {
		t.Fatalf("request validation via basic header failed")
	}

	// 8. Request without auth
	reqEmpty := httptest.NewRequest("GET", "/api/search", nil)
	if mgr.ValidateRequest(reqEmpty) {
		t.Fatalf("empty request should fail validation")
	}
}

func TestAuthManager_Disabled(t *testing.T) {
	mgr := NewManager(false, "admin", "wavemp3", "")
	if !mgr.ValidateRequest(httptest.NewRequest("GET", "/", nil)) {
		t.Fatalf("disabled auth should allow all requests")
	}
}
