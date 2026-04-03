package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithRequestObservabilityPreservesIncomingRequestID(t *testing.T) {
	a := &App{}
	var seenRequestID string
	handler := a.WithRequestObservability(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRequestID = requestIDFromContext(r.Context())
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/health?q=1", nil)
	req.Header.Set("X-Request-Id", "req-123")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if seenRequestID != "req-123" {
		t.Fatalf("expected request id in context, got %q", seenRequestID)
	}
	if responseID := w.Header().Get("X-Request-Id"); responseID != "req-123" {
		t.Fatalf("expected request id on response, got %q", responseID)
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected downstream status to be preserved, got %d", w.Code)
	}
}

func TestWithRequestObservabilityGeneratesRequestIDWhenMissing(t *testing.T) {
	a := &App{}
	var seenRequestID string
	handler := a.WithRequestObservability(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRequestID = requestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if strings.TrimSpace(seenRequestID) == "" {
		t.Fatal("expected generated request id in context")
	}
	if responseID := strings.TrimSpace(w.Header().Get("X-Request-Id")); responseID == "" || responseID != seenRequestID {
		t.Fatalf("expected generated request id on response, got context=%q response=%q", seenRequestID, responseID)
	}
}

func TestForwardedClientIPPrefersForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.20:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
	if ip := forwardedClientIP(req); ip != "203.0.113.10" {
		t.Fatalf("expected first forwarded IP, got %q", ip)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.20:1234"
	req.Header.Set("X-Real-IP", "203.0.113.20")
	if ip := forwardedClientIP(req); ip != "203.0.113.20" {
		t.Fatalf("expected X-Real-IP fallback, got %q", ip)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.20:1234"
	if ip := forwardedClientIP(req); ip != "198.51.100.20" {
		t.Fatalf("expected remote addr fallback, got %q", ip)
	}
}
