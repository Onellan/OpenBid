package source

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openbid/internal/models"
)

func TestOnlineTendersAdapterFetchMapsListingsAcrossPages(t *testing.T) {
	t.Parallel()

	pageOne := `
<div class="pagination pagination-new push-half-bottom pagination-top"><label>Showing 1 to 2 of 3 Tenders</label><ul class="clearfix"><li class="active"><span>1</span></li><li><a href="/tenders/south-africa?tcs=civil%23engineering+consultants&page=2" rel="nofollow">2</a></li></ul></div>
<div class="table-tenders">
  <div class="tender" data-tid='101' data-closed='0'>
    <div class="tender-cn clearfix"><a rel="nofollow" class="" data-action="login" href="javascript:void(0)"><i class="icon-search text-big a-i"></i>OT-001</a><span class='text-red'>New</span></div>
    <div class="tender-desc">Upgrade of municipal pump station.<br><br>Scope includes civils and mechanical refurbishment.<a class="show-more-tender-details" data-action="login" href="javascript:void(0)"><div class="p-t-4">show more details...</div></a></div>
    <div class="tender-si">Compulsory briefing at municipal depot</div>
    <div class="tender-cd">2026-12-10 11H00<br/></div>
    <div class="tender-attb text-big"></div>
  </div>
  <div class="tender" data-tid='102' data-closed='1'>
    <div class="tender-cn clearfix"><a rel="nofollow" class="" data-action="login" href="javascript:void(0)">OT-002</a></div>
    <div class="tender-desc">Road rehabilitation works in district corridor.</div>
    <div class="tender-si">To view details<br><div><a href='/pricing-and-plans.aspx'>Subscribe Now!</a></div></div>
    <div class="tender-cd">2026-01-10 10H00<br/></div>
    <div class="tender-attb text-big"></div>
  </div>
</div>`
	pageTwo := `
<div class="pagination pagination-new push-half-bottom pagination-top"><label>Showing 3 to 3 of 3 Tenders</label><ul class="clearfix"><li><a href="/tenders/south-africa?tcs=civil%23engineering+consultants&page=1" rel="nofollow">1</a></li><li class="active"><span>2</span></li></ul></div>
<div class="table-tenders">
  <div class="tender" data-tid='103' data-closed='0'>
    <div class="tender-cn clearfix"><a rel="nofollow" class="" data-action="login" href="javascript:void(0)">OT-003</a></div>
    <div class="tender-desc">Consulting engineering services for stormwater master planning.</div>
    <div class="tender-si">Optional briefing online</div>
    <div class="tender-cd">2026-11-01 09H30<br/></div>
    <div class="tender-attb text-big"></div>
  </div>
</div>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			_, _ = w.Write([]byte(pageOne))
		case "2":
			_, _ = w.Write([]byte(pageTwo))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewOnlineTendersAdapter("onlinetenders", server.URL+"/tenders/south-africa?tcs=civil%23engineering%20consultants")
	items, msg, err := adapter.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 listings, got %d", len(items))
	}
	if !strings.Contains(msg, "loaded 3 OnlineTenders listings") {
		t.Fatalf("unexpected fetch message: %s", msg)
	}

	first := items[0]
	if first.ExternalID != "101" || first.TenderNumber != "OT-001" {
		t.Fatalf("unexpected first listing identifiers: %#v", first)
	}
	if first.Title != "Upgrade of municipal pump station." {
		t.Fatalf("unexpected title: %q", first.Title)
	}
	if first.Issuer != "OnlineTenders" {
		t.Fatalf("expected OnlineTenders issuer placeholder, got %q", first.Issuer)
	}
	if first.Category != "civil / engineering consultants" {
		t.Fatalf("unexpected category: %q", first.Category)
	}
	if first.ClosingDate != "2026-12-10 11:00" {
		t.Fatalf("unexpected closing date: %q", first.ClosingDate)
	}
	if first.Status != "open" {
		t.Fatalf("expected open status, got %q", first.Status)
	}
	if first.DocumentURL != "" || first.DocumentStatus != "" || len(first.Documents) != 0 {
		t.Fatalf("expected listing-only source without docs, got %#v", first)
	}
	if !strings.Contains(first.OriginalURL, "page=1") || !strings.Contains(first.OriginalURL, "#tender-101") {
		t.Fatalf("unexpected original url: %q", first.OriginalURL)
	}
	if first.PageFacts["detail_access"] != "subscription_required" || first.PageFacts["site_inspection"] != "Compulsory briefing at municipal depot" {
		t.Fatalf("unexpected page facts: %#v", first.PageFacts)
	}
	if items[1].Status != "closed" {
		t.Fatalf("expected second listing to be closed, got %q", items[1].Status)
	}
	if items[2].PageFacts["site_inspection"] != "Optional briefing online" {
		t.Fatalf("unexpected optional briefing parsing: %#v", items[2].PageFacts)
	}
}

func TestAdapterFromConfigBuildsOnlineTendersAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "onlinetenders",
		Type:    TypeOnlineTenders,
		FeedURL: "https://www.onlinetenders.co.za/tenders/south-africa?tcs=civil%23engineering%20consultants",
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Key() != "onlinetenders" {
		t.Fatalf("unexpected adapter key: %s", adapter.Key())
	}
}
