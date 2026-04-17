package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"openbid/internal/mail"
	"openbid/internal/models"
)

type emailAdminView struct {
	Settings      models.EmailSettings
	Readiness     mail.Readiness
	HasPassword   bool
	SecurityModes []string
}

func (a *App) AdminEmail(w http.ResponseWriter, r *http.Request) {
	u, _, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canManagePlatform(u) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	view, err := a.emailAdminView(r)
	if err != nil {
		a.serverError(w, r, "unable to load email settings", err)
		return
	}
	a.render(w, r, "admin_email.html", map[string]any{
		"Title":      "Email settings",
		"EmailAdmin": view,
	})
}

func (a *App) AdminSaveEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canManagePlatform(u) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	existing, err := a.Store.GetEmailSettings(r.Context())
	if err != nil {
		a.redirectAfterAction(w, r, "/admin/email", "error", "Email settings could not be loaded")
		return
	}
	settings := existing
	settings.Enabled = r.FormValue("enabled") == "1"
	settings.SMTPHost = r.FormValue("smtp_host")
	settings.SMTPPort = atoi(r.FormValue("smtp_port"), existing.SMTPPort)
	settings.SMTPSecurityMode = r.FormValue("smtp_security_mode")
	settings.SMTPAuthRequired = r.FormValue("smtp_auth_required") == "1"
	settings.SMTPUsername = r.FormValue("smtp_username")
	if strings.TrimSpace(r.FormValue("clear_smtp_password")) == "1" {
		settings.SMTPPassword = ""
	} else if password := r.FormValue("smtp_password"); password != "" {
		settings.SMTPPassword = password
	}
	settings.SMTPFromEmail = r.FormValue("smtp_from_email")
	settings.SMTPFromName = r.FormValue("smtp_from_name")
	settings.SMTPReplyTo = r.FormValue("smtp_reply_to")
	settings.TimeoutSeconds = atoi(r.FormValue("email_timeout_seconds"), existing.TimeoutSeconds)
	settings.TestRecipient = r.FormValue("test_recipient")
	if err := a.Store.UpsertEmailSettings(r.Context(), settings); err != nil {
		a.redirectAfterAction(w, r, "/admin/email", "error", "Email settings could not be saved")
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "update", "email_settings", "global", "Updated outbound email settings", map[string]string{
		"enabled":       strconv.FormatBool(settings.Enabled),
		"security_mode": strings.ToLower(strings.TrimSpace(settings.SMTPSecurityMode)),
		"auth_required": strconv.FormatBool(settings.SMTPAuthRequired),
	})
	a.redirectAfterAction(w, r, "/admin/email", "success", "Email settings saved")
}

func (a *App) AdminTestEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canManagePlatform(u) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if a.Email == nil {
		a.redirectAfterAction(w, r, "/admin/email", "error", "Email service is unavailable")
		return
	}
	settings, err := a.Store.GetEmailSettings(r.Context())
	if err != nil {
		a.redirectAfterAction(w, r, "/admin/email", "error", "Email settings could not be loaded")
		return
	}
	recipient := strings.TrimSpace(r.FormValue("test_recipient"))
	if recipient == "" {
		recipient = strings.TrimSpace(settings.TestRecipient)
	}
	if recipient == "" {
		a.redirectAfterAction(w, r, "/admin/email", "error", "Enter a test recipient email address before sending a test email")
		return
	}
	readiness := mail.SettingsReadiness(settings)
	if !readiness.Ready {
		a.redirectAfterAction(w, r, "/admin/email", "error", "Test email blocked: "+emailReadinessProblem(readiness))
		return
	}
	result, err := a.Email.Send(r.Context(), models.EmailMessage{
		To:       []string{recipient},
		Subject:  "OpenBid test email",
		TextBody: "This is a test email from OpenBid. If you received it, outbound email is configured correctly.",
		HTMLBody: "<p>This is a test email from <strong>OpenBid</strong>.</p><p>If you received it, outbound email is configured correctly.</p>",
	})
	if err != nil {
		if errors.Is(err, mail.ErrEmailConfig) {
			a.redirectAfterAction(w, r, "/admin/email", "error", "Test email blocked: "+err.Error())
			return
		}
		a.redirectAfterAction(w, r, "/admin/email", "error", "Test email failed: "+err.Error())
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "test", "email_settings", "global", "Sent outbound email test", map[string]string{"recipient": recipient})
	a.redirectAfterAction(w, r, "/admin/email", "success", fmt.Sprintf("Test email sent to %s (%d recipient accepted)", recipient, result.AcceptedRecipients))
}

func (a *App) emailAdminView(r *http.Request) (emailAdminView, error) {
	settings, err := a.Store.GetEmailSettings(r.Context())
	if err != nil {
		return emailAdminView{}, err
	}
	return emailAdminView{
		Settings:      settings,
		Readiness:     mail.SettingsReadiness(settings),
		HasPassword:   strings.TrimSpace(settings.SMTPPassword) != "",
		SecurityModes: []string{mail.SecuritySTARTTLS, mail.SecurityTLS, mail.SecurityPlain},
	}, nil
}

func emailReadinessProblem(readiness mail.Readiness) string {
	parts := []string{}
	if len(readiness.MissingFields) > 0 {
		parts = append(parts, "missing "+strings.Join(readiness.MissingFields, ", "))
	}
	if len(readiness.InvalidFields) > 0 {
		parts = append(parts, "invalid "+strings.Join(readiness.InvalidFields, ", "))
	}
	if len(parts) == 0 {
		return readiness.Summary
	}
	return strings.Join(parts, "; ")
}
