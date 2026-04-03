package app

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"os"
	"sort"
	"strconv"
	"strings"
	"tenderhub-za/internal/auth"
	"tenderhub-za/internal/extract"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
	"tenderhub-za/internal/store"
	"tenderhub-za/internal/worker"
	"time"
)

type Config struct {
	AppEnv                                                              string
	AppAddr, DataPath, SecretKey, ExtractorURL                          string
	SecureCookies, LowMemoryMode, AnalyticsEnabled                      bool
	BootstrapSyncOnStartup                                              bool
	SessionHours, WorkerSyncMinutes, WorkerLoopSeconds                  int
	BootstrapAdminUsername, BootstrapAdminEmail, BootstrapAdminPassword string
}
type App struct {
	Config    Config
	Store     store.Store
	Templates map[string]*template.Template
	Server    *http.Server
	Sources   source.Registry
	Extractor *extract.Client
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func atoi(s string, d int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return v
}

func boolenv(k string, d bool) bool {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func normalizeAppEnv(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "dev", "development", "local", "test":
		return "development"
	case "prod", "production":
		return "production"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func usesWeakSecret(secret string) bool {
	secret = strings.TrimSpace(secret)
	lower := strings.ToLower(secret)
	return len(secret) < 32 || lower == "change-me-now" || lower == "local-dev-secret-change-me" || strings.Contains(lower, "change-me") || strings.Contains(lower, "replace-with")
}

func validateConfig(cfg Config) error {
	if cfg.BootstrapAdminPassword != "" {
		if err := auth.StrongEnoughPassword(cfg.BootstrapAdminPassword); err != nil {
			return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD is not strong enough: %w", err)
		}
	}
	if cfg.AppEnv != "production" {
		return nil
	}
	if usesWeakSecret(cfg.SecretKey) {
		return errors.New("SECRET_KEY must be a strong non-default value in production")
	}
	if !cfg.SecureCookies {
		return errors.New("SECURE_COOKIES must be true in production")
	}
	return nil
}

func (c Config) ShowDemoCredentials() bool {
	return c.AppEnv != "production" && strings.TrimSpace(c.BootstrapAdminPassword) == ""
}

func New() (*App, error) {
	appEnv := normalizeAppEnv(getenv("APP_ENV", "development"))
	cfg := Config{
		AppEnv:                 appEnv,
		AppAddr:                getenv("APP_ADDR", ":8080"),
		DataPath:               getenv("DATA_PATH", "./data/store.db"),
		SecretKey:              getenv("SECRET_KEY", "change-me-now"),
		SecureCookies:          boolenv("SECURE_COOKIES", false),
		LowMemoryMode:          boolenv("LOW_MEMORY_MODE", true),
		AnalyticsEnabled:       boolenv("ANALYTICS_ENABLED", false),
		BootstrapSyncOnStartup: boolenv("BOOTSTRAP_SYNC_ON_STARTUP", appEnv != "production"),
		ExtractorURL:           getenv("EXTRACTOR_URL", "http://extractor:9090"),
		SessionHours:           atoi(getenv("SESSION_HOURS", "12"), 12),
		WorkerSyncMinutes:      atoi(getenv("WORKER_SYNC_MINUTES", "360"), 360),
		WorkerLoopSeconds:      atoi(getenv("WORKER_LOOP_SECONDS", "30"), 30),
		BootstrapAdminUsername: getenv("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		BootstrapAdminEmail:    getenv("BOOTSTRAP_ADMIN_EMAIL", "admin@localhost"),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	st, err := store.NewSQLiteStore(cfg.DataPath)
	if err != nil {
		return nil, err
	}
	tpl, err := parseTemplates()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	a := &App{Config: cfg, Store: st, Templates: tpl, Sources: source.NewRegistry(), Extractor: extract.New(cfg.ExtractorURL)}
	if err := a.seed(context.Background()); err != nil {
		_ = st.Close()
		return nil, err
	}
	a.Server = &http.Server{Addr: cfg.AppAddr, Handler: routes(a), ReadHeaderTimeout: 10 * time.Second}
	return a, nil
}
func (a *App) seed(ctx context.Context) error {
	users, _ := a.Store.ListUsers(ctx)
	seededUsers := len(users) == 0
	if seededUsers {
		password := a.Config.BootstrapAdminPassword
		if password == "" {
			if a.Config.AppEnv == "production" {
				return errors.New("BOOTSTRAP_ADMIN_PASSWORD must be set before starting with an empty production database")
			}
			password = "TenderHub!2026"
		}
		salt, hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		if err := a.Store.UpsertUser(ctx, models.User{Username: a.Config.BootstrapAdminUsername, DisplayName: "Platform Admin", Email: a.Config.BootstrapAdminEmail, PasswordSalt: salt, PasswordHash: hash, IsActive: true}); err != nil {
			return err
		}
		users, _ = a.Store.ListUsers(ctx)
		if len(users) == 0 {
			return errors.New("failed to seed user")
		}
		if err := a.Store.UpsertTenant(ctx, models.Tenant{Name: "Default Engineering Team", Slug: "default-engineering-team"}); err != nil {
			return err
		}
		tenants, _ := a.Store.ListTenants(ctx)
		if len(tenants) == 0 {
			return errors.New("failed to seed tenant")
		}
		_ = a.Store.UpsertMembership(ctx, models.Membership{UserID: users[0].ID, TenantID: tenants[0].ID, Role: models.RoleAdmin, Responsibilities: "Platform administration and portfolio oversight"})
	}
	if seededUsers {
		if err := a.ensureSourceConfigs(ctx); err != nil {
			return err
		}
	}
	if err := a.ensureSourceScheduleSettings(ctx, seededUsers); err != nil {
		return err
	}
	if err := a.ensureSourceHealthState(ctx); err != nil {
		return err
	}
	registry := a.mustLoadSourceRegistry(ctx)
	a.Sources = registry
	if seededUsers && a.Config.BootstrapSyncOnStartup {
		return a.syncSources(ctx, registry)
	}
	return nil
}

func (a *App) ensureSourceConfigs(ctx context.Context) error {
	configs, err := a.Store.ListSourceConfigs(ctx)
	if err != nil {
		return err
	}
	if len(configs) > 0 {
		return nil
	}
	for _, cfg := range source.DefaultConfigs(source.DefaultFeedURL()) {
		if err := a.Store.UpsertSourceConfig(ctx, cfg); err != nil {
			return err
		}
		_ = a.Store.UpsertSourceHealth(ctx, models.SourceHealth{
			SourceKey:     cfg.Key,
			LastStatus:    "configured",
			LastMessage:   "Waiting for the next sync cycle.",
			LastItemCount: 0,
		})
	}
	return nil
}

func (a *App) defaultSourceScheduleSettings() models.SourceScheduleSettings {
	return source.NormalizeScheduleSettings(models.SourceScheduleSettings{
		ID:                     "global",
		DefaultIntervalMinutes: a.Config.WorkerSyncMinutes,
	}, a.Config.WorkerSyncMinutes)
}

func (a *App) loadSourceScheduleSettings(ctx context.Context) models.SourceScheduleSettings {
	settings, err := a.Store.GetSourceScheduleSettings(ctx)
	if err != nil {
		return a.defaultSourceScheduleSettings()
	}
	return source.NormalizeScheduleSettings(settings, a.Config.WorkerSyncMinutes)
}

func (a *App) ensureSourceScheduleSettings(ctx context.Context, migrateLegacy bool) error {
	if _, err := a.Store.GetSourceScheduleSettings(ctx); err == nil {
		return nil
	}
	settings := a.defaultSourceScheduleSettings()
	if err := a.Store.UpsertSourceScheduleSettings(ctx, settings); err != nil {
		return err
	}
	if !migrateLegacy {
		configs, err := a.Store.ListSourceConfigs(ctx)
		if err != nil {
			return err
		}
		for _, cfg := range configs {
			cfg.ManualChecksEnabled = true
			if !cfg.AutoCheckEnabled {
				cfg.AutoCheckEnabled = true
			}
			if err := a.Store.UpsertSourceConfig(ctx, cfg); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) ensureSourceHealthState(ctx context.Context) error {
	configs, err := a.Store.ListSourceConfigs(ctx)
	if err != nil {
		return err
	}
	settings := a.loadSourceScheduleSettings(ctx)
	healthItems, err := a.Store.ListSourceHealth(ctx)
	if err != nil {
		return err
	}
	healthByKey := map[string]models.SourceHealth{}
	for _, item := range healthItems {
		item.Running = false
		healthByKey[item.SourceKey] = item
	}
	now := time.Now().UTC()
	for _, cfg := range configs {
		if !cfg.ManualChecksEnabled {
			cfg.ManualChecksEnabled = true
			if err := a.Store.UpsertSourceConfig(ctx, cfg); err != nil {
				return err
			}
		}
		health := healthByKey[cfg.Key]
		health.SourceKey = cfg.Key
		if health.LastStatus == "" {
			health.LastStatus = "configured"
		}
		if health.LastMessage == "" {
			health.LastMessage = "Waiting for the next source check."
		}
		if health.NextScheduledCheckAt.IsZero() {
			health.NextScheduledCheckAt = source.InitialNextCheckAt(now, cfg, settings)
		}
		health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
		if err := a.Store.UpsertSourceHealth(ctx, health); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) loadSourceRegistry(ctx context.Context) (source.Registry, error) {
	configs, err := a.Store.ListSourceConfigs(ctx)
	if err != nil {
		return source.Registry{}, err
	}
	return source.RegistryFromConfigs(configs), nil
}

func (a *App) mustLoadSourceRegistry(ctx context.Context) source.Registry {
	registry, err := a.loadSourceRegistry(ctx)
	if err != nil {
		return a.Sources
	}
	return registry
}

func (a *App) syncSources(ctx context.Context, registry source.Registry) error {
	settings := a.loadSourceScheduleSettings(ctx)
	for _, ad := range registry.Adapters {
		items, msg, err := ad.Fetch(ctx)
		status := "success"
		if err != nil {
			status = "failed"
			msg = err.Error()
		}
		now := time.Now().UTC()
		_ = a.Store.AddSyncRun(ctx, models.SyncRun{SourceKey: ad.Key(), StartedAt: now, FinishedAt: now, Status: status, Message: msg, Trigger: "startup", ItemCount: len(items)})
		cfg, _ := a.Store.GetSourceConfig(ctx, ad.Key())
		health, _ := a.Store.GetSourceHealth(ctx, ad.Key())
		health.SourceKey = ad.Key()
		health.LastSyncAt = now
		health.LastCheckedAt = now
		health.LastStatus = status
		health.LastMessage = msg
		health.LastItemCount = len(items)
		health.LastTrigger = "startup"
		if err != nil {
			health.ConsecutiveFailures++
		} else {
			health.ConsecutiveFailures = 0
			health.LastSuccessfulCheckAt = now
		}
		health.NextScheduledCheckAt = source.NextScheduledCheckAt(now, cfg, settings, health.ConsecutiveFailures, err == nil)
		health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
		_ = a.Store.UpsertSourceHealth(ctx, health)
		for _, t := range items {
			if t.DocumentStatus == "" {
				t.DocumentStatus = models.ExtractionQueued
			}
			_ = a.Store.UpsertTender(ctx, t)
			if t.DocumentURL != "" {
				_ = a.Store.QueueJob(ctx, models.ExtractionJob{TenderID: t.ID, DocumentURL: t.DocumentURL, State: models.ExtractionQueued})
			}
		}
	}
	return nil
}
func (a *App) currentSession(r *http.Request) (models.Session, bool) {
	c, err := r.Cookie("th_session")
	if err != nil {
		return models.Session{}, false
	}
	return auth.DecodeSession(a.Config.SecretKey, c.Value)
}
func (a *App) currentUserTenant(r *http.Request) (models.User, models.Tenant, models.Membership, bool) {
	sess, ok := a.currentSession(r)
	if !ok {
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	u, err := a.Store.GetUser(r.Context(), sess.UserID)
	if err != nil || !u.IsActive {
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	t, err := a.Store.GetTenant(r.Context(), sess.TenantID)
	if err != nil {
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	m, err := a.Store.GetMembership(r.Context(), sess.UserID, sess.TenantID)
	if err != nil {
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	return u, t, m, true
}

type TenantChoice struct {
	Tenant models.Tenant
	Role   models.Role
}

func (a *App) mustCSRF(r *http.Request) string { s, _ := a.currentSession(r); return s.CSRF }
func (a *App) userTenants(ctx context.Context, userID string) []TenantChoice {
	ms, _ := a.Store.ListMemberships(ctx, userID)
	out := []TenantChoice{}
	for _, m := range ms {
		t, err := a.Store.GetTenant(ctx, m.TenantID)
		if err == nil {
			out = append(out, TenantChoice{Tenant: t, Role: m.Role})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tenant.Name < out[j].Tenant.Name })
	return out
}
func (a *App) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	if _, exists := data["CurrentPath"]; !exists {
		data["CurrentPath"] = r.URL.Path
	}
	if u, t, m, ok := a.currentUserTenant(r); ok {
		if _, exists := data["User"]; !exists {
			data["User"] = u
		}
		if _, exists := data["Tenant"]; !exists {
			data["Tenant"] = t
		}
		if _, exists := data["CSRFToken"]; !exists {
			data["CSRFToken"] = a.mustCSRF(r)
		}
		if _, exists := data["UserTenants"]; !exists {
			data["UserTenants"] = a.userTenants(r.Context(), u.ID)
		}
		if _, exists := data["Member"]; !exists {
			data["Member"] = m
		}
		if _, exists := data["CanAdminUsers"]; !exists {
			data["CanAdminUsers"] = canAdminUsers(m.Role)
		}
		if _, exists := data["CanManageTenants"]; !exists {
			data["CanManageTenants"] = canManageTenants(m.Role)
		}
		if _, exists := data["CanManageAudit"]; !exists {
			data["CanManageAudit"] = canManageAudit(m.Role)
		}
		if _, exists := data["CanManageSources"]; !exists {
			data["CanManageSources"] = canManageSources(m.Role)
		}
	}
	if _, exists := data["Error"]; !exists {
		data["Error"] = r.URL.Query().Get("error")
	}
	if _, exists := data["Message"]; !exists {
		data["Message"] = r.URL.Query().Get("message")
	}
	if _, exists := data["ShowDemoCredentials"]; !exists {
		data["ShowDemoCredentials"] = a.Config.ShowDemoCredentials()
	}
	tpl, ok := a.Templates[name]
	if !ok {
		http.Error(w, fmt.Sprintf("template %s not configured", name), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "internal server error", 500)
	}
}
func (a *App) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, _, ok := a.currentUserTenant(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}
func (a *App) Healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if validator, ok := a.Store.(interface{ ValidateRuntime(context.Context) error }); ok {
		if err := validator.ValidateRuntime(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"ok":false,"error":"store_unhealthy"}`))
			return
		}
	}
	_, _ = w.Write([]byte(`{"ok":true}`))
}
func (a *App) WithSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		if a.Config.SecureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) ensureCSRF(r *http.Request) bool {
	s, ok := a.currentSession(r)
	return ok && r.FormValue("csrf_token") == s.CSRF
}
func (a *App) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.render(w, r, "login.html", map[string]any{"Title": "Login"})
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", 400)
		return
	}
	u, err := a.Store.GetUserByUsername(r.Context(), r.FormValue("username"))
	if err != nil || !u.IsActive {
		a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Invalid credentials"})
		return
	}
	if !u.LockedUntil.IsZero() && time.Now().Before(u.LockedUntil) {
		a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Account temporarily locked"})
		return
	}
	if !auth.VerifyPassword(r.FormValue("password"), u.PasswordSalt, u.PasswordHash) {
		u.FailedLogins++
		if u.FailedLogins >= 5 {
			u.LockedUntil = time.Now().Add(15 * time.Minute)
			u.FailedLogins = 0
		}
		_ = a.Store.UpsertUser(r.Context(), u)
		a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Invalid credentials"})
		return
	}
	if u.MFAEnabled && !auth.ValidateTOTP(u.MFASecret, r.FormValue("mfa_code"), time.Now()) {
		a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Invalid MFA code"})
		return
	}
	u.FailedLogins = 0
	u.LockedUntil = time.Time{}
	_ = a.Store.UpsertUser(r.Context(), u)
	memberships, _ := a.Store.ListMemberships(r.Context(), u.ID)
	if len(memberships) == 0 {
		http.Error(w, "No tenant membership assigned", 403)
		return
	}
	s := models.Session{UserID: u.ID, TenantID: memberships[0].TenantID, Expires: time.Now().Add(time.Duration(a.Config.SessionHours) * time.Hour), CSRF: auth.RandomString(32)}
	_ = auth.SetSessionCookie(w, a.Config.SecretKey, s, a.Config.SecureCookies)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *App) Logout(w http.ResponseWriter, r *http.Request) {
	auth.ClearSessionCookie(w, a.Config.SecureCookies)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (a *App) RunWorker() error {
	return worker.Runner{
		Store:      a.Store,
		Sources:    a.Sources,
		SourceLoad: a.loadSourceRegistry,
		Extractor:  a.Extractor,
		SyncEvery:  time.Duration(a.Config.WorkerSyncMinutes) * time.Minute,
		LoopEvery:  time.Duration(a.Config.WorkerLoopSeconds) * time.Second,
	}.Run(context.Background())
}
func init() { log.SetFlags(log.LstdFlags | log.Lshortfile) }
