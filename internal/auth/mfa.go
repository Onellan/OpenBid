package auth

import (
	"encoding/base32"
	"strings"
)

func NewTOTPSecret() string {
	b := make([]byte, 10)
	fillRandomBytes(b)
	return strings.TrimRight(base32.StdEncoding.EncodeToString(b), "=")
}
