package app

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"openbid/internal/models"
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
	if err := a.writeAuditEntry(ctx, ac.Tenant.ID, ac.User.ID, action, entity, entityID, summary, metadata); err != nil {
		log.Printf("audit write failed for tenant=%s entity=%s id=%s: %v", ac.Tenant.ID, entity, entityID, err)
	}
}

func (a *App) writeAuditEntry(ctx context.Context, tenantID, userID, action, entity, entityID, summary string, metadata map[string]string) error {
	return a.Store.AddAuditEntry(ctx, models.AuditEntry{
		TenantID: tenantID,
		UserID:   userID,
		Action:   action,
		Entity:   entity,
		EntityID: entityID,
		Summary:  summary,
		Metadata: metadata,
	})
}

func (a *App) auditSecurityForUserTenants(ctx context.Context, user models.User, action, entity, entityID, summary string, metadata map[string]string) {
	if strings.TrimSpace(user.ID) == "" {
		return
	}
	memberships, err := a.Store.ListMemberships(ctx, user.ID)
	if err != nil {
		log.Printf("security audit tenant lookup failed for user=%s entity=%s id=%s: %v", user.ID, entity, entityID, err)
		return
	}
	tenantIDs := map[string]bool{}
	for _, membership := range memberships {
		if strings.TrimSpace(membership.TenantID) == "" || tenantIDs[membership.TenantID] {
			continue
		}
		tenantIDs[membership.TenantID] = true
		if err := a.writeAuditEntry(ctx, membership.TenantID, user.ID, action, entity, entityID, summary, securityMetadata(metadata)); err != nil {
			log.Printf("security audit write failed for tenant=%s entity=%s id=%s: %v", membership.TenantID, entity, entityID, err)
		}
	}
}

func mergeMetadata(metadata map[string]string, extras map[string]string) map[string]string {
	if len(metadata) == 0 && len(extras) == 0 {
		return nil
	}
	merged := make(map[string]string, len(metadata)+len(extras))
	for key, value := range metadata {
		merged[key] = value
	}
	for key, value := range extras {
		merged[key] = value
	}
	return merged
}

func securityMetadata(metadata map[string]string) map[string]string {
	return mergeMetadata(metadata, map[string]string{"category": "security"})
}

func formatAuditMetadata(metadata map[string]string) []string {
	if len(metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+": "+metadata[key])
	}
	return lines
}

func (a *App) canManageUser(ctx context.Context, actorRole models.Role, currentTenantID, targetUserID string) bool {
	if canManagePlatform(actorRole) {
		return true
	}
	if !isTenantScopedAdmin(actorRole) || strings.TrimSpace(currentTenantID) == "" || strings.TrimSpace(targetUserID) == "" {
		return false
	}
	memberships, err := a.Store.ListMemberships(ctx, targetUserID)
	if err != nil {
		return false
	}
	for _, membership := range memberships {
		if membership.TenantID == currentTenantID {
			return true
		}
	}
	return false
}

func (a *App) addWorkflowSnapshot(ctx context.Context, ac actionContext, wf models.Workflow) {
	if ac.Tenant.ID == "" || wf.TenderID == "" {
		return
	}
	if err := a.Store.AddWorkflowEvent(ctx, models.WorkflowEvent{
		TenantID:     ac.Tenant.ID,
		TenderID:     wf.TenderID,
		ChangedBy:    ac.User.ID,
		Status:       wf.Status,
		Priority:     wf.Priority,
		AssignedUser: wf.AssignedUser,
		Notes:        wf.Notes,
	}); err != nil {
		log.Printf("workflow snapshot write failed for tenant=%s tender=%s: %v", ac.Tenant.ID, wf.TenderID, err)
	}
}
