package app

import (
	"net/http"
	"strings"
	"time"

	"tenderhub-za/internal/auth"
)

func (a *App) PasswordPage(w http.ResponseWriter, r *http.Request) {
	user, tenant, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method == http.MethodGet {
		a.render(w, r, "password.html", map[string]any{"Title": "Password"})
		return
	}
	if !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if !auth.VerifyPassword(r.FormValue("current_password"), user.PasswordSalt, user.PasswordHash) {
		a.render(w, r, "password.html", map[string]any{"Title": "Password", "Error": "Current password is incorrect"})
		return
	}
	if r.FormValue("new_password") != r.FormValue("confirm_password") {
		a.render(w, r, "password.html", map[string]any{"Title": "Password", "Error": "New passwords do not match"})
		return
	}
	if err := auth.StrongEnoughPassword(r.FormValue("new_password")); err != nil {
		a.render(w, r, "password.html", map[string]any{"Title": "Password", "Error": err.Error()})
		return
	}
	salt, hash, err := auth.HashPassword(r.FormValue("new_password"))
	if err != nil {
		a.serverError(w, r, "unable to update password", err)
		return
	}
	user.PasswordSalt = salt
	user.PasswordHash = hash
	user.SessionVersion++
	if err := a.persistUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to save password change", err)
		return
	}
	if err := a.revokeUserSessions(r.Context(), user.ID); err != nil {
		a.serverError(w, r, "unable to revoke existing sessions", err)
		return
	}
	if _, err := a.issueSession(r.Context(), w, user, tenant.ID); err != nil {
		a.serverError(w, r, "unable to refresh session", err)
		return
	}
	a.render(w, r, "password.html", map[string]any{"Title": "Password", "Message": "Password updated"})
}

func (a *App) MFAPage(w http.ResponseWriter, r *http.Request) {
	user, tenant, member, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "User": user, "Tenant": tenant, "Member": member, "RecoveryCodeCount": len(user.RecoveryCodes)})
}

func (a *App) MFASetup(w http.ResponseWriter, r *http.Request) {
	user, tenant, member, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method == http.MethodGet {
		a.render(w, r, "mfa_setup.html", map[string]any{"Title": "MFA Setup", "Message": auth.NewTOTPSecret()})
		return
	}
	if !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	secret := r.FormValue("secret")
	code := r.FormValue("code")
	if strings.TrimSpace(secret) == "" || strings.TrimSpace(code) == "" {
		a.render(w, r, "mfa_setup.html", map[string]any{"Title": "MFA Setup", "Message": secret, "Error": "Secret and MFA code are required"})
		return
	}
	if !auth.ValidateTOTP(secret, code, time.Now()) {
		a.render(w, r, "mfa_setup.html", map[string]any{"Title": "MFA Setup", "Message": secret, "Error": "Invalid MFA code"})
		return
	}
	encryptedSecret, err := auth.EncryptSensitiveValue(a.Config.SecretKey, secret)
	if err != nil {
		a.serverError(w, r, "unable to protect MFA secret", err)
		return
	}
	user.MFASecret = encryptedSecret
	user.MFAEnabled = true
	recoveryCodes := auth.NewRecoveryCodes(10)
	user.RecoveryCodes = recoveryCodes
	if err := a.persistUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to enable MFA", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: user, Tenant: tenant, Member: member}, "update", "user_security", user.ID, "MFA enabled", map[string]string{"recovery_codes": "generated"})
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Message": "MFA enabled. Save your recovery codes now.", "RecoveryCodes": recoveryCodes, "RecoveryCodeCount": len(recoveryCodes)})
}

func (a *App) MFADisable(w http.ResponseWriter, r *http.Request) {
	user, tenant, member, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if a.userRequiresPrivilegedMFA(r.Context(), user.ID) {
		a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Error": "MFA is required for your role", "RecoveryCodeCount": len(user.RecoveryCodes)})
		return
	}
	if !auth.VerifyPassword(r.FormValue("password"), user.PasswordSalt, user.PasswordHash) {
		a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Error": "Password confirmation failed"})
		return
	}
	user.MFAEnabled = false
	user.MFASecret = ""
	user.RecoveryCodes = nil
	if err := a.persistUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to disable MFA", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: user, Tenant: tenant, Member: member}, "update", "user_security", user.ID, "MFA disabled", nil)
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Message": "MFA disabled"})
}

func (a *App) MFARegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user, tenant, member, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if !user.MFAEnabled || strings.TrimSpace(user.MFASecret) == "" {
		a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "User": user, "Tenant": tenant, "Error": "Enable MFA before regenerating recovery codes"})
		return
	}
	if !auth.VerifyPassword(r.FormValue("password"), user.PasswordSalt, user.PasswordHash) {
		a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "User": user, "Tenant": tenant, "Error": "Password confirmation failed", "RecoveryCodeCount": len(user.RecoveryCodes)})
		return
	}
	recoveryCodes := auth.NewRecoveryCodes(10)
	user.RecoveryCodes = recoveryCodes
	if err := a.persistUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to regenerate recovery codes", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: user, Tenant: tenant, Member: member}, "update", "user_security", user.ID, "Recovery codes regenerated", nil)
	a.render(w, r, "mfa.html", map[string]any{
		"Title":             "MFA",
		"User":              user,
		"Tenant":            tenant,
		"Message":           "Recovery codes regenerated. Save the new set now.",
		"RecoveryCodes":     recoveryCodes,
		"RecoveryCodeCount": len(recoveryCodes),
	})
}
