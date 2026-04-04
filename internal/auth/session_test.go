package auth

import (
	"openbid/internal/models"
	"testing"
	"time"
)

func TestSessionEncodeDecodeRoundTrip(t *testing.T) {
	raw, err := EncodeSession("0123456789abcdef0123456789abcdef", models.Session{
		UserID:   "user-1",
		TenantID: "tenant-1",
		CSRF:     "csrf-token",
		Expires:  time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, ok := DecodeSession("0123456789abcdef0123456789abcdef", raw)
	if !ok {
		t.Fatal("expected session to decode")
	}
	if session.UserID != "user-1" || session.TenantID != "tenant-1" {
		t.Fatalf("unexpected session payload: %#v", session)
	}
}

func TestSessionDecodeRejectsTamperedSignature(t *testing.T) {
	raw, err := EncodeSession("0123456789abcdef0123456789abcdef", models.Session{
		UserID:   "user-1",
		TenantID: "tenant-1",
		Expires:  time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := DecodeSession("different-secret-0123456789abcdef", raw); ok {
		t.Fatal("expected tampered session to be rejected")
	}
}
