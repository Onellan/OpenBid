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
	if err := a.Store.UpsertMembership(t.Context(), models.Membership{UserID: user.ID, TenantID: tenants[0].ID, Role: models.RoleAnalyst}); err != nil {
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
	_, _, cookie, csrf := sessionForRole(t, a, models.RoleViewer)
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
