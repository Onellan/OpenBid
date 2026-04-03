package app

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"tenderhub-za/internal/models"
)

type actionContext struct {
	User   models.User
	Tenant models.Tenant
	Member models.Membership
}

func pageLink(path string, params map[string]string, page int) string {
	if page < 1 {
		page = 1
	}
	values := url.Values{}
	for key, value := range params {
		if value == "" {
			continue
		}
		values.Set(key, value)
	}
	values.Set("page", strconv.Itoa(page))
	encoded := values.Encode()
	if encoded == "" {
		return path
	}
	return path + "?" + encoded
}

func canAdminUsers(role models.Role) bool {
	return role == models.RoleAdmin || role == models.RolePortfolioManager || role == models.RoleTenantAdmin
}

func canManageTenants(role models.Role) bool {
	return role == models.RoleAdmin || role == models.RolePortfolioManager
}

func canManageSources(role models.Role) bool {
	return canAdminUsers(role)
}

func canManageAudit(role models.Role) bool {
	return canAdminUsers(role)
}

func canEditWorkspace(role models.Role) bool {
	return role != models.RoleViewer
}

func safeReturnTarget(dest, fallback string) *url.URL {
	fallbackURL := &url.URL{Path: fallback}
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return fallbackURL
	}
	u, err := url.Parse(dest)
	if err != nil || u == nil {
		return fallbackURL
	}
	if u.IsAbs() || u.Host != "" || strings.HasPrefix(dest, "//") || !strings.HasPrefix(u.Path, "/") {
		return fallbackURL
	}
	return u
}

func (a *App) redirectAfterAction(w http.ResponseWriter, r *http.Request, fallback, tone, message string) {
	u := safeReturnTarget(r.FormValue("return_to"), fallback)
	query := u.Query()
	switch tone {
	case "danger", "error":
		query.Set("error", message)
	default:
		query.Set("message", message)
	}
	u.RawQuery = query.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

func (a *App) auditAction(ctx context.Context, ac actionContext, action, entity, entityID, summary string, metadata map[string]string) {
	if ac.Tenant.ID == "" {
		return
	}
	_ = a.Store.AddAuditEntry(ctx, models.AuditEntry{
		TenantID: ac.Tenant.ID,
		UserID:   ac.User.ID,
		Action:   action,
		Entity:   entity,
		EntityID: entityID,
		Summary:  summary,
		Metadata: metadata,
	})
}

func (a *App) addWorkflowSnapshot(ctx context.Context, ac actionContext, wf models.Workflow) {
	if ac.Tenant.ID == "" || wf.TenderID == "" {
		return
	}
	_ = a.Store.AddWorkflowEvent(ctx, models.WorkflowEvent{
		TenantID:     ac.Tenant.ID,
		TenderID:     wf.TenderID,
		ChangedBy:    ac.User.ID,
		Status:       wf.Status,
		Priority:     wf.Priority,
		AssignedUser: wf.AssignedUser,
		Notes:        wf.Notes,
	})
}
