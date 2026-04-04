package app

import (
	"testing"

	"openbid/internal/models"
)

func TestPermissionResolutionByScope(t *testing.T) {
	tests := []struct {
		name       string
		user       models.User
		membership models.Membership
		permission Permission
		want       bool
	}{
		{
			name:       "platform admin can read platform health",
			user:       models.User{PlatformRole: models.PlatformRoleAdmin},
			membership: models.Membership{Role: models.TenantRoleViewer},
			permission: PermissionPlatformHealthRead,
			want:       true,
		},
		{
			name:       "tenant owner can manage tenant users",
			user:       models.User{},
			membership: models.Membership{Role: models.TenantRoleOwner},
			permission: PermissionTenantUsersWrite,
			want:       true,
		},
		{
			name:       "viewer is read only",
			user:       models.User{},
			membership: models.Membership{Role: models.TenantRoleViewer},
			permission: PermissionBidsWrite,
			want:       false,
		},
		{
			name:       "viewer cannot read platform health",
			user:       models.User{},
			membership: models.Membership{Role: models.TenantRoleViewer},
			permission: PermissionPlatformHealthRead,
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasPermission(tc.user, tc.membership, tc.permission); got != tc.want {
				t.Fatalf("hasPermission(%s) = %v want %v", tc.permission, got, tc.want)
			}
		})
	}
}

func TestRoleAssignmentRules(t *testing.T) {
	superAdmin := models.User{PlatformRole: models.PlatformRoleSuperAdmin}
	platformAdmin := models.User{PlatformRole: models.PlatformRoleAdmin}
	tenantOwner := models.Membership{Role: models.TenantRoleOwner}
	tenantAdmin := models.Membership{Role: models.TenantRoleAdmin}

	if !canAssignPlatformRole(superAdmin, models.PlatformRoleSuperAdmin) {
		t.Fatal("expected platform super admin to assign platform super admin")
	}
	if canAssignPlatformRole(platformAdmin, models.PlatformRoleSuperAdmin) {
		t.Fatal("expected platform admin to be blocked from assigning super admin")
	}
	if !canAssignTenantRole(superAdmin, tenantOwner, models.TenantRoleOwner) {
		t.Fatal("expected platform super admin to assign tenant owner")
	}
	if !canAssignTenantRole(models.User{}, tenantOwner, models.TenantRoleAdmin) {
		t.Fatal("expected tenant owner to assign tenant admin")
	}
	if canAssignTenantRole(models.User{}, tenantOwner, models.TenantRoleOwner) {
		t.Fatal("expected tenant owner to be blocked from assigning another owner")
	}
	if !canAssignTenantRole(models.User{}, tenantAdmin, models.TenantRoleSuperUser) {
		t.Fatal("expected tenant admin to assign super user")
	}
	if canAssignTenantRole(models.User{}, tenantAdmin, models.TenantRoleAdmin) {
		t.Fatal("expected tenant admin to be blocked from assigning tenant admin")
	}
}

func TestLegacyRoleMigrationMappings(t *testing.T) {
	if got := migrateLegacyPlatformRole(models.User{}, []models.Membership{{Role: models.TenantRole("admin")}}); got != models.PlatformRoleSuperAdmin {
		t.Fatalf("expected legacy admin to map to platform super admin, got %q", got)
	}
	if got := migrateLegacyPlatformRole(models.User{}, []models.Membership{{Role: models.TenantRole("portfolio_manager")}}); got != models.PlatformRoleAdmin {
		t.Fatalf("expected legacy portfolio manager to map to platform admin, got %q", got)
	}
	if got := migrateLegacyTenantRole(models.TenantRole("reviewer")); got != models.TenantRoleSuperUser {
		t.Fatalf("expected legacy reviewer to map to super user, got %q", got)
	}
	if got := migrateLegacyTenantRole(models.TenantRole("analyst")); got != models.TenantRoleUser {
		t.Fatalf("expected legacy analyst to map to user, got %q", got)
	}
}
