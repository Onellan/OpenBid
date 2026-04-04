package source

import (
	"testing"

	"openbid/internal/models"
)

func TestNormalizeKey(t *testing.T) {
	t.Parallel()

	if got := NormalizeKey("  Metro_Works South!  "); got != "metro-works-south" {
		t.Fatalf("unexpected normalized key: %q", got)
	}
}

func TestAdapterFromConfigRejectsInvalidTypeAndURL(t *testing.T) {
	t.Parallel()

	if _, err := AdapterFromConfig(models.SourceConfig{Key: "x", Type: "xml", FeedURL: "https://example.org/feed.xml"}); err == nil {
		t.Fatal("expected unsupported source type to be rejected")
	}
	if _, err := AdapterFromConfig(models.SourceConfig{Key: "x", Type: TypeJSONFeed, FeedURL: "http://127.0.0.1/feed.json"}); err == nil {
		t.Fatal("expected private feed url to be rejected")
	}
}

func TestRegistryFromConfigsSkipsDisabledInvalidAndDuplicateEntries(t *testing.T) {
	t.Parallel()

	registry := RegistryFromConfigs([]models.SourceConfig{
		{Key: "b", Name: "Bravo", Type: TypeJSONFeed, FeedURL: "https://example.org/b.json", Enabled: true},
		{Key: "a", Name: "Alpha", Type: TypeJSONFeed, FeedURL: "https://example.org/a.json", Enabled: true},
		{Key: "a", Name: "Alpha Duplicate", Type: TypeJSONFeed, FeedURL: "https://example.org/dup.json", Enabled: true},
		{Key: "disabled", Name: "Disabled", Type: TypeJSONFeed, FeedURL: "https://example.org/disabled.json", Enabled: false},
		{Key: "invalid", Name: "Invalid", Type: TypeJSONFeed, FeedURL: "http://localhost/feed.json", Enabled: true},
	})

	if len(registry.Adapters) != 2 {
		t.Fatalf("expected 2 valid enabled unique adapters, got %d", len(registry.Adapters))
	}
	if registry.Adapters[0].Key() != "a" || registry.Adapters[1].Key() != "b" {
		t.Fatalf("expected adapters sorted by display name and deduplicated, got %#v %#v", registry.Adapters[0].Key(), registry.Adapters[1].Key())
	}
}

func TestNormalizeTenderIdentityProducesStableIDs(t *testing.T) {
	t.Parallel()

	first := NormalizeTenderIdentity(models.Tender{
		SourceKey:   "etenders",
		ExternalID:  "152485",
		Title:       "Plant and equipment panel",
		Issuer:      "Madibeng Local Municipality",
		ClosingDate: "2026-04-30 10:00",
	})
	second := NormalizeTenderIdentity(models.Tender{
		SourceKey:   "etenders",
		ExternalID:  "152485",
		Title:       "Plant and equipment panel",
		Issuer:      "Madibeng Local Municipality",
		ClosingDate: "2026-04-30 10:00",
	})
	documentOnly := NormalizeTenderIdentity(models.Tender{
		SourceKey:   "treasury",
		DocumentURL: "https://example.org/docs/spec.pdf",
		Title:       "Treasury tender",
	})

	if first.ID == "" {
		t.Fatal("expected stable id for external-id-backed tender")
	}
	if first.ID != second.ID {
		t.Fatalf("expected matching ids for same tender identity, got %q and %q", first.ID, second.ID)
	}
	if documentOnly.ID == "" {
		t.Fatal("expected stable id for document-backed tender")
	}
}
