# Authorization model

OpenBid uses a scoped authorization model with:

- platform roles on the user record
- tenant roles on tenant memberships
- permissions resolved from both scopes at request time

This keeps platform operations separate from tenant operations and gives the app room to add module-level permissions later without rewriting role checks again.

## Role hierarchy

Platform roles:

- `Platform Super Admin`
- `Platform Admin`

Tenant roles:

- `Tenant Owner`
- `Tenant Admin`
- `Super User`
- `User`
- `Viewer`

Platform roles are stored on `models.User.PlatformRole`.

Tenant roles are stored on `models.Membership.Role`, so the same user can belong to multiple tenants with different tenant roles.

## Permission model

Permissions are defined centrally in [policy.go](/C:/Users/onell/OneDrive/Documents/MyGitProjects/OpenBid/internal/app/policy.go).

Examples:

- `platform.manage`
- `platform.users.read`
- `platform.users.write`
- `platform.roles.assign`
- `platform.tenants.read`
- `platform.tenants.write`
- `platform.sources.read`
- `platform.sources.write`
- `platform.health.read`
- `platform.audit.read`
- `tenant.settings.read`
- `tenant.settings.write`
- `tenant.users.read`
- `tenant.users.write`
- `tenant.roles.assign`
- `bids.read`
- `bids.write`
- `bids.delete`
- `reports.read`
- `reports.export`
- `workflows.manage`
- `integrations.manage`
- `audit.read`
- `queue.manage`
- `bookmarks.manage`
- `saved_searches.manage`

The request-time permission set is the union of:

- permissions granted by the user’s platform role
- permissions granted by the active tenant membership

## Scope rules

- Platform permissions never come from tenant memberships.
- Tenant permissions never come from platform roles unless the route explicitly allows platform-wide access.
- Tenant-scoped reads and writes must always resolve through the active tenant in the current session.
- Platform users still need a tenant membership for a usable in-app workspace context.
- Tenant users cannot see or change platform-only surfaces such as source operations or platform health.

## Enforcement rules

OpenBid now enforces authorization through shared helpers instead of scattered role-name checks:

- permission resolution: [policy.go](/C:/Users/onell/OneDrive/Documents/MyGitProjects/OpenBid/internal/app/policy.go)
- tenant-sensitive action guards: [actions.go](/C:/Users/onell/OneDrive/Documents/MyGitProjects/OpenBid/internal/app/actions.go)
- admin flows: [admin.go](/C:/Users/onell/OneDrive/Documents/MyGitProjects/OpenBid/internal/app/admin.go)

Important behavior:

- Platform Admin cannot manage a Platform Super Admin.
- Tenant Admin cannot assign `Tenant Owner` or `Tenant Admin`.
- Tenant Owner can assign `Tenant Admin`, `Super User`, `User`, and `Viewer`, but not another owner.
- Viewer is read-only and cannot modify workflows, queue state, users, sources, or tenant configuration.
- A tenant must retain at least one `Tenant Owner`.

## Tenant isolation

Tenant isolation is enforced in two layers:

1. Session scope

- every authenticated request resolves an active tenant from the server-side session
- stale or revoked sessions are rejected before tenant data is loaded

2. Data access and mutation scope

- memberships, workflows, bookmarks, saved searches, audit entries, and queue/workflow actions all verify tenant scope
- tenant-scoped administrators cannot manage users or memberships outside their current tenant
- platform roles can act across tenants only on routes that intentionally support platform-wide administration

## Legacy role migration

Existing legacy role names are migrated at startup:

- legacy `admin` membership -> `Platform Super Admin` plus `Tenant Owner`
- legacy `portfolio_manager` membership -> `Platform Admin` plus `Tenant Admin`
- legacy `tenant_admin` -> `Tenant Admin`
- legacy `reviewer` and `operator` -> `Super User`
- legacy `analyst` -> `User`
- legacy `viewer` -> `Viewer`

The migration is idempotent and runs during startup bootstrap.

## Adding a new permission

1. Add the permission constant in [policy.go](/C:/Users/onell/OneDrive/Documents/MyGitProjects/OpenBid/internal/app/policy.go).
2. Add it to the appropriate platform and/or tenant role matrix entries.
3. Introduce a focused helper if the permission will be checked from multiple handlers.
4. Use that helper in handlers instead of inline role-name checks.
5. Add tests for:
   - permission resolution
   - allowed role assignment, if relevant
   - protected route or action behavior

## Developer guidance

- Do not add new authorization checks by comparing raw role strings in handlers.
- Prefer `hasPermission`, `hasAnyPermission`, or the existing helper functions.
- Keep platform and tenant scopes separate even if one user holds both kinds of access.
- When adding tenant-sensitive queries, always require `tenant_id` from server-side context rather than trusting the client.
