package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tenderhub-za/internal/models"
)

func TestFeedAdapterEmbeddedSamples(t *testing.T) {
	t.Parallel()

	adapter := NewFeedAdapter("Treasury Feed", "")
	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 embedded sample tenders, got %d", len(items))
	}
	if !strings.Contains(msg, "embedded sample feed") {
		t.Fatalf("unexpected message: %q", msg)
	}
	if items[0].SourceKey != "treasury-feed" {
		t.Fatalf("expected normalized source key, got %#v", items[0])
	}
	if items[0].DocumentStatus != models.ExtractionQueued || items[1].DocumentStatus != models.ExtractionQueued {
		t.Fatalf("expected queued document status for embedded samples, got %#v %#v", items[0], items[1])
	}
}

func TestFeedAdapterRemoteFeedMapping(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"releases": [
				{
					"ocid": "ocid-1",
					"title": "Electrical substation engineering roads water upgrade",
					"issuer": "Metro Works",
					"province": "Gauteng",
					"category": "Electrical Engineering",
					"tender_number": "TH-100",
					"published_date": "2026-04-01",
					"closing_date": "2026-04-21",
					"status": "open",
					"cidb_grading": "6EP",
					"summary": "Upgrade of electrical infrastructure and stormwater systems",
					"original_url": "https://example.org/opportunities/1",
					"document_url": "https://example.org/docs/1.pdf"
				},
				{
					"ocid": "ocid-2",
					"title": "Office chairs supply",
					"issuer": "Metro Works",
					"province": "Gauteng",
					"category": "Furniture",
					"status": "open"
				}
			]
		}`))
	}))
	defer server.Close()

	adapter := NewFeedAdapter("treasury", server.URL)
	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 mapped tenders, got %d", len(items))
	}
	if msg != "loaded remote feed" {
		t.Fatalf("unexpected message: %q", msg)
	}
	if items[0].ExternalID != "ocid-1" || items[0].TenderNumber != "TH-100" {
		t.Fatalf("unexpected remote tender mapping: %#v", items[0])
	}
	if !items[0].EngineeringRelevant || items[0].RelevanceScore <= 0.5 {
		t.Fatalf("expected engineering tender to score as relevant, got %#v", items[0])
	}
	if items[1].EngineeringRelevant || items[1].RelevanceScore <= 0 || items[1].RelevanceScore >= 0.5 {
		t.Fatalf("expected non-engineering tender to receive low relevance score, got %#v", items[1])
	}
}

func TestFeedAdapterRejectsHTTPErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failed", http.StatusBadGateway)
	}))
	defer server.Close()

	adapter := NewFeedAdapter("treasury", server.URL)
	if _, _, err := adapter.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "feed returned 502") {
		t.Fatalf("expected upstream error to be surfaced, got %v", err)
	}
}
