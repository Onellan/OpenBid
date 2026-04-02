package app

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
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

func parseTemplates() (*template.Template, error) {
	return template.New("").
		Funcs(template.FuncMap{
			"dict": dict,
			"slice": func(values ...any) []any { return values },
			"condTone": func(state string) string { switch state { case "completed": return "success"; case "failed": return "danger"; case "processing", "queued", "retry": return "warning"; default: return "info" } },
		}).
		ParseGlob(filepath.Join("web", "templates", "*.html"))
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
