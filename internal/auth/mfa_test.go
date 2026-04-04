package auth

import (
	"regexp"
	"testing"
)

func TestNewRecoveryCodesDefaultCountAndFormat(t *testing.T) {
	codes := NewRecoveryCodes(0)
	if len(codes) != 10 {
		t.Fatalf("expected default 10 recovery codes, got %d", len(codes))
	}
	pattern := regexp.MustCompile(`^[A-F0-9]{4}-[A-F0-9]{4}$`)
	for _, code := range codes {
		if !pattern.MatchString(code) {
			t.Fatalf("unexpected recovery code format: %q", code)
		}
	}
}

func TestConsumeRecoveryCodeRejectsMismatchWithoutMutatingSlice(t *testing.T) {
	original := []string{"ABCD-EF12", "1234-5678"}
	remaining, ok := ConsumeRecoveryCode(original, "FFFF-FFFF")
	if ok {
		t.Fatal("expected mismatch candidate not to be consumed")
	}
	if len(remaining) != len(original) || remaining[0] != original[0] || remaining[1] != original[1] {
		t.Fatalf("expected original codes to remain unchanged, got %#v", remaining)
	}
}

func TestNewTOTPSecretFormat(t *testing.T) {
	secret := NewTOTPSecret()
	if secret == "" {
		t.Fatal("expected non-empty TOTP secret")
	}
	pattern := regexp.MustCompile(`^[A-Z2-7]+$`)
	if !pattern.MatchString(secret) {
		t.Fatalf("unexpected TOTP secret format: %q", secret)
	}
}
