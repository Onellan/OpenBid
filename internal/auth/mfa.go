package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

const recoveryHashPrefix = "rc:v1:"

func NewRecoveryCodes(count int) []string {
	if count <= 0 {
		count = 10
	}
	codes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		raw := make([]byte, 4)
		fillRandomBytes(raw)
		encoded := strings.ToUpper(hex.EncodeToString(raw))
		codes = append(codes, encoded[:4]+"-"+encoded[4:])
	}
	return codes
}

func NormalizeRecoveryCode(raw string) string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, "-", "")
	raw = strings.ReplaceAll(raw, " ", "")
	return raw
}

func ConsumeRecoveryCode(codes []string, candidate string) ([]string, bool) {
	return ConsumeRecoveryCodeWithKey(codes, candidate, "")
}

func HashRecoveryCode(key, code string) string {
	normalized := NormalizeRecoveryCode(code)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(normalized))
	return recoveryHashPrefix + hex.EncodeToString(mac.Sum(nil))
}

func IsHashedRecoveryCode(code string) bool {
	return strings.HasPrefix(strings.TrimSpace(code), recoveryHashPrefix)
}

func ConsumeRecoveryCodeWithKey(codes []string, candidate, key string) ([]string, bool) {
	normalizedCandidate := NormalizeRecoveryCode(candidate)
	if normalizedCandidate == "" {
		return codes, false
	}
	candidateHash := HashRecoveryCode(key, normalizedCandidate)
	for index, code := range codes {
		normalizedCode := NormalizeRecoveryCode(code)
		candidateValue := normalizedCandidate
		if IsHashedRecoveryCode(code) {
			normalizedCode = strings.TrimSpace(code)
			candidateValue = candidateHash
		}
		if len(normalizedCode) != len(candidateValue) {
			continue
		}
		if subtleConstantStringCompare(normalizedCode, candidateValue) {
			remaining := append([]string{}, codes[:index]...)
			remaining = append(remaining, codes[index+1:]...)
			return remaining, true
		}
	}
	return codes, false
}

func subtleConstantStringCompare(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var diff byte
	for i := 0; i < len(left); i++ {
		diff |= left[i] ^ right[i]
	}
	return diff == 0
}

func NewTOTPSecret() string {
	b := make([]byte, 20)
	fillRandomBytes(b)
	return strings.TrimRight(base32.StdEncoding.EncodeToString(b), "=")
}
