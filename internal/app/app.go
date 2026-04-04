package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"

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
	AppEnv                                                                 string
	AppAddr, DataPath, SecretKey, ExtractorURL, TreasuryFeedURL            string
	SecureCookies, LowMemoryMode, AnalyticsEnabled, BootstrapSyncOnStartup bool
	SessionHours, WorkerSyncMinutes, WorkerLoopSeconds                     int
	LoginRateLimitWindowSeconds, LoginRateLimitMaxAttempts                 int
	BootstrapAdminUsername, BootstrapAdminEmail, BootstrapAdminPassword    string
}
type App struct {
	Config           Config
	Store            store.Store
	Templates        map[string]*template.Template
	Server           *http.Server
	Sources          source.Registry
	Extractor        *extract.Client
	StartedAt        time.Time
	LoginRateLimiter *LoginRateLimiter
}

func atoi(s string, d int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return v
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
	if strings.TrimSpace(cfg.AppAddr) == "" {
		return errors.New("APP_ADDR must not be empty")
	}
	if strings.TrimSpace(cfg.DataPath) == "" {
		return errors.New("DATA_PATH must not be empty")
	}
	if cfg.SessionHours <= 0 {
		return errors.New("SESSION_HOURS must be greater than zero")
	}
	if cfg.WorkerSyncMinutes <= 0 {
		return errors.New("WORKER_SYNC_MINUTES must be greater than zero")
	}
	if cfg.WorkerLoopSeconds <= 0 {
		return errors.New("WORKER_LOOP_SECONDS must be greater than zero")
	}
	if cfg.LoginRateLimitWindowSeconds <= 0 {
		return errors.New("LOGIN_RATE_LIMIT_WINDOW_SECONDS must be greater than zero")
	}
	if cfg.LoginRateLimitMaxAttempts <= 0 {
		return errors.New("LOGIN_RATE_LIMIT_MAX_ATTEMPTS must be greater than zero")
	}
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
	cfg, err := loadConfigFromEnv()
	if err != nil {
		return nil, err
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
	a := &App{
		Config: cfg, Store: st, Templates: tpl, Sources: source.NewRegistry(), Extractor: extract.New(cfg.ExtractorURL), StartedAt: time.Now().UTC(),
		LoginRateLimiter: NewLoginRateLimiter(time.Duration(cfg.LoginRateLimitWindowSeconds)*time.Second, cfg.LoginRateLimitMaxAttempts),
	}
	if err := a.seed(context.Background()); err != nil {
		_ = st.Close()
		return nil, err
	}
	a.Server = &http.Server{
		Addr:              cfg.AppAddr,
		Handler:           routes(a),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
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
		if err := a.persistUser(ctx, models.User{Username: a.Config.BootstrapAdminUsername, DisplayName: "Platform Admin", Email: a.Config.BootstrapAdminEmail, PasswordSalt: salt, PasswordHash: hash, IsActive: true}); err != nil {
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
		if err := a.Store.UpsertMembership(ctx, models.Membership{UserID: users[0].ID, TenantID: tenants[0].ID, Role: models.RoleAdmin, Responsibilities: "Platform administration and portfolio oversight"}); err != nil {
			return err
		}
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
	for _, cfg := range source.DefaultConfigs(a.Config.TreasuryFeedURL) {
		if err := a.Store.UpsertSourceConfig(ctx, cfg); err != nil {
			return err
		}
		if err := a.Store.UpsertSourceHealth(ctx, models.SourceHealth{
			SourceKey:     cfg.Key,
			LastStatus:    "configured",
			LastMessage:   "Waiting for the next sync cycle.",
			LastItemCount: 0,
		}); err != nil {
			return err
		}
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
		if err := a.Store.AddSyncRun(ctx, models.SyncRun{SourceKey: ad.Key(), StartedAt: now, FinishedAt: now, Status: status, Message: msg, Trigger: "startup", ItemCount: len(items)}); err != nil {
			return fmt.Errorf("record startup sync run for %s: %w", ad.Key(), err)
		}
		cfg, err := a.Store.GetSourceConfig(ctx, ad.Key())
		if err != nil {
			return fmt.Errorf("load source config for %s: %w", ad.Key(), err)
		}
		health, err := a.Store.GetSourceHealth(ctx, ad.Key())
		if err != nil {
			health = models.SourceHealth{SourceKey: ad.Key()}
		}
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
		if err := a.Store.UpsertSourceHealth(ctx, health); err != nil {
			return fmt.Errorf("update source health for %s: %w", ad.Key(), err)
		}
		for _, t := range items {
			if t.DocumentStatus == "" {
				t.DocumentStatus = models.ExtractionQueued
			}
			if err := a.Store.UpsertTender(ctx, t); err != nil {
				return fmt.Errorf("persist tender %s from %s: %w", t.ID, ad.Key(), err)
			}
			if t.DocumentURL != "" {
				if err := a.Store.QueueJob(ctx, models.ExtractionJob{TenderID: t.ID, DocumentURL: t.DocumentURL, State: models.ExtractionQueued}); err != nil {
					return fmt.Errorf("queue extraction for tender %s from %s: %w", t.ID, ad.Key(), err)
				}
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
	decoded, ok := auth.DecodeSession(a.Config.SecretKey, c.Value)
	if !ok || strings.TrimSpace(decoded.ID) == "" {
		return models.Session{}, false
	}
	session, err := a.Store.GetSession(r.Context(), decoded.ID)
	if err != nil {
		return models.Session{}, false
	}
	return session, true
}
func (a *App) currentUserTenant(r *http.Request) (models.User, models.Tenant, models.Membership, bool) {
	sess, ok := a.currentSession(r)
	if !ok {
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	u, err := a.Store.GetUser(r.Context(), sess.UserID)
	if err != nil || !u.IsActive {
		if strings.TrimSpace(sess.ID) != "" {
			_ = a.Store.DeleteSession(r.Context(), sess.ID)
		}
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	if sess.SessionVersion != u.SessionVersion {
		_ = a.Store.DeleteSession(r.Context(), sess.ID)
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	u, err = a.hydrateUserSensitiveFields(r.Context(), u)
	if err != nil {
		log.Printf("failed to hydrate user %s secrets: %v", u.ID, err)
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	t, err := a.Store.GetTenant(r.Context(), sess.TenantID)
	if err != nil {
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	m, err := a.Store.GetMembership(r.Context(), sess.UserID, sess.TenantID)
	if err != nil {
		_ = a.Store.DeleteSession(r.Context(), sess.ID)
		return models.User{}, models.Tenant{}, models.Membership{}, false
	}
	return u, t, m, true
}

func (a *App) hydrateUserSensitiveFields(ctx context.Context, user models.User) (models.User, error) {
	if strings.TrimSpace(user.MFASecret) == "" {
		return a.hydrateUserRecoveryCodes(ctx, user)
	}
	decryptedSecret, legacyPlaintext, err := auth.DecryptSensitiveValue(a.Config.SecretKey, user.MFASecret)
	if err != nil {
		return models.User{}, err
	}
	user.MFASecret = decryptedSecret
	if legacyPlaintext {
		protectedSecret, err := auth.EncryptSensitiveValue(a.Config.SecretKey, decryptedSecret)
		if err != nil {
			return models.User{}, err
		}
		storedUser := user
		storedUser.MFASecret = protectedSecret
		if err := a.Store.UpsertUser(ctx, storedUser); err != nil {
			return models.User{}, err
		}
	}
	return a.hydrateUserRecoveryCodes(ctx, user)
}

func (a *App) hydrateUserRecoveryCodes(ctx context.Context, user models.User) (models.User, error) {
	if len(user.RecoveryCodes) == 0 {
		return user, nil
	}
	decryptedCodes := make([]string, 0, len(user.RecoveryCodes))
	legacyPlaintext := false
	for _, code := range user.RecoveryCodes {
		decryptedCode, wasLegacyPlaintext, err := auth.DecryptSensitiveValue(a.Config.SecretKey, code)
		if err != nil {
			return models.User{}, err
		}
		if strings.TrimSpace(decryptedCode) == "" {
			continue
		}
		if wasLegacyPlaintext {
			legacyPlaintext = true
		}
		decryptedCodes = append(decryptedCodes, decryptedCode)
	}
	user.RecoveryCodes = decryptedCodes
	if legacyPlaintext {
		if err := a.persistUser(ctx, user); err != nil {
			return models.User{}, err
		}
	}
	return user, nil
}

func (a *App) persistUser(ctx context.Context, user models.User) error {
	if strings.TrimSpace(user.MFASecret) != "" {
		decryptedSecret, _, err := auth.DecryptSensitiveValue(a.Config.SecretKey, user.MFASecret)
		if err != nil {
			return err
		}
		protectedSecret, err := auth.EncryptSensitiveValue(a.Config.SecretKey, decryptedSecret)
		if err != nil {
			return err
		}
		user.MFASecret = protectedSecret
	}
	if len(user.RecoveryCodes) > 0 {
		protectedCodes := make([]string, 0, len(user.RecoveryCodes))
		for _, code := range user.RecoveryCodes {
			if strings.TrimSpace(code) == "" {
				continue
			}
			decryptedCode, _, err := auth.DecryptSensitiveValue(a.Config.SecretKey, code)
			if err != nil {
				return err
			}
			protectedCode, err := auth.EncryptSensitiveValue(a.Config.SecretKey, decryptedCode)
			if err != nil {
				return err
			}
			protectedCodes = append(protectedCodes, protectedCode)
		}
		user.RecoveryCodes = protectedCodes
	}
	return a.Store.UpsertUser(ctx, user)
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
	a.renderStatus(w, r, http.StatusOK, name, data)
}

func (a *App) renderStatus(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
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
		if _, exists := data["CanViewPlatformHealth"]; !exists {
			data["CanViewPlatformHealth"] = canViewPlatformHealth(m.Role)
		}
		if _, exists := data["CanEditWorkspace"]; !exists {
			data["CanEditWorkspace"] = canEditWorkspace(m.Role)
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
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template render failed for %s on %s: %v", name, r.URL.Path, err)
		http.Error(w, "internal server error", 500)
	}
}

func (a *App) badRequest(w http.ResponseWriter, r *http.Request, message string, err error) {
	if err != nil {
		log.Printf("bad request on %s %s: %v", r.Method, r.URL.Path, err)
	}
	http.Error(w, message, http.StatusBadRequest)
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request, message string, err error) {
	if err != nil {
		log.Printf("not found on %s %s: %v", r.Method, r.URL.Path, err)
	}
	http.Error(w, message, http.StatusNotFound)
}

func (a *App) serverError(w http.ResponseWriter, r *http.Request, message string, err error) {
	if err != nil {
		log.Printf("server error on %s %s: %v", r.Method, r.URL.Path, err)
	}
	http.Error(w, message, http.StatusInternalServerError)
}

func (a *App) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _, m, ok := a.currentUserTenant(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if m.Role == "" {
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
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		if a.Config.SecureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) ensureCSRF(r *http.Request) bool {
	s, ok := a.currentSession(r)
	if !ok || !a.sameOriginRequest(r) {
		return false
	}
	token := r.FormValue("csrf_token")
	if len(token) != len(s.CSRF) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRF)) == 1
}

func (a *App) sameOriginRequest(r *http.Request) bool {
	expectedScheme := forwardedRequestScheme(r)
	expectedHost := forwardedRequestHost(r)
	if expectedHost == "" {
		expectedHost = r.Host
	}
	if expectedHost == "" || expectedScheme == "" {
		return false
	}
	for _, header := range []string{"Origin", "Referer"} {
		raw := strings.TrimSpace(r.Header.Get(header))
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return false
		}
		if !strings.EqualFold(u.Scheme, expectedScheme) {
			return false
		}
		if !strings.EqualFold(u.Host, expectedHost) {
			return false
		}
	}
	return true
}

func forwardedRequestHost(r *http.Request) string {
	for _, header := range []string{"X-Forwarded-Host", "Host"} {
		raw := strings.TrimSpace(r.Header.Get(header))
		if raw == "" {
			continue
		}
		if index := strings.Index(raw, ","); index >= 0 {
			raw = strings.TrimSpace(raw[:index])
		}
		return raw
	}
	return ""
}

func forwardedRequestScheme(r *http.Request) string {
	if raw := strings.TrimSpace(r.Header.Get("Forwarded")); raw != "" {
		for _, part := range strings.Split(raw, ";") {
			part = strings.TrimSpace(part)
			lowerPart := strings.ToLower(part)
			if !strings.HasPrefix(lowerPart, "proto=") {
				continue
			}
			value := strings.Trim(strings.TrimSpace(part[len("proto="):]), "\"")
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "http" || value == "https" {
				return value
			}
		}
	}
	for _, header := range []string{"X-Forwarded-Proto"} {
		raw := strings.TrimSpace(r.Header.Get(header))
		if raw == "" {
			continue
		}
		if index := strings.Index(raw, ","); index >= 0 {
			raw = strings.TrimSpace(raw[:index])
		}
		return strings.ToLower(raw)
	}
	if raw := strings.TrimSpace(r.Header.Get("CF-Visitor")); raw != "" {
		lower := strings.ToLower(raw)
		switch {
		case strings.Contains(lower, `"scheme":"https"`):
			return "https"
		case strings.Contains(lower, `"scheme":"http"`):
			return "http"
		}
	}
	if raw := strings.TrimSpace(r.Header.Get("X-Forwarded-Ssl")); strings.EqualFold(raw, "on") {
		return "https"
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
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
	loginKey := forwardedClientIP(r)
	if a.LoginRateLimiter != nil {
		if allowed, retryAfter := a.LoginRateLimiter.Allow(loginKey, time.Now()); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			a.renderStatus(w, r, http.StatusTooManyRequests, "login.html", map[string]any{"Title": "Login", "Error": "Too many login attempts. Please wait and try again."})
			return
		}
	}
	u, err := a.Store.GetUserByUsername(r.Context(), r.FormValue("username"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if a.LoginRateLimiter != nil {
				a.LoginRateLimiter.RegisterFailure(loginKey, time.Now())
			}
			a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Invalid credentials"})
			return
		}
		a.serverError(w, r, "unable to load user", err)
		return
	}
	if !u.IsActive {
		if a.LoginRateLimiter != nil {
			a.LoginRateLimiter.RegisterFailure(loginKey, time.Now())
		}
		a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Invalid credentials"})
		return
	}
	u, err = a.hydrateUserSensitiveFields(r.Context(), u)
	if err != nil {
		a.serverError(w, r, "unable to load MFA settings", err)
		return
	}
	if !u.LockedUntil.IsZero() && time.Now().Before(u.LockedUntil) {
		a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Account temporarily locked"})
		return
	}
	if !auth.VerifyPassword(r.FormValue("password"), u.PasswordSalt, u.PasswordHash) {
		if a.LoginRateLimiter != nil {
			a.LoginRateLimiter.RegisterFailure(loginKey, time.Now())
		}
		u.FailedLogins++
		if u.FailedLogins >= 5 {
			u.LockedUntil = time.Now().Add(15 * time.Minute)
			u.FailedLogins = 0
		}
		if err := a.persistUser(r.Context(), u); err != nil {
			log.Printf("failed to persist login attempt for user=%s: %v", u.Username, err)
		}
		a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Invalid credentials"})
		return
	}
	if u.MFAEnabled {
		mfaInput := r.FormValue("mfa_code")
		if !auth.ValidateTOTP(u.MFASecret, mfaInput, time.Now()) {
			remainingCodes, usedRecoveryCode := auth.ConsumeRecoveryCode(u.RecoveryCodes, mfaInput)
			if !usedRecoveryCode {
				if a.LoginRateLimiter != nil {
					a.LoginRateLimiter.RegisterFailure(loginKey, time.Now())
				}
				a.render(w, r, "login.html", map[string]any{"Title": "Login", "Error": "Invalid MFA or recovery code"})
				return
			}
			u.RecoveryCodes = remainingCodes
		}
	}
	u.FailedLogins = 0
	u.LockedUntil = time.Time{}
	if err := a.persistUser(r.Context(), u); err != nil {
		a.serverError(w, r, "unable to update login state", err)
		return
	}
	memberships, err := a.Store.ListMemberships(r.Context(), u.ID)
	if err != nil {
		a.serverError(w, r, "unable to load memberships", err)
		return
	}
	if len(memberships) == 0 {
		http.Error(w, "No tenant membership assigned", 403)
		return
	}
	if a.LoginRateLimiter != nil {
		a.LoginRateLimiter.RegisterSuccess(loginKey)
	}
	if _, err := a.issueSession(r.Context(), w, u, memberships[0].TenantID); err != nil {
		a.serverError(w, r, "unable to start session", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *App) Logout(w http.ResponseWriter, r *http.Request) {
	a.clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) Close() error {
	if closer, ok := a.Store.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (a *App) RunWorker() error {
	return a.RunWorkerContext(context.Background())
}

func (a *App) RunWorkerContext(ctx context.Context) error {
	return worker.Runner{
		Store:         a.Store,
		Sources:       a.Sources,
		SourceLoad:    a.loadSourceRegistry,
		Extractor:     a.Extractor,
		SyncEvery:     time.Duration(a.Config.WorkerSyncMinutes) * time.Minute,
		LoopEvery:     time.Duration(a.Config.WorkerLoopSeconds) * time.Second,
		HeartbeatPath: "/tmp/tenderhub-worker-heartbeat",
	}.Run(ctx)
}
func init() { log.SetFlags(log.LstdFlags | log.Lshortfile) }
