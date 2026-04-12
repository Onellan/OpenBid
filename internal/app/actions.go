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
	fallbackURL := safeLocalTarget(fallback, "/")
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return fallbackURL
	}
	return safeLocalTarget(dest, fallbackURL.String())
}

func safeLocalTarget(dest, fallback string) *url.URL {
	fallbackURL := &url.URL{Path: "/"}
	if u, ok := parseSafeLocalURL(fallback); ok {
		fallbackURL = u
	}
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return fallbackURL
	}
	if u, ok := parseSafeLocalURL(dest); ok {
		return u
	}
	return fallbackURL
}

func parseSafeLocalURL(dest string) (*url.URL, bool) {
	dest = strings.TrimSpace(dest)
	u, err := url.Parse(dest)
	if err != nil || u == nil {
		return nil, false
	}
	if u.IsAbs() || u.Host != "" || strings.HasPrefix(dest, "//") || !strings.HasPrefix(u.Path, "/") {
		return nil, false
	}
	return u, true
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

func (a *App) canManageUser(ctx context.Context, actorUser models.User, actorMembership models.Membership, currentTenantID, targetUserID string) bool {
	if strings.TrimSpace(targetUserID) == "" {
		return false
	}
	targetUser, err := a.Store.GetUser(ctx, targetUserID)
	if err != nil {
		return false
	}
	if canManagePlatform(actorUser) {
		return normalizePlatformRole(targetUser.PlatformRole) != models.PlatformRoleSuperAdmin || normalizePlatformRole(actorUser.PlatformRole) == models.PlatformRoleSuperAdmin
	}
	if strings.TrimSpace(currentTenantID) == "" {
		return false
	}
	if normalizePlatformRole(targetUser.PlatformRole) != models.PlatformRoleNone {
		return false
	}
	targetMembership, err := a.Store.GetMembership(ctx, targetUserID, currentTenantID)
	if err != nil {
		return false
	}
	switch normalizeTenantRole(actorMembership.Role) {
	case models.TenantRoleOwner:
		return normalizeTenantRole(targetMembership.Role) != models.TenantRoleOwner
	case models.TenantRoleAdmin:
		switch normalizeTenantRole(targetMembership.Role) {
		case models.TenantRoleSuperUser, models.TenantRoleUser, models.TenantRoleViewer:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (a *App) canManageMembershipAssignment(ctx context.Context, actorUser models.User, actorMembership models.Membership, currentTenantID, targetTenantID string, targetUser models.User, targetRole models.TenantRole) bool {
	if strings.TrimSpace(targetTenantID) == "" || strings.TrimSpace(targetUser.ID) == "" {
		return false
	}
	targetRole = normalizeTenantRole(targetRole)
	if !canAssignTenantRole(actorUser, actorMembership, targetRole) {
		return false
	}
	if canManagePlatform(actorUser) {
		return normalizePlatformRole(targetUser.PlatformRole) != models.PlatformRoleSuperAdmin || normalizePlatformRole(actorUser.PlatformRole) == models.PlatformRoleSuperAdmin
	}
	if targetTenantID != currentTenantID || normalizePlatformRole(targetUser.PlatformRole) != models.PlatformRoleNone {
		return false
	}
	targetMembership, err := a.Store.GetMembership(ctx, targetUser.ID, targetTenantID)
	if err != nil {
		switch normalizeTenantRole(actorMembership.Role) {
		case models.TenantRoleOwner, models.TenantRoleAdmin:
			return true
		default:
			return false
		}
	}
	switch normalizeTenantRole(actorMembership.Role) {
	case models.TenantRoleOwner:
		return normalizeTenantRole(targetMembership.Role) != models.TenantRoleOwner
	case models.TenantRoleAdmin:
		switch normalizeTenantRole(targetMembership.Role) {
		case models.TenantRoleSuperUser, models.TenantRoleUser, models.TenantRoleViewer:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (a *App) isLastTenantOwner(ctx context.Context, tenantID, membershipID string) bool {
	memberships, err := a.Store.ListMembershipsByTenant(ctx, tenantID)
	if err != nil {
		return false
	}
	owners := 0
	for _, membership := range memberships {
		if membership.ID == membershipID {
			continue
		}
		if normalizeTenantRole(membership.Role) == models.TenantRoleOwner {
			owners++
		}
	}
	return owners == 0
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
