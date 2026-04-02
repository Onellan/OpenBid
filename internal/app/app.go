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
	"tenderhub-za/internal/auth"
	"tenderhub-za/internal/extract"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
	"tenderhub-za/internal/store"
	"tenderhub-za/internal/worker"
	"time"
)

type Config struct {
	AppAddr, DataPath, SecretKey, ExtractorURL         string
	SecureCookies, LowMemoryMode, AnalyticsEnabled     bool
	SessionHours, WorkerSyncMinutes, WorkerLoopSeconds int
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
func New() (*App, error) {
	cfg := Config{AppAddr: getenv("APP_ADDR", ":8080"), DataPath: getenv("DATA_PATH", "./data/store.db"), SecretKey: getenv("SECRET_KEY", "change-me-now"), SecureCookies: getenv("SECURE_COOKIES", "false") == "true", LowMemoryMode: getenv("LOW_MEMORY_MODE", "true") == "true", AnalyticsEnabled: getenv("ANALYTICS_ENABLED", "false") == "true", ExtractorURL: getenv("EXTRACTOR_URL", "http://extractor:9090"), SessionHours: atoi(getenv("SESSION_HOURS", "12"), 12), WorkerSyncMinutes: atoi(getenv("WORKER_SYNC_MINUTES", "360"), 360), WorkerLoopSeconds: atoi(getenv("WORKER_LOOP_SECONDS", "30"), 30)}
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
		salt, hash, err := auth.HashPassword("TenderHub!2026")
		if err != nil {
			return err
		}
		if err := a.Store.UpsertUser(ctx, models.User{Username: "admin", DisplayName: "Platform Admin", Email: "admin@localhost", PasswordSalt: salt, PasswordHash: hash, IsActive: true}); err != nil {
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
	registry := a.mustLoadSourceRegistry(ctx)
	a.Sources = registry
	if seededUsers {
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
	for _, ad := range registry.Adapters {
		items, msg, err := ad.Fetch(ctx)
		status := "success"
		if err != nil {
			status = "failed"
			msg = err.Error()
		}
		now := time.Now().UTC()
		_ = a.Store.AddSyncRun(ctx, models.SyncRun{SourceKey: ad.Key(), StartedAt: now, FinishedAt: now, Status: status, Message: msg})
		_ = a.Store.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: ad.Key(), LastSyncAt: now, LastStatus: status, LastMessage: msg, LastItemCount: len(items)})
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
	if err != nil {
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
	tpl, ok := a.Templates[name]
	if !ok {
		http.Error(w, fmt.Sprintf("template %s not configured", name), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
func (a *App) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.currentSession(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}
func (a *App) Healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
func (a *App) WithSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' https://unpkg.com; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'")
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
		http.Error(w, err.Error(), 400)
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
