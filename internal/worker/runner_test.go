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
				"cidb_hints":         "CIDB 3GB",
				"submission_details": "sealed envelope at Komani office",
			},
			"type": "pdf",
		})
	}))
	defer server.Close()

	tender := models.Tender{
		ID:             "1",
		Title:          "Civil",
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
}
