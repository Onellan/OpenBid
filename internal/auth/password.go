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
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonKeyLength   = 32
)

func fillRandomBytes(buf []byte) {
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
}

func RandomString(n int) string {
	b := make([]byte, n)
	fillRandomBytes(b)
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
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s", argon2.Version, argonMemory, argonIterations, argonParallelism, base64.RawStdEncoding.EncodeToString(key))
	return hex.EncodeToString(salt), encoded, nil
}
func VerifyPassword(password, saltHex, hashHex string) bool {
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}
	if strings.HasPrefix(hashHex, "$argon2id$") {
		var version int
		var memory, iterations uint32
		var parallelism uint8
		parts := strings.Split(hashHex, "$")
		if len(parts) != 5 || parts[1] != "argon2id" {
			return false
		}
		if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
			return false
		}
		if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil || memory < 8*1024 || memory > 256*1024 || iterations < 1 || iterations > 10 || parallelism < 1 || parallelism > 8 {
			return false
		}
		hash, err := base64.RawStdEncoding.DecodeString(parts[4])
		if err != nil || len(hash) < 16 || len(hash) > 64 {
			return false
		}
		key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(hash)))
		return subtle.ConstantTimeCompare(key, hash) == 1
	}
	hash, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	key := PBKDF2SHA256([]byte(password), salt, 100000, 32)
	return subtle.ConstantTimeCompare(key, hash) == 1
}

func PasswordNeedsRehash(hash string) bool {
	prefix := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$", argon2.Version, argonMemory, argonIterations, argonParallelism)
	return !strings.HasPrefix(hash, prefix)
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
