package source

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openbid/internal/models"
)

func TestTendersSAAdapterFetchMapsPublicTenderCards(t *testing.T) {
	t.Parallel()
	page := `<article><div class="tender-number">RFP078/2026</div><div class="category">Construction</div><div class="province">Gauteng</div><a href="/sa-tenders/tender/rfp078-2026-bridge"><h2>Build a pedestrian bridge</h2></a><div class="issuer">Development Bank of Southern Africa</div><div>Closes: <time>04 Sept 2026</time></div></article>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(page)) }))
	defer server.Close()
	items, msg, err := NewTendersSAAdapter("tenders-sa", server.URL+"/sa-tenders/tenders").Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !strings.Contains(msg, "loaded 1 Tenders SA") {
		t.Fatalf("unexpected result: items=%#v msg=%q", items, msg)
	}
	item := items[0]
	if item.ExternalID != "RFP078/2026" || item.Title != "Build a pedestrian bridge" || item.Issuer != "Development Bank of Southern Africa" {
		t.Fatalf("unexpected mapping: %#v", item)
	}
	if item.Province != "Gauteng" || item.Category != "Construction" || item.ClosingDate != "2026-09-04" || item.Status != "open" {
		t.Fatalf("unexpected tender fields: %#v", item)
	}
}

func TestTendersSAAdapterTreatsBlockedPortalAsAccessRequired(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "blocked", http.StatusForbidden) }))
	defer server.Close()
	items, msg, err := NewTendersSAAdapter("tenders-sa", server.URL).Fetch(t.Context())
	if err != nil || len(items) != 0 || !strings.Contains(msg, "blocks this automated request") {
		t.Fatalf("unexpected blocked response: items=%d msg=%q err=%v", len(items), msg, err)
	}
}

func TestAdapterFromConfigBuildsTendersSAAdapter(t *testing.T) {
	t.Parallel()
	adapter, err := AdapterFromConfig(models.SourceConfig{Key: "tenders-sa", Type: TypeTendersSA, FeedURL: DefaultTendersSAPageURL})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*TendersSAAdapter); !ok {
		t.Fatalf("expected TendersSAAdapter, got %T", adapter)
	}
}
