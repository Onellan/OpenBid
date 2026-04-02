package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

func GenerateTOTPFromSecret(secret string, now time.Time) string {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	secret += strings.Repeat("=", (8-len(secret)%8)%8)
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return ""
	}
	counter := uint64(now.Unix() / 30)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	h := hmac.New(sha1.New, key)
	h.Write(msg[:])
	sum := h.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (int(sum[offset])&0x7f)<<24 | int(sum[offset+1])<<16 | int(sum[offset+2])<<8 | int(sum[offset+3])
	return fmt.Sprintf("%06d", code%1000000)
}
func ValidateTOTP(secret, code string, now time.Time) bool {
	for i := -1; i <= 1; i++ {
		if GenerateTOTPFromSecret(secret, now.Add(time.Duration(i)*30*time.Second)) == code {
			return true
		}
	}
	return false
}
