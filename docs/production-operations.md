# Production Operations

This runbook is the production baseline for OpenBid when you are running the packaged containers behind a reverse proxy or Cloudflare.

## Deployment model

- Use versioned image tags, not `latest`.
- Keep the OpenBid origin on HTTP if Cloudflare is terminating HTTPS for end users.
- Mount persistent host paths for `./data` and `./backups`.
- Mount `./secrets` read-only and prefer `SECRET_KEY_FILE` plus `BOOTSTRAP_ADMIN_PASSWORD_FILE`.
- Configure `ALERT_WEBHOOK_URL` if you want the app and scripts to push operational alerts out to Slack, Teams, ntfy, or another webhook receiver.

## Recommended files

```bash
cp .env.production.example .env.production
mkdir -p data backups secrets
printf '%s' 'replace-with-a-strong-secret-key' > secrets/openbid_secret_key
printf '%s' 'replace-with-a-strong-bootstrap-password' > secrets/openbid_bootstrap_admin_password
chmod 600 secrets/openbid_secret_key secrets/openbid_bootstrap_admin_password
```

## First deploy

```bash
docker compose -f docker-compose.ghcr.yml --env-file .env.production pull
docker compose -f docker-compose.ghcr.yml --env-file .env.production up -d
docker compose -f docker-compose.ghcr.yml ps
```

## Health checks

Use these commands after every deploy:

```bash
docker compose -f docker-compose.ghcr.yml --env-file .env.production ps
docker compose -f docker-compose.ghcr.yml --env-file .env.production logs app --tail=100
docker compose -f docker-compose.ghcr.yml --env-file .env.production logs worker --tail=100
curl -fsS http://127.0.0.1:8088/healthz
```

The alert JSON feed at `/health/alerts.json` requires an authenticated admin session.

## Backups

Create a consistent SQLite backup through the running app container:

```bash
./scripts/sqlite-backup.sh ./backups/store-$(date +%Y%m%d-%H%M%S).db
```

If `ALERT_WEBHOOK_URL` is set in the environment, a backup failure also sends an immediate webhook alert.

Validate a database file:

```bash
./scripts/sqlite-validate.sh ./data/store.db
```

## Restore

Restore only from a tested backup:

```bash
./scripts/sqlite-restore.sh ./backups/store-YYYYMMDD-HHMMSS.db ./data/store.db
```

The restore script stops the write-path containers, replaces the database, clears SQLite WAL side files, validates the runtime schema, and starts the stack again.

## Operational alerting

The app now raises operational alerts for:

- stale or missing backups
- repeated login throttling events
- extractor health failures
- accumulated extraction failures
- worker backlog growth

View the current alert set in:

- the `/health` page
- `/health/alerts.json`

### Recommended webhook receiver

Set this in `.env.production`:

```bash
ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts
```

The app posts JSON payloads with `status`, `code`, `severity`, `summary`, and `details`.

### Host-side container monitoring

The app cannot inspect Docker container health from inside the container safely, so host-side container alerting is handled by a helper script:

```bash
ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts \
./scripts/docker-alert-check.sh
```

It sends a `containers_unhealthy` alert when one or more Compose services are unhealthy or not running, and a resolved alert when the stack recovers.

Run it every 5 minutes from cron or systemd timer. Example cron entry:

```bash
*/5 * * * * cd /path/to/openbid && ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts ./scripts/docker-alert-check.sh >> /var/log/openbid-container-alerts.log 2>&1
```

## Upgrade strategy

1. Create a backup.
2. Change `APP_IMAGE_TAG` and `EXTRACTOR_IMAGE_TAG` in `.env.production`.
3. Pull the new images.
4. Start the new version.
5. Check `/healthz`, the `/health` page, and recent logs.

Example:

```bash
sed -i 's/^APP_IMAGE_TAG=.*/APP_IMAGE_TAG=v1.2.3/' .env.production
sed -i 's/^EXTRACTOR_IMAGE_TAG=.*/EXTRACTOR_IMAGE_TAG=v1.2.3/' .env.production
docker compose -f docker-compose.ghcr.yml --env-file .env.production pull
docker compose -f docker-compose.ghcr.yml --env-file .env.production up -d
```

## Rollback

Rollback is just a tag reversal plus restart:

```bash
sed -i 's/^APP_IMAGE_TAG=.*/APP_IMAGE_TAG=v1.2.2/' .env.production
sed -i 's/^EXTRACTOR_IMAGE_TAG=.*/EXTRACTOR_IMAGE_TAG=v1.2.2/' .env.production
docker compose -f docker-compose.ghcr.yml --env-file .env.production pull
docker compose -f docker-compose.ghcr.yml --env-file .env.production up -d
```

If the issue is data-related, restore the pre-upgrade SQLite backup as well.

## Observability

- `proxy` emits structured access logs with request IDs and upstream timing.
- `app` emits request logs with request IDs, status, duration, and client IP context.
- `worker` exposes liveness through the heartbeat health check and logs structured worker events.
- The `/health` page shows active operational alerts plus application, database, queue, extractor, and runtime state.
- `/health/alerts.json` exposes the same active alert set for automation.

## CI/CD guidance

- CI is path-filtered so docs-only changes do not spend runner minutes.
- Image builds run only when Docker or runtime files change.
- Releases publish from signed-off `v*` tags rather than moving `latest` from release branches.

## Cloudflare notes

- Keep `SECURE_COOKIES=true` in production.
- Point Cloudflare at the local OpenBid HTTP origin, usually `http://host:8088`.
- Ensure Cloudflare forwards the correct `X-Forwarded-Proto` so the app keeps browser-facing HTTPS semantics.
