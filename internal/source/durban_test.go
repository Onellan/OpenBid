package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openbid/internal/models"
)

func TestDurbanAdapterFetchMapsTenderRowsAndDocuments(t *testing.T) {
	t.Parallel()

	page := `
<html><body>
<table>
  <tbody>
    <tr style="font-weight: 380; font-size: 14px">
      <td>General</td>
      <td>RFQ</td>
      <td><p><span>Supply, Installation, Maintenance And Support Of Structured Network Cabling Infrastructure</span></p></td>
      <td>SZ 24</td>
      <td>2026-04-21 11:00:00</td>
    </tr>
    <tr style="background: #ffcb056b; border-bottom: 10px solid #f1f1f1">
      <td colspan="6">
        <div class="list-group" style="width:100%">
          <a href="#" class="list-group-item list-group-item-action active"><strong>Additional Information</strong></a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Procuring Entity:&nbsp;&nbsp;</strong></span>UShaka Marine World</a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Contact Person:&nbsp;&nbsp;</strong></span>Anda Boyana</a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Contact Number:&nbsp;&nbsp;</strong></span>031 328 8286</a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Email:&nbsp;&nbsp;</strong></span>aboyana@ushakamarineworld.co.za</a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Fax:&nbsp;&nbsp;</strong></span></a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Closing Date:&nbsp;&nbsp;</strong></span>2026-04-21 11:00:00</a>
        </div>
        <ul>
          <li><span>Main Tender Document| </span><a href="/uploads/0000/13/2026/04/14/sz-24.pdf">sz-24.pdf</a></li>
        </ul>
      </td>
    </tr>
    <tr style="font-weight: 380; font-size: 14px">
      <td>General</td>
      <td>Tenders</td>
      <td><p><span>Trenance 3 Reservoir: The Construction of a 6 Ml Reinforced Concrete Reservoir, Pump Station and Ancillary Works</span></p></td>
      <td>34730-5W</td>
      <td>2026-05-15 11:00:00</td>
    </tr>
    <tr style="background: #ffcb056b; border-bottom: 10px solid #f1f1f1">
      <td colspan="6">
        <div class="list-group" style="width:100%">
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Procuring Entity:&nbsp;&nbsp;</strong></span>Water Services</a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Contact Person:&nbsp;&nbsp;</strong></span>Sivashan Pillay</a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Contact Number:&nbsp;&nbsp;</strong></span>031-322-2636</a>
          <a href="#" class="list-group-item list-group-item-action"><span><strong>Enquiries Email:&nbsp;&nbsp;</strong></span>Sivashan.Pillay@durban.gov.za.</a>
        </div>
        <ul>
          <li><span>Main Tender Document| </span><a href="https://www.durban.gov.za/uploads/0000/13/2026/04/14/34730-5w-tender-document.pdf">34730-5w-tender-document.pdf</a></li>
          <li><span>Additional Tender Document| </span><a href="https://www.durban.gov.za/uploads/0000/13/2026/04/14/34730-5w-excel-boq.xlsx">34730-5w-excel-boq.xlsx</a></li>
        </ul>
      </td>
    </tr>
  </tbody>
</table>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.Contains(got, "OpenBid") {
			t.Fatalf("expected OpenBid user agent, got %q", got)
		}
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	adapter := NewDurbanAdapter("durban", server.URL+"/pages/business/procurement")
	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 Durban tenders, got %d", len(items))
	}
	if !strings.Contains(msg, "loaded 2 Durban procurement tenders") {
		t.Fatalf("unexpected message: %s", msg)
	}

	first := items[0]
	if first.ID != "durban-sz-24" || first.ExternalID != "SZ 24" {
		t.Fatalf("unexpected first identity: %#v", first)
	}
	if first.Title != "Supply, Installation, Maintenance And Support Of Structured Network Cabling Infrastructure" {
		t.Fatalf("unexpected title: %s", first.Title)
	}
	if first.Issuer != "UShaka Marine World" || first.TenderType != "RFQ" || first.Category != "General" {
		t.Fatalf("unexpected listing metadata: %#v", first)
	}
	if first.ClosingDate != "2026-04-21 11:00" || first.PublishedDate != "2026-04-14" || first.Status != "open" {
		t.Fatalf("unexpected date/status mapping: %#v", first)
	}
	if first.DocumentURL != server.URL+"/uploads/0000/13/2026/04/14/sz-24.pdf" {
		t.Fatalf("unexpected resolved document url: %s", first.DocumentURL)
	}
	if len(first.Contacts) != 1 || first.Contacts[0].Name != "Anda Boyana" || first.Contacts[0].Email != "aboyana@ushakamarineworld.co.za" {
		t.Fatalf("expected enquiries contact metadata, got %#v", first.Contacts)
	}
	if first.PageFacts["enquiries_procuring_entity"] != "UShaka Marine World" || first.PageFacts["document_count"] != "1" {
		t.Fatalf("expected page facts, got %#v", first.PageFacts)
	}
	if first.Location.Province != "KwaZulu-Natal" || !first.Submission.PhysicalAllowed {
		t.Fatalf("expected location/submission defaults, got %#v %#v", first.Location, first.Submission)
	}

	second := items[1]
	if second.ID != "durban-34730-5w" || second.TenderNumber != "34730-5W" {
		t.Fatalf("unexpected second identity: %#v", second)
	}
	if len(second.Documents) != 2 || second.Documents[0].Role != "notice" || second.Documents[1].Role != "support_document" {
		t.Fatalf("unexpected document roles: %#v", second.Documents)
	}
	if second.Contacts[0].Email != "Sivashan.Pillay@durban.gov.za" {
		t.Fatalf("expected trailing punctuation to be trimmed from contact email, got %#v", second.Contacts)
	}
	if second.Documents[1].MIMEType == "" || !strings.Contains(second.Documents[1].MIMEType, "sheet") {
		t.Fatalf("expected spreadsheet MIME type, got %#v", second.Documents[1])
	}
}

func TestAdapterFromConfigBuildsDurbanAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "durban",
		Type:    TypeDurbanPortal,
		FeedURL: "https://www.durban.gov.za/pages/business/procurement",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*DurbanAdapter); !ok {
		t.Fatalf("expected DurbanAdapter, got %T", adapter)
	}
}
