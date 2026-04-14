# Production Operations

Run production commands from `ProductionDeployment/`.

## Runtime Layout

All production runtime state lives under `runtime/`:

- `runtime/data`: SQLite database files
- `runtime/backups`: database backups
- `runtime/secrets`: host-side source files for Docker Compose secrets

Create or repair the directory layout with:

```bash
./setup.sh
```

The script is idempotent and does not overwrite existing data.

## Secrets

`setup.sh` creates these files when they are missing:

```text
runtime/secrets/openbid_secret_key
runtime/secrets/openbid_bootstrap_admin_password
```

The compose files expose those files to only the `app` and `worker` services through Compose secrets:

```text
/run/secrets/openbid_secret_key
/run/secrets/openbid_bootstrap_admin_password
```

OpenBid reads those paths through `SECRET_KEY_FILE` and `BOOTSTRAP_ADMIN_PASSWORD_FILE`. GitHub Actions smoke tests are the exception: CI uses fixed environment-based test secrets so the ephemeral smoke stack does not depend on host secret file ownership.

## Source Build

```bash
./setup.sh
docker compose up -d --build
docker compose ps
curl http://localhost:8088/healthz
```

## GHCR Images

```bash
./setup.sh
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
docker compose -f docker-compose.ghcr.yml ps
curl http://localhost:8088/healthz
```

## Logs

```bash
docker compose logs --tail=200
docker compose logs -f
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

For GHCR deployments, add `-f docker-compose.ghcr.yml`.

## Backups

```bash
docker compose exec -T app openbid-sqlite-backup /app/backups/store-$(date +%Y%m%d-%H%M%S).db
ls -lh runtime/backups
```

For GHCR deployments:

```bash
docker compose -f docker-compose.ghcr.yml exec -T app openbid-sqlite-backup /app/backups/store-$(date +%Y%m%d-%H%M%S).db
ls -lh runtime/backups
```

## Restore

Stop write-path containers before replacing the database:

```bash
docker compose stop proxy app worker
cp runtime/backups/store-YYYYMMDD-HHMMSS.db runtime/data/store.db
rm -f runtime/data/store.db-wal runtime/data/store.db-shm
docker compose run --rm --no-deps app openbid-sqlite-check
docker compose up -d
```

For GHCR deployments, add `-f docker-compose.ghcr.yml` to the compose commands.

## Updates

Source build:

```bash
git pull
./setup.sh
docker compose up -d --build
```

GHCR images:

```bash
./setup.sh
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
```

Pinned GHCR deploys use matching image tags in `.env`:

```env
APP_IMAGE_TAG=sha-FULL_COMMIT_SHA
EXTRACTOR_IMAGE_TAG=sha-FULL_COMMIT_SHA
```

## Alerts

The `/health` page shows application, database, queue, extractor, runtime, and alert state for admins.

Optional webhook alerts use:

```env
ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts
```

Container alert checks can run from this folder:

```bash
ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts ../scripts/docker-alert-check.sh
```

For GHCR deployments:

```bash
COMPOSE_FILE=docker-compose.ghcr.yml ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts ../scripts/docker-alert-check.sh
```
