package auth

import "testing"

func TestEncryptAndDecryptSensitiveValue(t *testing.T) {
	encrypted, err := EncryptSensitiveValue("0123456789abcdef0123456789abcdef", "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "" || encrypted == "JBSWY3DPEHPK3PXP" {
		t.Fatalf("expected encrypted payload, got %q", encrypted)
	}
	decrypted, legacy, err := DecryptSensitiveValue("0123456789abcdef0123456789abcdef", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		t.Fatal("expected encrypted payload not to be treated as legacy")
	}
	if decrypted != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("unexpected decrypted value: %q", decrypted)
	}
}

func TestDecryptSensitiveValueSupportsLegacyPlaintext(t *testing.T) {
	decrypted, legacy, err := DecryptSensitiveValue("0123456789abcdef0123456789abcdef", "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if !legacy {
		t.Fatal("expected plaintext secret to be marked legacy")
	}
	if decrypted != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("unexpected decrypted value: %q", decrypted)
	}
}
