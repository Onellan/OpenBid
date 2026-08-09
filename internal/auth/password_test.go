package auth

import (
	"encoding/hex"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	salt, hash, err := HashPassword("OpenBid!2026")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("OpenBid!2026", salt, hash) {
		t.Fatal("verify failed")
	}
	if VerifyPassword("bad", salt, hash) {
		t.Fatal("expected mismatch")
	}
	if PasswordNeedsRehash(hash) {
		t.Fatal("expected current Argon2id hash not to need rehashing")
	}
}

func TestVerifyPasswordAcceptsLegacyPBKDF2ForMigration(t *testing.T) {
	salt := []byte("0123456789abcdef")
	legacy := PBKDF2SHA256([]byte("OpenBid!2026"), salt, 100000, 32)
	encoded := hex.EncodeToString(legacy)
	if !VerifyPassword("OpenBid!2026", hex.EncodeToString(salt), encoded) {
		t.Fatal("expected legacy PBKDF2 password to remain valid")
	}
	if !PasswordNeedsRehash(encoded) {
		t.Fatal("expected legacy PBKDF2 password to require rehashing")
	}
}
func TestStrongEnoughPassword(t *testing.T) {
	if err := StrongEnoughPassword("weak"); err == nil {
		t.Fatal("expected failure")
	}
	if err := StrongEnoughPassword("Strong!2026Pass"); err != nil {
		t.Fatal(err)
	}
}
