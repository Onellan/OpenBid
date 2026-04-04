package app

import (
	"net/http"
	"slices"
	"strings"

	"openbid/internal/models"
)

type Permission string

const (
	PermissionPlatformManage       Permission = "platform.manage"
	PermissionPlatformUsersRead    Permission = "platform.users.read"
	PermissionPlatformUsersWrite   Permission = "platform.users.write"
	PermissionPlatformRolesAssign  Permission = "platform.roles.assign"
	PermissionPlatformTenantsRead  Permission = "platform.tenants.read"
	PermissionPlatformTenantsWrite Permission = "platform.tenants.write"
	PermissionPlatformSourcesRead  Permission = "platform.sources.read"
	PermissionPlatformSourcesWrite Permission = "platform.sources.write"
	PermissionPlatformHealthRead   Permission = "platform.health.read"
	PermissionPlatformAuditRead    Permission = "platform.audit.read"
	PermissionTenantSettingsRead   Permission = "tenant.settings.read"
	PermissionTenantSettingsWrite  Permission = "tenant.settings.write"
	PermissionTenantUsersRead      Permission = "tenant.users.read"
	PermissionTenantUsersWrite     Permission = "tenant.users.write"
	PermissionTenantRolesAssign    Permission = "tenant.roles.assign"
	PermissionBidsRead             Permission = "bids.read"
	PermissionBidsWrite            Permission = "bids.write"
	PermissionBidsDelete           Permission = "bids.delete"
	PermissionReportsRead          Permission = "reports.read"
	PermissionReportsExport        Permission = "reports.export"
	PermissionWorkflowsManage      Permission = "workflows.manage"
	PermissionIntegrationsManage   Permission = "integrations.manage"
	PermissionAuditRead            Permission = "audit.read"
	PermissionQueueManage          Permission = "queue.manage"
	PermissionBookmarksManage      Permission = "bookmarks.manage"
	PermissionSavedSearchesManage  Permission = "saved_searches.manage"
)

type permissionSet map[Permission]bool

var platformRolePermissionMatrix = map[models.PlatformRole][]Permission{
	models.PlatformRoleSuperAdmin: {
		PermissionPlatformManage,
		PermissionPlatformUsersRead,
		PermissionPlatformUsersWrite,
		PermissionPlatformRolesAssign,
		PermissionPlatformTenantsRead,
		PermissionPlatformTenantsWrite,
		PermissionPlatformSourcesRead,
		PermissionPlatformSourcesWrite,
		PermissionPlatformHealthRead,
		PermissionPlatformAuditRead,
	},
	models.PlatformRoleAdmin: {
		PermissionPlatformUsersRead,
		PermissionPlatformUsersWrite,
		PermissionPlatformTenantsRead,
		PermissionPlatformTenantsWrite,
		PermissionPlatformSourcesRead,
		PermissionPlatformSourcesWrite,
		PermissionPlatformHealthRead,
		PermissionPlatformAuditRead,
	},
}

var tenantRolePermissionMatrix = map[models.TenantRole][]Permission{
	models.TenantRoleOwner: {
		PermissionTenantSettingsRead,
		PermissionTenantSettingsWrite,
		PermissionTenantUsersRead,
		PermissionTenantUsersWrite,
		PermissionTenantRolesAssign,
		PermissionBidsRead,
		PermissionBidsWrite,
		PermissionBidsDelete,
		PermissionReportsRead,
		PermissionReportsExport,
		PermissionWorkflowsManage,
		PermissionIntegrationsManage,
		PermissionAuditRead,
		PermissionQueueManage,
		PermissionBookmarksManage,
		PermissionSavedSearchesManage,
	},
	models.TenantRoleAdmin: {
		PermissionTenantSettingsRead,
		PermissionTenantSettingsWrite,
		PermissionTenantUsersRead,
		PermissionTenantUsersWrite,
		PermissionTenantRolesAssign,
		PermissionBidsRead,
		PermissionBidsWrite,
		PermissionReportsRead,
		PermissionReportsExport,
		PermissionWorkflowsManage,
		PermissionIntegrationsManage,
		PermissionAuditRead,
		PermissionQueueManage,
		PermissionBookmarksManage,
		PermissionSavedSearchesManage,
	},
	models.TenantRoleSuperUser: {
		PermissionTenantSettingsRead,
		PermissionTenantUsersRead,
		PermissionBidsRead,
		PermissionBidsWrite,
		PermissionReportsRead,
		PermissionReportsExport,
		PermissionWorkflowsManage,
		PermissionAuditRead,
		PermissionQueueManage,
		PermissionBookmarksManage,
		PermissionSavedSearchesManage,
	},
	models.TenantRoleUser: {
		PermissionTenantSettingsRead,
		PermissionTenantUsersRead,
		PermissionBidsRead,
		PermissionBidsWrite,
		PermissionReportsRead,
		PermissionReportsExport,
		PermissionWorkflowsManage,
		PermissionQueueManage,
		PermissionBookmarksManage,
		PermissionSavedSearchesManage,
	},
	models.TenantRoleViewer: {
		PermissionTenantSettingsRead,
		PermissionTenantUsersRead,
		PermissionBidsRead,
		PermissionReportsRead,
	},
}

var allTenantRoles = []models.TenantRole{
	models.TenantRoleOwner,
	models.TenantRoleAdmin,
	models.TenantRoleSuperUser,
	models.TenantRoleUser,
	models.TenantRoleViewer,
}

var allPlatformRoles = []models.PlatformRole{
	models.PlatformRoleSuperAdmin,
	models.PlatformRoleAdmin,
}

func buildPermissionSet(user models.User, membership models.Membership) permissionSet {
	set := permissionSet{}
	for _, permission := range platformRolePermissionMatrix[normalizePlatformRole(user.PlatformRole)] {
		set[permission] = true
	}
	for _, permission := range tenantRolePermissionMatrix[normalizeTenantRole(membership.Role)] {
		set[permission] = true
	}
	return set
}

func hasPermission(user models.User, membership models.Membership, permission Permission) bool {
	return buildPermissionSet(user, membership)[permission]
}

func hasAnyPermission(user models.User, membership models.Membership, permissions ...Permission) bool {
	set := buildPermissionSet(user, membership)
	for _, permission := range permissions {
		if set[permission] {
			return true
		}
	}
	return false
}

func normalizePlatformRole(role models.PlatformRole) models.PlatformRole {
	switch strings.TrimSpace(string(role)) {
	case string(models.PlatformRoleSuperAdmin):
		return models.PlatformRoleSuperAdmin
	case string(models.PlatformRoleAdmin):
		return models.PlatformRoleAdmin
	default:
		return models.PlatformRoleNone
	}
}

func normalizeTenantRole(role models.TenantRole) models.TenantRole {
	switch strings.TrimSpace(string(role)) {
	case string(models.TenantRoleOwner):
		return models.TenantRoleOwner
	case string(models.TenantRoleAdmin):
		return models.TenantRoleAdmin
	case string(models.TenantRoleSuperUser):
		return models.TenantRoleSuperUser
	case string(models.TenantRoleUser):
		return models.TenantRoleUser
	case string(models.TenantRoleViewer):
		return models.TenantRoleViewer
	case "admin":
		return models.TenantRoleOwner
	case "portfolio_manager":
		return models.TenantRoleAdmin
	case "analyst":
		return models.TenantRoleUser
	case "reviewer", "operator":
		return models.TenantRoleSuperUser
	default:
		return models.TenantRoleUser
	}
}

func platformRoleLabel(role models.PlatformRole) string {
	switch normalizePlatformRole(role) {
	case models.PlatformRoleSuperAdmin:
		return "Platform Super Admin"
	case models.PlatformRoleAdmin:
		return "Platform Admin"
	default:
		return "None"
	}
}

func tenantRoleLabel(role models.TenantRole) string {
	switch normalizeTenantRole(role) {
	case models.TenantRoleOwner:
		return "Tenant Owner"
	case models.TenantRoleAdmin:
		return "Tenant Admin"
	case models.TenantRoleSuperUser:
		return "Super User"
	case models.TenantRoleUser:
		return "User"
	case models.TenantRoleViewer:
		return "Viewer"
	default:
		return "User"
	}
}

func validPlatformRole(role models.PlatformRole) bool {
	switch strings.TrimSpace(string(role)) {
	case "", string(models.PlatformRoleSuperAdmin), string(models.PlatformRoleAdmin):
		return true
	default:
		return false
	}
}

func validTenantRole(role models.TenantRole) bool {
	switch strings.TrimSpace(string(role)) {
	case string(models.TenantRoleOwner),
		string(models.TenantRoleAdmin),
		string(models.TenantRoleSuperUser),
		string(models.TenantRoleUser),
		string(models.TenantRoleViewer),
		"admin",
		"portfolio_manager",
		"analyst",
		"reviewer",
		"operator":
		return true
	default:
		return false
	}
}

func allowedPlatformRolesForActor(user models.User) []models.PlatformRole {
	switch normalizePlatformRole(user.PlatformRole) {
	case models.PlatformRoleSuperAdmin:
		return append([]models.PlatformRole{}, allPlatformRoles...)
	case models.PlatformRoleAdmin:
		return []models.PlatformRole{models.PlatformRoleAdmin}
	default:
		return nil
	}
}

func allowedTenantRolesForActor(user models.User, membership models.Membership) []models.TenantRole {
	switch {
	case normalizePlatformRole(user.PlatformRole) == models.PlatformRoleSuperAdmin:
		return append([]models.TenantRole{}, allTenantRoles...)
	case normalizePlatformRole(user.PlatformRole) == models.PlatformRoleAdmin:
		return append([]models.TenantRole{}, allTenantRoles...)
	}
	switch normalizeTenantRole(membership.Role) {
	case models.TenantRoleOwner:
		return []models.TenantRole{models.TenantRoleAdmin, models.TenantRoleSuperUser, models.TenantRoleUser, models.TenantRoleViewer}
	case models.TenantRoleAdmin:
		return []models.TenantRole{models.TenantRoleSuperUser, models.TenantRoleUser, models.TenantRoleViewer}
	default:
		return nil
	}
}

func canAssignPlatformRole(actorUser models.User, targetRole models.PlatformRole) bool {
	if !validPlatformRole(targetRole) {
		return false
	}
	return slices.Contains(allowedPlatformRolesForActor(actorUser), normalizePlatformRole(targetRole))
}

func canAssignTenantRole(actorUser models.User, actorMembership models.Membership, targetRole models.TenantRole) bool {
	if !validTenantRole(targetRole) {
		return false
	}
	return slices.Contains(allowedTenantRolesForActor(actorUser, actorMembership), normalizeTenantRole(targetRole))
}

func migrateLegacyPlatformRole(user models.User, memberships []models.Membership) models.PlatformRole {
	role := normalizePlatformRole(user.PlatformRole)
	if role != models.PlatformRoleNone {
		return role
	}
	for _, membership := range memberships {
		switch strings.TrimSpace(string(membership.Role)) {
		case "admin":
			return models.PlatformRoleSuperAdmin
		case "portfolio_manager":
			return models.PlatformRoleAdmin
		}
	}
	return models.PlatformRoleNone
}

func migrateLegacyTenantRole(role models.TenantRole) models.TenantRole {
	return normalizeTenantRole(role)
}

func canAdminUsers(user models.User, membership models.Membership) bool {
	return hasAnyPermission(user, membership, PermissionPlatformUsersWrite, PermissionTenantUsersWrite)
}

func canManageTenants(user models.User, membership models.Membership) bool {
	return hasPermission(user, membership, PermissionPlatformTenantsWrite)
}

func canManageSources(user models.User, membership models.Membership) bool {
	return hasPermission(user, membership, PermissionPlatformSourcesWrite)
}

func canManageAudit(user models.User, membership models.Membership) bool {
	return hasAnyPermission(user, membership, PermissionPlatformAuditRead, PermissionAuditRead)
}

func canViewPlatformHealth(user models.User, membership models.Membership) bool {
	return hasPermission(user, membership, PermissionPlatformHealthRead)
}

func canManagePlatform(user models.User) bool {
	return normalizePlatformRole(user.PlatformRole) == models.PlatformRoleSuperAdmin || normalizePlatformRole(user.PlatformRole) == models.PlatformRoleAdmin
}

func canReadSources(user models.User, membership models.Membership) bool {
	return hasPermission(user, membership, PermissionPlatformSourcesRead)
}

func canEditWorkspace(user models.User, membership models.Membership) bool {
	return hasAnyPermission(user, membership, PermissionBidsWrite, PermissionWorkflowsManage, PermissionQueueManage)
}

func canQueueWork(user models.User, membership models.Membership) bool {
	return hasPermission(user, membership, PermissionQueueManage)
}

func canManageTenantSettings(user models.User, membership models.Membership) bool {
	return hasPermission(user, membership, PermissionTenantSettingsWrite)
}

func proxyHeadersPresent(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-Forwarded-Host")) != "" && strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")) != ""
}
