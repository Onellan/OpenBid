package app

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

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

func (a *App) redirectAfterAction(w http.ResponseWriter, r *http.Request, fallback, tone, message string) {
	dest := r.FormValue("return_to")
	if dest == "" {
		dest = fallback
	}
	u, err := url.Parse(dest)
	if err != nil || u.Path == "" {
		u = &url.URL{Path: fallback}
	}
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
