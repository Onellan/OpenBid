package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"tenderhub-za/internal/models"
	"testing"
)

func seedTestTender(t *testing.T, a *App, tender models.Tender) models.Tender {
	t.Helper()
	if err := a.Store.UpsertTender(t.Context(), tender); err != nil {
		t.Fatal(err)
	}
	stored, err := a.Store.GetTender(t.Context(), tender.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestUpdateWorkflowStoresAssignmentPriorityAndNotes(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, csrf := adminSession(t, a)
	seedTestTender(t, a, models.Tender{ID: "wf-1", Title: "Workflow tender"})

	form := url.Values{
		"csrf_token":    {csrf},
		"tender_id":     {"wf-1"},
		"status":        {"reviewing"},
		"priority":      {"high"},
		"assigned_user": {"owner@example.com"},
		"notes":         {"Need pricing review"},
	}
	req := httptest.NewRequest(http.MethodPost, "/tenders/workflow", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()

	a.UpdateWorkflow(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	workflow, err := a.Store.GetWorkflow(t.Context(), tenant.ID, "wf-1")
	if err != nil {
		t.Fatal(err)
	}
	if workflow.Status != "reviewing" || workflow.Priority != "high" || workflow.AssignedUser != "owner@example.com" || workflow.Notes != "Need pricing review" {
		t.Fatalf("unexpected workflow state: %+v", workflow)
	}
	history, err := a.Store.ListWorkflowEvents(t.Context(), tenant.ID, "wf-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 {
		t.Fatal("expected workflow history to be recorded")
	}
}

func TestQueueExtractionQueuesPendingJobAndUpdatesTenderStatus(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	seedTestTender(t, a, models.Tender{
		ID:          "queue-1",
		Title:       "Queued tender",
		DocumentURL: "https://example.com/tender.pdf",
		OriginalURL: "https://example.com/opportunity",
	})

	form := url.Values{"csrf_token": {csrf}, "tender_id": {"queue-1"}}
	req := httptest.NewRequest(http.MethodPost, "/tenders/queue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()

	a.QueueExtraction(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	tender, err := a.Store.GetTender(t.Context(), "queue-1")
	if err != nil {
		t.Fatal(err)
	}
	if tender.DocumentStatus != models.ExtractionQueued {
		t.Fatalf("expected queued document status, got %s", tender.DocumentStatus)
	}
	jobs, err := a.Store.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range jobs {
		if job.TenderID == "queue-1" && job.State == models.ExtractionQueued && job.DocumentURL == "https://example.com/tender.pdf" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected queue job for tender queue-1, got %+v", jobs)
	}
}

func TestQueueExtractionRejectsTenderWithoutDocumentURL(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	seedTestTender(t, a, models.Tender{ID: "queue-2", Title: "No document"})

	form := url.Values{"csrf_token": {csrf}, "tender_id": {"queue-2"}}
	req := httptest.NewRequest(http.MethodPost, "/tenders/queue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()

	a.QueueExtraction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", w.Code)
	}
}

func TestBulkTendersAppliesWorkflowToUniqueSelection(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, csrf := adminSession(t, a)
	seedTestTender(t, a, models.Tender{ID: "bulk-1", Title: "Bulk 1"})
	seedTestTender(t, a, models.Tender{ID: "bulk-2", Title: "Bulk 2"})

	form := url.Values{
		"csrf_token":    {csrf},
		"selected_ids":  {"bulk-1, bulk-1, bulk-2"},
		"status":        {"qualified"},
		"priority":      {"medium"},
		"assigned_user": {"analyst@example.com"},
		"notes":         {"Bulk update"},
	}
	req := httptest.NewRequest(http.MethodPost, "/tenders/bulk", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()

	a.BulkTenders(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	for _, tenderID := range []string{"bulk-1", "bulk-2"} {
		workflow, err := a.Store.GetWorkflow(t.Context(), tenant.ID, tenderID)
		if err != nil {
			t.Fatal(err)
		}
		if workflow.Status != "qualified" || workflow.Priority != "medium" || workflow.AssignedUser != "analyst@example.com" || workflow.Notes != "Bulk update" {
			t.Fatalf("unexpected workflow for %s: %+v", tenderID, workflow)
		}
		history, err := a.Store.ListWorkflowEvents(t.Context(), tenant.ID, tenderID)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 {
			t.Fatalf("expected one workflow history entry for %s, got %d", tenderID, len(history))
		}
	}
}

func TestBulkTendersBookmarkAndQueueActions(t *testing.T) {
	a := newTestApp(t)
	user, tenant, cookie, csrf := adminSession(t, a)
	seedTestTender(t, a, models.Tender{ID: "bulk-bookmark", Title: "Bookmarked"})
	seedTestTender(t, a, models.Tender{
		ID:          "bulk-queue",
		Title:       "Queued in bulk",
		DocumentURL: "https://example.com/bulk.pdf",
	})

	bookmarkForm := url.Values{
		"csrf_token":   {csrf},
		"selected_ids": {"bulk-bookmark"},
		"action":       {"bookmark"},
		"notes":        {"Worth saving"},
	}
	bookmarkReq := httptest.NewRequest(http.MethodPost, "/tenders/bulk", strings.NewReader(bookmarkForm.Encode()))
	bookmarkReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bookmarkReq.AddCookie(cookie)
	bookmarkW := httptest.NewRecorder()
	a.BulkTenders(bookmarkW, bookmarkReq)
	if bookmarkW.Code != http.StatusSeeOther {
		t.Fatalf("expected bookmark redirect, got %d", bookmarkW.Code)
	}
	bookmarks, err := a.Store.ListBookmarks(t.Context(), tenant.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bookmarks) != 1 || bookmarks[0].TenderID != "bulk-bookmark" || bookmarks[0].Note != "Worth saving" {
		t.Fatalf("unexpected bookmarks: %+v", bookmarks)
	}

	queueForm := url.Values{
		"csrf_token":   {csrf},
		"selected_ids": {"bulk-queue"},
		"action":       {"queue"},
	}
	queueReq := httptest.NewRequest(http.MethodPost, "/tenders/bulk", strings.NewReader(queueForm.Encode()))
	queueReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	queueReq.AddCookie(cookie)
	queueW := httptest.NewRecorder()
	a.BulkTenders(queueW, queueReq)
	if queueW.Code != http.StatusSeeOther {
		t.Fatalf("expected queue redirect, got %d", queueW.Code)
	}
	jobs, err := a.Store.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range jobs {
		if job.TenderID == "bulk-queue" && job.DocumentURL == "https://example.com/bulk.pdf" && job.State == models.ExtractionQueued {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected bulk queue job for tender bulk-queue, got %+v", jobs)
	}
}

func TestResetWorkflowAndRemoveBookmarkClearStoredState(t *testing.T) {
	a := newTestApp(t)
	user, tenant, cookie, csrf := adminSession(t, a)
	seedTestTender(t, a, models.Tender{ID: "clear-1", Title: "Reset me"})
	if err := a.Store.UpsertWorkflow(t.Context(), models.Workflow{
		TenantID: tenant.ID, TenderID: "clear-1", Status: "reviewing", Priority: "high", AssignedUser: "owner", Notes: "Initial note",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertBookmark(t.Context(), models.Bookmark{
		TenantID: tenant.ID, UserID: user.ID, TenderID: "clear-1", Note: "Saved note",
	}); err != nil {
		t.Fatal(err)
	}

	resetForm := url.Values{"csrf_token": {csrf}, "tender_id": {"clear-1"}}
	resetReq := httptest.NewRequest(http.MethodPost, "/tenders/workflow/reset", strings.NewReader(resetForm.Encode()))
	resetReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resetReq.AddCookie(cookie)
	resetW := httptest.NewRecorder()
	a.ResetWorkflow(resetW, resetReq)
	if resetW.Code != http.StatusSeeOther {
		t.Fatalf("expected reset redirect, got %d", resetW.Code)
	}
	workflow, err := a.Store.GetWorkflow(t.Context(), tenant.ID, "clear-1")
	if err != nil {
		t.Fatal(err)
	}
	if workflow.Status != "" || workflow.Priority != "" || workflow.AssignedUser != "" || workflow.Notes != "" {
		t.Fatalf("expected cleared workflow, got %+v", workflow)
	}

	removeReq := httptest.NewRequest(http.MethodPost, "/tenders/bookmark/remove", strings.NewReader(resetForm.Encode()))
	removeReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	removeReq.AddCookie(cookie)
	removeW := httptest.NewRecorder()
	a.RemoveBookmark(removeW, removeReq)
	if removeW.Code != http.StatusSeeOther {
		t.Fatalf("expected bookmark removal redirect, got %d", removeW.Code)
	}
	bookmarks, err := a.Store.ListBookmarks(t.Context(), tenant.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bookmarks) != 0 {
		t.Fatalf("expected bookmark to be removed, got %+v", bookmarks)
	}
}
