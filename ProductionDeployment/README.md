# OpenBid Production Deployment

Everything needed to deploy OpenBid in production lives in this folder.

Runtime state is created and kept under:

```text
ProductionDeployment/runtime/
ProductionDeployment/runtime/data/
ProductionDeployment/runtime/backups/
ProductionDeployment/runtime/secrets/
```

The deployment is path-agnostic. Clone the repository anywhere, then run commands from this folder.

## First Start From Source

```bash
cd ProductionDeployment
./setup.sh
docker compose up -d --build
```

Open:

```text
http://localhost:8088
```

## First Start From GHCR

GHCR images are published by `.github/workflows/release-images.yml` after the `ci` workflow succeeds for a push to `main`.

```bash
cd ProductionDeployment
./setup.sh
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
```

## What setup.sh Does

`setup.sh` is idempotent. It is safe to run multiple times.

It creates:

- `runtime/data`
- `runtime/backups`
- `runtime/secrets`

It also creates missing production secret files:

- `runtime/secrets/openbid_secret_key`
- `runtime/secrets/openbid_bootstrap_admin_password`

Existing runtime files are never overwritten. The bootstrap admin password is only used when the database is empty.

Read the generated first-login password with:

```bash
cat runtime/secrets/openbid_bootstrap_admin_password
```

After first login, rotate the admin password in the UI.

## Environment Overrides

The compose files have production-safe defaults. To override settings:

```bash
cp .env.example .env
nano .env
```

Common values:

```env
OPENBID_HTTP_PORT=8088
LOW_MEMORY_MODE=true
BOOTSTRAP_SYNC_ON_STARTUP=false
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
APP_IMAGE_TAG=latest
EXTRACTOR_IMAGE_TAG=latest
```

Host-side production secrets live in `runtime/secrets/` and Docker Compose injects them into the `app` and `worker` containers as Compose secrets. The application keeps reading the same in-container paths:

```env
SECRET_KEY_FILE=/run/secrets/openbid_secret_key
BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/openbid_bootstrap_admin_password
```

Do not put production secrets directly into `.env`; keep them in `runtime/secrets/` and let Compose mount them as secrets.

## Runtime Mounts

All runtime bind mounts are relative to this folder:

```yaml
./runtime/data:/app/data
./runtime/backups:/app/backups
```

Secrets are not exposed with a direct directory bind mount. They are declared as Compose secrets sourced from:

```yaml
runtime/secrets/openbid_secret_key
runtime/secrets/openbid_bootstrap_admin_password
```

No host-specific install path is required.

## CI Smoke Secrets

GitHub Actions smoke tests use environment-based test secrets only for the short-lived CI stack. Production deployments should use the `runtime/secrets/` files injected through Compose secrets.

## Daily Commands

Status:

```bash
docker compose ps
curl http://localhost:8088/healthz
```

Logs:

```bash
docker compose logs --tail=200
docker compose logs -f
```

Restart:

```bash
docker compose restart
```

Stop:

```bash
docker compose down
```

Update source build:

```bash
git pull
cd ProductionDeployment
./setup.sh
docker compose up -d --build
```

Update GHCR images:

The `latest` tag follows successful `main` builds. For a pinned deploy, set `APP_IMAGE_TAG` and `EXTRACTOR_IMAGE_TAG` to the matching `sha-FULL_COMMIT_SHA` tag before pulling.

```bash
cd ProductionDeployment
./setup.sh
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
```

## Backup And Restore

Create a database backup:

```bash
docker compose exec -T app openbid-sqlite-backup /app/backups/store-$(date +%Y%m%d-%H%M%S).db
```

Backups appear in:

```text
runtime/backups/
```

Validate the current database:

```bash
docker compose run --rm --no-deps app openbid-sqlite-check
```

Restore a backup while the write-path containers are stopped:

```bash
docker compose stop proxy app worker
cp runtime/backups/store-YYYYMMDD-HHMMSS.db runtime/data/store.db
rm -f runtime/data/store.db-wal runtime/data/store.db-shm
docker compose run --rm --no-deps app openbid-sqlite-check
docker compose up -d
```

## Nginx

Default compose files use:

```text
nginx/nginx.conf
```

Raspberry Pi-specific tuning is available at:

```text
nginx/nginx.raspberry-pi.conf
```

To use it, change the proxy bind mount in the compose file to:

```yaml
./nginx/nginx.raspberry-pi.conf:/etc/nginx/conf.d/default.conf:ro
```

## Optional Systemd

The compose services already use `restart: unless-stopped`, which is enough when Docker starts on boot.

For hosts that need a systemd unit, use `systemd/openbid-compose.service` as a template and set `OPENBID_DEPLOY_DIR` to this folder when installing the unit. The template does not contain a fixed clone path.

## Troubleshooting

Check service health:

```bash
docker compose ps
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

If production startup fails, check:

- `runtime/secrets/openbid_secret_key` exists and is non-empty
- `runtime/secrets/openbid_bootstrap_admin_password` exists and is non-empty
- `docker compose ps` shows the `app` and `worker` containers healthy after Compose injects those files into `/run/secrets/...`
- `SECURE_COOKIES=true` when `APP_ENV=production`
- the command is being run from `ProductionDeployment/`
