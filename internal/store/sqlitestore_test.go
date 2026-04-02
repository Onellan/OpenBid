package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"tenderhub-za/internal/models"
	"testing"
)

func TestSQLiteStoreBasicFlow(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.UpsertTenant(ctx, models.Tenant{ID: "t1", Name: "Tenant One"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, models.User{ID: "u1", Username: "alice", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, models.Membership{UserID: "u1", TenantID: "t1", Role: models.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "x1", Title: "Civil works", Issuer: "Metro", SourceKey: "treasury", Status: "open", DocumentURL: "https://example.org/a.pdf"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWorkflow(ctx, models.Workflow{TenantID: "t1", TenderID: "x1", Status: "reviewing", Priority: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{TenderID: "x1", DocumentURL: "https://example.org/a.pdf", State: models.ExtractionQueued}); err != nil {
		t.Fatal(err)
	}

	items, total, err := s.ListTenders(ctx, ListFilter{Query: "civil", WorkflowStatus: "reviewing", TenantID: "t1", UserID: "u1", Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("unexpected tender list result: err=%v total=%d len=%d", err, total, len(items))
	}
}

func TestSQLiteMigrationAndRuntimeValidation(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ValidateRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteQueueWritesDeduplicate(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	job := models.ExtractionJob{TenderID: "t1", DocumentURL: "https://example.org/doc.pdf", State: models.ExtractionQueued}
	if err := s.QueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 queued job, got %d", len(jobs))
	}
}

func TestSQLiteConcurrentWrites(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("tender-%d", n)
			_ = s.UpsertTender(ctx, models.Tender{ID: id, Title: "Tender", Issuer: "Issuer", SourceKey: "treasury", Status: "open"})
		}(i)
	}
	wg.Wait()
	items, total, err := s.ListTenders(ctx, ListFilter{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != len(items) || total < 15 {
		t.Fatalf("expected >=15 tenders, got total=%d len=%d", total, len(items))
	}
}

func TestSQLiteTenderRoundTripsStructuredFields(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	original := models.Tender{
		ID:           "rich-1",
		Title:        "Structured Tender",
		Issuer:       "Example Works",
		SourceKey:    "etenders",
		Status:       "open",
		TenderType:   "Request for Bid(Open-Tender)",
		ValidityDays: 90,
		PageFacts: map[string]string{
			"closing_details": "2026-05-11 11:00",
		},
		DocumentFacts: map[string]string{
			"cidb_hints": "CIDB 3GB",
		},
		Location: models.TenderLocation{
			Town:     "Komani",
			Province: "Eastern Cape",
		},
		Submission: models.TenderSubmission{
			Method:            "physical",
			ElectronicAllowed: false,
			PhysicalAllowed:   true,
		},
		Evaluation: models.TenderEvaluation{
			Method:                    "80/20",
			PricePoints:               80,
			PreferencePoints:          20,
			MinimumFunctionalityScore: 60,
		},
		Contacts: []models.TenderContact{{
			Role:      "listing_contact",
			Name:      "Ms K Mbuqwa",
			Email:     "khutala.mbuqwa@ecagriculture.gov.za",
			Telephone: "083-262-2633",
		}},
		Briefings: []models.TenderBriefing{{
			Label:    "site_inspection",
			DateTime: "2026-04-16 10:00",
			Required: true,
		}},
		Documents: []models.TenderDocument{{
			URL:      "https://example.org/doc.pdf",
			FileName: "doc.pdf",
			Role:     "notice",
			Source:   "listing",
		}},
		Requirements: []models.TenderRequirement{{
			Category:    "registration",
			Description: "Active CSD registration",
			Required:    true,
		}},
	}
	if err := s.UpsertTender(ctx, original); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetTender(ctx, "rich-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Evaluation.Method != "80/20" || stored.Location.Town != "Komani" {
		t.Fatalf("expected structured fields to round-trip, got %#v", stored)
	}
	if len(stored.Documents) != 1 || stored.Documents[0].FileName != "doc.pdf" {
		t.Fatalf("expected documents to round-trip, got %#v", stored.Documents)
	}
	if stored.DocumentFacts["cidb_hints"] != "CIDB 3GB" || stored.PageFacts["closing_details"] == "" {
		t.Fatalf("expected fact maps to round-trip, got page=%#v doc=%#v", stored.PageFacts, stored.DocumentFacts)
	}
}
