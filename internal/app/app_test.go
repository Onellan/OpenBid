package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"tenderhub-za/internal/auth"
	"tenderhub-za/internal/models"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *App {
	t.Setenv("DATA_PATH", filepath.Join(t.TempDir(), "store.db"))
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := a.Store.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
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
func sessionForRole(t *testing.T, a *App, role models.Role) (models.User, models.Tenant, *http.Cookie, string) {
	_, tenant, _, _ := adminSession(t, a)
	if role == models.RoleAdmin {
		return adminSession(t, a)
	}
	user := models.User{
		Username:    "user-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		DisplayName: "Viewer User",
		Email:       "viewer@example.com",
		IsActive:    true,
	}
	if err := a.Store.UpsertUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	users, _ := a.Store.ListUsers(t.Context())
	user = users[len(users)-1]
	if err := a.Store.UpsertMembership(t.Context(), models.Membership{UserID: user.ID, TenantID: tenant.ID, Role: role, Responsibilities: "Read-only access"}); err != nil {
		t.Fatal(err)
	}
	s := models.Session{UserID: user.ID, TenantID: tenant.ID, CSRF: "csrf-role", Expires: time.Now().Add(time.Hour)}
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
func TestAdminCreateSourceStoresConfig(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"Municipal Feed"},
		"feed_url":   {"https://example.org/municipal.json"},
	}
	req := httptest.NewRequest(http.MethodPost, "/sources/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	configs, err := a.Store.ListSourceConfigs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cfg := range configs {
		if cfg.Key == "municipal-feed" && cfg.FeedURL == "https://example.org/municipal.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stored source config, got %#v", configs)
	}
}

func TestAdminCreateSourceRejectsUnsupportedType(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"XML Feed"},
		"feed_url":   {"https://example.org/feed.xml"},
		"type":       {"xml"},
	}
	req := httptest.NewRequest(http.MethodPost, "/sources/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", w.Code)
	}
}

func TestViewerCannotCreateSource(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := sessionForRole(t, a, models.RoleViewer)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"Viewer Feed"},
		"feed_url":   {"https://example.org/viewer.json"},
	}
	req := httptest.NewRequest(http.MethodPost, "/sources/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 got %d", w.Code)
	}
}

func TestAdminSourcesPageRendersSourceManagementContent(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/sources", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Source configuration and sync health") || !strings.Contains(body, "Add source") {
		t.Fatalf("sources page missing expected content: %s", body)
	}
}
func TestLoginPageRendersSignInContent(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Welcome back") || !strings.Contains(body, "Sign in to OpenBid") {
		t.Fatalf("login page missing sign-in content: %s", body)
	}
	if strings.Contains(body, "Filters and search") {
		t.Fatalf("login page rendered tenders content")
	}
}
func TestHomePageRendersHomepageContent(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "A lighter front door for daily bidding work") || !strings.Contains(body, "Bookmarks") {
		t.Fatalf("home page missing expected content: %s", body)
	}
}

func TestTendersPageRendersTendersContent(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/tenders", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Filters and search") || !strings.Contains(body, "Bulk actions") {
		t.Fatalf("tenders page missing expected workspace content: %s", body)
	}
	if strings.Contains(body, "Welcome back") {
		t.Fatalf("tenders page rendered login content")
	}
}
func TestRoleBasedNavigationVisibility(t *testing.T) {
	a := newTestApp(t)
	_, _, adminCookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	adminBody := w.Body.String()
	if !strings.Contains(adminBody, "User Admin") || !strings.Contains(adminBody, "Tenant Admin") {
		t.Fatalf("admin navigation missing admin links: %s", adminBody)
	}

	_, _, viewerCookie, _ := sessionForRole(t, a, models.RoleViewer)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(viewerCookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	viewerBody := w.Body.String()
	if strings.Contains(viewerBody, "User Admin") || strings.Contains(viewerBody, "Tenant Admin") || strings.Contains(viewerBody, "Audit Log") {
		t.Fatalf("viewer navigation exposed admin links: %s", viewerBody)
	}
	if !strings.Contains(viewerBody, "Settings") || !strings.Contains(viewerBody, "Bookmarks") {
		t.Fatalf("viewer navigation missing core links: %s", viewerBody)
	}
}
func TestRouteAccessibilityAndPageRendering(t *testing.T) {
	a := newTestApp(t)
	_, _, viewerCookie, _ := sessionForRole(t, a, models.RoleViewer)
	routes := map[string]string{
		"/dashboard":      "Operational metrics for the current workspace",
		"/bookmarks":      "Keep active opportunities separate",
		"/saved-searches": "Reusable market views",
		"/queue":          "Queue and extraction monitoring",
		"/sources":        "Source configuration and sync health",
		"/settings":       "Account and security settings",
	}
	for path, marker := range routes {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(viewerCookie)
		w := httptest.NewRecorder()
		a.Server.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s got %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), marker) {
			t.Fatalf("expected marker %q in %s", marker, path)
		}
	}

	for _, path := range []string{"/admin/users", "/admin/tenants"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(viewerCookie)
		w := httptest.NewRecorder()
		a.Server.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for %s got %d", path, w.Code)
		}
	}
}
