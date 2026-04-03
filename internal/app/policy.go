package app

import (
	"context"
	"net/http"
	"strings"

	"tenderhub-za/internal/models"
)

func canAdminUsers(role models.Role) bool {
	return role == models.RoleAdmin || role == models.RolePortfolioManager || role == models.RoleTenantAdmin
}

func canManageTenants(role models.Role) bool {
	return role == models.RoleAdmin || role == models.RolePortfolioManager
}

func canManageSources(role models.Role) bool {
	return canManagePlatform(role)
}

func canManageAudit(role models.Role) bool {
	return canAdminUsers(role)
}

func canViewPlatformHealth(role models.Role) bool {
	return canManagePlatform(role)
}

func canManagePlatform(role models.Role) bool {
	return role == models.RoleAdmin || role == models.RolePortfolioManager
}

func isTenantScopedAdmin(role models.Role) bool {
	return role == models.RoleTenantAdmin
}

func canAssignManagedRole(actorRole, targetRole models.Role) bool {
	if !isValidRole(targetRole) {
		return false
	}
	if canManagePlatform(actorRole) {
		return true
	}
	if !isTenantScopedAdmin(actorRole) {
		return false
	}
	switch targetRole {
	case models.RoleAnalyst, models.RoleReviewer, models.RoleOperator, models.RoleViewer:
		return true
	default:
		return false
	}
}

func canEditWorkspace(role models.Role) bool {
	return role != models.RoleViewer
}

func roleRequiresMFA(role models.Role) bool {
	return role == models.RoleAdmin || role == models.RolePortfolioManager
}

func hasPrivilegedMembership(memberships []models.Membership) bool {
	for _, membership := range memberships {
		if roleRequiresMFA(membership.Role) {
			return true
		}
	}
	return false
}

func (a *App) userRequiresPrivilegedMFA(ctx context.Context, userID string) bool {
	if strings.TrimSpace(userID) == "" {
		return false
	}
	memberships, err := a.Store.ListMemberships(ctx, userID)
	if err != nil {
		return false
	}
	return hasPrivilegedMembership(memberships)
}

func allowsMFABootstrapPath(path string) bool {
	switch strings.TrimSpace(path) {
	case "/logout", "/settings", "/password", "/settings/password", "/mfa", "/settings/mfa", "/mfa/setup", "/mfa/disable", "/mfa/recovery/regenerate":
		return true
	default:
		return false
	}
}

func proxyHeadersPresent(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-Forwarded-Host")) != "" && strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")) != ""
}
