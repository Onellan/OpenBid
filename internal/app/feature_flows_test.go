package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"openbid/internal/auth"
	"openbid/internal/models"
	"openbid/internal/store"
)

func TestLogoutClearsServerSideSession(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	session, ok := auth.DecodeSession(a.Config.SecretKey, cookie.Value)
	if !ok {
		t.Fatal("expected session cookie to decode")
	}

	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect on logout, got %d", w.Code)
	}
	if _, err := a.Store.GetSession(t.Context(), session.ID); err != store.ErrNotFound {
		t.Fatalf("expected logout to revoke stored session, got %v", err)
	}
	cleared := false
	for _, setCookie := range w.Result().Cookies() {
		if setCookie.Name == "th_session" && setCookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("expected logout to clear session cookie")
	}
}

func TestSavedSearchCreateAndDeleteFlow(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := adminSession(t, a)

	createForm := url.Values{
		"csrf_token": {csrf},
		"name":       {"Metro search"},
		"query":      {"q=metro&status=open"},
		"filters":    {`{"source":"treasury"}`},
	}
	req := httptest.NewRequest(http.MethodPost, "/saved-searches", strings.NewReader(createForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after saved search create, got %d", w.Code)
	}

	user, tenant, _, _ := adminSession(t, a)
	items, err := a.Store.ListSavedSearches(t.Context(), tenant.ID, user.ID)
	if err != nil || len(items) != 1 || items[0].Name != "Metro search" {
		t.Fatalf("expected saved search to persist, err=%v items=%#v", err, items)
	}

	deleteForm := url.Values{
		"csrf_token": {csrf},
		"id":         {items[0].ID},
	}
	req = httptest.NewRequest(http.MethodPost, "/saved-searches/delete", strings.NewReader(deleteForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after saved search delete, got %d", w.Code)
	}

	items, err = a.Store.ListSavedSearches(t.Context(), tenant.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected saved search deletion, got %#v", items)
	}
}

func TestKeywordSearchFlowHomepageAndNavigation(t *testing.T) {
	a := newTestApp(t)
	user, tenant, cookie, csrf := adminSession(t, a)
	if err := a.Store.UpsertTender(t.Context(), models.Tender{
		ID:          "keyword-match",
		Title:       "Solar backup installation",
		Summary:     "Battery and inverter installation",
		Issuer:      "City Power",
		SourceKey:   "treasury",
		Province:    "Gauteng",
		Status:      "open",
		ClosingDate: "2026-05-12",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertTender(t.Context(), models.Tender{
		ID:          "keyword-nomatch",
		Title:       "Road resurfacing",
		Summary:     "Asphalt and drainage",
		Issuer:      "Roads Agency",
		SourceKey:   "cidb",
		Province:    "Free State",
		Status:      "open",
		ClosingDate: "2026-05-13",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected home 200 got %d", w.Code)
	}
	if !strings.Contains(body, "Keyword Search") || !strings.Contains(body, "href=\"/keyword-search\"") || !strings.Contains(body, "Smart Keyword Extraction") || !strings.Contains(body, "href=\"/smart-keywords\"") || !strings.Contains(body, "0 matched") {
		t.Fatalf("home/nav missing keyword search empty state: %s", body)
	}

	form := url.Values{
		"csrf_token": {csrf},
		"value":      {"solar backup"},
		"enabled":    {"1"},
	}
	req = httptest.NewRequest(http.MethodPost, "/keyword-search/keywords", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after keyword save, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/keyword-search/refresh", strings.NewReader(url.Values{"csrf_token": {csrf}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after manual refresh, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/keyword-search", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body = w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected keyword search page 200 got %d", w.Code)
	}
	for _, marker := range []string{
		"Keyword Search",
		"keyword-manager-disclosure\" open",
		"keyword-filters-disclosure",
		"keyword-results-disclosure\" open",
		"Solar backup installation",
		"solar backup",
		"Refresh matches",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("keyword search page missing %q: %s", marker, body)
		}
	}
	if strings.Contains(body, "Road resurfacing") {
		t.Fatalf("keyword search page included non-matching tender: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body = w.Body.String()
	if !strings.Contains(body, "1 matched") || !strings.Contains(body, "1 active keywords") {
		t.Fatalf("home keyword widget did not update after refresh: %s", body)
	}

	keywords, err := a.Store.ListKeywords(t.Context(), tenant.ID, user.ID)
	if err != nil || len(keywords) != 1 {
		t.Fatalf("expected keyword to persist, err=%v keywords=%#v", err, keywords)
	}
	deleteForm := url.Values{"csrf_token": {csrf}, "id": {keywords[0].ID}}
	req = httptest.NewRequest(http.MethodPost, "/keyword-search/keywords/delete", strings.NewReader(deleteForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after keyword delete, got %d", w.Code)
	}
	summary, err := a.Store.KeywordSearchSummary(t.Context(), tenant.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MatchedTenderCount != 0 || summary.TotalKeywordCount != 0 {
		t.Fatalf("expected delete to clear matches, got %#v", summary)
	}
}

func TestMobileNavigationRendersRoleAwareLinksAndRoutes(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected home 200 got %d", w.Code)
	}
	for _, marker := range []string{
		"data-mobile-menu",
		"aria-label=\"Open navigation menu\"",
		"<summary>Find Work</summary>",
		"data-mobile-menu-link href=\"/tenders\"",
		"data-mobile-menu-link href=\"/keyword-search\"",
		"data-mobile-menu-link href=\"/smart-keywords\"",
		"data-mobile-menu-link href=\"/queue\"",
		"data-mobile-menu-link href=\"/sources\"",
		"data-mobile-menu-link href=\"/admin/users\"",
		"data-mobile-menu-link href=\"/admin/tenants\"",
		"data-mobile-menu-link href=\"/health\"",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("mobile navigation missing %q: %s", marker, body)
		}
	}
	for _, path := range []string{"/tenders", "/keyword-search", "/smart-keywords", "/queue", "/sources", "/settings", "/admin/users", "/admin/tenants", "/health"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		w = httptest.NewRecorder()
		a.Server.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected mobile nav route %s to return 200, got %d", path, w.Code)
		}
	}
}

func TestMobileNavigationHidesPrivilegedLinksForViewer(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := sessionForRole(t, a, models.TenantRoleViewer)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected home 200 got %d", w.Code)
	}
	for _, marker := range []string{
		"data-mobile-menu",
		"data-mobile-menu-link href=\"/tenders\"",
		"data-mobile-menu-link href=\"/keyword-search\"",
		"data-mobile-menu-link href=\"/smart-keywords\"",
		"data-mobile-menu-link href=\"/queue\"",
		"data-mobile-menu-link href=\"/settings\"",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("viewer mobile navigation missing %q: %s", marker, body)
		}
	}
	for _, forbidden := range []string{
		"data-mobile-menu-link href=\"/sources\"",
		"data-mobile-menu-link href=\"/admin/users\"",
		"data-mobile-menu-link href=\"/admin/tenants\"",
		"data-mobile-menu-link href=\"/admin/email\"",
		"data-mobile-menu-link href=\"/audit-log/security\"",
		"data-mobile-menu-link href=\"/health\"",
		"data-mobile-menu-link href=\"/queue#expired-tender-cleanup\"",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("viewer mobile navigation should not include %q: %s", forbidden, body)
		}
	}
}

func TestSmartKeywordsPageShowsGroupToggleControls(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, _ := sessionForRole(t, a, models.TenantRoleAdmin)
	if _, err := a.Store.UpsertSmartKeywordGroup(t.Context(), models.SmartKeywordGroup{
		TenantID:      tenant.ID,
		Name:          "Water Services",
		TagName:       "Water Services",
		Description:   "Water services opportunities",
		Enabled:       false,
		MatchMode:     models.SmartMatchModeAny,
		MinMatchCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/smart-keywords", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected smart keywords page 200 got %d", w.Code)
	}
	for _, marker := range []string{
		"data-smart-keywords-screen",
		"id=\"smart-overview\"",
		"id=\"smart-keyword-manager\"",
		"id=\"smart-groups-panel\"",
		"data-compact-panel",
		"Water Services",
		"href=\"/smart-keywords/groups/",
		"<button type=\"submit\">Enable</button>",
		"name=\"return_to\" value=\"/smart-keywords\"",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("smart keywords page missing %q: %s", marker, body)
		}
	}
}

func TestSmartKeywordsOptimizedScreenSectionsPreviewAndActions(t *testing.T) {
	a := newTestApp(t)
	_, tenant, cookie, csrf := sessionForRole(t, a, models.TenantRoleAdmin)
	group, err := a.Store.UpsertSmartKeywordGroup(t.Context(), models.SmartKeywordGroup{
		TenantID:      tenant.ID,
		Name:          "Water",
		TagName:       "Water",
		Enabled:       true,
		MatchMode:     models.SmartMatchModeAny,
		MinMatchCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.UpsertSmartKeyword(t.Context(), models.SmartKeyword{TenantID: tenant.ID, GroupID: group.ID, Value: "water services", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.UpsertSmartKeyword(t.Context(), models.SmartKeyword{TenantID: tenant.ID, Value: "pump station", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertSmartExtractionSettings(t.Context(), models.SmartExtractionSettings{TenantID: tenant.ID, Enabled: true, AlertsEnabled: true, EmailAlertsEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertTender(t.Context(), models.Tender{
		ID:          "smart-match",
		Title:       "Water services upgrade",
		Issuer:      "Metro Water",
		SourceKey:   "treasury",
		Status:      "open",
		ClosingDate: "2026-07-01",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertTender(t.Context(), models.Tender{
		ID:        "smart-nomatch",
		Title:     "Road resurfacing",
		Issuer:    "Roads Agency",
		SourceKey: "cidb",
		Status:    "open",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/smart-keywords", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected smart keywords page 200 got %d", w.Code)
	}
	for _, marker := range []string{
		"Smart Keyword Extraction",
		"summary-stat",
		"Active keywords",
		"Source extraction mode",
		"Configure this in <a href=\"/sources\">Sources",
		"Preview matches",
		"Reprocess matches",
		"Send email alerts",
		"name=\"email_alerts_enabled\"",
		"id=\"smart-preview-panel\"",
		"id=\"smart-views-panel\"",
		"id=\"smart-alert-history\"",
		"data-mobile-stack",
		"data-compact-panel",
		"Water services upgrade",
		"href=\"/tenders/smart-match\"",
		"<span class=\"badge success\">Matched</span>",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("optimized smart keywords page missing %q: %s", marker, body)
		}
	}
	if strings.Contains(body, "Enable smart extraction") {
		t.Fatalf("smart keywords page still renders legacy extraction toggle: %s", body)
	}

	addForm := url.Values{"csrf_token": {csrf}, "value": {"treatment plant"}, "enabled": {"1"}}
	req = httptest.NewRequest(http.MethodPost, "/smart-keywords/keywords", strings.NewReader(addForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after smart keyword add, got %d", w.Code)
	}
	keywords, err := a.Store.ListSmartKeywords(t.Context(), tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundAdded := false
	for _, keyword := range keywords {
		if keyword.Value == "treatment plant" && keyword.Enabled {
			foundAdded = true
			break
		}
	}
	if !foundAdded {
		t.Fatalf("expected added smart keyword to persist, got %#v", keywords)
	}

	reprocessForm := url.Values{"csrf_token": {csrf}}
	req = httptest.NewRequest(http.MethodPost, "/smart-keywords/reprocess", strings.NewReader(reprocessForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after smart reprocess, got %d", w.Code)
	}
	if location := w.Header().Get("Location"); !strings.Contains(location, "Reprocessed") {
		t.Fatalf("expected reprocess success message, got %q", location)
	}
}

func TestSmartKeywordsOptimizedEmptyState(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := sessionForRole(t, a, models.TenantRoleAdmin)
	req := httptest.NewRequest(http.MethodGet, "/smart-keywords", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected smart keywords page 200 got %d", w.Code)
	}
	for _, marker := range []string{
		"No standalone keywords yet",
		"No tenders are available to preview yet.",
		"id=\"smart-overview\"",
		"id=\"smart-keyword-manager\"",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("smart keywords empty state missing %q: %s", marker, body)
		}
	}
}

func TestLoginLocksAccountAfterRepeatedFailures(t *testing.T) {
	a := newTestApp(t)
	salt, hash, err := auth.HashPassword("Correct!2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.persistUser(t.Context(), models.User{
		Username:     "lockout-user",
		DisplayName:  "Lockout User",
		Email:        "lockout@example.org",
		PasswordSalt: salt,
		PasswordHash: hash,
		IsActive:     true,
	}); err != nil {
		t.Fatal(err)
	}
	user, err := a.Store.GetUserByUsername(t.Context(), "lockout-user")
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := a.Store.ListTenants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertMembership(t.Context(), models.Membership{UserID: user.ID, TenantID: tenants[0].ID, Role: models.TenantRoleUser}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		form := url.Values{"username": {"lockout-user"}, "password": {"wrong-password"}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.Server.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected invalid login to re-render form, got %d", w.Code)
		}
	}

	form := url.Values{"username": {"lockout-user"}, "password": {"Correct!2026"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Account temporarily locked") {
		t.Fatalf("expected account lockout message, got %s", body)
	}
	entries, _, err := a.Store.ListSecurityAuditEntriesPage(t.Context(), tenants[0].ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundLockout := false
	for _, entry := range entries {
		if entry.Action == "lockout" && entry.Entity == "auth" && entry.EntityID == user.ID {
			foundLockout = true
			break
		}
	}
	if !foundLockout {
		t.Fatalf("expected lockout event to be audited, got %#v", entries)
	}
}

func TestQueueRequeueRejectsViewerDirectPost(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, csrf := sessionForRole(t, a, models.TenantRoleViewer)
	if err := a.Store.UpsertTender(t.Context(), models.Tender{
		ID:             "viewer-direct-requeue",
		Title:          "Viewer Requeue",
		Issuer:         "Metro",
		SourceKey:      "treasury",
		Status:         "open",
		DocumentURL:    "https://example.org/doc.pdf",
		DocumentStatus: models.ExtractionFailed,
	}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf_token": {csrf}, "tender_id": {"viewer-direct-requeue"}}
	req := httptest.NewRequest(http.MethodPost, "/queue/requeue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden direct viewer retry, got %d", w.Code)
	}
}

func TestDataPipesMenuAndExpiredTenderCleanupFlow(t *testing.T) {
	a := newTestApp(t)
	user, tenant, cookie, csrf := adminSession(t, a)
	if err := a.Store.UpsertTender(t.Context(), models.Tender{
		ID:          "cleanup-expired",
		Title:       "Expired Cleanup Tender",
		Issuer:      "Metro",
		SourceKey:   "treasury",
		Status:      "open",
		ClosingDate: "2026-01-01",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertTender(t.Context(), models.Tender{
		ID:          "cleanup-active",
		Title:       "Active Cleanup Tender",
		Issuer:      "Metro",
		SourceKey:   "treasury",
		Status:      "open",
		ClosingDate: "2999-01-01",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpsertBookmark(t.Context(), models.Bookmark{TenantID: tenant.ID, UserID: user.ID, TenderID: "cleanup-expired", Note: "preserve"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/queue", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected queue page 200 got %d", w.Code)
	}
	for _, marker := range []string{"Data Pipes", "Remove Expired Tenders", "data-confirm=", "/data-pipes/remove-expired-tenders", "closing date/time"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("queue page missing %q: %s", marker, body)
		}
	}
	if strings.Contains(body, "Run Pipeline") {
		t.Fatalf("old Run Pipeline menu label still rendered: %s", body)
	}

	form := url.Values{"csrf_token": {csrf}}
	req = httptest.NewRequest(http.MethodPost, "/data-pipes/remove-expired-tenders", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after cleanup, got %d body=%s", w.Code, w.Body.String())
	}
	if location := w.Header().Get("Location"); !strings.Contains(location, "Expired+tender+cleanup+queued") {
		t.Fatalf("expected queued cleanup message, got %q", location)
	}
	if _, err := a.Store.GetTender(t.Context(), "cleanup-expired"); err != nil {
		t.Fatalf("expected expired tender to remain until worker runs cleanup, got %v", err)
	}
	jobs, err := a.Store.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != models.ExpiredTenderCleanupJobID || jobs[0].State != models.ExtractionQueued {
		t.Fatalf("expected queued expired cleanup job, got %#v", jobs)
	}
	entries, err := a.Store.ListAuditEntries(t.Context(), tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundAudit := false
	for _, entry := range entries {
		if entry.Action == "queue" && entry.Entity == "expired_tender_cleanup" {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected cleanup queue audit entry, got %#v", entries)
	}

	req = httptest.NewRequest(http.MethodPost, "/data-pipes/remove-expired-tenders", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect after repeated cleanup enqueue, got %d", w.Code)
	}
	if location := w.Header().Get("Location"); !strings.Contains(location, "Expired+tender+cleanup+queued") {
		t.Fatalf("expected repeated enqueue message, got %q", location)
	}
	jobs, err = a.Store.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected repeated cleanup enqueue to keep a single tracking job, got %#v", jobs)
	}
}

func TestExpiredTenderCleanupRequiresCSRFAndPermission(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodPost, "/data-pipes/remove-expired-tenders", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected missing CSRF to be forbidden, got %d", w.Code)
	}

	_, _, viewerCookie, viewerCSRF := sessionForRole(t, a, models.TenantRoleViewer)
	form := url.Values{"csrf_token": {viewerCSRF}}
	req = httptest.NewRequest(http.MethodPost, "/data-pipes/remove-expired-tenders", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(viewerCookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected viewer cleanup to be forbidden, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/queue", nil)
	req.AddCookie(viewerCookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, "action=\"/data-pipes/remove-expired-tenders\"") {
		t.Fatalf("viewer should not see cleanup action form: %s", body)
	}
	if !strings.Contains(body, "Read-only access") {
		t.Fatalf("viewer should see read-only cleanup state: %s", body)
	}
}
