package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"tenderhub-za/internal/auth"
	"tenderhub-za/internal/models"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *App {
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func adminSession(t *testing.T, a *App) (models.User, models.Tenant, *http.Cookie, string) {
	users, _ := a.Store.ListUsers(t.Context())
	user := users[0]
	ms, _ := a.Store.ListMemberships(t.Context(), user.ID)
	tenant, _ := a.Store.GetTenant(t.Context(), ms[0].TenantID)
	s := models.Session{UserID: user.ID, TenantID: tenant.ID, CSRF: "csrf123", Expires: time.Now().Add(time.Hour)}
	raw, err := auth.EncodeSession(a.Config.SecretKey, s)
	if err != nil {
		t.Fatal(err)
	}
	return user, tenant, &http.Cookie{Name: "th_session", Value: raw}, s.CSRF
}
func TestRequireAuthRedirects(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	a.RequireAuth(a.Dashboard)(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
}
func TestBulkTendersRequiresCSRF(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/tenders/bulk", strings.NewReader(url.Values{"selected_ids": {"1"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, _, cookie, _ := adminSession(t, a)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.BulkTenders(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d", w.Code)
	}
}
func TestPasswordChangeFlow(t *testing.T) {
	a := newTestApp(t)
	user, _, cookie, csrf := adminSession(t, a)
	form := url.Values{"csrf_token": {csrf}, "current_password": {"TenderHub!2026"}, "new_password": {"Stronger!2026"}, "confirm_password": {"Stronger!2026"}}
	req := httptest.NewRequest(http.MethodPost, "/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.PasswordPage(w, req)
	updated, err := a.Store.GetUser(req.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.VerifyPassword("Stronger!2026", updated.PasswordSalt, updated.PasswordHash) {
		t.Fatal("password was not updated")
	}
}
func TestExportCSVIncludesWorkflowColumns(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{ID: "csv1", Title: "Electrical", Issuer: "City", SourceKey: "treasury", DocumentStatus: models.ExtractionCompleted, ExtractedFacts: map[string]string{"closing_details": "close soon"}})
	_ = a.Store.UpsertWorkflow(t.Context(), models.Workflow{TenantID: tenant.ID, TenderID: "csv1", Status: "reviewing", Priority: "high", AssignedUser: "alice"})
	req := httptest.NewRequest(http.MethodGet, "/tenders/export.csv", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.ExportCSV(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "workflow_status") || !strings.Contains(body, "close soon") || !strings.Contains(body, "reviewing") {
		t.Fatalf("csv missing enriched fields: %s", body)
	}
}
func TestSwitchTenantRejectsUnauthorizedTenant(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	req := httptest.NewRequest(http.MethodPost, "/tenant/switch", strings.NewReader(url.Values{"csrf_token": {csrf}, "tenant_id": {"missing"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.SwitchTenant(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d", w.Code)
	}
}
