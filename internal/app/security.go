package app

import (
	"net/http"
	"strings"
	"time"

	"tenderhub-za/internal/auth"
)

func (a *App) PasswordPage(w http.ResponseWriter, r *http.Request) {
	user, _, _, ok := a.currentUserTenant(r)
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
	if err := a.Store.UpsertUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to save password change", err)
		return
	}
	a.render(w, r, "password.html", map[string]any{"Title": "Password", "Message": "Password updated"})
}

func (a *App) MFAPage(w http.ResponseWriter, r *http.Request) {
	user, tenant, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "User": user, "Tenant": tenant})
}

func (a *App) MFASetup(w http.ResponseWriter, r *http.Request) {
	user, _, _, ok := a.currentUserTenant(r)
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
	user.MFASecret = secret
	user.MFAEnabled = true
	if err := a.Store.UpsertUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to enable MFA", err)
		return
	}
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Message": "MFA enabled"})
}

func (a *App) MFADisable(w http.ResponseWriter, r *http.Request) {
	user, _, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if !auth.VerifyPassword(r.FormValue("password"), user.PasswordSalt, user.PasswordHash) {
		a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Error": "Password confirmation failed"})
		return
	}
	user.MFAEnabled = false
	user.MFASecret = ""
	if err := a.Store.UpsertUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to disable MFA", err)
		return
	}
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Message": "MFA disabled"})
}
