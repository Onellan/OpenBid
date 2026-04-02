package auth

import "testing"

func TestPasswordHashAndVerify(t *testing.T) {
	salt, hash, err := HashPassword("TenderHub!2026")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("TenderHub!2026", salt, hash) {
		t.Fatal("verify failed")
	}
	if VerifyPassword("bad", salt, hash) {
		t.Fatal("expected mismatch")
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
