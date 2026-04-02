package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

func RandomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}
func pbkdf2f(password, salt []byte, iter, block int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(block))
	mac.Write(b[:])
	u := mac.Sum(nil)
	t := make([]byte, len(u))
	copy(t, u)
	for i := 1; i < iter; i++ {
		mac = hmac.New(sha256.New, password)
		mac.Write(u)
		u = mac.Sum(nil)
		for x := range t {
			t[x] ^= u[x]
		}
	}
	return t
}
func PBKDF2SHA256(password, salt []byte, iter, keyLen int) []byte {
	out := []byte{}
	for block := 1; len(out) < keyLen; block++ {
		out = append(out, pbkdf2f(password, salt, iter, block)...)
	}
	return out[:keyLen]
}
func HashPassword(password string) (string, string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}
	key := PBKDF2SHA256([]byte(password), salt, 100000, 32)
	return hex.EncodeToString(salt), hex.EncodeToString(key), nil
}
func VerifyPassword(password, saltHex, hashHex string) bool {
	salt, err1 := hex.DecodeString(saltHex)
	hash, err2 := hex.DecodeString(hashHex)
	if err1 != nil || err2 != nil {
		return false
	}
	key := PBKDF2SHA256([]byte(password), salt, 100000, 32)
	return subtle.ConstantTimeCompare(key, hash) == 1
}
func StrongEnoughPassword(pw string) error {
	if len(pw) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	var upper, lower, digit, symbol bool
	for _, r := range pw {
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	if !(upper && lower && digit && symbol) {
		return fmt.Errorf("password must contain upper, lower, digit and symbol")
	}
	return nil
}
