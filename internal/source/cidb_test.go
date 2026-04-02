package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tenderhub-za/internal/models"
)

func TestCIDBAdapterFetchMapsListingAndDocuments(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tenders.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"tender_count":"1",
			"tm_tenders":[
				{
					"tender_ID":"39",
					"user_ID":"32",
					"region":"1",
					"region_name":"Tenders",
					"description":"Appointment of a service provider for network cabling. <a href=\"/wp-content/uploads/2026/04/pricing-schedule.xlsx\" target=\"_blank\">Pricing Schedule</a>",
					"bid_number":" cidb 018 2526",
					"tender_advert_file":"https://example.org/cidb-018.pdf",
					"tender_advert_date_time":"2026-03-31 16:49:26",
					"tender_specification_file":"",
					"tender_specification_date_time":"2026-04-02 17:49:26",
					"tender_awards_file":"https://example.org/opening-register.pdf",
					"tender_briefing_file":"https://example.org/msa-draft.pdf",
					"tender_awards_date_time":"2026-04-02 17:49:26",
					"status":"active",
					"realstatus":"Open",
					"row_num":2,
					"tender_advert_file_link":"<a href=\"https://example.org/cidb-018.pdf\">2026-03-31 16:49:26</a>",
					"tender_specification_file_link":"N/A",
					"tender_awards_file_link":"<a href=\"https://example.org/opening-register.pdf\">2026-04-02 17:49:26</a>",
					"tender_briefing_file_link":"<a href=\"https://example.org/msa-draft.pdf\">Tender Briefing File</a>"
				}
			]
		}`))
	}))
	defer server.Close()

	adapter := NewCIDBAdapter("cidb", server.URL+"/cidb-tenders/current-tenders/")
	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !strings.Contains(msg, "loaded 1 CIDB tenders") {
		t.Fatalf("unexpected message: %s", msg)
	}

	item := items[0]
	if item.ID != "cidb-39" || item.ExternalID != "39" {
		t.Fatalf("unexpected identity mapping: %#v", item)
	}
	if item.TenderNumber != "CIDB 018 2526" {
		t.Fatalf("unexpected bid number: %s", item.TenderNumber)
	}
	if item.PublishedDate != "2026-03-31" || item.Status != "open" {
		t.Fatalf("unexpected publish/status mapping: %#v", item)
	}
	if item.DocumentURL != "https://example.org/cidb-018.pdf" {
		t.Fatalf("unexpected primary document url: %s", item.DocumentURL)
	}
	if len(item.Documents) != 4 {
		t.Fatalf("expected advert, award, briefing, and extra attachment documents, got %#v", item.Documents)
	}
	if item.Documents[0].Role != "advert" || item.Documents[1].Role != "opening_register" || item.Documents[2].Role != "briefing_note" {
		t.Fatalf("unexpected document role mapping: %#v", item.Documents)
	}
	if item.Documents[3].MIMEType == "" || !strings.Contains(item.Documents[3].MIMEType, "sheet") {
		t.Fatalf("expected xlsx mime type on extra attachment, got %#v", item.Documents[3])
	}
	if item.PageFacts["advertised_at"] != "2026-03-31 16:49" || item.SourceMetadata["region_name"] != "Tenders" {
		t.Fatalf("expected listing metadata, got facts=%#v meta=%#v", item.PageFacts, item.SourceMetadata)
	}
}

func TestAdapterFromConfigBuildsCIDBAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "cidb",
		Type:    TypeCIDBPortal,
		FeedURL: "https://www.cidb.org.za/cidb-tenders/current-tenders/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*CIDBAdapter); !ok {
		t.Fatalf("expected CIDBAdapter, got %T", adapter)
	}
}
