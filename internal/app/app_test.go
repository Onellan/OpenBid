package app

import (
	"encoding/json"
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
	_ = a.Store.UpsertWorkflow(t.Context(), models.Workflow{TenantID: tenant.ID, TenderID: "csv1", Status: "reviewing", Priority: "high", AssignedUser: "alice"})
	req := httptest.NewRequest(http.MethodGet, "/tenders/export.csv", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.ExportCSV(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "workflow_status") || !strings.Contains(body, "tender_type") || !strings.Contains(body, "documents_json") || !strings.Contains(body, "close soon") || !strings.Contains(body, "reviewing") || !strings.Contains(body, "CIDB 6EP") {
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

func TestAdminCreateETendersSourceStoresConfig(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {"eTenders"},
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
		if cfg.Key == "etenders" && cfg.Type == "etenders_portal" && cfg.FeedURL == "https://www.etenders.gov.za/Home/opportunities?id=1" {
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
	if !strings.Contains(body, "Source checks, schedules, and sync health") || !strings.Contains(body, "Add source") {
		t.Fatalf("sources page missing expected content: %s", body)
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

	req := httptest.NewRequest(http.MethodGet, "/tenders", nil)
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

func TestQueuePagePrunesOrphanJobsAndRendersTypedStates(t *testing.T) {
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
	if strings.Contains(body, "template:") {
		t.Fatalf("unexpected template execution error: %s", body)
	}
	jobs, err := a.Store.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-valid" {
		t.Fatalf("expected orphan job to be pruned, got %#v", jobs)
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
		"/sources":        "Source checks, schedules, and sync health",
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
