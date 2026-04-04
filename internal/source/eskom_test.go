package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openbid/internal/models"
)

func TestEskomAdapterFetchMapsTendersAndDocuments(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/webapi/api/Lookup/GetTender":
			_, _ = w.Write([]byte(`[
				{
					"TENDER_ID":73478,
					"GROUP_ID":6,
					"DESCRIPTION":"ESKOM ENTERPRISES",
					"CONTRACT_ID":11,
					"Audience":"All Suppliers",
					"HEADER_DESC":"The Provision of cleaning services for the Ash and Coal plant",
					"REFERENCE":"ERI/2022/BMS/08",
					"SCOPE_DETAILS":"Detailed scope text",
					"SUMMARY":"Summary text",
					"OFFICE_LOCATION":"ERI GERMISTON",
					"DOCS":1,
					"CLOSING_DATE":"2027-02-22T13:33:00",
					"PUBLISHEDDATE":"2023-05-22T13:35:12.307",
					"DATEMODIFIED":"2023-05-22T13:34:42.63",
					"FAX":"0110000000",
					"EMAIL":"mandavt@eskom.co.za",
					"ADDRESS":"Megawatt Park",
					"PUBLISH":"Y",
					"PROVINCE_ID":10,
					"Province":"National",
					"E_TENDERING":false
				},
				{
					"TENDER_ID":79192,
					"GROUP_ID":2,
					"DESCRIPTION":"DISTRIBUTION",
					"CONTRACT_ID":3,
					"Audience":"All Suppliers",
					"HEADER_DESC":"Industrial gases cancellation",
					"REFERENCE":"MWP2457DXCancellatio",
					"SCOPE_DETAILS":"cancellation",
					"SUMMARY":"",
					"OFFICE_LOCATION":"Megawatt Park",
					"DOCS":0,
					"CLOSING_DATE":"2024-05-28T10:00:00",
					"PUBLISHEDDATE":"2024-05-28T09:37:08.273",
					"DATEMODIFIED":"2025-07-30T20:34:08.3",
					"FAX":"",
					"EMAIL":"mandavt@eskom.co.za",
					"ADDRESS":"Megawatt Park",
					"PUBLISH":"Y",
					"PROVINCE_ID":10,
					"Province":"National",
					"E_TENDERING":true
				}
			]`))
		case r.URL.Path == "/webapi/api/Lookup/GetContract":
			_, _ = w.Write([]byte(`[{"CONTRACT_ID":11,"DESCRIPTION":"Term Service Contract"},{"CONTRACT_ID":3,"DESCRIPTION":"Engineering and Construction Short Contract"}]`))
		case r.URL.Path == "/webapi/api/Lookup/GetDivision":
			_, _ = w.Write([]byte(`[{"GROUP_ID":6,"DESCRIPTION":"ESKOM ENTERPRISES"},{"GROUP_ID":2,"DESCRIPTION":"DISTRIBUTION"}]`))
		case r.URL.Path == "/webapi/api/Lookup/GetProvince":
			_, _ = w.Write([]byte(`[{"PROVINCE_ID":10,"DESCRIPTION":"National"}]`))
		case r.URL.Path == "/webapi/api/Files/GetDocs" && r.URL.Query().Get("TENDER_ID") == "73478":
			_, _ = w.Write([]byte(`[{"ID":380938,"NAME":"Regret Letter.pdf","SIZE":204730,"SEQUENCE":0,"CDATE":"2023-05-22T13:34:42.613","ContentType":"application/pdf"}]`))
		case r.URL.Path == "/webapi/api/Files/GetDocs" && r.URL.Query().Get("TENDER_ID") == "79192":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewEskomAdapter("eskom", server.URL+"/?pageSize=5&pageNumber=1")
	adapter.Concurrency = 1

	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if !strings.Contains(msg, "loaded 2 Eskom tenders") {
		t.Fatalf("unexpected message: %s", msg)
	}

	first := items[0]
	if first.ExternalID != "73478" || first.TenderNumber != "ERI/2022/BMS/08" {
		t.Fatalf("unexpected identity mapping: %#v", first)
	}
	if first.ID == "" {
		t.Fatalf("expected stable tender id, got %#v", first)
	}
	if first.Issuer != "ESKOM ENTERPRISES" || first.TenderType != "Term Service Contract" {
		t.Fatalf("unexpected issuer/type mapping: %#v", first)
	}
	if first.DocumentURL != server.URL+"/webapi/api/Files/GetFile?FileID=380938" {
		t.Fatalf("unexpected document url: %s", first.DocumentURL)
	}
	if len(first.Documents) != 1 || first.Documents[0].FileName != "Regret Letter.pdf" {
		t.Fatalf("expected mapped documents, got %#v", first.Documents)
	}
	if first.PublishedDate != "2023-05-22" || first.ClosingDate != "2027-02-22 13:33" || first.Status != "open" {
		t.Fatalf("unexpected date/status mapping: %#v", first)
	}
	if first.PageFacts["contract_type"] != "Term Service Contract" || first.SourceMetadata["division"] != "ESKOM ENTERPRISES" {
		t.Fatalf("expected contract and division metadata, got facts=%#v meta=%#v", first.PageFacts, first.SourceMetadata)
	}
	if len(first.Contacts) != 1 || first.Contacts[0].Email != "mandavt@eskom.co.za" {
		t.Fatalf("expected contact metadata, got %#v", first.Contacts)
	}

	second := items[1]
	if second.Status != "cancelled" {
		t.Fatalf("expected cancelled status inference, got %#v", second)
	}
	if second.DocumentURL != "" || len(second.Documents) != 0 {
		t.Fatalf("expected no documents for second tender, got %#v", second.Documents)
	}
	if second.Submission.Method != "electronic" {
		t.Fatalf("expected electronic submission mapping, got %#v", second.Submission)
	}
}

func TestAdapterFromConfigBuildsEskomAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "eskom",
		Type:    TypeEskomPortal,
		FeedURL: "https://tenderbulletin.eskom.co.za/?pageSize=5&pageNumber=1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*EskomAdapter); !ok {
		t.Fatalf("expected EskomAdapter, got %T", adapter)
	}
}
