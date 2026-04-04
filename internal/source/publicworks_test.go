package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openbid/internal/models"
)

func TestPublicWorksAdapterFetchMapsActiveTenderList(t *testing.T) {
	t.Parallel()

	page := `
<html><body>
<div class="tab-pane fade show active" id="pills-advert" role="tabpanel">
  <div class="tenders-heading"><h4>Active Tenders</h4></div>
  <div class="tender-set-1" id="first-set">
    <ol class="tender-info">
      <li>
        <span class="bid-num"><i class="fa fa-file-pdf-o"></i> <a href="PDFs/procurement/Advert/2026/PT25-020.pdf" target="_blank">PT25/020</a></span>
        <span class="closing-date">[Closing: 22 April 2026 at 11H00]</span>
        <span class="posted-date">30 March 2025</span>
      </li>
      <li>
        <span class="bid-num"><i class="fa fa-file-pdf-o"></i> <a href="PDFs/procurement/Advert/2026/PLK26-04.pdf" target="_blank">PLK26/04</a></span>
        <span class="closing-date">[Closing: 17 April 2026 at 11H00]</span>
        <span class="posted-date">27 March 2025</span>
        <span class="status">Advertised</span>
      </li>
    </ol>
  </div>
</div>
<div class="tab-pane fade" id="pills-received" role="tabpanel">
  <ol class="tender-info">
    <li>
      <span class="bid-num"><a href="PDFs/procurement/received/2026/IGNORED.pdf">IGNORED</a></span>
      <span class="closing-date">[Closing: 01 May 2026 at 11H00]</span>
      <span class="posted-date">01 April 2025</span>
    </li>
  </ol>
</div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	adapter := NewPublicWorksAdapter("public-works", server.URL+"/tenders.html")

	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 active tenders, got %d", len(items))
	}
	if !strings.Contains(msg, "loaded 2 Public Works tenders") {
		t.Fatalf("unexpected message: %s", msg)
	}

	first := items[0]
	if first.ID != "public-works-pt25-020" {
		t.Fatalf("unexpected stable id: %s", first.ID)
	}
	if first.DocumentURL != server.URL+"/PDFs/procurement/Advert/2026/PT25-020.pdf" {
		t.Fatalf("unexpected document url: %s", first.DocumentURL)
	}
	if first.TenderNumber != "PT25/020" || first.Title != "DPWI tender PT25/020" {
		t.Fatalf("unexpected tender naming: %#v", first)
	}
	if first.PublishedDate != "2025-03-30" || first.ClosingDate != "2026-04-22 11:00" {
		t.Fatalf("unexpected date mapping: %#v", first)
	}
	if first.Status != "active" {
		t.Fatalf("expected default active status, got %s", first.Status)
	}
	if len(first.Documents) != 1 || first.Documents[0].Role != "notice" {
		t.Fatalf("expected notice document metadata, got %#v", first.Documents)
	}
	if first.PageFacts["document_name"] != "PT25-020.pdf" {
		t.Fatalf("expected page facts to retain listing metadata, got %#v", first.PageFacts)
	}
	if first.Submission.PhysicalAllowed != true {
		t.Fatalf("expected default physical submission hint, got %#v", first.Submission)
	}

	second := items[1]
	if second.Status != "advertised" {
		t.Fatalf("expected listed status to be preserved, got %s", second.Status)
	}
	if second.ExtractedFacts["listing_section"] != "active_tenders" || second.PageFacts["document_name"] != "PLK26-04.pdf" {
		t.Fatalf("unexpected extracted facts/page facts: %#v %#v", second.ExtractedFacts, second.PageFacts)
	}
}

func TestAdapterFromConfigBuildsPublicWorksAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "public-works",
		Type:    TypePublicWorks,
		FeedURL: "http://www.publicworks.gov.za/tenders.html",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*PublicWorksAdapter); !ok {
		t.Fatalf("expected PublicWorksAdapter, got %T", adapter)
	}
}
