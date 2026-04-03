package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

const encryptedSecretPrefix = "enc:v1:"

func encryptionKey(secret string) []byte {
	sum := sha256.Sum256([]byte("openbid-sensitive:" + secret))
	return sum[:]
}

func EncryptSensitiveValue(secret, plaintext string) (string, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(encryptionKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	fillRandomBytes(nonce)
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return encryptedSecretPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecryptSensitiveValue(secret, stored string) (plaintext string, legacyPlaintext bool, err error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return "", false, nil
	}
	if !strings.HasPrefix(stored, encryptedSecretPrefix) {
		return stored, true, nil
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(stored, encryptedSecretPrefix))
	if err != nil {
		return "", false, fmt.Errorf("decode encrypted secret: %w", err)
	}
	block, err := aes.NewCipher(encryptionKey(secret))
	if err != nil {
		return "", false, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", false, err
	}
	if len(rawPayload) < gcm.NonceSize() {
		return "", false, fmt.Errorf("encrypted secret payload is truncated")
	}
	nonce := rawPayload[:gcm.NonceSize()]
	ciphertext := rawPayload[gcm.NonceSize():]
	plaintextBytes, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", false, fmt.Errorf("decrypt encrypted secret: %w", err)
	}
	return string(plaintextBytes), false, nil
}
