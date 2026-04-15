package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openbid/internal/models"
)

func TestTransnetAdapterFetchMapsAdvertisedTenders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Home/GetAdvertisedTenders" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Referer"); !strings.Contains(got, "/Home/AdvertisedTenders") {
			t.Fatalf("expected advertised tenders referer, got %q", got)
		}
		_, _ = w.Write([]byte(`{
			"success": true,
			"result": [{
				"nameOfTender": "TRIMRFPB43 Bela Bela",
				"descriptionOfTender": "For Leasing of the Transnet Rail Infrastructure Manager Sidings",
				"tenderNumber": "TFR/2026/04/0004/114328/RFP",
				"briefingDate": "4/17/2026 10:00:00 AM",
				"briefingDetails": "Non-compulsory briefing via MS Teams",
				"closingDate": "5/28/2026 10:00:00 AM",
				"contactPersonEmailAddress": "Thuli.Mathebula@transnet.net",
				"contactPersonName": "Thuli Mathebula",
				"publishedDate": "4/5/2026 7:26:07 PM",
				"attachment": "https://publishedetenders.blob.core.windows.net/publishedetenderscontainer/114328",
				"tenderType": "RFP",
				"locationOfService": "Limpopo",
				"nameOfInstitution": "TFR",
				"tenderCategory": "Services",
				"tenderStatus": "Open",
				"rowKey": "114328",
				"tenderAccessType": "Open"
			}],
			"recordsFiltered": 1,
			"totalCount": 1
		}`))
	}))
	defer server.Close()

	adapter := NewTransnetAdapter("transnet", server.URL+"/Home/AdvertisedTenders")
	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 Transnet tender, got %d", len(items))
	}
	if !strings.Contains(msg, "loaded 1 Transnet advertised tenders") {
		t.Fatalf("unexpected message: %s", msg)
	}

	item := items[0]
	if item.ExternalID != "114328" || item.TenderNumber != "TFR/2026/04/0004/114328/RFP" {
		t.Fatalf("unexpected identity mapping: %#v", item)
	}
	if item.PublishedDate != "2026-04-05" || item.ClosingDate != "2026-05-28 10:00" {
		t.Fatalf("unexpected date mapping: %#v", item)
	}
	if item.OriginalURL != server.URL+"/Home/TenderDetails?Id=114328" {
		t.Fatalf("unexpected detail URL: %s", item.OriginalURL)
	}
	if item.DocumentURL != "https://publishedetenders.blob.core.windows.net/publishedetenderscontainer/114328" {
		t.Fatalf("unexpected document URL: %s", item.DocumentURL)
	}
	if len(item.Contacts) != 1 || item.Contacts[0].Email != "Thuli.Mathebula@transnet.net" {
		t.Fatalf("expected contact metadata, got %#v", item.Contacts)
	}
	if len(item.Briefings) != 1 || item.Briefings[0].DateTime != "2026-04-17 10:00" {
		t.Fatalf("expected briefing metadata, got %#v", item.Briefings)
	}
}

func TestTransnetAdapterFetchMapsSupplierPortalTenders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/portal/supplier_relationship_management/tenders" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Referer"); !strings.Contains(got, "/portal/advertisedTenders") {
			t.Fatalf("expected advertised tenders referer, got %q", got)
		}
		_, _ = w.Write([]byte(`{
			"draw": 1,
			"recordsTotal": 1,
			"recordsFiltered": 1,
			"data": [{
				"id": 2827,
				"status": {"id": 3, "name": "Published", "code": "3"},
				"tender_reference": "TE/2026/03/0732/2827/RFQ",
				"tender_title": "Service Effluent Plant",
				"tender_description": "Provide maintenance of the effluent treatment plant.",
				"open_date": "2026-04-13",
				"open_time": "15:29:05",
				"issue_date": "2026-03-13",
				"closing_date": "2026-05-05",
				"closing_time": "16:00:00",
				"is_there_a_briefing_session": true,
				"briefing_description": "Compulsory site meeting at Germiston.",
				"briefing_session_date": "2026-04-21",
				"briefing_session_time": "10:00:00",
				"location_of_service": "Germiston",
				"validity_end_date": "2027-01-12",
				"requirements_document": "/portal/documents/2827.pdf",
				"allow_manual_submission": false,
				"tactical": 12577,
				"stage": 1,
				"mechanism": 12,
				"tender_type": 3,
				"tender_category": 4,
				"contact_person": 5715,
				"second_contact_person": 5763,
				"operating_division": 3,
				"bid_validity_period": 4
			}]
		}`))
	}))
	defer server.Close()

	adapter := NewTransnetAdapter("transnet-esupplier", server.URL+"/portal/advertisedTenders")
	items, msg, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 Transnet eSupplier tender, got %d", len(items))
	}
	if !strings.Contains(msg, "loaded 1 Transnet eSupplier advertised tenders") {
		t.Fatalf("unexpected message: %s", msg)
	}

	item := items[0]
	if item.SourceKey != "transnet-esupplier" || item.ExternalID != "2827" || item.TenderNumber != "TE/2026/03/0732/2827/RFQ" {
		t.Fatalf("unexpected identity mapping: %#v", item)
	}
	if item.PublishedDate != "2026-03-13" || item.ClosingDate != "2026-05-05 16:00" {
		t.Fatalf("unexpected date mapping: %#v", item)
	}
	if item.Status != "open" || item.TenderType != "RFQ" {
		t.Fatalf("unexpected status/type mapping: %#v", item)
	}
	if item.OriginalURL != server.URL+"/portal/procurement/supplier_relationship_management/tenders/detailed/2827" {
		t.Fatalf("unexpected detail URL: %s", item.OriginalURL)
	}
	if item.DocumentURL != server.URL+"/portal/documents/2827.pdf" || item.DocumentStatus != models.ExtractionQueued {
		t.Fatalf("unexpected document metadata: %#v", item)
	}
	if len(item.Briefings) != 1 || item.Briefings[0].DateTime != "2026-04-21 10:00" {
		t.Fatalf("expected briefing metadata, got %#v", item.Briefings)
	}
	if item.SourceMetadata["contact_person_id"] != "5715" || item.PageFacts["location_of_service"] != "Germiston" {
		t.Fatalf("expected supplier portal metadata, got facts=%#v metadata=%#v", item.PageFacts, item.SourceMetadata)
	}
}

func TestAdapterFromConfigBuildsTransnetAdapter(t *testing.T) {
	t.Parallel()

	adapter, err := AdapterFromConfig(models.SourceConfig{
		Key:     "transnet",
		Type:    TypeTransnetPortal,
		FeedURL: "https://transnetetenders.azurewebsites.net/Home/AdvertisedTenders",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.(*TransnetAdapter); !ok {
		t.Fatalf("expected TransnetAdapter, got %T", adapter)
	}
}
