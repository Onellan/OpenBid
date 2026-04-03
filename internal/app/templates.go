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
	mux.HandleFunc("/logout", a.RequireAuth(a.Logout))
	mux.HandleFunc("/", a.RequireAuth(a.Home))
	mux.HandleFunc("/home", a.RequireAuth(a.Home))
	mux.HandleFunc("/dashboard", a.RequireAuth(a.Dashboard))
	mux.HandleFunc("/tenders", a.RequireAuth(a.Tenders))
	mux.HandleFunc("/tenders/", a.RequireAuth(a.TenderDetail))
	mux.HandleFunc("/tenders/export.csv", a.RequireAuth(a.ExportCSV))
	mux.HandleFunc("/tenders/bookmark", a.RequireAuth(a.ToggleBookmark))
	mux.HandleFunc("/tenders/workflow", a.RequireAuth(a.UpdateWorkflow))
	mux.HandleFunc("/tenders/queue", a.RequireAuth(a.QueueExtraction))
	mux.HandleFunc("/tenders/bulk", a.RequireAuth(a.BulkTenders))
	mux.HandleFunc("/tenders/workflow/reset", a.RequireAuth(a.ResetWorkflow))
	mux.HandleFunc("/tenders/bookmark/remove", a.RequireAuth(a.RemoveBookmark))
	mux.HandleFunc("/bookmarks", a.RequireAuth(a.BookmarksPage))
	mux.HandleFunc("/queue", a.RequireAuth(a.QueuePage))
	mux.HandleFunc("/audit-log", a.RequireAuth(a.AuditLogPage))
	mux.HandleFunc("/health", a.RequireAuth(a.HealthPage))
	mux.HandleFunc("/queue/requeue", a.RequireAuth(a.QueueRequeue))
	mux.HandleFunc("/settings", a.RequireAuth(a.SettingsPage))
	mux.HandleFunc("/password", a.RequireAuth(a.PasswordPage))
	mux.HandleFunc("/settings/password", a.RequireAuth(a.PasswordPage))
	mux.HandleFunc("/mfa", a.RequireAuth(a.MFAPage))
	mux.HandleFunc("/settings/mfa", a.RequireAuth(a.MFAPage))
	mux.HandleFunc("/mfa/setup", a.RequireAuth(a.MFASetup))
	mux.HandleFunc("/mfa/disable", a.RequireAuth(a.MFADisable))
	mux.HandleFunc("/mfa/recovery/regenerate", a.RequireAuth(a.MFARegenerateRecoveryCodes))
	mux.HandleFunc("/saved-searches", a.RequireAuth(a.SavedSearches))
	mux.HandleFunc("/saved-searches/delete", a.RequireAuth(a.DeleteSavedSearch))
	mux.HandleFunc("/admin/users", a.RequireAuth(a.AdminUsers))
	mux.HandleFunc("/admin/users/create", a.RequireAuth(a.AdminCreateUser))
	mux.HandleFunc("/admin/users/toggle", a.RequireAuth(a.AdminToggleUser))
	mux.HandleFunc("/admin/users/reset-password", a.RequireAuth(a.AdminResetPassword))
	mux.HandleFunc("/admin/memberships/upsert", a.RequireAuth(a.AdminUpsertMembership))
	mux.HandleFunc("/admin/memberships/delete", a.RequireAuth(a.AdminDeleteMembership))
	mux.HandleFunc("/admin/tenants", a.RequireAuth(a.AdminTenants))
	mux.HandleFunc("/admin/tenants/create", a.RequireAuth(a.AdminCreateTenant))
	mux.HandleFunc("/sources", a.RequireAuth(a.SourcesPage))
	mux.HandleFunc("/sources/status.json", a.RequireAuth(a.SourceStatusJSON))
	mux.HandleFunc("/sources/create", a.RequireAuth(a.AdminCreateSource))
	mux.HandleFunc("/sources/update", a.RequireAuth(a.AdminUpdateSource))
	mux.HandleFunc("/sources/check", a.RequireAuth(a.AdminTriggerSourceCheck))
	mux.HandleFunc("/sources/check-selected", a.RequireAuth(a.AdminTriggerSelectedSourceChecks))
	mux.HandleFunc("/sources/check-all", a.RequireAuth(a.AdminTriggerAllSourceChecks))
	mux.HandleFunc("/sources/schedule", a.RequireAuth(a.AdminUpdateSourceSchedule))
	mux.HandleFunc("/sources/delete", a.RequireAuth(a.AdminDeleteSource))
	mux.HandleFunc("/admin/sources", a.RequireAuth(a.AdminSources))
	mux.HandleFunc("/admin/sources/create", a.RequireAuth(a.AdminCreateSource))
	mux.HandleFunc("/admin/sources/update", a.RequireAuth(a.AdminUpdateSource))
	mux.HandleFunc("/admin/sources/check", a.RequireAuth(a.AdminTriggerSourceCheck))
	mux.HandleFunc("/admin/sources/check-selected", a.RequireAuth(a.AdminTriggerSelectedSourceChecks))
	mux.HandleFunc("/admin/sources/check-all", a.RequireAuth(a.AdminTriggerAllSourceChecks))
	mux.HandleFunc("/admin/sources/schedule", a.RequireAuth(a.AdminUpdateSourceSchedule))
	mux.HandleFunc("/admin/sources/delete", a.RequireAuth(a.AdminDeleteSource))
	mux.HandleFunc("/tenant/switch", a.RequireAuth(a.SwitchTenant))
	return a.WithRequestObservability(a.WithSecurityHeaders(mux))
}
