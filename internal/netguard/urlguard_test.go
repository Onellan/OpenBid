package netguard

import "testing"

func TestNormalizePublicHTTPURLAcceptsPublicHTTPSURL(t *testing.T) {
	normalized, err := NormalizePublicHTTPURL("https://example.org/feed.json")
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "https://example.org/feed.json" {
		t.Fatalf("unexpected normalized url: %q", normalized)
	}
}

func TestNormalizePublicHTTPURLRejectsPrivateHosts(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/feed.json",
		"http://localhost/feed.json",
		"http://192.168.1.10/feed.json",
	} {
		if _, err := NormalizePublicHTTPURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}
