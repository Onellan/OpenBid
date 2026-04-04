# OpenBid

OpenBid is a lightweight, self-hosted, multi-tenant tender aggregation platform for engineering-focused South African opportunities.

## What is implemented

- Go web app with standard-library server rendering
- HTMX-style server-side workflow with reusable template partials
- SQLite runtime store at `./data/store.db`
- Go worker for sync and extraction queue processing
- Python extractor service for document text extraction
- Multi-tenant workflow, bookmarks, saved searches, audit log, queue views, and tender detail pages
- Docker Compose deployment
- Unit and integration-style tests for the current runtime path

## Development bootstrap credentials

- Username: `admin`
- Password: `OpenBid!2026`

These are seeded only for local development when `APP_ENV=development` and `BOOTSTRAP_ADMIN_PASSWORD` is left empty. For production, set a strong `SECRET_KEY`, enable `SECURE_COOKIES=true`, and provide `BOOTSTRAP_ADMIN_PASSWORD` before first startup.

## Quick start

### Local Go run

```bash
cp .env.example .env
go mod tidy
go test ./...
go run ./cmd/server
```

Open `http://localhost:8080`.

### Docker Compose run

```bash
cp .env.example .env
docker compose up --build
```

Open `http://localhost:8088`.

## Configuration notes

- Default local database path: `./data/store.db`
- Docker database path: `/app/data/store.db`
- The app and worker both expect SQLite.
- Fresh production deployments do not auto-import tenders on first boot unless `BOOTSTRAP_SYNC_ON_STARTUP=true` is explicitly set.
- Production startup requires a strong non-default `SECRET_KEY`, `SECURE_COOKIES=true`, and a strong `BOOTSTRAP_ADMIN_PASSWORD` when the database is empty.

## Environment file

Start from:

```bash
cp .env.example .env
```

Common variables:

- `APP_ENV`: use `development` locally, `production` for hardened deployments
- `SECRET_KEY`: at least 32 strong random characters in production
- `SECRET_KEY_FILE`: preferred production path for a mounted secret file
- `SECURE_COOKIES`: must be `true` in production
- `BOOTSTRAP_ADMIN_PASSWORD`: initial admin password for first boot in production
- `BOOTSTRAP_ADMIN_PASSWORD_FILE`: preferred production path for a mounted bootstrap password file
- `BOOTSTRAP_SYNC_ON_STARTUP`: whether to sync sources on the first startup. Default: `false`
- `LOW_MEMORY_MODE`: keep `true` on smaller machines such as Raspberry Pi
- `BACKUP_DIR`: directory used for runtime backup freshness checks
- `WORKER_SYNC_MINUTES`: default source check interval
- `WORKER_LOOP_SECONDS`: worker polling loop interval
- `LOGIN_RATE_LIMIT_WINDOW_SECONDS`: rolling per-IP login throttling window for direct app access
- `LOGIN_RATE_LIMIT_MAX_ATTEMPTS`: maximum failed login attempts allowed within that window
- `ALERT_WEBHOOK_URL`: optional webhook for operational alerts
- `ALERT_EVAL_SECONDS`: how often the server evaluates operational alerts
- `ALERT_BACKUP_MAX_AGE_MINUTES`: backup freshness threshold before raising an alert
- `ALERT_BACKLOG_MAX_JOBS`: queued/retry/processing job count threshold before raising a backlog alert
- `ALERT_BACKLOG_MAX_AGE_MINUTES`: oldest pending job age threshold before raising a backlog alert
- `ALERT_LOGIN_THROTTLE_THRESHOLD`: recent login throttle events required before raising an alert
- `ALERT_EXTRACTOR_FAILURE_THRESHOLD`: failed/retry extraction jobs required before raising an alert

Production deployments should prefer `.env.production.example`, pinned GHCR image tags, mounted secret files under `./secrets`, and the packaged operations runbook in `docs/production-operations.md`.

## Raspberry Pi deployment

For a self-contained Raspberry Pi deployment bundle with Dockerfiles, Compose config, nginx config, `.env` example, and a step-by-step installation guide, see [InstallationInstructions/README.md](InstallationInstructions/README.md).

For the older narrative Raspberry Pi guide, including Cloudflare-in-front HTTP origin deployment notes, see [docs/raspberry-pi-docker-setup.md](docs/raspberry-pi-docker-setup.md).

## Operations

- Production runbook: [docs/production-operations.md](docs/production-operations.md)
- SQLite backup: `./scripts/sqlite-backup.sh`
- SQLite restore: `./scripts/sqlite-restore.sh`
- SQLite validation: `./scripts/sqlite-validate.sh`
- Container alert check: `./scripts/docker-alert-check.sh`
- Operational alert feed: `/health/alerts.json`

## Validation

```bash
go mod tidy
go test ./...
docker compose up --build
```

## Useful files

- `cmd/server`
- `cmd/worker`
- `cmd/sqlite_check`
- `internal/app`
- `internal/store`
- `web/templates`
- `scripts/run-local.sh`
- `scripts/sqlite-validate.sh`
