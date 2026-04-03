# Production Operations

This runbook is the production baseline for OpenBid when you are running the packaged containers behind a reverse proxy or Cloudflare.

## Deployment model

- Use versioned image tags, not `latest`.
- Keep the OpenBid origin on HTTP if Cloudflare is terminating HTTPS for end users.
- Mount persistent host paths for `./data` and `./backups`.
- Mount `./secrets` read-only and prefer `SECRET_KEY_FILE` plus `BOOTSTRAP_ADMIN_PASSWORD_FILE`.

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

## Backups

Create a consistent SQLite backup through the running app container:

```bash
./scripts/sqlite-backup.sh ./backups/store-$(date +%Y%m%d-%H%M%S).db
```

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
- The `/health` page shows application, database, queue, extractor, and runtime state.

## CI/CD guidance

- CI is path-filtered so docs-only changes do not spend runner minutes.
- Image builds run only when Docker or runtime files change.
- Releases publish from signed-off `v*` tags rather than moving `latest` from release branches.

## Cloudflare notes

- Keep `SECURE_COOKIES=true` in production.
- Point Cloudflare at the local OpenBid HTTP origin, usually `http://host:8088`.
- Ensure Cloudflare forwards the correct `X-Forwarded-Proto` so the app keeps browser-facing HTTPS semantics.
