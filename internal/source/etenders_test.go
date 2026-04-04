package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openbid/internal/models"
)

func TestETendersAdapterFetchMapsRowsAcrossPages(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/Home/PaginatedTenderOpportunities" && r.URL.Query().Get("start") == "0":
			_, _ = w.Write([]byte(`{"recordsFiltered":2,"data":[{"id":101,"tender_No":"ABC-1","description":"Civil roads maintenance","category":"Services: Civil","type":"Request for Quotation","department":"City Works","organ_of_State":"City Works","status":"Published","closing_Date":"2026-04-17T11:00:00","date_Published":"2026-04-03T00:00:00","compulsory_briefing_session":"2026-04-10T09:30:00","briefingVenue":"Main Hall","streetname":"123 Main","surburb":"CBD","town":"Pretoria","code":"0001","conditions":"CIDB 6CE","contactPerson":"Jane Doe","email":"jane@example.org","telephone":"0123456789","fax":"0123456790","province":"Gauteng","delivery":"Pretoria Depot","briefingSession":true,"briefingCompulsory":true,"validity":90,"eSubmission":false,"twoEnvelopeSubmission":true,"supportDocument":[{"supportDocumentID":"doc-1","fileName":"spec.pdf","extension":".pdf"}]}]}`))
		case r.URL.Path == "/Home/PaginatedTenderOpportunities" && r.URL.Query().Get("start") == "1":
			_, _ = w.Write([]byte(`{"recordsFiltered":2,"data":[{"id":102,"tender_No":"XYZ-2","description":"Electrical substation upgrade","category":"Services: Electrical","type":"Request for Bid(Open-Tender)","department":"Provincial Works","organ_of_State":"Provincial Works","status":"Published","closing_Date":"2026-04-20T12:00:00","date_Published":"2026-04-04T00:00:00","province":"KwaZulu-Natal","delivery":"Durban","briefingSession":false,"briefingCompulsory":false,"eSubmission":true,"supportDocument":[]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewETendersAdapter("etenders", server.URL+"/Home/opportunities?id=1")
	adapter.PageSize = 1

	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if !strings.Contains(msg, "loaded 2 eTenders opportunities") {
		t.Fatalf("unexpected message: %s", msg)
	}

	first := items[0]
	if first.ExternalID != "101" || first.TenderNumber != "ABC-1" {
		t.Fatalf("unexpected first tender identity: %#v", first)
	}
	if first.ID == "" {
		t.Fatalf("expected stable tender id, got %#v", first)
	}
	if first.DocumentURL != server.URL+"/home/Download/?blobName=doc-1.pdf&downloadedFileName=spec.pdf" {
		t.Fatalf("unexpected document url: %s", first.DocumentURL)
	}
	if first.PublishedDate != "2026-04-03" || first.ClosingDate != "2026-04-17 11:00" {
		t.Fatalf("unexpected date mapping: %#v", first)
	}
	if first.ExtractedFacts["briefing_details"] == "" || first.ExtractedFacts["contact_details"] == "" {
		t.Fatalf("expected extracted facts to be populated: %#v", first.ExtractedFacts)
	}
	if first.TenderType != "Request for Quotation" || first.ValidityDays != 90 {
		t.Fatalf("expected typed tender metadata, got %#v", first)
	}
	if len(first.Documents) != 1 || first.Documents[0].FileName != "spec.pdf" {
		t.Fatalf("expected document metadata, got %#v", first.Documents)
	}
	if len(first.Contacts) != 1 || first.Contacts[0].Name != "Jane Doe" {
		t.Fatalf("expected contact metadata, got %#v", first.Contacts)
	}
	if len(first.Briefings) != 1 || !first.Briefings[0].Required {
		t.Fatalf("expected briefing metadata, got %#v", first.Briefings)
	}
	if first.Location.Town != "Pretoria" || first.Submission.TwoEnvelope != true {
		t.Fatalf("expected location/submission metadata, got %#v %#v", first.Location, first.Submission)
	}

	second := items[1]
	if second.ExternalID != "102" || second.Issuer != "Provincial Works" {
		t.Fatalf("unexpected second tender mapping: %#v", second)
	}
	if second.ID == "" {
		t.Fatalf("expected stable tender id for second tender, got %#v", second)
	}
	if second.DocumentURL != "" {
		t.Fatalf("expected empty document url, got %s", second.DocumentURL)
	}
	if second.ExtractedFacts["submission_details"] != "e_submission=yes; delivery=Durban" {
		t.Fatalf("unexpected submission details: %s", second.ExtractedFacts["submission_details"])
	}
	if second.Submission.ElectronicAllowed != true || second.Submission.Method != "electronic" {
		t.Fatalf("expected electronic submission metadata, got %#v", second.Submission)
	}
}

func TestAdapterFromConfigBuildsETendersAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "etenders",
		Type:    TypeETendersPortal,
		FeedURL: "https://www.etenders.gov.za/Home/opportunities?id=1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*ETendersAdapter); !ok {
		t.Fatalf("expected ETendersAdapter, got %T", adapter)
	}
}
