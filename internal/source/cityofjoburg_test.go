package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"openbid/internal/models"
)

func TestCityOfJoburgAdapterFetchCombinesPublicYearPages(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/work_/Pages/Work in Joburg/Tenders and Quotations/2022 Tenders and Quotations/2022 TENDERS/BID OPENING REGISTERS/Invitation to Bid.aspx":
			_, _ = w.Write([]byte(`<html><head><title>Current Bid Proposals</title></head><body>
				<a href="/work_/Documents/2026-Tenders/COJ-TRP002-25-26-TENDER-DOCUMENT.pdf">Tender document</a>
			</body></html>`))
		case "/work_/Pages/2026-Tenders/Bid-Opening-Registers-2026.aspx":
			_, _ = w.Write([]byte(`<html><head><title>Bid Opening Registers 2026</title></head><body>
				<a href="/work_/Documents/2026-Tenders/OPENING-REGISTER-COJ-DEVP001-25-26.pdf">Opening register</a>
			</body></html>`))
		case "/work_/Pages/2025-Tenders-and-Quotations/Bid-Opening-Registers.aspx":
			_, _ = w.Write([]byte(`<html><head><title>Bid Opening Registers 2025</title></head><body>
				<a href="/work_/Documents/2025-Tenders-and-Quotations/COJ-GFIN002-25-26-OPENING-REGISTER.pdf">Opening register</a>
			</body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewCityOfJoburgAdapter("city-of-joburg", server.URL+"/work_/Pages/Work%20in%20Joburg/Tenders%20and%20Quotations/2022%20Tenders%20and%20Quotations/2022%20TENDERS/BID%20OPENING%20REGISTERS/Invitation%20to%20Bid.aspx")
	adapter.Client = server.Client()
	adapter.Now = func() time.Time { return time.Date(2026, time.April, 14, 12, 0, 0, 0, time.UTC) }

	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 COJ listings, got %d: %#v", len(items), items)
	}
	if !strings.Contains(msg, "loaded 3 City of Johannesburg listings from 3 pages") {
		t.Fatalf("unexpected message: %s", msg)
	}

	byTender := map[string]models.Tender{}
	for _, item := range items {
		byTender[item.TenderNumber] = item
		if item.SourceKey != "city-of-joburg" || item.Issuer != "City of Johannesburg" || item.Province != "Gauteng" {
			t.Fatalf("unexpected COJ metadata: %#v", item)
		}
	}
	current := byTender["COJ-TRP002-25-26"]
	if current.Status != "open" || current.OriginalURL == "" || current.DocumentStatus != models.ExtractionQueued {
		t.Fatalf("expected current bid proposal metadata, got %#v", current)
	}
	register := byTender["COJ-DEVP001-25-26"]
	if register.Status != "closed" || register.Documents[0].Role != "bid_opening_register" {
		t.Fatalf("expected bid opening register metadata, got %#v", register)
	}
	if _, ok := byTender["COJ-GFIN002-25-26"]; !ok {
		t.Fatalf("expected previous-year register to be included, got %#v", byTender)
	}
}

func TestCityOfJoburgPagesAdaptYearPatterns(t *testing.T) {
	t.Parallel()

	base, err := urlParseForTest("https://joburg.org.za/work_/Pages/2026-Tenders/Bid-Opening-Registers-2026.aspx")
	if err != nil {
		t.Fatal(err)
	}
	pages := cityOfJoburgPages(base, time.Date(2026, time.April, 14, 12, 0, 0, 0, time.UTC))
	joined := ""
	for _, page := range pages {
		joined += page.URL + "\n"
	}
	for _, want := range []string{
		"/work_/Pages/2026-Tenders/Bid-Opening-Registers-2026.aspx",
		"/work_/Pages/2026-Tenders-and-Quotations/Bid-Opening-Registers.aspx",
		"/work_/Pages/2025-Tenders/Bid-Opening-Registers-2025.aspx",
		"/work_/Pages/2025-Tenders-and-Quotations/Bid-Opening-Registers.aspx",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected generated COJ page %s in:\n%s", want, joined)
		}
	}
}

func TestAdapterFromConfigBuildsCityOfJoburgAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "city-of-joburg",
		Type:    TypeCityOfJoburgPortal,
		FeedURL: DefaultCityOfJoburgPageURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*CityOfJoburgAdapter); !ok {
		t.Fatalf("expected CityOfJoburgAdapter, got %T", adapter)
	}
}

func urlParseForTest(rawURL string) (*url.URL, error) { return url.Parse(rawURL) }
