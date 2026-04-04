package app

import "testing"

func TestValidationHelpersNormalizeAndValidateInput(t *testing.T) {
	if got := normalizeUsername("  Alice.Admin  "); got != "Alice.Admin" {
		t.Fatalf("unexpected normalized username: %q", got)
	}
	if got := normalizeEmail("  USER@Example.COM "); got != "user@example.com" {
		t.Fatalf("unexpected normalized email: %q", got)
	}
	if got := normalizeTenantSlug("", "  Acme Engineering West "); got != "acme-engineering-west" {
		t.Fatalf("unexpected normalized tenant slug: %q", got)
	}
	if !validEmailAddress("team@example.org") {
		t.Fatal("expected valid email address to pass")
	}
	if validEmailAddress("not-an-email") {
		t.Fatal("expected invalid email address to fail")
	}
	if !isValidRole("viewer") || isValidRole("root") {
		t.Fatal("unexpected role validation result")
	}
}

func TestNormalizeSafeOutboundURLValidation(t *testing.T) {
	normalized, err := normalizeSafeOutboundURL("https://example.org/feed.json")
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "https://example.org/feed.json" {
		t.Fatalf("unexpected normalized outbound url: %q", normalized)
	}
	if _, err := normalizeSafeOutboundURL("ftp://example.org/file.txt"); err == nil {
		t.Fatal("expected non-http outbound url to be rejected")
	}
	if _, err := normalizeSafeOutboundURL("http://127.0.0.1/feed.json"); err == nil {
		t.Fatal("expected private outbound url to be rejected")
	}
}
