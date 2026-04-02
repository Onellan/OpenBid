package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"tenderhub-za/internal/extract"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
	"tenderhub-za/internal/store"
	"testing"
	"time"
)

func TestProcessJobsRetry(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.UpsertTender(context.Background(), models.Tender{ID: "1", Title: "Civil", DocumentURL: "http://127.0.0.1:1/fail", DocumentStatus: models.ExtractionQueued})
	_ = s.QueueJob(context.Background(), models.ExtractionJob{ID: "j1", TenderID: "1", DocumentURL: "http://127.0.0.1:1/fail", State: models.ExtractionQueued})
	r := Runner{Store: s, Sources: source.NewRegistry(), Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Millisecond}
	r.processJobs(context.Background())
	jobs, _ := s.ListJobs(context.Background())
	if len(jobs) == 0 || (jobs[0].State != models.ExtractionRetry && jobs[0].State != models.ExtractionFailed) {
		t.Fatal("expected retry or failed state")
	}
}
func TestSyncAllUsesDynamicSourceLoader(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Runner{
		Store: s,
		SourceLoad: func(context.Context) (source.Registry, error) {
			return source.RegistryFromConfigs([]models.SourceConfig{{
				Key:     "municipal-feed",
				Name:    "Municipal Feed",
				Type:    source.TypeJSONFeed,
				Enabled: true,
			}}), nil
		},
		Extractor: extract.New("http://127.0.0.1:1"),
	}
	r.syncAll(context.Background())
	health, err := s.ListSourceHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range health {
		if item.SourceKey == "municipal-feed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dynamic source health entry, got %#v", health)
	}
}

func TestProcessJobsMergesPageAndDocumentFacts(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"excerpt": "Document summary",
			"facts": map[string]string{
				"document_title":              "Supply, installation and maintenance of network cabling",
				"closing_date":                "2026-05-05",
				"issued_date":                 "2026-03-31",
				"validity_days":               "90",
				"cidb_hints":                  "CIDB 3GB",
				"cidb_grade":                  "3GB",
				"submission_details":          "sealed envelope at Komani office",
				"submission_address":          "1267 Gordon Hood Road, Centurion, Pretoria, South Africa",
				"contact_name":                "Ulizwi Mngoma",
				"contact_email":               "ulizwim@cidb.org.za",
				"contact_phone":               "012 482 7252",
				"briefing_required":           "yes",
				"briefing_date":               "2026-04-08",
				"briefing_time":               "10:00",
				"briefing_venue":              "191 Madiba Street, Pretoria",
				"location_city":               "Centurion",
				"location_province":           "Gauteng",
				"evaluation_method":           "80/20",
				"price_points":                "80",
				"preference_points":           "20",
				"minimum_functionality_score": "60",
			},
			"type": "pdf",
		})
	}))
	defer server.Close()

	tender := models.Tender{
		ID:             "1",
		Title:          "DPWI tender PT25/020",
		DocumentURL:    "https://example.org/doc.pdf",
		DocumentStatus: models.ExtractionQueued,
		PageFacts: map[string]string{
			"contact_details":  "person=Jane Doe",
			"closing_details":  "2026-04-17 11:00",
			"submission_notes": "listing note",
		},
		ExtractedFacts: map[string]string{
			"legacy_fact": "keep me",
		},
	}
	_ = s.UpsertTender(context.Background(), tender)
	_ = s.QueueJob(context.Background(), models.ExtractionJob{ID: "j1", TenderID: "1", DocumentURL: "https://example.org/doc.pdf", State: models.ExtractionQueued})

	r := Runner{Store: s, Sources: source.NewRegistry(), Extractor: extract.New(server.URL), SyncEvery: time.Hour, LoopEvery: time.Millisecond}
	r.processJobs(context.Background())

	updated, err := s.GetTender(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.DocumentStatus != models.ExtractionCompleted {
		t.Fatalf("expected completed document status, got %s", updated.DocumentStatus)
	}
	if updated.PageFacts["contact_details"] == "" || updated.DocumentFacts["cidb_hints"] == "" {
		t.Fatalf("expected separated fact maps, got page=%#v doc=%#v", updated.PageFacts, updated.DocumentFacts)
	}
	if updated.ExtractedFacts["contact_details"] == "" || updated.ExtractedFacts["cidb_hints"] == "" || updated.ExtractedFacts["legacy_fact"] == "" {
		t.Fatalf("expected merged extracted facts, got %#v", updated.ExtractedFacts)
	}
	if updated.Title != "Supply, installation and maintenance of network cabling" {
		t.Fatalf("expected promoted title, got %#v", updated.Title)
	}
	if updated.PublishedDate != "2026-03-31" {
		t.Fatalf("expected issued date promotion, got %#v", updated.PublishedDate)
	}
	if updated.ClosingDate != "2026-05-05" {
		t.Fatalf("expected closing date promotion, got %#v", updated.ClosingDate)
	}
	if updated.ValidityDays != 90 || updated.CIDBGrading != "3GB" {
		t.Fatalf("expected validity/CIDB promotions, got days=%d cidb=%q", updated.ValidityDays, updated.CIDBGrading)
	}
	if updated.Submission.Address != "1267 Gordon Hood Road, Centurion, Pretoria, South Africa" || !updated.Submission.PhysicalAllowed {
		t.Fatalf("expected submission promotion, got %#v", updated.Submission)
	}
	if updated.Location.Town != "Centurion" || updated.Location.Province != "Gauteng" || updated.Province != "Gauteng" {
		t.Fatalf("expected location promotion, got location=%#v province=%q", updated.Location, updated.Province)
	}
	if updated.Evaluation.Method != "80/20" || updated.Evaluation.PricePoints != 80 || updated.Evaluation.PreferencePoints != 20 || updated.Evaluation.MinimumFunctionalityScore != 60 {
		t.Fatalf("expected evaluation promotion, got %#v", updated.Evaluation)
	}
	if len(updated.Contacts) != 1 || updated.Contacts[0].Email != "ulizwim@cidb.org.za" {
		t.Fatalf("expected promoted contact, got %#v", updated.Contacts)
	}
	if len(updated.Briefings) != 1 || updated.Briefings[0].DateTime != "2026-04-08 10:00" || updated.Briefings[0].Venue != "191 Madiba Street, Pretoria" || !updated.Briefings[0].Required {
		t.Fatalf("expected promoted briefing, got %#v", updated.Briefings)
	}
}
