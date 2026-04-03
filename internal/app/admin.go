package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tenderhub-za/internal/auth"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
)

type SourceAdminItem struct {
	Config    models.SourceConfig
	Health    models.SourceHealth
	RecentRun models.SyncRun
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
	if isTenantScopedAdmin(m.Role) {
		allowedTenantIDs := map[string]bool{t.ID: true}
		filteredMemberships := make([]models.Membership, 0, len(memberships))
		visibleUserIDs := map[string]bool{}
		for _, membership := range memberships {
			if !allowedTenantIDs[membership.TenantID] {
				continue
			}
			filteredMemberships = append(filteredMemberships, membership)
			visibleUserIDs[membership.UserID] = true
		}
		filteredUsers := make([]models.User, 0, len(users))
		for _, user := range users {
			if visibleUserIDs[user.ID] {
				filteredUsers = append(filteredUsers, user)
			}
		}
		users = filteredUsers
		tenants = []models.Tenant{t}
		memberships = filteredMemberships
	}
	a.render(w, r, "admin_users.html", map[string]any{"Title": "Users", "User": u, "Tenant": t, "Items": users, "Tenants": tenants, "Memberships": memberships})
}

func (a *App) AdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	currentUser, currentTenant, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	username := normalizeUsername(r.FormValue("username"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	email := normalizeEmail(r.FormValue("email"))
	password := r.FormValue("password")
	tenantID := strings.TrimSpace(r.FormValue("tenant_id"))
	role := models.Role(strings.TrimSpace(r.FormValue("role")))
	responsibilities := strings.TrimSpace(r.FormValue("responsibilities"))
	if username == "" || displayName == "" || email == "" || tenantID == "" {
		http.Error(w, "username, display name, email, and tenant are required", 400)
		return
	}
	if !validEmailAddress(email) {
		http.Error(w, "invalid email address", 400)
		return
	}
	if !isValidRole(role) {
		http.Error(w, "invalid role", 400)
		return
	}
	if !canAssignManagedRole(m.Role, role) {
		http.Error(w, "forbidden", 403)
		return
	}
	if isTenantScopedAdmin(m.Role) && tenantID != currentTenant.ID {
		http.Error(w, "forbidden", 403)
		return
	}
	if _, err := a.Store.GetTenant(r.Context(), tenantID); err != nil {
		a.notFound(w, r, "tenant not found", err)
		return
	}
	users, err := a.Store.ListUsers(r.Context())
	if err != nil {
		a.serverError(w, r, "unable to list users", err)
		return
	}
	if hasUserWithUsername(users, username) {
		http.Error(w, "username already exists", 409)
		return
	}
	if hasUserWithEmail(users, email) {
		http.Error(w, "email already exists", 409)
		return
	}
	if err := auth.StrongEnoughPassword(password); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	salt, hash, err := auth.HashPassword(password)
	if err != nil {
		a.serverError(w, r, "unable to hash password", err)
		return
	}
	if err := a.persistUser(r.Context(), models.User{Username: username, DisplayName: displayName, Email: email, PasswordSalt: salt, PasswordHash: hash, IsActive: true}); err != nil {
		a.serverError(w, r, "unable to save user", err)
		return
	}
	user, err := a.Store.GetUserByUsername(r.Context(), username)
	if err != nil {
		a.serverError(w, r, "unable to load created user", err)
		return
	}
	if err := a.Store.UpsertMembership(r.Context(), models.Membership{UserID: user.ID, TenantID: tenantID, Role: role, Responsibilities: responsibilities}); err != nil {
		a.serverError(w, r, "unable to save membership", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: currentUser, Tenant: currentTenant, Member: m}, "create", "user", user.ID, "User created", map[string]string{"username": username, "role": string(role), "tenant_id": tenantID})
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
	currentUser, currentTenant, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canManageTenants(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	slug := normalizeTenantSlug(r.FormValue("slug"), name)
	if name == "" {
		http.Error(w, "tenant name is required", 400)
		return
	}
	if slug == "" {
		http.Error(w, "tenant slug is required", 400)
		return
	}
	tenants, err := a.Store.ListTenants(r.Context())
	if err != nil {
		a.serverError(w, r, "unable to list tenants", err)
		return
	}
	if hasTenantWithSlug(tenants, slug) {
		http.Error(w, "tenant slug already exists", 409)
		return
	}
	if err := a.Store.UpsertTenant(r.Context(), models.Tenant{Name: name, Slug: slug}); err != nil {
		a.serverError(w, r, "unable to save tenant", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: currentUser, Tenant: currentTenant, Member: m}, "create", "tenant", slug, "Tenant created", map[string]string{"name": name, "slug": slug})
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
	syncRuns, _ := a.Store.ListSyncRuns(r.Context())
	healthByKey := map[string]models.SourceHealth{}
	for _, item := range health {
		healthByKey[item.SourceKey] = item
	}
	runByKey := map[string]models.SyncRun{}
	for _, run := range syncRuns {
		if _, exists := runByKey[run.SourceKey]; !exists {
			runByKey[run.SourceKey] = run
		}
	}
	items := make([]SourceAdminItem, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, SourceAdminItem{Config: cfg, Health: healthByKey[cfg.Key], RecentRun: runByKey[cfg.Key]})
	}
	settings := a.loadSourceScheduleSettings(r.Context())
	a.render(w, r, "sources.html", map[string]any{
		"Title":            "Sources",
		"User":             u,
		"Tenant":           t,
		"Items":            items,
		"RecentRuns":       syncRuns,
		"ScheduleSettings": settings,
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
	normalizedFeedURL, err := normalizeSafeOutboundURL(feedURL)
	if err != nil {
		http.Error(w, "feed url must be a public http or https endpoint", 400)
		return
	}
	sourceType := strings.TrimSpace(r.FormValue("type"))
	if sourceType == "" {
		sourceType = source.TypeJSONFeed
	}
	if !source.IsSupportedType(sourceType) {
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
	cfg := models.SourceConfig{
		Key:                 key,
		Name:                name,
		Type:                sourceType,
		FeedURL:             normalizedFeedURL,
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    true,
	}
	if err := a.Store.UpsertSourceConfig(r.Context(), cfg); err != nil {
		a.serverError(w, r, "unable to save source configuration", err)
		return
	}
	settings := a.loadSourceScheduleSettings(r.Context())
	now := time.Now().UTC()
	if err := a.Store.UpsertSourceHealth(r.Context(), models.SourceHealth{
		SourceKey:            key,
		LastStatus:           "configured",
		LastMessage:          "Waiting for the next source check.",
		LastItemCount:        0,
		NextScheduledCheckAt: source.InitialNextCheckAt(now, cfg, settings),
		HealthStatus:         source.ComputeHealthStatus(cfg, settings, models.SourceHealth{}),
	}); err != nil {
		a.serverError(w, r, "unable to initialize source health", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "create", "source", key, "Source added", map[string]string{"name": name, "type": sourceType})
	a.redirectAfterAction(w, r, "/sources", "success", "Source added")
}

func (a *App) AdminUpdateSource(w http.ResponseWriter, r *http.Request) {
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
	key := source.NormalizeKey(r.FormValue("key"))
	if key == "" {
		http.Error(w, "missing source key", 400)
		return
	}
	cfg, err := a.Store.GetSourceConfig(r.Context(), key)
	if err != nil {
		a.notFound(w, r, "source not found", err)
		return
	}
	interval := 0
	if raw := strings.TrimSpace(r.FormValue("interval_minutes")); raw != "" {
		interval, err = strconv.Atoi(raw)
		if err != nil || interval < 0 {
			http.Error(w, "interval must be a whole number of minutes", 400)
			return
		}
	}
	cfg.Enabled = r.FormValue("enabled") == "on"
	cfg.ManualChecksEnabled = true
	cfg.AutoCheckEnabled = r.FormValue("auto_check_enabled") == "on"
	cfg.IntervalMinutes = interval
	if err := a.Store.UpsertSourceConfig(r.Context(), cfg); err != nil {
		a.serverError(w, r, "unable to update source settings", err)
		return
	}
	health, _ := a.Store.GetSourceHealth(r.Context(), key)
	settings := a.loadSourceScheduleSettings(r.Context())
	now := time.Now().UTC()
	if source.ShouldAutoCheck(cfg, settings) {
		health.NextScheduledCheckAt = source.InitialNextCheckAt(now, cfg, settings)
	} else {
		health.NextScheduledCheckAt = time.Time{}
	}
	health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
	if err := a.Store.UpsertSourceHealth(r.Context(), health); err != nil {
		a.serverError(w, r, "unable to update source health", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "update", "source", key, "Source settings updated", map[string]string{
		"enabled":            strconv.FormatBool(cfg.Enabled),
		"auto_check_enabled": strconv.FormatBool(cfg.AutoCheckEnabled),
		"interval_minutes":   strconv.Itoa(cfg.IntervalMinutes),
	})
	a.redirectAfterAction(w, r, "/sources", "success", "Source settings updated")
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
	if err := a.Store.DeleteSourceConfig(r.Context(), id); err != nil {
		a.serverError(w, r, "unable to remove source configuration", err)
		return
	}
	if err := a.Store.DeleteSourceHealth(r.Context(), key); err != nil {
		a.serverError(w, r, "unable to remove source health", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "delete", "source", key, "Source removed", nil)
	a.redirectAfterAction(w, r, "/sources", "success", "Source removed")
}

func (a *App) AdminUpdateSourceSchedule(w http.ResponseWriter, r *http.Request) {
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
	interval, err := strconv.Atoi(strings.TrimSpace(r.FormValue("default_interval_minutes")))
	if err != nil || interval <= 0 {
		http.Error(w, "default interval must be a positive whole number of minutes", 400)
		return
	}
	settings := models.SourceScheduleSettings{
		ID:                     "global",
		DefaultIntervalMinutes: interval,
		Paused:                 r.FormValue("paused") == "on",
	}
	if err := a.Store.UpsertSourceScheduleSettings(r.Context(), settings); err != nil {
		a.serverError(w, r, "unable to save source schedule", err)
		return
	}
	if err := a.recalculateSourceSchedules(r.Context(), time.Now().UTC()); err != nil {
		a.serverError(w, r, "unable to recalculate source schedules", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "update", "source_schedule", "global", "Global source schedule updated", map[string]string{
		"default_interval_minutes": strconv.Itoa(interval),
		"paused":                   strconv.FormatBool(settings.Paused),
	})
	a.redirectAfterAction(w, r, "/sources", "success", "Global source schedule updated")
}

func (a *App) AdminTriggerSourceCheck(w http.ResponseWriter, r *http.Request) {
	a.triggerSourceChecks(w, r, []string{source.NormalizeKey(r.FormValue("key"))}, "Source check queued")
}

func (a *App) AdminTriggerSelectedSourceChecks(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.badRequest(w, r, "invalid form submission", err)
		return
	}
	a.triggerSourceChecks(w, r, r.Form["source_keys"], "Selected source checks queued")
}

func (a *App) AdminTriggerAllSourceChecks(w http.ResponseWriter, r *http.Request) {
	configs, _ := a.Store.ListSourceConfigs(r.Context())
	keys := make([]string, 0, len(configs))
	for _, cfg := range configs {
		if cfg.Enabled {
			keys = append(keys, cfg.Key)
		}
	}
	a.triggerSourceChecks(w, r, keys, "All enabled source checks queued")
}

func (a *App) SourceStatusJSON(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	configs, _ := a.Store.ListSourceConfigs(r.Context())
	health, _ := a.Store.ListSourceHealth(r.Context())
	runs, _ := a.Store.ListSyncRuns(r.Context())
	settings := a.loadSourceScheduleSettings(r.Context())
	payload := map[string]any{
		"settings": settings,
		"configs":  configs,
		"health":   health,
		"runs":     runs,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (a *App) triggerSourceChecks(w http.ResponseWriter, r *http.Request, rawKeys []string, successMessage string) {
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
	queued, err := a.queueSourceChecks(r.Context(), rawKeys)
	if err != nil {
		a.serverError(w, r, "unable to queue source checks", err)
		return
	}
	if queued == 0 {
		a.redirectAfterAction(w, r, "/sources", "warning", "No eligible sources were queued")
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "trigger", "source_check", strings.Join(rawKeys, ","), successMessage, map[string]string{"queued": strconv.Itoa(queued)})
	a.redirectAfterAction(w, r, "/sources", "success", successMessage)
}

func (a *App) queueSourceChecks(ctx context.Context, rawKeys []string) (int, error) {
	configs, err := a.Store.ListSourceConfigs(ctx)
	if err != nil {
		return 0, err
	}
	settings := a.loadSourceScheduleSettings(ctx)
	selected := map[string]bool{}
	for _, key := range rawKeys {
		key = source.NormalizeKey(key)
		if key != "" {
			selected[key] = true
		}
	}
	now := time.Now().UTC()
	queued := 0
	for _, cfg := range configs {
		if !selected[cfg.Key] || !cfg.Enabled || !cfg.ManualChecksEnabled {
			continue
		}
		health, _ := a.Store.GetSourceHealth(ctx, cfg.Key)
		health.SourceKey = cfg.Key
		health.PendingManualCheck = true
		if !health.Running {
			health.LastStatus = "queued"
			health.LastMessage = "Manual check queued."
		} else {
			health.LastMessage = "Manual check queued to run after the current check."
		}
		if health.NextScheduledCheckAt.IsZero() && source.ShouldAutoCheck(cfg, settings) {
			health.NextScheduledCheckAt = source.InitialNextCheckAt(now, cfg, settings)
		}
		health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
		if err := a.Store.UpsertSourceHealth(ctx, health); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}

func (a *App) recalculateSourceSchedules(ctx context.Context, now time.Time) error {
	configs, err := a.Store.ListSourceConfigs(ctx)
	if err != nil {
		return err
	}
	settings := a.loadSourceScheduleSettings(ctx)
	for _, cfg := range configs {
		health, _ := a.Store.GetSourceHealth(ctx, cfg.Key)
		health.SourceKey = cfg.Key
		if source.ShouldAutoCheck(cfg, settings) {
			health.NextScheduledCheckAt = source.InitialNextCheckAt(now, cfg, settings)
		} else {
			health.NextScheduledCheckAt = time.Time{}
		}
		health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
		if err := a.Store.UpsertSourceHealth(ctx, health); err != nil {
			return err
		}
	}
	return nil
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
	tenantID := strings.TrimSpace(r.FormValue("tenant_id"))
	if tenantID == "" {
		http.Error(w, "missing tenant id", 400)
		return
	}
	if _, err := a.Store.GetMembership(r.Context(), session.UserID, tenantID); err != nil {
		http.Error(w, "forbidden", 403)
		return
	}
	session.TenantID = tenantID
	if err := auth.SetSessionCookie(w, a.Config.SecretKey, session, a.Config.SecureCookies); err != nil {
		a.serverError(w, r, "unable to update session", err)
		return
	}
	if user, tenant, member, ok := a.currentUserTenant(r); ok {
		a.auditAction(r.Context(), actionContext{User: user, Tenant: tenant, Member: member}, "switch", "tenant", tenantID, "Workspace switched", nil)
	}
	http.Redirect(w, r, safeReturnTarget(r.FormValue("return_to"), "/").String(), 303)
}

func (a *App) AdminToggleUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	currentUser, currentTenant, m, ok := a.currentUserTenant(r)
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
		a.notFound(w, r, "user not found", err)
		return
	}
	if !a.canManageUser(r.Context(), m.Role, currentTenant.ID, user.ID) {
		http.Error(w, "forbidden", 403)
		return
	}
	user.IsActive = !user.IsActive
	if err := a.persistUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to update user", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: currentUser, Tenant: currentTenant, Member: m}, "update", "user", user.ID, "User activation updated", map[string]string{"active": strconv.FormatBool(user.IsActive)})
	http.Redirect(w, r, "/admin/users", 303)
}

func (a *App) AdminResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	currentUser, currentTenant, m, ok := a.currentUserTenant(r)
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
		a.notFound(w, r, "user not found", err)
		return
	}
	if !a.canManageUser(r.Context(), m.Role, currentTenant.ID, user.ID) {
		http.Error(w, "forbidden", 403)
		return
	}
	password := r.FormValue("new_password")
	if strings.TrimSpace(password) == "" {
		http.Error(w, "new password is required", 400)
		return
	}
	if err := auth.StrongEnoughPassword(password); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	salt, hash, err := auth.HashPassword(password)
	if err != nil {
		a.serverError(w, r, "unable to hash password", err)
		return
	}
	user.PasswordSalt = salt
	user.PasswordHash = hash
	if err := a.persistUser(r.Context(), user); err != nil {
		a.serverError(w, r, "unable to update user password", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: currentUser, Tenant: currentTenant, Member: m}, "update", "user_password", user.ID, "User password reset", nil)
	http.Redirect(w, r, "/admin/users", 303)
}

func (a *App) AdminUpsertMembership(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	currentUser, currentTenant, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	userID := strings.TrimSpace(r.FormValue("user_id"))
	tenantID := strings.TrimSpace(r.FormValue("tenant_id"))
	role := models.Role(strings.TrimSpace(r.FormValue("role")))
	if userID == "" || tenantID == "" {
		http.Error(w, "user and tenant are required", 400)
		return
	}
	if !isValidRole(role) {
		http.Error(w, "invalid role", 400)
		return
	}
	if _, err := a.Store.GetUser(r.Context(), userID); err != nil {
		a.notFound(w, r, "user not found", err)
		return
	}
	if _, err := a.Store.GetTenant(r.Context(), tenantID); err != nil {
		a.notFound(w, r, "tenant not found", err)
		return
	}
	if !canAssignManagedRole(m.Role, role) {
		http.Error(w, "forbidden", 403)
		return
	}
	if isTenantScopedAdmin(m.Role) && tenantID != currentTenant.ID {
		http.Error(w, "forbidden", 403)
		return
	}
	if err := a.Store.UpsertMembership(r.Context(), models.Membership{
		ID:               r.FormValue("id"),
		UserID:           userID,
		TenantID:         tenantID,
		Role:             role,
		Responsibilities: strings.TrimSpace(r.FormValue("responsibilities")),
	}); err != nil {
		a.serverError(w, r, "unable to save membership", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: currentUser, Tenant: currentTenant, Member: m}, "update", "membership", userID+":"+tenantID, "Membership updated", map[string]string{"role": string(role)})
	http.Redirect(w, r, "/admin/users", 303)
}

func (a *App) AdminDeleteMembership(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	currentUser, currentTenant, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canAdminUsers(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Error(w, "membership id is required", 400)
		return
	}
	memberships, err := a.Store.ListAllMemberships(r.Context())
	if err != nil {
		a.serverError(w, r, "unable to load memberships", err)
		return
	}
	var target models.Membership
	found := false
	for _, membership := range memberships {
		if membership.ID == id {
			target = membership
			found = true
			break
		}
	}
	if !found {
		a.notFound(w, r, "membership not found", nil)
		return
	}
	if isTenantScopedAdmin(m.Role) && target.TenantID != currentTenant.ID {
		http.Error(w, "forbidden", 403)
		return
	}
	if target.UserID == currentUser.ID && target.TenantID == currentTenant.ID {
		http.Error(w, "cannot remove your active membership", http.StatusBadRequest)
		return
	}
	if err := a.Store.DeleteMembership(r.Context(), id); err != nil {
		a.serverError(w, r, "unable to delete membership", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: currentUser, Tenant: currentTenant, Member: m}, "delete", "membership", target.UserID+":"+target.TenantID, "Membership removed", map[string]string{"role": string(target.Role)})
	http.Redirect(w, r, "/admin/users", 303)
}
