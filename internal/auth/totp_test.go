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

func TestConsumeRecoveryCode(t *testing.T) {
	codes := []string{"ABCD-EF12", "1234-5678"}
	remaining, ok := ConsumeRecoveryCode(codes, "abcd ef12")
	if !ok {
		t.Fatal("expected recovery code to be consumed")
	}
	if len(remaining) != 1 || remaining[0] != "1234-5678" {
		t.Fatalf("unexpected remaining codes: %#v", remaining)
	}
}
