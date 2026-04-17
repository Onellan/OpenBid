package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"openbid/internal/auth"
	"openbid/internal/models"
	"openbid/internal/source"
	"openbid/internal/tenderstate"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *App {
	root := t.TempDir()
	t.Setenv("DATA_PATH", filepath.Join(root, "store.db"))
	t.Setenv("BOOTSTRAP_SYNC_ON_STARTUP", "false")
	t.Setenv("EXTRACTOR_URL", "")
	backupDir := filepath.Join(root, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "store-bootstrap.db"), []byte("test-backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BACKUP_DIR", backupDir)
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	a.Config.ExtractorURL = ""
	a.Extractor = nil
	if closer, ok := a.Store.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	return a
}

type staticSourceAdapter struct {
	key   string
	items []models.Tender
}

func (a staticSourceAdapter) Key() string { return a.key }

func (a staticSourceAdapter) Fetch(context.Context) ([]models.Tender, string, error) {
	return a.items, "static test source", nil
}

func persistSessionCookie(t *testing.T, a *App, session models.Session) *http.Cookie {
	t.Helper()
	if session.ID == "" {
		session.ID = "sess-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	if session.CSRF == "" {
		session.CSRF = "csrf-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	if session.Expires.IsZero() {
		session.Expires = time.Now().Add(time.Hour)
	}
	if err := a.Store.UpsertSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	raw, err := auth.EncodeSession(a.Config.SecretKey, session)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "th_session", Value: raw}
}

func adminSession(t *testing.T, a *App) (models.User, models.Tenant, *http.Cookie, string) {
	users, _ := a.Store.ListUsers(t.Context())
	user := users[0]
	user.MFAEnabled = true
	user.MFASecret = "test-admin-secret"
	if err := a.persistUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	ms, _ := a.Store.ListMemberships(t.Context(), user.ID)
	tenant, _ := a.Store.GetTenant(t.Context(), ms[0].TenantID)
	s := models.Session{ID: "sess-admin-" + strconv.FormatInt(time.Now().UnixNano(), 10), UserID: user.ID, TenantID: tenant.ID, CSRF: "csrf123", SessionVersion: user.SessionVersion, Expires: time.Now().Add(time.Hour)}
	return user, tenant, persistSessionCookie(t, a, s), s.CSRF
}
func sessionForRole(t *testing.T, a *App, role models.TenantRole) (models.User, models.Tenant, *http.Cookie, string) {
	_, tenant, _, _ := adminSession(t, a)
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	user := models.User{
		Username:    "user-" + nonce,
		DisplayName: "Tenant User",
		Email:       "user-" + nonce + "@example.com",
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
	s := models.Session{ID: "sess-role-" + strconv.FormatInt(time.Now().UnixNano(), 10), UserID: user.ID, TenantID: tenant.ID, CSRF: "csrf-role", SessionVersion: user.SessionVersion, Expires: time.Now().Add(time.Hour)}
	return user, tenant, persistSessionCookie(t, a, s), s.CSRF
}

func sessionForPlatformRole(t *testing.T, a *App, role models.PlatformRole) (models.User, models.Tenant, *http.Cookie, string) {
	_, tenant, _, _ := adminSession(t, a)
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	user := models.User{
		Username:     "platform-" + nonce,
		DisplayName:  "Platform User",
		Email:        "platform-" + nonce + "@example.com",
		PlatformRole: role,
		IsActive:     true,
	}
	if err := a.Store.UpsertUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	users, _ := a.Store.ListUsers(t.Context())
	user = users[len(users)-1]
	if err := a.Store.UpsertMembership(t.Context(), models.Membership{UserID: user.ID, TenantID: tenant.ID, Role: models.TenantRoleOwner, Responsibilities: "Platform access"}); err != nil {
		t.Fatal(err)
	}
	s := models.Session{ID: "sess-platform-" + strconv.FormatInt(time.Now().UnixNano(), 10), UserID: user.ID, TenantID: tenant.ID, CSRF: "csrf-platform", SessionVersion: user.SessionVersion, Expires: time.Now().Add(time.Hour)}
	return user, tenant, persistSessionCookie(t, a, s), s.CSRF
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
	form := url.Values{"csrf_token": {csrf}, "current_password": {"OpenBid!2026"}, "new_password": {"Stronger!2026"}, "confirm_password": {"Stronger!2026"}}
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
	user, tenant, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "csv1",
		Title:          "Electrical",
		Issuer:         "City",
		SourceKey:      "treasury",
		TenderType:     "Request for Bid",
		ValidityDays:   90,
		DocumentStatus: models.ExtractionCompleted,
		ExtractedFacts: map[string]string{"closing_details": "close soon"},
		PageFacts:      map[string]string{"briefing_details": "listing briefing"},
		DocumentFacts:  map[string]string{"cidb_hints": "CIDB 6EP"},
		Documents:      []models.TenderDocument{{URL: "https://example.org/doc.pdf", FileName: "doc.pdf", Role: "notice"}},
		Contacts:       []models.TenderContact{{Name: "Jane Doe", Role: "listing_contact"}},
	})
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "csv2",
		Title:          "Filtered Out",
		Issuer:         "Town",
		SourceKey:      "treasury",
		DocumentStatus: models.ExtractionQueued,
	})
	_ = a.Store.UpsertWorkflow(t.Context(), models.Workflow{TenantID: tenant.ID, TenderID: "csv1", Status: "reviewing", Priority: "high", AssignedUser: "alice"})
	_ = a.Store.UpsertBookmark(t.Context(), models.Bookmark{TenantID: tenant.ID, UserID: user.ID, TenderID: "csv1"})
	req := httptest.NewRequest(http.MethodGet, "/tenders/export.csv?workflow_status=reviewing&bookmarked_only=1&document_status=completed", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.ExportCSV(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "workflow_status") || !strings.Contains(body, "tender_type") || !strings.Contains(body, "documents_json") || !strings.Contains(body, "close soon") || !strings.Contains(body, "reviewing") || !strings.Contains(body, "CIDB 6EP") {
		t.Fatalf("csv missing enriched fields: %s", body)
	}
	if strings.Contains(body, "Filtered Out") {
		t.Fatalf("csv should respect export filters, got %s", body)
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

func TestSwitchTenantRejectsExternalReturnTo(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"tenant_id":  {tenant.ID},
		"return_to":  {"//evil.example/phish"},
	}
	req := httptest.NewRequest(http.MethodPost, "/tenant/switch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.SwitchTenant(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if location := w.Header().Get("Location"); location != "/" {
		t.Fatalf("expected safe fallback redirect, got %q", location)
	}
}

func TestAdminTenantsPageRendersTenantManagementContent(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/admin/tenants", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Switch workspace") || !strings.Contains(body, "Create tenant") || !strings.Contains(body, "action=\"/tenant/switch\"") || !strings.Contains(body, "action=\"/admin/tenants/create\"") {
		t.Fatalf("tenant admin page missing management controls: %s", body)
	}
}

func TestRedirectAfterActionRejectsExternalReturnTo(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/bookmark", strings.NewReader(url.Values{
		"return_to": {"https://evil.example/steal"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.redirectAfterAction(w, req, "/tenders", "success", "Bookmark saved")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	location := w.Header().Get("Location")
	if location != "/tenders?message=Bookmark+saved" {
		t.Fatalf("expected safe fallback redirect, got %q", location)
	}
}

func TestRedirectAfterActionPreservesSafeReturnToQuery(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/bookmark", strings.NewReader(url.Values{
		"return_to": {"/tenders?q=metro&page=2"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.redirectAfterAction(w, req, "/tenders", "success", "Bookmark saved")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	location := w.Header().Get("Location")
	if location != "/tenders?message=Bookmark+saved&page=2&q=metro" {
		t.Fatalf("expected redirect with preserved query, got %q", location)
	}
}

func TestRedirectAfterActionPreservesFallbackFragment(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/data-pipes/remove-expired-tenders", strings.NewReader(url.Values{
		"return_to": {"https://evil.example/steal"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.redirectAfterAction(w, req, "/queue#expired-tender-cleanup", "success", "No expired tenders to remove")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	location := w.Header().Get("Location")
	if location != "/queue?message=No+expired+tenders+to+remove#expired-tender-cleanup" {
		t.Fatalf("expected fallback fragment to remain a fragment, got %q", location)
	}
}

func TestStartupSourceSyncSkipsExpiredTenderExtraction(t *testing.T) {
	a := newTestApp(t)
	if err := a.Store.UpsertSourceConfig(t.Context(), models.SourceConfig{
		Key:                 "startup-expiry",
		Name:                "Startup Expiry",
		Type:                source.TypeJSONFeed,
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    true,
	}); err != nil {
		t.Fatal(err)
	}
	registry := source.NewRegistry(staticSourceAdapter{
		key: "startup-expiry",
		items: []models.Tender{
			{
				ID:          "startup-expired",
				SourceKey:   "startup-expiry",
				Title:       "Startup expired tender",
				ClosingDate: time.Now().Add(-24 * time.Hour).Format("2006-01-02 15:04"),
				DocumentURL: "https://example.org/startup-expired.pdf",
			},
			{
				ID:          "startup-active",
				SourceKey:   "startup-expiry",
				Title:       "Startup active tender",
				ClosingDate: time.Now().Add(24 * time.Hour).Format("2006-01-02 15:04"),
				DocumentURL: "https://example.org/startup-active.pdf",
			},
		},
	})
	if err := a.syncSources(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	expired, err := a.Store.GetTender(t.Context(), "startup-expired")
	if err != nil {
		t.Fatal(err)
	}
	if expired.DocumentStatus != models.ExtractionSkipped || expired.ExtractionSkippedReason != tenderstate.ExpiredSkipReason {
		t.Fatalf("expected startup expired tender skipped, got %#v", expired)
	}
	active, err := a.Store.GetTender(t.Context(), "startup-active")
	if err != nil {
		t.Fatal(err)
	}
	if active.DocumentStatus != models.ExtractionQueued {
		t.Fatalf("expected startup active tender queued, got %#v", active)
	}
	jobs, err := a.Store.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].TenderID != "startup-active" || jobs[0].State != models.ExtractionQueued {
		t.Fatalf("expected only active startup tender queued, got %#v", jobs)
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

func TestAdminCreateTenantNormalizesSlugFromName(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"  Acme Engineering West  "},
		"slug":       {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/tenants/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	tenants, err := a.Store.ListTenants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tenant := range tenants {
		if tenant.Name == "Acme Engineering West" && tenant.Slug == "acme-engineering-west" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected normalized tenant slug, got %#v", tenants)
	}
}

func TestAdminCreateUserRejectsDuplicateUsername(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token":       {csrf},
		"username":         {"admin"},
		"display_name":     {"Another Admin"},
		"email":            {"another-admin@example.com"},
		"password":         {"Strong!2026Pass"},
		"tenant_id":        {tenant.ID},
		"platform_role":    {string(models.PlatformRoleSuperAdmin)},
		"tenant_role":      {string(models.TenantRoleOwner)},
		"responsibilities": {"Testing"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 got %d", w.Code)
	}
}

func TestAdminCreateETendersSourceStoresConfig(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"eTenders Custom"},
		"feed_url":   {"https://www.etenders.gov.za/Home/opportunities?id=1"},
		"type":       {"etenders_portal"},
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
		if cfg.Key == "etenders-custom" && cfg.Type == "etenders_portal" && cfg.FeedURL == "https://www.etenders.gov.za/Home/opportunities?id=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stored etenders source config, got %#v", configs)
	}
}

func TestAdminCreatePublicWorksSourceStoresConfig(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"DPWI"},
		"feed_url":   {"http://www.publicworks.gov.za/tenders.html#gsc.tab=0"},
		"type":       {"publicworks_portal"},
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
		if cfg.Key == "dpwi" && cfg.Type == "publicworks_portal" && cfg.FeedURL == "http://www.publicworks.gov.za/tenders.html#gsc.tab=0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stored public works source config, got %#v", configs)
	}
}

func TestAdminCreateCIDBSourceStoresConfig(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"CIDB"},
		"feed_url":   {"https://www.cidb.org.za/cidb-tenders/current-tenders/"},
		"type":       {"cidb_portal"},
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
		if cfg.Key == "cidb" && cfg.Type == "cidb_portal" && cfg.FeedURL == "https://www.cidb.org.za/cidb-tenders/current-tenders/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stored CIDB source config, got %#v", configs)
	}
}

func TestAdminCreateEskomSourceStoresConfig(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"Eskom Tender Bulletin"},
		"feed_url":   {"https://tenderbulletin.eskom.co.za/?pageSize=5&pageNumber=1"},
		"type":       {"eskom_portal"},
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
		if cfg.Key == "eskom-tender-bulletin" && cfg.Type == "eskom_portal" && cfg.FeedURL == "https://tenderbulletin.eskom.co.za/?pageSize=5&pageNumber=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stored Eskom source config, got %#v", configs)
	}
}

func TestAdminCreateDurbanSourceStoresConfig(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"Durban Procurement"},
		"feed_url":   {"https://www.durban.gov.za/pages/business/procurement"},
		"type":       {"durban_procurement_portal"},
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
		if cfg.Key == "durban-procurement" && cfg.Type == "durban_procurement_portal" && cfg.FeedURL == "https://www.durban.gov.za/pages/business/procurement" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stored Durban source config, got %#v", configs)
	}
}

func TestAdminCreateCityOfJoburgSourceStoresConfig(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"City of Johannesburg Custom"},
		"feed_url":   {"https://joburg.org.za/work_/Pages/2026-Tenders/Bid-Opening-Registers-2026.aspx"},
		"type":       {"city_of_joburg_portal"},
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
		if cfg.Key == "city-of-johannesburg-custom" && cfg.Type == "city_of_joburg_portal" && cfg.FeedURL == "https://joburg.org.za/work_/Pages/2026-Tenders/Bid-Opening-Registers-2026.aspx" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stored City of Johannesburg source config, got %#v", configs)
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
	_, _, cookie, csrf := sessionForRole(t, a, models.TenantRoleViewer)
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
	if !strings.Contains(body, "Source checks, schedules, and sync health") || !strings.Contains(body, "Add source") {
		t.Fatalf("sources page missing expected content: %s", body)
	}
	if !strings.Contains(body, "Configured sources with health, scheduling, and manual actions.") || !strings.Contains(body, "Recent source execution history.") {
		t.Fatalf("expected accessible table captions on sources page, got %s", body)
	}
}

func TestAdminTriggerSingleSourceCheckQueuesPending(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	_ = a.Store.UpsertSourceScheduleSettings(t.Context(), models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60})
	_ = a.Store.UpsertSourceConfig(t.Context(), models.SourceConfig{Key: "metro", Name: "Metro", Type: "json_feed", FeedURL: "https://example.org/feed.json", Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true})
	_ = a.Store.UpsertSourceHealth(t.Context(), models.SourceHealth{SourceKey: "metro"})

	form := url.Values{"csrf_token": {csrf}, "key": {"metro"}}
	req := httptest.NewRequest(http.MethodPost, "/sources/check", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	health, err := a.Store.GetSourceHealth(t.Context(), "metro")
	if err != nil {
		t.Fatal(err)
	}
	if !health.PendingManualCheck || health.LastStatus != "queued" {
		t.Fatalf("expected queued manual check, got %#v", health)
	}
}

func TestAdminTriggerSelectedSourceChecksQueuesMultiple(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	_ = a.Store.UpsertSourceScheduleSettings(t.Context(), models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60})
	for _, key := range []string{"one", "two"} {
		_ = a.Store.UpsertSourceConfig(t.Context(), models.SourceConfig{Key: key, Name: key, Type: "json_feed", FeedURL: "https://example.org/" + key + ".json", Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true})
		_ = a.Store.UpsertSourceHealth(t.Context(), models.SourceHealth{SourceKey: key})
	}
	form := url.Values{"csrf_token": {csrf}, "source_keys": {"one", "two"}}
	req := httptest.NewRequest(http.MethodPost, "/sources/check-selected", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	for _, key := range []string{"one", "two"} {
		health, _ := a.Store.GetSourceHealth(t.Context(), key)
		if !health.PendingManualCheck {
			t.Fatalf("expected %s to be queued, got %#v", key, health)
		}
	}
}

func TestAdminTriggerAllSourceChecksQueuesEnabledOnly(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	_ = a.Store.UpsertSourceScheduleSettings(t.Context(), models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60})
	configs := []models.SourceConfig{
		{Key: "enabled", Name: "Enabled", Type: "json_feed", FeedURL: "https://example.org/enabled.json", Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true},
		{Key: "disabled", Name: "Disabled", Type: "json_feed", FeedURL: "https://example.org/disabled.json", Enabled: false, ManualChecksEnabled: true, AutoCheckEnabled: true},
	}
	for _, cfg := range configs {
		_ = a.Store.UpsertSourceConfig(t.Context(), cfg)
		_ = a.Store.UpsertSourceHealth(t.Context(), models.SourceHealth{SourceKey: cfg.Key})
	}
	form := url.Values{"csrf_token": {csrf}}
	req := httptest.NewRequest(http.MethodPost, "/sources/check-all", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	enabledHealth, _ := a.Store.GetSourceHealth(t.Context(), "enabled")
	disabledHealth, _ := a.Store.GetSourceHealth(t.Context(), "disabled")
	if !enabledHealth.PendingManualCheck || disabledHealth.PendingManualCheck {
		t.Fatalf("expected only enabled source queued, enabled=%#v disabled=%#v", enabledHealth, disabledHealth)
	}
}

func TestAdminUpdateSourceScheduleStoresSettings(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{"csrf_token": {csrf}, "default_interval_minutes": {"90"}, "paused": {"on"}}
	req := httptest.NewRequest(http.MethodPost, "/sources/schedule", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	settings, err := a.Store.GetSourceScheduleSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if settings.DefaultIntervalMinutes != 90 || !settings.Paused {
		t.Fatalf("unexpected settings: %#v", settings)
	}
}

func TestAdminUpdateSourceStoresOverrideAndFlags(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	_ = a.Store.UpsertSourceScheduleSettings(t.Context(), models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60})
	_ = a.Store.UpsertSourceConfig(t.Context(), models.SourceConfig{Key: "metro", Name: "Metro", Type: "json_feed", FeedURL: "https://example.org/feed.json", Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true})
	_ = a.Store.UpsertSourceHealth(t.Context(), models.SourceHealth{SourceKey: "metro"})
	form := url.Values{"csrf_token": {csrf}, "key": {"metro"}, "enabled": {"on"}, "interval_minutes": {"15"}}
	req := httptest.NewRequest(http.MethodPost, "/sources/update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	cfg, err := a.Store.GetSourceConfig(t.Context(), "metro")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.AutoCheckEnabled || cfg.IntervalMinutes != 15 {
		t.Fatalf("unexpected updated config: %#v", cfg)
	}
	health, _ := a.Store.GetSourceHealth(t.Context(), "metro")
	if !health.NextScheduledCheckAt.IsZero() {
		t.Fatalf("expected no next schedule when auto-check disabled, got %#v", health)
	}
}

func TestSourceStatusJSONReturnsSchedulingMetadata(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertSourceScheduleSettings(t.Context(), models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 75})
	_ = a.Store.UpsertSourceConfig(t.Context(), models.SourceConfig{Key: "metro", Name: "Metro", Type: "json_feed", FeedURL: "https://example.org/feed.json", Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true})
	_ = a.Store.UpsertSourceHealth(t.Context(), models.SourceHealth{SourceKey: "metro", HealthStatus: "healthy"})

	req := httptest.NewRequest(http.MethodGet, "/sources/status.json", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	var payload struct {
		Settings models.SourceScheduleSettings `json:"settings"`
		Configs  []models.SourceConfig         `json:"configs"`
		Health   []models.SourceHealth         `json:"health"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Settings.DefaultIntervalMinutes != 75 || len(payload.Configs) == 0 || len(payload.Health) == 0 {
		t.Fatalf("unexpected source status payload: %#v", payload)
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

func TestLoginPageUsesExternalAssetsAndTighterCSP(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `href="/assets/styles.css"`) || !strings.Contains(body, `src="/assets/app.js"`) {
		t.Fatalf("expected external asset references, got %s", body)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("expected tightened CSP, got %q", csp)
	}
	if strings.Contains(csp, "default-src 'self' 'unsafe-inline'") {
		t.Fatalf("expected inline script allowance to be removed, got %q", csp)
	}
}

func TestAssetRouteServesSharedAppJS(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "desktopMenus") || !strings.Contains(w.Body.String(), "mobileDrawer") {
		t.Fatalf("expected shared app script, got %s", w.Body.String())
	}
	if cacheControl := w.Header().Get("Cache-Control"); cacheControl != "public, max-age=3600" {
		t.Fatalf("expected asset cache header, got %q", cacheControl)
	}
}

func TestCrossOriginCSRFIsRejected(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{"csrf_token": {csrf}, "default_interval_minutes": {"90"}}
	req := httptest.NewRequest(http.MethodPost, "/sources/schedule", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 got %d", w.Code)
	}
}

func TestAdminResetPasswordRequiresExplicitPassword(t *testing.T) {
	a := newTestApp(t)
	user, _, cookie, csrf := adminSession(t, a)
	form := url.Values{"csrf_token": {csrf}, "user_id": {user.ID}, "new_password": {""}}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/reset-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", w.Code)
	}
}

func TestAdminCreateSourceRejectsPrivateFeedURL(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"Internal Feed"},
		"feed_url":   {"http://127.0.0.1/feed.json"},
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

func TestTenantAdminCannotCreateAdminUser(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, csrf := sessionForRole(t, a, models.TenantRoleAdmin)
	form := url.Values{
		"csrf_token":       {csrf},
		"username":         {"tenant-admin-peer"},
		"display_name":     {"Tenant Admin Peer"},
		"email":            {"tenant-admin-peer@example.com"},
		"password":         {"Strong!2026Pass"},
		"tenant_id":        {tenant.ID},
		"platform_role":    {string(models.PlatformRoleNone)},
		"tenant_role":      {string(models.TenantRoleOwner)},
		"responsibilities": {"Testing"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 got %d", w.Code)
	}
}

func TestTenantAdminCannotManageOtherTenantMemberships(t *testing.T) {
	a := newTestApp(t)
	_, currentTenant, cookie, csrf := sessionForRole(t, a, models.TenantRoleAdmin)
	if err := a.Store.UpsertTenant(t.Context(), models.Tenant{Name: "Other Workspace", Slug: "other-workspace"}); err != nil {
		t.Fatal(err)
	}
	tenants, err := a.Store.ListTenants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	otherTenantID := ""
	for _, tenant := range tenants {
		if tenant.ID != currentTenant.ID && tenant.Slug == "other-workspace" {
			otherTenantID = tenant.ID
		}
	}
	if otherTenantID == "" {
		t.Fatal("expected second tenant")
	}
	form := url.Values{
		"csrf_token":       {csrf},
		"username":         {"cross-tenant-user"},
		"display_name":     {"Cross Tenant"},
		"email":            {"cross-tenant@example.com"},
		"password":         {"Strong!2026Pass"},
		"tenant_id":        {otherTenantID},
		"platform_role":    {string(models.PlatformRoleNone)},
		"tenant_role":      {string(models.TenantRoleViewer)},
		"responsibilities": {"Testing"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 got %d", w.Code)
	}
}

func TestTenantAdminUserAdminPageIsScopedToCurrentTenant(t *testing.T) {
	a := newTestApp(t)
	_, currentTenant, cookie, _ := sessionForRole(t, a, models.TenantRoleAdmin)
	if err := a.Store.UpsertTenant(t.Context(), models.Tenant{Name: "Other Workspace", Slug: "other-workspace"}); err != nil {
		t.Fatal(err)
	}
	tenants, err := a.Store.ListTenants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	otherTenantID := ""
	for _, tenant := range tenants {
		if tenant.ID != currentTenant.ID && tenant.Slug == "other-workspace" {
			otherTenantID = tenant.ID
		}
	}
	if otherTenantID == "" {
		t.Fatal("expected second tenant")
	}
	if err := a.Store.UpsertUser(t.Context(), models.User{Username: "otheruser", DisplayName: "Other User", Email: "other@example.com", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	otherUser, err := a.Store.GetUserByUsername(t.Context(), "otheruser")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertMembership(t.Context(), models.Membership{UserID: otherUser.ID, TenantID: otherTenantID, Role: models.TenantRoleViewer}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "other@example.com") || strings.Contains(body, "Other Workspace") {
		t.Fatalf("expected tenant-scoped admin view, got %s", body)
	}
}

func TestLoginPageHidesDemoCredentialsInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATA_PATH", filepath.Join(t.TempDir(), "store.db"))
	t.Setenv("SECRET_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SECURE_COOKIES", "true")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "Strong!2026Password")
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := a.Store.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, "OpenBid!2026") || strings.Contains(body, "Demo access") {
		t.Fatalf("production login page exposed demo credentials: %s", body)
	}
}

func TestCurrentUserTenantRejectsInactiveUser(t *testing.T) {
	a := newTestApp(t)
	user, _, cookie, _ := adminSession(t, a)
	user.IsActive = false
	if err := a.Store.UpsertUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(cookie)
	if _, _, _, ok := a.currentUserTenant(req); ok {
		t.Fatal("expected inactive user session to be rejected")
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
	if !strings.Contains(body, "One home for daily bidding work and operational visibility") || !strings.Contains(body, "Bookmarks") {
		t.Fatalf("home page missing expected content: %s", body)
	}
	if !strings.Contains(body, "Recent opportunities") {
		t.Fatalf("home page missing recent opportunities section: %s", body)
	}
	if strings.Contains(body, "<h2 class=\"card-title\">Source health</h2>") {
		t.Fatalf("home page should not render source health card anymore: %s", body)
	}
}

func TestDashboardRouteRendersMergedHomeView(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "One home for daily bidding work and operational visibility") {
		t.Fatalf("dashboard route did not render merged home view: %s", body)
	}
	if strings.Contains(body, "Operational metrics for the current workspace") {
		t.Fatalf("dashboard route still rendered legacy dashboard copy: %s", body)
	}
}

func TestHomePageCollapsesRecentOpportunitiesWithoutSourceHealthCard(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	recentIndex := strings.Index(body, "Recent opportunities")
	if recentIndex == -1 {
		t.Fatalf("expected recent opportunities section: %s", body)
	}
	if strings.Contains(body, "<h2 class=\"card-title\">Source health</h2>") {
		t.Fatalf("expected source health card to move off the home page: %s", body)
	}
	if !strings.Contains(body, "<details class=\"section-disclosure\"") {
		t.Fatalf("expected recent opportunities disclosure markup: %s", body)
	}
}

func TestTendersPageRendersTendersContent(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/tenders?view=cards", nil)
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

func TestTendersPageUsesExpectedDisclosureDefaults(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:        "disclosure-tender",
		Title:     "Disclosure Tender",
		Issuer:    "Metro",
		SourceKey: "treasury",
		Status:    "open",
	})
	const tendersDefaultPath = "/tenders"
	req := httptest.NewRequest(http.MethodGet, tendersDefaultPath, nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<details class=\"section-disclosure tenders-filters-disclosure\" open>") {
		t.Fatalf("expected expanded filters disclosure, got %s", body)
	}
	if !strings.Contains(body, "<details class=\"section-disclosure tenders-bulk-disclosure\">") {
		t.Fatalf("expected collapsed bulk disclosure, got %s", body)
	}
	if !strings.Contains(body, "Quick actions") || !strings.Contains(body, "Add bookmark") {
		t.Fatalf("default tenders view should keep lightweight quick actions, got %s", body)
	}
	if strings.Contains(body, "Queue extraction") || strings.Contains(body, "Re-run extraction") {
		t.Fatalf("default tenders view should not expose manual extraction controls, got %s", body)
	}
	if strings.Contains(body, "<details class=\"section-disclosure opportunity-actions-disclosure\">") {
		t.Fatalf("default tenders view should avoid heavy card action disclosures, got %s", body)
	}
	bulkIndex := strings.Index(body, "tenders-bulk-disclosure")
	filterIndex := strings.Index(body, "tenders-filters-disclosure")
	if filterIndex == -1 || bulkIndex == -1 || bulkIndex < filterIndex {
		t.Fatalf("expected filters before bulk actions, got %s", body)
	}
}

func TestTendersPageRendersTypedDocumentStatus(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "typed-status",
		Title:          "Typed Status Tender",
		Issuer:         "CIDB",
		SourceKey:      "cidb",
		Status:         "open",
		DocumentStatus: models.ExtractionQueued,
	})

	req := httptest.NewRequest(http.MethodGet, "/tenders?view=cards", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	if !strings.Contains(body, "Typed Status Tender") {
		t.Fatalf("expected tender to render, got %s", body)
	}
	if strings.Contains(body, "template:") {
		t.Fatalf("unexpected template execution error: %s", body)
	}
}

func TestTendersPageRendersFilterDropdownOptionsFromDatabase(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "filter-options",
		Title:          "Filter Options Tender",
		Issuer:         "Metro Water",
		SourceKey:      "treasury",
		Province:       "Gauteng",
		Status:         "open",
		Category:       "Civil Engineering",
		CIDBGrading:    "7CE",
		DocumentStatus: models.ExtractionCompleted,
	})
	_ = a.Store.UpsertWorkflow(t.Context(), models.Workflow{
		TenantID: tenant.ID,
		TenderID: "filter-options",
		Status:   "reviewing",
	})

	req := httptest.NewRequest(http.MethodGet, "/tenders?view=cards", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	for _, expected := range []string{
		`<option value="treasury"`,
		`>National Treasury</option>`,
		`<option value="Gauteng"`,
		`<option value="open"`,
		`<option value="Civil Engineering"`,
		`<option value="Metro Water"`,
		`<option value="7CE"`,
		`<option value="reviewing"`,
		`<option value="completed"`,
		`<option value="source"`,
		`<option value="workflow_status"`,
		`<option value="document_status"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected filter dropdown content %q, got %s", expected, body)
		}
	}
}

func TestTendersPageShowsExplicitBookmarkActionLabels(t *testing.T) {
	a := newTestApp(t)
	user, tenant, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:        "bookmark-visible",
		Title:     "Bookmark Visible Tender",
		Issuer:    "Metro",
		SourceKey: "treasury",
		Status:    "open",
	})
	_ = a.Store.UpsertBookmark(t.Context(), models.Bookmark{
		TenantID: tenant.ID,
		UserID:   user.ID,
		TenderID: "bookmark-visible",
		Note:     "track this one",
	})

	req := httptest.NewRequest(http.MethodGet, "/tenders?view=cards", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Remove bookmark") || !strings.Contains(body, "Update saved note") {
		t.Fatalf("expected explicit bookmark controls, got %s", body)
	}
}

func TestTendersBookmarkFlowShowsAddThenRemove(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:        "bookmark-flow",
		Title:     "Bookmark Flow Tender",
		Issuer:    "Metro",
		SourceKey: "treasury",
		Status:    "open",
	})

	req := httptest.NewRequest(http.MethodGet, "/tenders?view=cards", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	initialBody := w.Body.String()
	if !strings.Contains(initialBody, "Add bookmark") {
		t.Fatalf("expected add bookmark action before saving, got %s", initialBody)
	}

	form := url.Values{"csrf_token": {csrf}, "tender_id": {"bookmark-flow"}, "return_to": {"/tenders"}}
	req = httptest.NewRequest(http.MethodPost, "/tenders/bookmark", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after bookmarking, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/tenders?view=cards", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	updatedBody := w.Body.String()
	if !strings.Contains(updatedBody, "Remove bookmark") {
		t.Fatalf("expected remove bookmark action after saving, got %s", updatedBody)
	}
}

func TestTendersBookmarkRedirectPreservesSearchState(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:        "bookmark-search-state",
		Title:     "Search State Tender",
		Issuer:    "Metro",
		SourceKey: "treasury",
		Status:    "open",
	})

	form := url.Values{
		"csrf_token": {csrf},
		"tender_id":  {"bookmark-search-state"},
		"return_to":  {"/tenders?q=metro&page=2&sort=published_date"},
	}
	req := httptest.NewRequest(http.MethodPost, "/tenders/bookmark", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after bookmarking, got %d", w.Code)
	}
	location := w.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("expected valid redirect url, got %q err=%v", location, err)
	}
	if redirectURL.Path != "/tenders" {
		t.Fatalf("expected redirect back to tenders, got %q", location)
	}
	query := redirectURL.Query()
	if query.Get("q") != "metro" || query.Get("page") != "2" || query.Get("sort") != "published_date" {
		t.Fatalf("expected search state to be preserved, got redirect %q", location)
	}
}

func TestTenderDetailShowsAddBookmarkAction(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:          "detail-bookmark",
		Title:       "Detail Bookmark Tender",
		Issuer:      "Metro",
		SourceKey:   "treasury",
		Status:      "open",
		OriginalURL: "https://example.org/tender",
	})

	req := httptest.NewRequest(http.MethodGet, "/tenders/detail-bookmark", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Add bookmark") {
		t.Fatalf("expected detail page bookmark action, got %s", body)
	}
}

func TestTenderDetailShowsRerunExtractionForFailedDocuments(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "detail-rerun",
		Title:          "Detail Rerun Tender",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		OriginalURL:    "https://example.org/tender",
		DocumentURL:    "https://example.org/tender.pdf",
		DocumentStatus: models.ExtractionFailed,
	})

	req := httptest.NewRequest(http.MethodGet, "/tenders/detail-rerun", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Re-run extraction") {
		t.Fatalf("expected detail page to offer extraction retry, got %s", body)
	}
}

func TestTenderDetailHidesRerunExtractionForCompletedDocuments(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "detail-complete",
		Title:          "Detail Complete Tender",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		OriginalURL:    "https://example.org/tender",
		DocumentURL:    "https://example.org/tender.pdf",
		DocumentStatus: models.ExtractionCompleted,
	})

	req := httptest.NewRequest(http.MethodGet, "/tenders/detail-complete", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, "Re-run extraction") {
		t.Fatalf("did not expect detail page retry control for completed extraction, got %s", body)
	}
}

func TestQueuePageHidesOrphanJobsAndRendersTypedStates(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "queue-tender",
		Title:          "Queue Tender",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		DocumentURL:    "https://example.org/doc.pdf",
		DocumentStatus: models.ExtractionQueued,
	})
	_ = a.Store.QueueJob(t.Context(), models.ExtractionJob{
		ID:          "job-valid",
		TenderID:    "queue-tender",
		DocumentURL: "https://example.org/doc.pdf",
		State:       models.ExtractionQueued,
	})
	_ = a.Store.QueueJob(t.Context(), models.ExtractionJob{
		ID:          "job-orphan",
		TenderID:    "missing-tender",
		DocumentURL: "https://example.org/missing.pdf",
		State:       models.ExtractionCompleted,
	})

	req := httptest.NewRequest(http.MethodGet, "/queue", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	if !strings.Contains(body, "Queue Tender") {
		t.Fatalf("expected valid queue item to render, got %s", body)
	}
	if strings.Contains(body, "missing-tender") {
		t.Fatalf("expected orphan queue item to be hidden, got %s", body)
	}
	if strings.Contains(body, "template:") {
		t.Fatalf("unexpected template execution error: %s", body)
	}
}

func TestQueuePageGroupsStatesAndHidesCompletedRetry(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	for i := 0; i < 12; i++ {
		tenderID := "failed-tender-" + strconv.Itoa(i)
		_ = a.Store.UpsertTender(t.Context(), models.Tender{
			ID:             tenderID,
			Title:          "Failed Tender " + strconv.Itoa(i),
			Issuer:         "Metro",
			SourceKey:      "treasury",
			Status:         "open",
			DocumentURL:    "https://example.org/" + strconv.Itoa(i) + ".pdf",
			DocumentStatus: models.ExtractionFailed,
		})
		_ = a.Store.QueueJob(t.Context(), models.ExtractionJob{
			ID:          "failed-job-" + strconv.Itoa(i),
			TenderID:    tenderID,
			DocumentURL: "https://example.org/" + strconv.Itoa(i) + ".pdf",
			State:       models.ExtractionFailed,
		})
	}
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "completed-tender",
		Title:          "Completed Tender",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		DocumentURL:    "https://example.org/completed.pdf",
		DocumentStatus: models.ExtractionCompleted,
	})
	_ = a.Store.QueueJob(t.Context(), models.ExtractionJob{
		ID:          "completed-job",
		TenderID:    "completed-tender",
		DocumentURL: "https://example.org/completed.pdf",
		State:       models.ExtractionCompleted,
	})
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:                      "skipped-tender",
		Title:                   "Skipped Tender",
		Issuer:                  "Metro",
		SourceKey:               "treasury",
		Status:                  "open",
		DocumentURL:             "https://example.org/skipped.pdf",
		DocumentStatus:          models.ExtractionSkipped,
		ExtractionSkippedReason: tenderstate.ExpiredSkipReason,
	})
	_ = a.Store.QueueJob(t.Context(), models.ExtractionJob{
		ID:          "skipped-job",
		TenderID:    "skipped-tender",
		DocumentURL: "https://example.org/skipped.pdf",
		State:       models.ExtractionSkipped,
		LastError:   "Skipped because the tender closing date/time has passed.",
		SkipReason:  tenderstate.ExpiredSkipReason,
	})

	req := httptest.NewRequest(http.MethodGet, "/queue", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "queue-state-failed") || !strings.Contains(body, "queue-state-completed") || !strings.Contains(body, "queue-state-skipped") {
		t.Fatalf("expected grouped queue sections, got %s", body)
	}
	if !strings.Contains(body, "<details class=\"section-disclosure queue-state-disclosure queue-state-failed\" open>") {
		t.Fatalf("expected failed section open by default, got %s", body)
	}
	if !strings.Contains(body, "queue-state-disclosure queue-state-completed") || strings.Contains(body, "queue-state-disclosure queue-state-completed\" open") {
		t.Fatalf("expected completed section collapsed by default, got %s", body)
	}
	if strings.Contains(body, "Requeue processing for &#39;Completed Tender&#39;?") || strings.Contains(body, "Requeue processing for 'Completed Tender'?") {
		t.Fatalf("completed job still exposed retry action: %s", body)
	}
	if !strings.Contains(body, "Skipped because the tender closing date/time has passed.") || strings.Contains(body, "Requeue processing for &#39;Skipped Tender&#39;?") || strings.Contains(body, "Requeue processing for 'Skipped Tender'?") {
		t.Fatalf("expected skipped expiry reason without retry action, got %s", body)
	}
	if !strings.Contains(body, "failed_page=2") || !strings.Contains(body, "Page 1 of 2") {
		t.Fatalf("expected failed section pagination controls, got %s", body)
	}
	if strings.Index(body, "queue-state-failed") > strings.Index(body, "queue-state-processing") {
		t.Fatalf("expected failed section before other states, got %s", body)
	}
}

func TestQueuePageHidesRetryForViewerRole(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := sessionForRole(t, a, models.TenantRoleViewer)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "viewer-queue-tender",
		Title:          "Viewer Queue Tender",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		DocumentURL:    "https://example.org/viewer.pdf",
		DocumentStatus: models.ExtractionFailed,
	})
	_ = a.Store.QueueJob(t.Context(), models.ExtractionJob{
		ID:          "viewer-queue-job",
		TenderID:    "viewer-queue-tender",
		DocumentURL: "https://example.org/viewer.pdf",
		State:       models.ExtractionFailed,
	})

	req := httptest.NewRequest(http.MethodGet, "/queue", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	if strings.Contains(body, "action=\"/queue/requeue\"") {
		t.Fatalf("expected retry action hidden for viewer, got %s", body)
	}
	if !strings.Contains(body, "Read only") {
		t.Fatalf("expected read-only marker for viewer queue item, got %s", body)
	}
}

func TestQueuePageShowsRetryForAdminRole(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "admin-queue-tender",
		Title:          "Admin Queue Tender",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		DocumentURL:    "https://example.org/admin.pdf",
		DocumentStatus: models.ExtractionFailed,
	})
	_ = a.Store.QueueJob(t.Context(), models.ExtractionJob{
		ID:          "admin-queue-job",
		TenderID:    "admin-queue-tender",
		DocumentURL: "https://example.org/admin.pdf",
		State:       models.ExtractionFailed,
	})

	req := httptest.NewRequest(http.MethodGet, "/queue", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	if !strings.Contains(body, "action=\"/queue/requeue\"") {
		t.Fatalf("expected retry action for admin, got %s", body)
	}
}

func TestQueueRequeueWorksForAdmin(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "requeue-admin-tender",
		Title:          "Requeue Admin Tender",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		DocumentURL:    "https://example.org/requeue.pdf",
		DocumentStatus: models.ExtractionFailed,
	})
	form := url.Values{
		"csrf_token": {csrf},
		"tender_id":  {"requeue-admin-tender"},
		"return_to":  {"/queue"},
	}
	req := httptest.NewRequest(http.MethodPost, "/queue/requeue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after requeue, got %d", w.Code)
	}
	if location := w.Header().Get("Location"); location != "/queue?message=Job+requeued" {
		t.Fatalf("unexpected redirect location: %q", location)
	}
	tender, err := a.Store.GetTender(t.Context(), "requeue-admin-tender")
	if err != nil {
		t.Fatal(err)
	}
	if tender.DocumentStatus != models.ExtractionQueued {
		t.Fatalf("expected tender document status queued, got %s", tender.DocumentStatus)
	}
	jobs, err := a.Store.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range jobs {
		if job.TenderID == "requeue-admin-tender" && job.State == models.ExtractionQueued {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected queued extraction job after retry, got %#v", jobs)
	}
}

func TestQueueRequeueSkipsExpiredTenderForAdmin(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	_ = a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "requeue-expired-tender",
		Title:          "Expired Requeue Tender",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		ClosingDate:    time.Now().Add(-24 * time.Hour).Format("2006-01-02 15:04"),
		DocumentURL:    "https://example.org/requeue-expired.pdf",
		DocumentStatus: models.ExtractionFailed,
	})
	form := url.Values{
		"csrf_token": {csrf},
		"tender_id":  {"requeue-expired-tender"},
		"return_to":  {"/queue"},
	}
	req := httptest.NewRequest(http.MethodPost, "/queue/requeue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after skipped requeue, got %d", w.Code)
	}
	if location := w.Header().Get("Location"); !strings.Contains(location, "expired") {
		t.Fatalf("expected expiry message redirect, got %q", location)
	}
	tender, err := a.Store.GetTender(t.Context(), "requeue-expired-tender")
	if err != nil {
		t.Fatal(err)
	}
	if tender.DocumentStatus != models.ExtractionSkipped || tender.ExtractionSkippedReason != tenderstate.ExpiredSkipReason {
		t.Fatalf("expected tender document status skipped, got %#v", tender)
	}
	jobs, err := a.Store.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected no queued extraction job after expired retry, got %#v", jobs)
	}
}

func TestAdminUsersPageShowsRoleScopeGuide(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	if !strings.Contains(body, "Role scope guide") || !strings.Contains(body, "Viewer") || !strings.Contains(body, "read-only access") {
		t.Fatalf("expected role scope guidance on admin users page, got %s", body)
	}
	if !strings.Contains(body, "Skip to main content") || !strings.Contains(body, "id=\"role-scope-guide\"") || !strings.Contains(body, "aria-describedby=\"role-scope-guide\"") {
		t.Fatalf("expected accessibility helpers on admin users page, got %s", body)
	}
}

func TestAdminSourcesAliasRemoved(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/admin/sources", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("expected removed admin sources alias to redirect to canonical path, got %d", w.Code)
	}
	if location := w.Header().Get("Location"); location != "/sources" {
		t.Fatalf("expected removed admin sources alias to redirect to /sources, got %q", location)
	}
}

func TestSourcesPageReordersSetupAndCollapsesHistory(t *testing.T) {
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
	if !strings.Contains(body, "sources-setup-stack") || !strings.Contains(body, "sources-history-disclosure") {
		t.Fatalf("expected updated sources layout markers, got %s", body)
	}
	if !strings.Contains(body, "<details class=\"section-disclosure sources-add-disclosure\" open>") {
		t.Fatalf("expected add source disclosure open by default, got %s", body)
	}
	if !strings.Contains(body, "<details class=\"section-disclosure sources-scheduling-disclosure\">") || strings.Contains(body, "<details class=\"section-disclosure sources-scheduling-disclosure\" open>") {
		t.Fatalf("expected scheduling disclosure collapsed by default, got %s", body)
	}
	if !strings.Contains(body, "sources-ops-disclosure") || strings.Contains(body, "sources-ops-disclosure\" open") {
		t.Fatalf("expected source operations disclosure collapsed by default, got %s", body)
	}
	if strings.Index(body, "Add source") > strings.Index(body, "Scheduling") {
		t.Fatalf("expected Add source above Scheduling, got %s", body)
	}
	if !strings.Contains(body, "<details class=\"section-disclosure sources-history-disclosure\"") {
		t.Fatalf("expected collapsible history section, got %s", body)
	}
	if strings.Contains(body, "<details class=\"section-disclosure sources-history-disclosure\" open>") {
		t.Fatalf("expected history section collapsed by default, got %s", body)
	}
	if !strings.Contains(body, "source-ops-table") || !strings.Contains(body, "source-inline-controls") {
		t.Fatalf("expected compact source operations markup, got %s", body)
	}
}

func TestSharedHeaderNoLongerRendersTenantSwitch(t *testing.T) {
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
	if strings.Contains(body, "action=\"/tenant/switch\"") {
		t.Fatalf("expected tenant switch removed from shared header, got %s", body)
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
	if strings.Contains(adminBody, "<summary class=\"nav-link utility \">Workspace</summary>") || strings.Contains(adminBody, "<summary class=\"nav-link utility \">Administration</summary>") {
		t.Fatalf("admin navigation still shows separated settings groups: %s", adminBody)
	}

	_, _, viewerCookie, _ := sessionForRole(t, a, models.TenantRoleViewer)
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

func TestSettingsPageShowsAdminCardsOnlyForAuthorizedUsers(t *testing.T) {
	a := newTestApp(t)
	_, _, adminCookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	adminBody := w.Body.String()
	if !strings.Contains(adminBody, "User Admin") || !strings.Contains(adminBody, "Tenant Admin") {
		t.Fatalf("expected admin settings page to show admin cards: %s", adminBody)
	}

	_, _, viewerCookie, _ := sessionForRole(t, a, models.TenantRoleViewer)
	req = httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(viewerCookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	viewerBody := w.Body.String()
	if strings.Contains(viewerBody, "User Admin") || strings.Contains(viewerBody, "Tenant Admin") || strings.Contains(viewerBody, "Audit Log") {
		t.Fatalf("viewer settings page exposed admin cards: %s", viewerBody)
	}
}
func TestRouteAccessibilityAndPageRendering(t *testing.T) {
	a := newTestApp(t)
	_, _, viewerCookie, _ := sessionForRole(t, a, models.TenantRoleViewer)
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected favicon to be a lightweight 204 got %d", w.Code)
	}
	if body := w.Body.String(); body != "" {
		t.Fatalf("expected favicon response to avoid rendering home page, got %q", body)
	}

	routes := map[string]string{
		"/dashboard":      "One home for daily bidding work and operational visibility",
		"/bookmarks":      "Keep active opportunities separate",
		"/keyword-search": "Track tenders that mention the words and phrases",
		"/saved-searches": "Reusable market views",
		"/queue":          "Queue and extraction monitoring",
		"/settings":       "Account, workspace, and administration settings",
	}
	for path, marker := range routes {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(viewerCookie)
		w = httptest.NewRecorder()
		a.Server.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s got %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), marker) {
			t.Fatalf("expected marker %q in %s", marker, path)
		}
	}

	for _, path := range []string{"/admin/users", "/admin/tenants", "/sources"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(viewerCookie)
		w = httptest.NewRecorder()
		a.Server.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for %s got %d", path, w.Code)
		}
	}
}

func TestMFASetupStoresEncryptedSecretAtRest(t *testing.T) {
	a := newTestApp(t)
	user, _, cookie, csrf := adminSession(t, a)
	secret := auth.NewTOTPSecret()
	code := auth.GenerateTOTPFromSecret(secret, time.Now())
	form := url.Values{
		"csrf_token": {csrf},
		"secret":     {secret},
		"code":       {code},
	}
	req := httptest.NewRequest(http.MethodPost, "/mfa/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	storedUser, err := a.Store.GetUser(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !storedUser.MFAEnabled {
		t.Fatalf("expected MFA to be enabled, got %#v", storedUser)
	}
	if storedUser.MFASecret == "" || storedUser.MFASecret == secret {
		t.Fatalf("expected encrypted MFA secret at rest, got %q", storedUser.MFASecret)
	}
	if len(storedUser.RecoveryCodes) != 10 {
		t.Fatalf("expected 10 recovery codes, got %#v", storedUser.RecoveryCodes)
	}
	for _, recoveryCode := range storedUser.RecoveryCodes {
		if !strings.HasPrefix(recoveryCode, "enc:v1:") {
			t.Fatalf("expected encrypted recovery code marker, got %#v", storedUser.RecoveryCodes)
		}
	}
}

func TestLoginAcceptsRecoveryCodeAndConsumesIt(t *testing.T) {
	a := newTestApp(t)
	user, _, _, _ := adminSession(t, a)
	secret := "JBSWY3DPEHPK3PXP"
	user.MFAEnabled = true
	user.MFASecret = secret
	user.RecoveryCodes = []string{"ABCD-EF12", "1234-5678"}
	if err := a.persistUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"username": {"admin"},
		"password": {"OpenBid!2026"},
		"mfa_code": {"ABCD-EF12"},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect got %d body=%s", w.Code, w.Body.String())
	}

	updatedUser, err := a.hydrateUserSensitiveFields(t.Context(), userMustGet(t, a, user.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedUser.RecoveryCodes) != 1 || updatedUser.RecoveryCodes[0] != "1234-5678" {
		t.Fatalf("expected used recovery code to be consumed, got %#v", updatedUser.RecoveryCodes)
	}
}

func TestLegacyPlaintextMFASecretAndRecoveryCodesUpgradeOnLogin(t *testing.T) {
	a := newTestApp(t)
	user, _, _, _ := adminSession(t, a)
	legacySecret := "JBSWY3DPEHPK3PXP"
	user.MFAEnabled = true
	user.MFASecret = legacySecret
	user.RecoveryCodes = []string{"ABCD-EF12", "1234-5678"}
	if err := a.Store.UpsertUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"username": {"admin"},
		"password": {"OpenBid!2026"},
		"mfa_code": {auth.GenerateTOTPFromSecret(legacySecret, time.Now())},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect got %d body=%s", w.Code, w.Body.String())
	}

	upgradedUser, err := a.Store.GetUser(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if upgradedUser.MFASecret == legacySecret || upgradedUser.MFASecret == "" {
		t.Fatalf("expected legacy plaintext secret to be upgraded, got %q", upgradedUser.MFASecret)
	}
	if len(upgradedUser.RecoveryCodes) != 2 || strings.Contains(strings.Join(upgradedUser.RecoveryCodes, ","), "ABCD-EF12") {
		t.Fatalf("expected legacy plaintext recovery codes to be upgraded, got %#v", upgradedUser.RecoveryCodes)
	}
}

func userMustGet(t *testing.T, a *App, userID string) models.User {
	t.Helper()
	user, err := a.Store.GetUser(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return user
}
