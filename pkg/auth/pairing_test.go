package auth

import (
	"testing"
)

func TestPairingManager_Lifecycle(t *testing.T) {
	authMgr := NewManager(true, "admin", "wavemp3", "testsecret")
	pm := NewPairingManager(authMgr)

	code := "482910"
	deviceName := "Living Room TV"

	// 1. Initial status before init -> not found
	paired, token, user, found := pm.GetStatus(code)
	if found || paired {
		t.Fatalf("expected code to not exist yet")
	}

	// 2. Init pairing
	req, err := pm.InitPairing(code, deviceName)
	if err != nil {
		t.Fatalf("failed to init pairing: %v", err)
	}
	if req.Code != code || req.DeviceName != deviceName || req.Paired {
		t.Fatalf("unexpected init request state: %+v", req)
	}

	// 3. Status after init -> found=true, paired=false
	paired, token, user, found = pm.GetStatus(code)
	if !found || paired || token != "" {
		t.Fatalf("expected code to be pending: found=%v, paired=%v, token=%s", found, paired, token)
	}

	// 4. Approve pairing
	approvedToken, err := pm.Approve(code, "admin")
	if err != nil {
		t.Fatalf("failed to approve pairing: %v", err)
	}
	if approvedToken == "" {
		t.Fatalf("expected non-empty token upon approval")
	}

	// 5. Status after approval -> paired=true, token matches, user="admin"
	paired, token, user, found = pm.GetStatus(code)
	if !found || !paired || token != approvedToken || user != "admin" {
		t.Fatalf("unexpected status after approval: paired=%v, token=%s, user=%s", paired, token, user)
	}

	// 6. Token validates in AuthManager
	if !authMgr.ValidateToken(token) {
		t.Fatalf("pairing token failed validation in auth manager")
	}

	// 7. Invalid code format rejection
	_, err = pm.InitPairing("1", "Short")
	if err == nil {
		t.Fatalf("expected error for short code")
	}
}
