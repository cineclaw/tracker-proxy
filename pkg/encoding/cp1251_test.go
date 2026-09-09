package encoding

import (
	"strings"
	"testing"
)

func TestURLEncodeCP1251(t *testing.T) {
	encoded, err := URLEncodeCP1251("титаник")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "титаник" in CP1251:
	// т = 0xF2, и = 0xE8, т = 0xF2, а = 0xE0, н = 0xED, и = 0xE8, к = 0xEA
	expected := "%F2%E8%F2%E0%ED%E8%EA"
	if strings.ToUpper(encoded) != expected {
		t.Errorf("got %s, want %s", encoded, expected)
	}
}

func TestFromCP1251(t *testing.T) {
	raw := []byte{0xF2, 0xE8, 0xF2, 0xE0, 0xED, 0xE8, 0xEA}
	decoded, err := FromCP1251(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded != "титаник" {
		t.Errorf("got %s, want титаник", decoded)
	}
}
