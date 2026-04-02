package app

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
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
	AppAddr, DataPath, SecretKey, ExtractorURL         string
	SecureCookies, LowMemoryMode, AnalyticsEnabled     bool
	SessionHours, WorkerSyncMinutes, WorkerLoopSeconds int
}
type App struct {
	Config    Config
	Store     store.Store
	Templates *template.Template
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
	cfg := Config{AppAddr: getenv("APP_ADDR", ":8080"), DataPath: getenv("DATA_PATH", "./data/store.json"), SecretKey: getenv("SECRET_KEY", "change-me-now"), SecureCookies: getenv("SECURE_COOKIES", "false") == "true", LowMemoryMode: getenv("LOW_MEMORY_MODE", "true") == "true", AnalyticsEnabled: getenv("ANALYTICS_ENABLED", "false") == "true", ExtractorURL: getenv("EXTRACTOR_URL", "http://extractor:9090"), SessionHours: atoi(getenv("SESSION_HOURS", "12"), 12), WorkerSyncMinutes: atoi(getenv("WORKER_SYNC_MINUTES", "360"), 360), WorkerLoopSeconds: atoi(getenv("WORKER_LOOP_SECONDS", "30"), 30)}
	st, err := store.NewSQLiteStore(cfg.DataPath)
	if err != nil {
		return nil, err
	}
	tpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	a := &App{Config: cfg, Store: st, Templates: tpl, Sources: source.NewRegistry(source.NewTreasuryAdapter(source.DefaultFeedURL()), source.StubAdapter{Source: "eskom"}, source.StubAdapter{Source: "transnet"}, source.StubAdapter{Source: "cidb"}, source.StubAdapter{Source: "dbsa"}, source.StubAdapter{Source: "dpwi"}), Extractor: extract.New(cfg.ExtractorURL)}
	if err := a.seed(context.Background()); err != nil {
		return nil, err
	}
	a.Server = &http.Server{Addr: cfg.AppAddr, Handler: routes(a), ReadHeaderTimeout: 10 * time.Second}
	return a, nil
}
func (a *App) seed(ctx context.Context) error {
	users, _ := a.Store.ListUsers(ctx)
	if len(users) > 0 {
		return nil
	}
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
	for _, ad := range a.Sources.Adapters {
		items, msg, err := ad.Fetch(ctx)
		status := "success"
		if err != nil {
			status = "failed"
			msg = err.Error()
		}
		_ = a.Store.AddSyncRun(ctx, models.SyncRun{SourceKey: ad.Key(), StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Status: status, Message: msg})
		_ = a.Store.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: ad.Key(), LastSyncAt: time.Now().UTC(), LastStatus: status, LastMessage: msg, LastItemCount: len(items)})
		for _, t := range items {
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
type QueueItem struct {
	Job    models.ExtractionJob
	Tender models.Tender
}

type QueueSummary struct {
	Queued int
	Processing int
	Failed int
	Completed int
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
	if u, t, _, ok := a.currentUserTenant(r); ok {
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
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.Templates.ExecuteTemplate(w, name, data); err != nil {
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
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
func (a *App) Logout(w http.ResponseWriter, r *http.Request) {
	auth.ClearSessionCookie(w, a.Config.SecureCookies)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (a *App) Dashboard(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	d, _ := a.Store.Dashboard(r.Context(), t.ID, a.Config.LowMemoryMode, a.Config.AnalyticsEnabled && !a.Config.LowMemoryMode && r.URL.Query().Get("analytics") == "1")
	a.render(w, r, "dashboard.html", map[string]any{"Title": "Dashboard", "User": u, "Tenant": t, "Dashboard": d, "CSRFToken": a.mustCSRF(r)})
}
func (a *App) Tenders(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok { http.Redirect(w, r, "/login", 303); return }
	pageSize := atoi(r.URL.Query().Get("page_size"), 20)
	if pageSize <= 0 || pageSize > 100 { pageSize = 20 }
	f := store.NormalizeFilter(store.ListFilter{
		Query: r.URL.Query().Get("q"), Source: r.URL.Query().Get("source"), Province: r.URL.Query().Get("province"),
		Category: r.URL.Query().Get("category"), Issuer: r.URL.Query().Get("issuer"), Status: r.URL.Query().Get("status"),
		CIDB: r.URL.Query().Get("cidb"), WorkflowStatus: r.URL.Query().Get("workflow_status"),
		DocumentStatus: r.URL.Query().Get("document_status"), BookmarkedOnly: r.URL.Query().Get("bookmarked_only") == "1",
		HasDocuments: r.URL.Query().Get("has_documents") == "1", Sort: r.URL.Query().Get("sort"), View: r.URL.Query().Get("view"),
		Page: atoi(r.URL.Query().Get("page"), 1), PageSize: pageSize, TenantID: t.ID, UserID: u.ID,
	})
	items, total, _ := a.Store.ListTenders(r.Context(), f)
	bm, _ := a.Store.ListBookmarks(r.Context(), t.ID, u.ID)
	bmMap := map[string]models.Bookmark{}
	for _, b := range bm { bmMap[b.TenderID] = b }
	wf, _ := a.Store.ListWorkflows(r.Context(), t.ID)
	wfMap := map[string]models.Workflow{}
	for _, x := range wf { wfMap[x.TenderID] = x }
	params := map[string]string{"q": f.Query, "source": f.Source, "province": f.Province, "category": f.Category, "issuer": f.Issuer, "status": f.Status, "cidb": f.CIDB, "workflow_status": f.WorkflowStatus, "document_status": f.DocumentStatus, "sort": f.Sort, "view": f.View, "page_size": strconv.Itoa(f.PageSize)}
	if f.BookmarkedOnly { params["bookmarked_only"] = "1" }
	totalPages := total / f.PageSize
	if total % f.PageSize != 0 { totalPages++ }
	if totalPages == 0 { totalPages = 1 }
	a.render(w, r, "tenders.html", map[string]any{
		"Title": "Tenders", "User": u, "Tenant": t, "Items": items, "Total": total,
		"Filter": f, "Bookmarks": bmMap, "Workflows": wfMap,
		"CurrentPage": f.Page, "TotalPages": totalPages, "HasPrevPage": f.Page > 1, "HasNextPage": f.Page < totalPages,
		"PrevPageURL": pageLink("/tenders", params, f.Page-1), "NextPageURL": pageLink("/tenders", params, f.Page+1),
	})
}
func (a *App) ExportCSV(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	items, _, _ := a.Store.ListTenders(r.Context(), store.ListFilter{Query: r.URL.Query().Get("q"), TenantID: t.ID, UserID: u.ID, Page: 1, PageSize: 5000})
	workflows, _ := a.Store.ListWorkflows(r.Context(), t.ID)
	wfMap := map[string]models.Workflow{}
	for _, wf := range workflows {
		wfMap[wf.TenderID] = wf
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=tenders.csv")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "title", "issuer", "source", "province", "category", "tender_number", "published_date", "closing_date", "status", "relevance_score", "cidb_grading", "document_status", "workflow_status", "workflow_priority", "assigned_user", "document_url", "original_url", "excerpt", "closing_details", "briefing_details", "submission_details", "contact_details", "cidb_hints"})
	for _, t := range items {
		facts := t.ExtractedFacts
		if facts == nil {
			facts = map[string]string{}
		}
		wf := wfMap[t.ID]
		_ = cw.Write([]string{t.ID, t.Title, t.Issuer, t.SourceKey, t.Province, t.Category, t.TenderNumber, t.PublishedDate, t.ClosingDate, t.Status, fmt.Sprintf("%.2f", t.RelevanceScore), t.CIDBGrading, string(t.DocumentStatus), wf.Status, wf.Priority, wf.AssignedUser, t.DocumentURL, t.OriginalURL, t.Excerpt, facts["closing_details"], facts["briefing_details"], facts["submission_details"], facts["contact_details"], facts["cidb_hints"]})
	}
	cw.Flush()
}
func (a *App) ToggleBookmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	_ = a.Store.ToggleBookmark(r.Context(), models.Bookmark{TenantID: t.ID, UserID: u.ID, TenderID: r.FormValue("tender_id"), Note: r.FormValue("note")})
	ac := actionContext{User: models.User{}, Tenant: t, Member: m}
	if u2, _, _, ok2 := a.currentUserTenant(r); ok2 { ac.User = u2 }
	a.addWorkflowSnapshot(r.Context(), ac, wf)
	a.auditAction(r.Context(), ac, "update", "workflow", wf.TenderID, "Workflow updated", map[string]string{"status": wf.Status, "priority": wf.Priority})
	http.Redirect(w, r, "/tenders", 303)
}
func (a *App) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if m.Role == models.RoleViewer {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = a.Store.UpsertWorkflow(r.Context(), models.Workflow{TenantID: t.ID, TenderID: r.FormValue("tender_id"), Status: r.FormValue("status"), Priority: r.FormValue("priority"), AssignedUser: r.FormValue("assigned_user"), Notes: r.FormValue("notes")})
	http.Redirect(w, r, "/tenders", 303)
}
func (a *App) QueueExtraction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if m.Role == models.RoleViewer {
		http.Error(w, "forbidden", 403)
		return
	}
	tender, err := a.Store.GetTender(r.Context(), r.FormValue("tender_id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if tender.DocumentURL == "" {
		http.Error(w, "no document url", 400)
		return
	}
	tender.DocumentStatus = models.ExtractionQueued
	_ = a.Store.UpsertTender(r.Context(), tender)
	_ = a.Store.QueueJob(r.Context(), models.ExtractionJob{TenderID: tender.ID, DocumentURL: tender.DocumentURL, State: models.ExtractionQueued})
	http.Redirect(w, r, "/tenders", 303)
}
func (a *App) SavedSearches(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method == http.MethodGet {
		items, _ := a.Store.ListSavedSearches(r.Context(), t.ID, u.ID)
		a.render(w, r, "saved_searches.html", map[string]any{"Title": "Saved searches", "User": u, "Tenant": t, "Items": items, "CSRFToken": a.mustCSRF(r)})
		return
	}
	if !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = a.Store.UpsertSavedSearch(r.Context(), models.SavedSearch{ID: r.FormValue("id"), TenantID: t.ID, UserID: u.ID, Name: r.FormValue("name"), Query: r.FormValue("query"), Filters: r.FormValue("filters")})
	a.auditAction(r.Context(), actionContext{User:u, Tenant:t}, "create", "saved_search", "", "Saved search saved", map[string]string{"name": r.FormValue("name")})
	a.redirectAfterAction(w, r, "/saved-searches", "success", "Saved search saved")
}
func (a *App) DeleteSavedSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	_ = a.Store.DeleteSavedSearch(r.Context(), t.ID, u.ID, r.FormValue("id"))
	http.Redirect(w, r, "/saved-searches", 303)
}
func (a *App) AdminUsers(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if m.Role != models.RoleAdmin && m.Role != models.RolePortfolioManager && m.Role != models.RoleTenantAdmin {
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
	if m.Role != models.RoleAdmin && m.Role != models.RolePortfolioManager && m.Role != models.RoleTenantAdmin {
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
	u := users[len(users)-1]
	_ = a.Store.UpsertMembership(r.Context(), models.Membership{UserID: u.ID, TenantID: r.FormValue("tenant_id"), Role: models.Role(r.FormValue("role")), Responsibilities: r.FormValue("responsibilities")})
	http.Redirect(w, r, "/admin/users", 303)
}
func (a *App) AdminTenants(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if m.Role != models.RoleAdmin && m.Role != models.RolePortfolioManager {
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
	if m.Role != models.RoleAdmin && m.Role != models.RolePortfolioManager {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = a.Store.UpsertTenant(r.Context(), models.Tenant{Name: r.FormValue("name"), Slug: r.FormValue("slug")})
	http.Redirect(w, r, "/admin/tenants", 303)
}
func (a *App) SwitchTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	sess, ok := a.currentSession(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if _, err := a.Store.GetMembership(r.Context(), sess.UserID, r.FormValue("tenant_id")); err != nil {
		http.Error(w, "forbidden", 403)
		return
	}
	sess.TenantID = r.FormValue("tenant_id")
	_ = auth.SetSessionCookie(w, a.Config.SecretKey, sess, a.Config.SecureCookies)
	dest := r.FormValue("return_to")
	if dest == "" {
		dest = "/dashboard"
	}
	http.Redirect(w, r, dest, 303)
}

func parseSelectedIDs(raw string) []string {
	parts := strings.Split(raw, ",")
	out := []string{}
	seen := map[string]bool{}
	for _, p := range parts {
		id := strings.TrimSpace(p)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
func (a *App) BulkTenders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if m.Role == models.RoleViewer {
		http.Error(w, "forbidden", 403)
		return
	}
	for _, id := range parseSelectedIDs(r.FormValue("selected_ids")) {
		switch r.FormValue("action") {
		case "bookmark":
			_ = a.Store.ToggleBookmark(r.Context(), models.Bookmark{TenantID: t.ID, UserID: u.ID, TenderID: id, Note: r.FormValue("notes")})
		case "queue":
			tender, err := a.Store.GetTender(r.Context(), id)
			if err == nil && tender.DocumentURL != "" {
				tender.DocumentStatus = models.ExtractionQueued
				_ = a.Store.UpsertTender(r.Context(), tender)
				_ = a.Store.QueueJob(r.Context(), models.ExtractionJob{TenderID: tender.ID, DocumentURL: tender.DocumentURL, State: models.ExtractionQueued})
			}
		default:
			_ = a.Store.UpsertWorkflow(r.Context(), models.Workflow{TenantID: t.ID, TenderID: id, Status: r.FormValue("status"), Priority: r.FormValue("priority"), AssignedUser: r.FormValue("assigned_user"), Notes: r.FormValue("notes")})
		}
	}
	http.Redirect(w, r, "/tenders", 303)
}
func (a *App) TenderDetail(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok { http.Redirect(w, r, "/login", 303); return }
	id := strings.TrimPrefix(r.URL.Path, "/tenders/")
	if id == "" || strings.Contains(id, "/") { http.NotFound(w, r); return }
	item, err := a.Store.GetTender(r.Context(), id)
	if err != nil { http.NotFound(w, r); return }
	wf, _ := a.Store.GetWorkflow(r.Context(), t.ID, id)
	history, _ := a.Store.ListWorkflowEvents(r.Context(), t.ID, id)
	a.render(w, r, "tender_detail.html", map[string]any{"Title":"Opportunity detail","User":u,"Tenant":t,"Item":item,"Workflow":wf,"WorkflowHistory":history})
}

func (a *App) AuditLogPage(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok { http.Redirect(w, r, "/login", 303); return }
	if !canAdminUsers(m.Role) { http.Error(w, "forbidden", 403); return }
	items, _ := a.Store.ListAuditEntries(r.Context(), t.ID)
	a.render(w, r, "audit_log.html", map[string]any{"Title":"Audit log","User":u,"Tenant":t,"Items":items})
}

func (a *App) QueuePage(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok { http.Redirect(w, r, "/login", 303); return }
	jobs, _ := a.Store.ListJobs(r.Context())
	items := []QueueItem{}
	summary := QueueSummary{}
	for _, job := range jobs {
		tender, _ := a.Store.GetTender(r.Context(), job.TenderID)
		items = append(items, QueueItem{Job: job, Tender: tender})
		switch job.State {
		case models.ExtractionQueued:
			summary.Queued++
		case models.ExtractionProcessing:
			summary.Processing++
		case models.ExtractionFailed:
			summary.Failed++
		case models.ExtractionCompleted:
			summary.Completed++
		}
	}
	a.render(w, r, "queue.html", map[string]any{"Title": "Queue", "User": u, "Tenant": t, "QueueItems": items, "QueueSummary": summary})
}
func (a *App) PasswordPage(w http.ResponseWriter, r *http.Request) {
	u, _, _, ok := a.currentUserTenant(r)
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
	if !auth.VerifyPassword(r.FormValue("current_password"), u.PasswordSalt, u.PasswordHash) {
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
	salt, hash, _ := auth.HashPassword(r.FormValue("new_password"))
	u.PasswordSalt = salt
	u.PasswordHash = hash
	_ = a.Store.UpsertUser(r.Context(), u)
	a.render(w, r, "password.html", map[string]any{"Title": "Password", "Message": "Password updated"})
}
func (a *App) MFAPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA"})
}
func (a *App) MFASetup(w http.ResponseWriter, r *http.Request) {
	u, _, _, ok := a.currentUserTenant(r)
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
	if !auth.ValidateTOTP(secret, code, time.Now()) {
		a.render(w, r, "mfa_setup.html", map[string]any{"Title": "MFA Setup", "Message": secret, "Error": "Invalid MFA code"})
		return
	}
	u.MFASecret = secret
	u.MFAEnabled = true
	_ = a.Store.UpsertUser(r.Context(), u)
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Message": "MFA enabled"})
}
func (a *App) MFADisable(w http.ResponseWriter, r *http.Request) {
	u, _, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if !auth.VerifyPassword(r.FormValue("password"), u.PasswordSalt, u.PasswordHash) {
		a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Error": "Password confirmation failed"})
		return
	}
	u.MFAEnabled = false
	u.MFASecret = ""
	_ = a.Store.UpsertUser(r.Context(), u)
	a.render(w, r, "mfa.html", map[string]any{"Title": "MFA", "Message": "MFA disabled"})
}
func isAdminish(role models.Role) bool {
	return role == models.RoleAdmin || role == models.RolePortfolioManager || role == models.RoleTenantAdmin
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
	if !isAdminish(m.Role) {
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
	if !isAdminish(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	user, err := a.Store.GetUser(r.Context(), r.FormValue("user_id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	pw := r.FormValue("new_password")
	if pw == "" {
		pw = "Reset!2026Pass"
	}
	if err := auth.StrongEnoughPassword(pw); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	salt, hash, _ := auth.HashPassword(pw)
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
	if !isAdminish(m.Role) {
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
	if !isAdminish(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = a.Store.DeleteMembership(r.Context(), r.FormValue("id"))
	http.Redirect(w, r, "/admin/users", 303)
}



func (a *App) QueueRequeue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) { http.Error(w, "forbidden", 403); return }
	_, _, m, ok := a.currentUserTenant(r)
	if !ok { http.Redirect(w, r, "/login", 303); return }
	if m.Role == models.RoleViewer { http.Error(w, "forbidden", 403); return }
	tender, err := a.Store.GetTender(r.Context(), r.FormValue("tender_id"))
	if err != nil { http.Error(w, err.Error(), 404); return }
	if tender.DocumentURL == "" { http.Error(w, "no document url", 400); return }
	tender.DocumentStatus = models.ExtractionQueued
	_ = a.Store.UpsertTender(r.Context(), tender)
	_ = a.Store.QueueJob(r.Context(), models.ExtractionJob{TenderID: tender.ID, DocumentURL: tender.DocumentURL, State: models.ExtractionQueued})
	http.Redirect(w, r, "/queue", 303)
}

func (a *App) ResetWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) { http.Error(w, "forbidden", 403); return }
	_, t, m, ok := a.currentUserTenant(r)
	if !ok { http.Redirect(w, r, "/login", 303); return }
	if m.Role == models.RoleViewer { http.Error(w, "forbidden", 403); return }
	_ = a.Store.UpsertWorkflow(r.Context(), models.Workflow{
		TenantID: t.ID, TenderID: r.FormValue("tender_id"),
		Status: "", Priority: "", AssignedUser: "", Notes: "",
	})
	http.Redirect(w, r, "/tenders", 303)
}

func (a *App) RemoveBookmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) { http.Error(w, "forbidden", 403); return }
	u, t, _, ok := a.currentUserTenant(r)
	if !ok { http.Redirect(w, r, "/login", 303); return }
	_ = a.Store.ToggleBookmark(r.Context(), models.Bookmark{TenantID: t.ID, UserID: u.ID, TenderID: r.FormValue("tender_id")})
	http.Redirect(w, r, "/tenders", 303)
}


func (a *App) RunWorker() error {
	return worker.Runner{Store: a.Store, Sources: a.Sources, Extractor: a.Extractor, SyncEvery: time.Duration(a.Config.WorkerSyncMinutes) * time.Minute, LoopEvery: time.Duration(a.Config.WorkerLoopSeconds) * time.Second}.Run(context.Background())
}
func init() { log.SetFlags(log.LstdFlags | log.Lshortfile) }
