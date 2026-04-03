package extract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthzReturnsNilOnHealthyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL)
	client.HTTP.Timeout = 2 * time.Second
	if err := client.Healthz(context.Background()); err != nil {
		t.Fatalf("expected healthy extractor, got %v", err)
	}
}

func TestHealthzReturnsErrorOnUnhealthyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unhealthy", http.StatusBadGateway)
	}))
	defer server.Close()

	client := New(server.URL)
	if err := client.Healthz(context.Background()); err == nil {
		t.Fatal("expected unhealthy response to return an error")
	}
}

func TestExtractPostsNormalizedURLAndDecodesResponse(t *testing.T) {
	t.Setenv("OPENBID_ALLOW_PRIVATE_URLS", "true")
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/extract" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if contentType := r.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
			t.Fatalf("expected json content type, got %q", contentType)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(Result{
			Excerpt: "Important excerpt",
			Facts:   map[string]string{"issuer": "Metro"},
			Type:    "pdf",
		})
	}))
	defer server.Close()

	client := New(server.URL)
	result, err := client.Extract(context.Background(), "HTTP://127.0.0.1:9090/docs/test.pdf")
	if err != nil {
		t.Fatalf("expected extract request to succeed, got %v", err)
	}
	if received["url"] != "http://127.0.0.1:9090/docs/test.pdf" {
		t.Fatalf("expected normalized URL in payload, got %#v", received)
	}
	if result.Excerpt != "Important excerpt" || result.Facts["issuer"] != "Metro" || result.Type != "pdf" {
		t.Fatalf("unexpected extractor result: %#v", result)
	}
}

func TestExtractRejectsUnsafeURLBeforeRequest(t *testing.T) {
	client := New("http://example.com")
	if _, err := client.Extract(context.Background(), "http://127.0.0.1/private.pdf"); err == nil {
		t.Fatal("expected private URL to be rejected")
	}
}
