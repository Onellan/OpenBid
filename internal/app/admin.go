package app

import (
	"net/http"
	"strings"

	"tenderhub-za/internal/auth"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
)

type SourceAdminItem struct {
	Config models.SourceConfig
	Health models.SourceHealth
}

func (a *App) SettingsPage(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	a.render(w, r, "settings.html", map[string]any{
		"Title":  "Settings",
		"User":   u,
		"Tenant": t,
	})
}

func (a *App) AdminUsers(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	users, _ := a.Store.ListUsers(r.Context())
	tenants, _ := a.Store.ListTenants(r.Context())
	memberships, _ := a.Store.ListAllMemberships(r.Context())
	a.render(w, r, "admin_users.html", map[string]any{"Title": "Users", "User": u, "Tenant": t, "Items": users, "Tenants": tenants, "Memberships": memberships})
}

func (a *App) AdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	if err := auth.StrongEnoughPassword(r.FormValue("password")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	salt, hash, _ := auth.HashPassword(r.FormValue("password"))
	_ = a.Store.UpsertUser(r.Context(), models.User{Username: r.FormValue("username"), DisplayName: r.FormValue("display_name"), Email: r.FormValue("email"), PasswordSalt: salt, PasswordHash: hash, IsActive: true})
	users, _ := a.Store.ListUsers(r.Context())
	user := users[len(users)-1]
	_ = a.Store.UpsertMembership(r.Context(), models.Membership{UserID: user.ID, TenantID: r.FormValue("tenant_id"), Role: models.Role(r.FormValue("role")), Responsibilities: r.FormValue("responsibilities")})
	http.Redirect(w, r, "/admin/users", 303)
}

func (a *App) AdminTenants(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canManageTenants(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	tenants, _ := a.Store.ListTenants(r.Context())
	a.render(w, r, "admin_tenants.html", map[string]any{"Title": "Tenants", "User": u, "Tenant": t, "Items": tenants, "CSRFToken": a.mustCSRF(r)})
}

func (a *App) AdminCreateTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canManageTenants(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = a.Store.UpsertTenant(r.Context(), models.Tenant{Name: r.FormValue("name"), Slug: r.FormValue("slug")})
	http.Redirect(w, r, "/admin/tenants", 303)
}

func (a *App) AdminSources(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/sources", http.StatusSeeOther)
}

func (a *App) SourcesPage(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	configs, _ := a.Store.ListSourceConfigs(r.Context())
	health, _ := a.Store.ListSourceHealth(r.Context())
	healthByKey := map[string]models.SourceHealth{}
	for _, item := range health {
		healthByKey[item.SourceKey] = item
	}
	items := make([]SourceAdminItem, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, SourceAdminItem{Config: cfg, Health: healthByKey[cfg.Key]})
	}
	a.render(w, r, "sources.html", map[string]any{
		"Title":            "Sources",
		"User":             u,
		"Tenant":           t,
		"Items":            items,
		"CSRFToken":        a.mustCSRF(r),
		"SourceType":       source.TypeJSONFeed,
		"CanManageSources": canManageSources(m.Role),
	})
}

func (a *App) AdminCreateSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canManageSources(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	feedURL := strings.TrimSpace(r.FormValue("feed_url"))
	if name == "" || feedURL == "" {
		http.Error(w, "name and feed url are required", 400)
		return
	}
	sourceType := strings.TrimSpace(r.FormValue("type"))
	if sourceType == "" {
		sourceType = source.TypeJSONFeed
	}
	if sourceType != source.TypeJSONFeed && sourceType != source.TypeETendersPortal {
		http.Error(w, "unsupported source type", 400)
		return
	}
	key := source.NormalizeKey(r.FormValue("key"))
	if key == "" {
		key = source.NormalizeKey(name)
	}
	if key == "" {
		http.Error(w, "source key is required", 400)
		return
	}
	configs, _ := a.Store.ListSourceConfigs(r.Context())
	for _, cfg := range configs {
		if cfg.Key == key {
			http.Error(w, "source key already exists", 409)
			return
		}
	}
	cfg := models.SourceConfig{Key: key, Name: name, Type: sourceType, FeedURL: feedURL, Enabled: true}
	if err := a.Store.UpsertSourceConfig(r.Context(), cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = a.Store.UpsertSourceHealth(r.Context(), models.SourceHealth{
		SourceKey:     key,
		LastStatus:    "configured",
		LastMessage:   "Waiting for the next sync cycle.",
		LastItemCount: 0,
	})
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "create", "source", key, "Source added", map[string]string{"name": name, "type": sourceType})
	a.redirectAfterAction(w, r, "/sources", "success", "Source added")
}

func (a *App) AdminDeleteSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canManageSources(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	id := r.FormValue("id")
	key := source.NormalizeKey(r.FormValue("key"))
	if id == "" || key == "" {
		http.Error(w, "missing source details", 400)
		return
	}
	_ = a.Store.DeleteSourceConfig(r.Context(), id)
	_ = a.Store.DeleteSourceHealth(r.Context(), key)
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "delete", "source", key, "Source removed", nil)
	a.redirectAfterAction(w, r, "/sources", "success", "Source removed")
}

func (a *App) SwitchTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	session, ok := a.currentSession(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if _, err := a.Store.GetMembership(r.Context(), session.UserID, r.FormValue("tenant_id")); err != nil {
		http.Error(w, "forbidden", 403)
		return
	}
	session.TenantID = r.FormValue("tenant_id")
	_ = auth.SetSessionCookie(w, a.Config.SecretKey, session, a.Config.SecureCookies)
	dest := r.FormValue("return_to")
	if dest == "" {
		dest = "/"
	}
	http.Redirect(w, r, dest, 303)
}

func (a *App) AdminToggleUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	user, err := a.Store.GetUser(r.Context(), r.FormValue("user_id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	user.IsActive = !user.IsActive
	_ = a.Store.UpsertUser(r.Context(), user)
	http.Redirect(w, r, "/admin/users", 303)
}

func (a *App) AdminResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	user, err := a.Store.GetUser(r.Context(), r.FormValue("user_id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	password := r.FormValue("new_password")
	if password == "" {
		password = "Reset!2026Pass"
	}
	if err := auth.StrongEnoughPassword(password); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	salt, hash, _ := auth.HashPassword(password)
	user.PasswordSalt = salt
	user.PasswordHash = hash
	_ = a.Store.UpsertUser(r.Context(), user)
	http.Redirect(w, r, "/admin/users", 303)
}

func (a *App) AdminUpsertMembership(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = a.Store.UpsertMembership(r.Context(), models.Membership{ID: r.FormValue("id"), UserID: r.FormValue("user_id"), TenantID: r.FormValue("tenant_id"), Role: models.Role(r.FormValue("role")), Responsibilities: r.FormValue("responsibilities")})
	http.Redirect(w, r, "/admin/users", 303)
}

func (a *App) AdminDeleteMembership(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = a.Store.DeleteMembership(r.Context(), r.FormValue("id"))
	http.Redirect(w, r, "/admin/users", 303)
}
