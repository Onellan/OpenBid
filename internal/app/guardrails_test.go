package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"tenderhub-za/internal/auth"
	"tenderhub-za/internal/models"
)

func TestSameOriginRequestRejectsSchemeMismatch(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/password", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Host", "openbid.example")
	req.Header.Set("Origin", "http://openbid.example")
	if a.sameOriginRequest(req) {
		t.Fatal("expected same-origin check to reject scheme mismatch")
	}
}

func TestLoginRateLimiterBlocksAfterConfiguredFailures(t *testing.T) {
	limiter := NewLoginRateLimiter(10*time.Minute, 2)
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	if allowed, _ := limiter.Allow("203.0.113.10", now); !allowed {
		t.Fatal("expected first attempt to be allowed")
	}
	limiter.RegisterFailure("203.0.113.10", now)
	if allowed, _ := limiter.Allow("203.0.113.10", now.Add(time.Minute)); !allowed {
		t.Fatal("expected second attempt to still be allowed")
	}
	limiter.RegisterFailure("203.0.113.10", now.Add(time.Minute))
	if allowed, retryAfter := limiter.Allow("203.0.113.10", now.Add(2*time.Minute)); allowed || retryAfter <= 0 {
		t.Fatalf("expected limiter block after repeated failures, allowed=%v retry_after=%v", allowed, retryAfter)
	}
	limiter.RegisterSuccess("203.0.113.10")
	if allowed, _ := limiter.Allow("203.0.113.10", now.Add(2*time.Minute)); !allowed {
		t.Fatal("expected success to clear limiter state")
	}
}

func TestWithRecoveryReturnsFiveHundredOnPanic(t *testing.T) {
	a := &App{}
	handler := a.WithRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from recovery middleware, got %d", w.Code)
	}
}

func TestAdminResetPasswordInvalidatesExistingSession(t *testing.T) {
	a := newTestApp(t)
	adminUser, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token":   {csrf},
		"user_id":      {adminUser.ID},
		"new_password": {"Replacement!2026"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/reset-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected password reset redirect, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("expected old session to be invalidated, got code=%d location=%q", w.Code, w.Header().Get("Location"))
	}
}

func TestPasswordChangeRefreshesSessionVersion(t *testing.T) {
	a := newTestApp(t)
	user, tenant, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token":       {csrf},
		"current_password": {"TenderHub!2026"},
		"new_password":     {"Stronger!2026"},
		"confirm_password": {"Stronger!2026"},
	}
	req := httptest.NewRequest(http.MethodPost, "/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected password change success, got %d", w.Code)
	}

	refreshed := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, candidate := range refreshed {
		if candidate.Name == "th_session" {
			sessionCookie = candidate
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected refreshed session cookie after password change")
	}
	session, ok := auth.DecodeSession(a.Config.SecretKey, sessionCookie.Value)
	if !ok {
		t.Fatal("expected refreshed session cookie to decode")
	}
	updatedUser, err := a.Store.GetUser(req.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionVersion != updatedUser.SessionVersion || session.TenantID != tenant.ID {
		t.Fatalf("expected refreshed session version to match user, session=%#v user=%#v", session, updatedUser)
	}
}

func TestCurrentUserTenantRejectsStaleSessionVersion(t *testing.T) {
	a := newTestApp(t)
	user, tenant, _, _ := adminSession(t, a)
	user.SessionVersion = 2
	if err := a.persistUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	raw, err := auth.EncodeSession(a.Config.SecretKey, models.Session{
		UserID:         user.ID,
		TenantID:       tenant.ID,
		CSRF:           "csrf-stale",
		SessionVersion: 1,
		Expires:        time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "th_session", Value: raw})
	if _, _, _, ok := a.currentUserTenant(req); ok {
		t.Fatal("expected stale session version to be rejected")
	}
}
