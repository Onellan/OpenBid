package app

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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

func locateTemplateDir() (string, error) {
	candidates := []string{filepath.Join("web", "templates")}
	if _, filename, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "web", "templates")))
	}
	for _, dir := range candidates {
		if info, err := os.Stat(filepath.Join(dir, "base.html")); err == nil && !info.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no templates found in known locations")
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"dict":  dict,
		"slice": func(values ...any) []any { return values },
		"condTone": func(state string) string {
			switch state {
			case "completed":
				return "success"
			case "failed":
				return "danger"
			case "processing", "queued", "retry":
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
		"interaction_partials.html",
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
		if filepath.Ext(name) != ".html" || sharedSet[name] {
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
	mux.HandleFunc("/healthz", a.Healthz)
	mux.HandleFunc("/login", a.Login)
	mux.HandleFunc("/logout", a.RequireAuth(a.Logout))
	mux.HandleFunc("/", a.RequireAuth(a.Dashboard))
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
	mux.HandleFunc("/queue", a.RequireAuth(a.QueuePage))
	mux.HandleFunc("/audit-log", a.RequireAuth(a.AuditLogPage))
	mux.HandleFunc("/queue/requeue", a.RequireAuth(a.QueueRequeue))
	mux.HandleFunc("/password", a.RequireAuth(a.PasswordPage))
	mux.HandleFunc("/mfa", a.RequireAuth(a.MFAPage))
	mux.HandleFunc("/mfa/setup", a.RequireAuth(a.MFASetup))
	mux.HandleFunc("/mfa/disable", a.RequireAuth(a.MFADisable))
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
	mux.HandleFunc("/tenant/switch", a.RequireAuth(a.SwitchTenant))
	return a.WithSecurityHeaders(mux)
}
