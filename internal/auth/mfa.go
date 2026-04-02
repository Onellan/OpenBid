package auth

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

func NewTOTPSecret() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return strings.TrimRight(base32.StdEncoding.EncodeToString(b), "=")
}
