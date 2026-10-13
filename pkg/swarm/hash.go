package swarm

import (
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
)

// ParseInfoHash converts a 40-character hex string or 32-character base32 string into a raw 20-byte slice.
func ParseInfoHash(raw string) ([20]byte, error) {
	var result [20]byte
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return result, fmt.Errorf("empty info_hash")
	}

	switch len(clean) {
	case 40:
		bytes, err := hex.DecodeString(clean)
		if err != nil {
			return result, fmt.Errorf("invalid hex info_hash: %w", err)
		}
		copy(result[:], bytes)
		return result, nil
	case 32:
		bytes, err := base32.StdEncoding.DecodeString(strings.ToUpper(clean))
		if err != nil {
			return result, fmt.Errorf("invalid base32 info_hash: %w", err)
		}
		copy(result[:], bytes)
		return result, nil
	default:
		return result, fmt.Errorf("invalid info_hash length %d (expected 40 hex or 32 base32)", len(clean))
	}
}

// InfoHashToHex converts a 20-byte hash into a 40-character lowercase hex string.
func InfoHashToHex(h [20]byte) string {
	return hex.EncodeToString(h[:])
}
