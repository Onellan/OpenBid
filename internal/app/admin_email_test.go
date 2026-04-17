package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"openbid/internal/mail"
	"openbid/internal/models"
)

type fakeAdminMailTransport struct {
	calls int
	last  models.EmailMessage
	err   error
}

func (f *fakeAdminMailTransport) Send(_ context.Context, _ models.EmailSettings, message models.EmailMessage) (models.EmailSendResult, error) {
	f.calls++
	f.last = message
	if f.err != nil {
		return models.EmailSendResult{}, f.err
	}
	return models.EmailSendResult{AcceptedRecipients: len(message.To), Message: "accepted"}, nil
}

func TestAdminEmailPageRendersRequiredOptionalAndStatus(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	req := httptest.NewRequest(http.MethodGet, "/admin/email", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.AdminEmail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Required settings",
		"Optional settings",
		"SMTP host",
		"SMTP password/app password",
		"Email not configured",
		"Send a test email",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected admin email page to contain %q", want)
		}
	}
	if strings.Contains(body, "app-password") {
		t.Fatal("admin page must not render SMTP passwords")
	}
}

func TestAdminEmailRequiresPlatformAdmin(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := sessionForRole(t, a, models.TenantRoleOwner)
	req := httptest.NewRequest(http.MethodGet, "/admin/email", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.AdminEmail(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAdminEmailSaveAndTestSend(t *testing.T) {
	a := newTestApp(t)
	transport := &fakeAdminMailTransport{}
	a.Email = mail.NewService(a.Store, transport)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{
		"csrf_token":            {csrf},
		"enabled":               {"1"},
		"smtp_host":             {"smtp.example.org"},
		"smtp_port":             {"587"},
		"smtp_security_mode":    {"starttls"},
		"smtp_auth_required":    {"1"},
		"smtp_username":         {"openbid"},
		"smtp_password":         {"app-password"},
		"smtp_from_email":       {"alerts@example.org"},
		"smtp_from_name":        {"OpenBid Alerts"},
		"email_timeout_seconds": {"10"},
		"test_recipient":        {"ops@example.org"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/email/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.AdminSaveEmail(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected save redirect, got %d", w.Code)
	}
	settings, err := a.Store.GetEmailSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || settings.SMTPHost != "smtp.example.org" || settings.SMTPPassword != "app-password" {
		t.Fatalf("saved settings mismatch: %#v", settings)
	}

	testForm := url.Values{"csrf_token": {csrf}, "test_recipient": {"ops@example.org"}}
	testReq := httptest.NewRequest(http.MethodPost, "/admin/email/test", strings.NewReader(testForm.Encode()))
	testReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	testReq.AddCookie(cookie)
	testW := httptest.NewRecorder()
	a.AdminTestEmail(testW, testReq)
	if testW.Code != http.StatusSeeOther {
		t.Fatalf("expected test redirect, got %d", testW.Code)
	}
	if transport.calls != 1 || len(transport.last.To) != 1 || transport.last.To[0] != "ops@example.org" {
		t.Fatalf("expected one test email to ops@example.org, calls=%d last=%#v", transport.calls, transport.last)
	}
}

func TestAdminTestEmailBlockedWhenConfigMissing(t *testing.T) {
	a := newTestApp(t)
	transport := &fakeAdminMailTransport{}
	a.Email = mail.NewService(a.Store, transport)
	_, _, cookie, csrf := adminSession(t, a)
	form := url.Values{"csrf_token": {csrf}, "test_recipient": {"ops@example.org"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/email/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.AdminTestEmail(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if transport.calls != 0 {
		t.Fatalf("missing config should not call transport, got %d calls", transport.calls)
	}
	location := w.Header().Get("Location")
	if !strings.Contains(location, "Test+email+blocked") {
		t.Fatalf("expected blocked test message in redirect, got %q", location)
	}
}
