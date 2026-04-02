package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/store"
	"testing"
)

func TestPaginationOnTendersPage(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	for i := 0; i < 25; i++ {
		_ = a.Store.UpsertTender(context.Background(), models.Tender{
			ID:    strings.Join([]string{"pg", string(rune('A' + (i % 26))), string(rune('a' + (i / 26)))}, "-"),
			Title: "Tender", Issuer: "Issuer", SourceKey: "treasury", Status: "open",
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/tenders?page=2&page_size=10&sort=published_date", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Tenders(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Page 2 of") && !strings.Contains(body, "page 2 of") {
		t.Fatalf("expected pagination markers in response")
	}
}

func TestTenderDetailAndAuditLogPages(t *testing.T) {
	a := newTestApp(t)
	user, tenant, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(context.Background(), models.Tender{
		ID:           "detail1",
		Title:        "Detail Tender",
		Issuer:       "City",
		SourceKey:    "treasury",
		TenderNumber: "TH-001",
		TenderType:   "Request for Bid",
		Documents:    []models.TenderDocument{{URL: "https://example.org/doc.pdf", FileName: "doc.pdf", Role: "notice", Source: "listing"}},
		Contacts:     []models.TenderContact{{Role: "listing_contact", Name: "Jane Doe", Email: "jane@example.com"}},
		Briefings:    []models.TenderBriefing{{Label: "site_briefing", DateTime: "2026-04-10 10:00", Required: true}},
		Requirements: []models.TenderRequirement{{Category: "registration", Description: "Active CSD registration", Required: true}},
		PageFacts:    map[string]string{"briefing": "yes"},
		DocumentFacts: map[string]string{
			"cidb_hints": "CIDB 3GB",
		},
		ExtractedFacts: map[string]string{"briefing": "yes", "cidb_hints": "CIDB 3GB"},
	})
	_ = a.Store.UpsertWorkflow(context.Background(), models.Workflow{TenantID: tenant.ID, TenderID: "detail1", Status: "reviewing"})
	_ = a.Store.AddWorkflowEvent(context.Background(), models.WorkflowEvent{TenantID: tenant.ID, TenderID: "detail1", ChangedBy: user.ID, Status: "reviewing"})
	_ = a.Store.AddAuditEntry(context.Background(), models.AuditEntry{TenantID: tenant.ID, UserID: user.ID, Action: "update", Entity: "workflow", EntityID: "detail1", Summary: "Workflow updated"})
	req := httptest.NewRequest(http.MethodGet, "/tenders/detail1", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.TenderDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Structured tender data") || !strings.Contains(body, "Page-derived facts") || !strings.Contains(body, "Document-derived facts") || !strings.Contains(body, "Jane Doe") {
		t.Fatalf("expected structured tender details in response: %s", body)
	}
	req = httptest.NewRequest(http.MethodGet, "/audit-log", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.AuditLogPage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
}

func TestAuditAndWorkflowHistoryStoreMethods(t *testing.T) {
	s, err := store.NewSQLiteStore(t.TempDir() + "/store.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.AddAuditEntry(ctx, models.AuditEntry{TenantID: "t1", UserID: "u1", Action: "create", Entity: "saved_search", Summary: "Created"})
	_ = s.AddWorkflowEvent(ctx, models.WorkflowEvent{TenantID: "t1", TenderID: "x1", ChangedBy: "u1", Status: "reviewing"})
	ae, err := s.ListAuditEntries(ctx, "t1")
	if err != nil || len(ae) != 1 {
		t.Fatalf("audit entries missing: %v %d", err, len(ae))
	}
	we, err := s.ListWorkflowEvents(ctx, "t1", "x1")
	if err != nil || len(we) != 1 {
		t.Fatalf("workflow events missing: %v %d", err, len(we))
	}
}
