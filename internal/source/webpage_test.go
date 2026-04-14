package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openbid/internal/models"
)

func TestWebPageAdapterFetchExtractsTenderLinks(t *testing.T) {
	t.Parallel()

	page := `
<html><head><title>Current Tenders - Example Agency</title></head><body>
  <nav><a href="/privacy">Privacy</a><a href="/procurement">Procurement</a></nav>
  <table>
    <tr>
      <td>RFQ 123/2026</td>
      <td>Supply and install pumps</td>
      <td><a href="/docs/2026/04/13/rfq-123-2026-tender-document.pdf">Download document</a></td>
    </tr>
  </table>
  <p><a href="/tenders/current-opportunities">Current opportunities</a></p>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	adapter := NewWebPageAdapter("example", server.URL+"/tenders")
	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 webpage listings, got %d: %#v", len(items), items)
	}
	if !strings.Contains(msg, "loaded 2 webpage listings") {
		t.Fatalf("unexpected message: %s", msg)
	}

	first := items[0]
	if first.SourceKey != "example" || first.Issuer != "Example Agency" {
		t.Fatalf("unexpected source/issuer mapping: %#v", first)
	}
	if first.DocumentURL != server.URL+"/docs/2026/04/13/rfq-123-2026-tender-document.pdf" {
		t.Fatalf("unexpected document URL: %s", first.DocumentURL)
	}
	if first.PublishedDate != "2026-04-13" || first.DocumentStatus != models.ExtractionQueued {
		t.Fatalf("unexpected document metadata: %#v", first)
	}
	if first.TenderNumber != "RFQ 123/2026" {
		t.Fatalf("expected tender number from row context, got %q", first.TenderNumber)
	}
	if len(first.Documents) != 1 || first.Documents[0].Role != "support_document" {
		t.Fatalf("expected document metadata, got %#v", first.Documents)
	}

	second := items[1]
	if second.DocumentURL != "" || second.OriginalURL != server.URL+"/tenders/current-opportunities" {
		t.Fatalf("expected non-document opportunity link, got %#v", second)
	}
}

func TestWebPageAdapterTreatsForbiddenPortalAsEmpty(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	adapter := NewWebPageAdapter("blocked", server.URL+"/portal")
	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || !strings.Contains(msg, "requires access") {
		t.Fatalf("expected empty gated portal result, got items=%d msg=%q", len(items), msg)
	}
}

func TestAdapterFromConfigBuildsWebPageAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "web",
		Type:    TypeWebPagePortal,
		FeedURL: "https://example.org/tenders",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*WebPageAdapter); !ok {
		t.Fatalf("expected WebPageAdapter, got %T", adapter)
	}
}
