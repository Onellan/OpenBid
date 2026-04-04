package app

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict expects an even number of arguments")
	}
	out := map[string]any{}
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		out[key] = values[i+1]
	}
	return out, nil
}

func locateWebSubdir(name string) (string, error) {
	candidates := []string{filepath.Join("web", name)}
	if _, filename, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "web", name)))
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no %s directory found in known locations", name)
}

func locateTemplateDir() (string, error) {
	dir, err := locateWebSubdir("templates")
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(filepath.Join(dir, "base.html")); err == nil && !info.IsDir() {
		return dir, nil
	}
	return "", fmt.Errorf("no templates found in known locations")
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"dict":  dict,
		"slice": func(values ...any) []any { return values },
		"formatTime": func(value any) string {
			t, ok := value.(time.Time)
			if !ok || t.IsZero() {
				return "Not scheduled"
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"hasPrefix": func(value, prefix string) bool {
			return strings.HasPrefix(value, prefix)
		},
		"humanBytes": func(value any) string {
			var size float64
			switch v := value.(type) {
			case int:
				size = float64(v)
			case int64:
				size = float64(v)
			case uint64:
				size = float64(v)
			case float64:
				size = v
			default:
				return fmt.Sprint(value)
			}
			units := []string{"B", "KB", "MB", "GB", "TB"}
			unit := 0
			for size >= 1024 && unit < len(units)-1 {
				size /= 1024
				unit++
			}
			if unit == 0 {
				return fmt.Sprintf("%.0f %s", size, units[unit])
			}
			return fmt.Sprintf("%.1f %s", size, units[unit])
		},
		"formatDuration": func(value any) string {
			d, ok := value.(time.Duration)
			if !ok {
				return fmt.Sprint(value)
			}
			if d < time.Minute {
				return d.Truncate(time.Second).String()
			}
			if d < time.Hour {
				return d.Truncate(time.Second).String()
			}
			return d.Truncate(time.Second).String()
		},
		"condTone": func(state any) string {
			value := strings.TrimSpace(fmt.Sprint(state))
			switch value {
			case "completed", "healthy", "success":
				return "success"
			case "failed", "failing", "degraded", "disabled":
				return "danger"
			case "processing", "queued", "retry", "running", "paused", "manual-only":
				return "warning"
			default:
				return "info"
			}
		},
		"platformRoleLabel": platformRoleLabel,
		"tenantRoleLabel":   tenantRoleLabel,
	}
}

func parseTemplates() (map[string]*template.Template, error) {
	dir, err := locateTemplateDir()
	if err != nil {
		return nil, err
	}

	sharedNames := []string{
		"base.html",
		"components.html",
		"patterns.html",
		"admin_partials.html",
		"domain_partials.html",
		"opportunity_partials.html",
	}
	sharedSet := map[string]bool{}
	sharedFiles := make([]string, 0, len(sharedNames))
	for _, name := range sharedNames {
		sharedSet[name] = true
		sharedFiles = append(sharedFiles, filepath.Join(dir, name))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	pageNames := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".html" || sharedSet[name] || name == "admin_sources.html" || name == "interaction_partials.html" {
			continue
		}
		pageNames = append(pageNames, name)
	}
	sort.Strings(pageNames)

	pages := make(map[string]*template.Template, len(pageNames))
	for _, pageName := range pageNames {
		files := append([]string{}, sharedFiles...)
		files = append(files, filepath.Join(dir, pageName))
		tpl, err := template.New("").Funcs(templateFuncs()).ParseFiles(files...)
		if err != nil {
			return nil, err
		}
		pages[pageName] = tpl
	}
	return pages, nil
}

func registerProtected(mux *http.ServeMux, a *App, handler http.HandlerFunc, paths ...string) {
	protected := a.RequireAuth(handler)
	for _, path := range paths {
		mux.HandleFunc(path, protected)
	}
}

func registerRedirect(mux *http.ServeMux, path, target string) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		destination := target
		if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
			destination += "?" + rawQuery
		}
		http.Redirect(w, r, destination, http.StatusPermanentRedirect)
	})
}

func routes(a *App) http.Handler {
	mux := http.NewServeMux()
	if assetDir, err := locateWebSubdir("assets"); err == nil {
		assetHandler := http.StripPrefix("/assets/", http.FileServer(http.Dir(assetDir)))
		mux.Handle("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			assetHandler.ServeHTTP(w, r)
		}))
	}
	mux.HandleFunc("/healthz", a.Healthz)
	mux.HandleFunc("/login", a.Login)
	registerProtected(mux, a, a.Logout, "/logout")
	registerProtected(mux, a, a.Home, "/", "/home")
	registerProtected(mux, a, a.Dashboard, "/dashboard")
	registerProtected(mux, a, a.Tenders, "/tenders")
	registerProtected(mux, a, a.TenderDetail, "/tenders/")
	registerProtected(mux, a, a.ExportCSV, "/tenders/export.csv")
	registerProtected(mux, a, a.ToggleBookmark, "/tenders/bookmark")
	registerProtected(mux, a, a.UpdateWorkflow, "/tenders/workflow")
	registerProtected(mux, a, a.QueueExtraction, "/tenders/queue")
	registerProtected(mux, a, a.BulkTenders, "/tenders/bulk")
	registerProtected(mux, a, a.ResetWorkflow, "/tenders/workflow/reset")
	registerProtected(mux, a, a.RemoveBookmark, "/tenders/bookmark/remove")
	registerProtected(mux, a, a.BookmarksPage, "/bookmarks")
	registerProtected(mux, a, a.QueuePage, "/queue")
	registerProtected(mux, a, a.AuditLogPage, "/audit-log")
	registerProtected(mux, a, a.SecurityAuditLogPage, "/audit-log/security")
	registerProtected(mux, a, a.HealthPage, "/health")
	registerProtected(mux, a, a.HealthAlertsJSON, "/health/alerts.json")
	registerProtected(mux, a, a.QueueRequeue, "/queue/requeue")
	registerProtected(mux, a, a.SettingsPage, "/settings")
	registerProtected(mux, a, a.PasswordPage, "/password", "/settings/password")
	registerProtected(mux, a, a.MFAPage, "/mfa", "/settings/mfa")
	registerProtected(mux, a, a.MFASetup, "/mfa/setup")
	registerProtected(mux, a, a.MFADisable, "/mfa/disable")
	registerProtected(mux, a, a.MFARegenerateRecoveryCodes, "/mfa/recovery/regenerate")
	registerProtected(mux, a, a.SavedSearches, "/saved-searches")
	registerProtected(mux, a, a.DeleteSavedSearch, "/saved-searches/delete")
	registerProtected(mux, a, a.AdminUsers, "/admin/users")
	registerProtected(mux, a, a.AdminCreateUser, "/admin/users/create")
	registerProtected(mux, a, a.AdminUpdatePlatformRole, "/admin/users/platform-role")
	registerProtected(mux, a, a.AdminToggleUser, "/admin/users/toggle")
	registerProtected(mux, a, a.AdminResetPassword, "/admin/users/reset-password")
	registerProtected(mux, a, a.AdminUpsertMembership, "/admin/memberships/upsert")
	registerProtected(mux, a, a.AdminDeleteMembership, "/admin/memberships/delete")
	registerProtected(mux, a, a.AdminTenants, "/admin/tenants")
	registerProtected(mux, a, a.AdminCreateTenant, "/admin/tenants/create")
	registerProtected(mux, a, a.SourcesPage, "/sources")
	registerProtected(mux, a, a.SourceStatusJSON, "/sources/status.json")
	registerProtected(mux, a, a.AdminCreateSource, "/sources/create")
	registerProtected(mux, a, a.AdminUpdateSource, "/sources/update")
	registerProtected(mux, a, a.AdminTriggerSourceCheck, "/sources/check")
	registerProtected(mux, a, a.AdminTriggerSelectedSourceChecks, "/sources/check-selected")
	registerProtected(mux, a, a.AdminTriggerAllSourceChecks, "/sources/check-all")
	registerProtected(mux, a, a.AdminUpdateSourceSchedule, "/sources/schedule")
	registerProtected(mux, a, a.AdminDeleteSource, "/sources/delete")
	registerRedirect(mux, "/admin/sources", "/sources")
	registerRedirect(mux, "/admin/sources/create", "/sources/create")
	registerRedirect(mux, "/admin/sources/update", "/sources/update")
	registerRedirect(mux, "/admin/sources/check", "/sources/check")
	registerRedirect(mux, "/admin/sources/check-selected", "/sources/check-selected")
	registerRedirect(mux, "/admin/sources/check-all", "/sources/check-all")
	registerRedirect(mux, "/admin/sources/schedule", "/sources/schedule")
	registerRedirect(mux, "/admin/sources/delete", "/sources/delete")
	registerProtected(mux, a, a.SwitchTenant, "/tenant/switch")
	return a.WithRequestObservability(a.WithSecurityHeaders(a.WithProxyRequirement(a.WithRecovery(mux))))
}
