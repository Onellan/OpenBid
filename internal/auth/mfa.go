package auth

import (
	"encoding/base32"
	"encoding/hex"
	"strings"
)

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
	normalizedCandidate := NormalizeRecoveryCode(candidate)
	if normalizedCandidate == "" {
		return codes, false
	}
	for index, code := range codes {
		normalizedCode := NormalizeRecoveryCode(code)
		if len(normalizedCode) != len(normalizedCandidate) {
			continue
		}
		if subtleConstantStringCompare(normalizedCode, normalizedCandidate) {
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
	b := make([]byte, 10)
	fillRandomBytes(b)
	return strings.TrimRight(base32.StdEncoding.EncodeToString(b), "=")
}
