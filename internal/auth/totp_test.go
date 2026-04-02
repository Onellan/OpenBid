package auth

import (
	"testing"
	"time"
)

func TestGenerateAndValidateTOTP(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	code := GenerateTOTPFromSecret(secret, time.Now())
	if len(code) != 6 {
		t.Fatal("expected 6 digits")
	}
	if !ValidateTOTP(secret, code, time.Now()) {
		t.Fatal("expected valid code")
	}
}
