package encoding

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

// ToCP1251 converts a UTF-8 string to Windows-1251 encoded bytes.
func ToCP1251(s string) ([]byte, error) {
	encoder := charmap.Windows1251.NewEncoder()
	return encoder.Bytes([]byte(s))
}

// FromCP1251 converts Windows-1251 encoded bytes to a UTF-8 string.
func FromCP1251(b []byte) (string, error) {
	decoder := charmap.Windows1251.NewDecoder()
	decoded, err := decoder.Bytes(b)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// URLEncodeCP1251 encodes a UTF-8 string into a percent-encoded Windows-1251 string.
// For example, "титаник" -> "%F2%E8%F2%E0%ED%E8%EA".
func URLEncodeCP1251(s string) (string, error) {
	bytes, err := ToCP1251(s)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range bytes {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == '~' {
			sb.WriteByte(b)
		} else if b == ' ' {
			sb.WriteString("%20")
		} else {
			sb.WriteString(fmt.Sprintf("%%%02X", b))
		}
	}
	return sb.String(), nil
}

// NewCP1251Reader wraps a reader providing Windows-1251 bytes and decodes to UTF-8 on the fly.
func NewCP1251Reader(r io.Reader) io.Reader {
	return charmap.Windows1251.NewDecoder().Reader(r)
}
