# TenderHub ZA second-pass completion sheet

This file closes the ambiguity from the previous bundle by mapping the uploaded brief to implementation status and the exact remaining code tasks.

## Added in this second-pass bundle
- GitHub Actions CI workflow
- GitHub Container Registry publish workflow for release branches
- GHCR pull-based compose example
- Explicit checklist of missing vs partial features
- Direct coding order for the next implementation pass

## Already present in the repo
- Go app + Go worker + Python extractor
- Docker Compose stack with app / worker / reverse proxy / extractor
- Treasury-first adapter approach plus source stubs
- Session cookies, lockout logic, password policy, CSRF checks
- Dashboard, tenders page, saved searches, user admin, tenant admin
- Queue and extraction flow scaffolding
- CSV export, health endpoint, seed data, README

## Still partial against the uploaded brief
1. Bulk workflow UI and bulk bookmark / bulk queue UX in the running app
2. Tenant selector visible in the layout
3. Full membership management flows in the admin screen
4. Password change flow in the UI
5. MFA setup / confirm / disable / recovery-code lifecycle in the UI
6. Activate / deactivate user flow in the UI
7. Admin reset-password flow in the UI
8. Queue visibility beyond dashboard basics
9. Table/card view toggle
10. Modal-based actions
11. Full CSV export including tenant workflow fields and extracted fact columns
12. Stronger tenant-boundary tests
13. HTTP handler tests for CSRF, lockout, exports, auth, and admin flows
14. Release-branch E2E job behavior
15. Dependency audit as a default lightweight step

## Exact coding order for the next code pass
1. Extend the store interface with workflow lookup, bulk workflow updates, membership list/update/delete, and bulk queue/bookmark helpers.
2. Add app handlers for:
   - /tenders/bulk
   - /password
   - /mfa
   - /mfa/setup
   - /mfa/disable
   - /admin/users/toggle
   - /admin/users/reset-password
   - /admin/memberships/upsert
   - /admin/memberships/delete
3. Update base template to include tenant selector and password/MFA navigation.
4. Update tenders template to include:
   - selected tender checkboxes
   - bulk action panel
   - workflow summary badges
   - clearer queue states
5. Update admin users template to include:
   - activate/deactivate
   - reset password
   - membership editing and deletion
6. Add password and MFA templates.
7. Expand CSV export to include:
   - workflow status
   - priority
   - assigned user
   - extracted fact fields
8. Add tests for:
   - lockouts
   - session cookie encode/decode
   - CSRF-required POSTs
   - tenant switching
   - bulk workflow updates
   - CSV field coverage
   - user admin actions

## Production readiness summary
The current repo is a strong runnable MVP skeleton, not yet a fully complete production implementation of every UI and test requirement from the uploaded brief. The repo is ready for the focused third pass above.
