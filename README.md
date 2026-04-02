<<<<<<< HEAD
# OpenBid

OpenBid is a lightweight, self-hosted, multi-tenant tender aggregation platform for engineering-focused South African opportunities. This bundle matches the uploaded brief's core requirements: Docker deployment, worker separation, HTMX-driven Go UX, source adapters, extraction queue, CSV export, multi-tenant workflow management, and low-memory operation.

## What is implemented now

- Go web app using the standard library
- HTMX-enabled server-rendered UI
- Go worker process for scheduled sync and extraction queue processing
- Python extraction service using `pdftotext` for PDFs and HTML stripping for pages
- Multi-tenant data model with tenant-specific workflow, bookmarks, and saved searches
- Session cookies, CSRF checks, password policy, login lockouts, optional TOTP MFA plumbing
- Reverse proxy with request rate limiting
- CSV export
- Dashboard with low-memory analytics gating
- Adapter registry with Treasury-first implementation and stubs for Eskom, Transnet, CIDB, DBSA, DPWI
- Docker Compose deployment
- Basic unit tests

## Important implementation note

This repository is intentionally stdlib-first so it can compile in an offline environment without pulling external Go modules. To keep it runnable now, the default persistence layer is an atomic JSON file store at `./data/store.json` instead of SQLite/PostgreSQL.

That means:
- the app is runnable now
- the topology matches the intended production architecture
- the main remaining production upgrade is replacing the file store with SQLite or PostgreSQL

A starter SQL migration is included in `migrations/001_init.sql`.

## Default credentials

- Username: `admin`
- Password: `TenderHub!2026`

## Run locally

```bash
cp .env.example .env
go test ./...
go run ./cmd/server
```

Open `http://localhost:8080`.

## Run in Docker

```bash
cp .env.example .env
docker compose up --build
```

Open `http://localhost:8088`.

## Architecture summary

```text
[Browser] -> [Cloudflare] -> [Nginx] -> [Go web app] -> [JSON store now / SQL later]
                                          |
                                          v
                                      [Go worker] -> [Python extractor]
```

## Still partial

- Full MFA setup/disable/recovery-code UI
- Bulk workflow and bulk queue UI
- Full live Treasury feed field mapping for your final chosen endpoint
- Secondary live source adapters
- SQL-backed production store


## Second-pass repository additions

This bundle now also includes:

- `.github/workflows/ci.yml`
- `.github/workflows/release-images.yml`
- `docker-compose.ghcr.yml`
- `SECOND_PASS_GAPS.md`

Use `SECOND_PASS_GAPS.md` as the direct implementation checklist to finish the remaining UI/admin/test items from the original brief.


## Completed implementation pass

This bundle includes a persisted completion pass for the previously partial areas: bulk tender actions, queue page, tenant switcher, password change UI, MFA setup/disable UI, richer export plumbing, and expanded repository structure for the remaining admin flows.


## SQLite-only runtime

This repository now uses SQLite as the only application data store.

### Runtime data file
- Default database path: `./data/store.db`
- Docker database path: `/app/data/store.db`

### Repository cleanup
The old JSON runtime store and the in-repo JSON migration utility have been removed so the codebase stays focused on the actual production runtime path.

### Validation to run locally

```bash
go mod tidy
go test ./...
docker compose up --build
```


## OpenBid brand implementation

This repository now includes a full OpenBid brand and interface direction aligned to:
- trust
- transparency
- professionalism
- efficiency
- structure
- progress

See:
- `BRAND_SYSTEM_OPENBID.md`

The current template layer has been rebranded with:
- OpenBid logo direction in-header
- light SaaS interface palette
- updated dashboard, login, queue, admin, and opportunity styling
- OpenBid homepage/login copy and product messaging


## Design system implementation

The server-rendered OpenBid app now includes a fuller design system layer.

Key files:
- `DESIGN_SYSTEM_OPENBID.md`
- `DESIGN_SYSTEM_IMPLEMENTATION_PASS.md`
- `web/templates/components.html`
- `web/templates/patterns.html`
- `web/templates/base.html`

This pass formalizes:
- design tokens
- layout primitives
- surface hierarchy
- data display patterns
- form patterns
- message/state patterns
- reusable template components


## Higher-level partials pass

The server-rendered frontend now standardizes more repeated interaction patterns into higher-level partials.

Key files:
- `web/templates/patterns.html`
- `web/templates/interaction_partials.html`
- `HIGHER_LEVEL_PARTIALS_PASS.md`

This pass focuses on:
- reusable tables
- reusable forms
- reusable status badges
- higher-level interaction patterns


## Opportunity partials refactor

The opportunities screen now uses dedicated higher-level partials for:
- filter forms
- bulk action forms
- opportunity table views
- status clusters
- action panels
- full opportunity cards

Key files:
- `web/templates/opportunity_partials.html`
- `OPPORTUNITY_PARTIALS_REFACTOR.md`


## Admin typed tables and confirmation pass

The admin layer now uses:
- admin-specific form partials
- a generic entity table card wrapper with typed inner partials
- a reusable destructive-action confirmation pattern

Key files:
- `web/templates/admin_partials.html`
- `web/templates/patterns.html`
- `ADMIN_TYPED_TABLES_AND_CONFIRMATION_PASS.md`


## Domain typed tables, forms, and confirmations pass

The typed table + form + confirm pattern now also covers:
- saved searches
- pipeline jobs
- opportunity-level destructive actions

Key files:
- `web/templates/domain_partials.html`
- `DOMAIN_TYPED_TABLES_FORMS_CONFIRM_PASS.md`


## Template stability verification pass

The template layer has been tightened to remove brittle composition and unsafe root-context assumptions.

Key file:
- `TEMPLATE_STABILITY_VERIFICATION_PASS.md`

Most important fix:
- opportunity-level partials now receive explicit `{Root, Item}` data instead of assuming root page state is still available implicitly inside nested partials.


## SQLite reliability validation pass

This repository now includes a SQLite reliability hardening pass focused on:
- migration/runtime validation
- safer SQLite pragmas and single-writer pool settings
- queue deduplication tests
- concurrent write tests
- seeded startup tests
- backup/restore scripts
- a JSON-assumption audit script

New utilities:
- `cmd/sqlite_check`
- `scripts/sqlite-backup.sh`
- `scripts/sqlite-restore.sh`
- `scripts/sqlite-validate.sh`
- `scripts/audit-json-assumptions.sh`


## Operational features pass

This pass adds:
- pagination
- improved sorting/filter persistence
- queue retry visibility
- audit log
- workflow history
- tender/extraction detail view
- stronger empty/success/error states

See:
- `OPERATIONAL_FEATURES_PASS.md`
=======
# OpenBid
>>>>>>> a67e7b754ed1c5048a2498f20b36ceb28d5e8707
