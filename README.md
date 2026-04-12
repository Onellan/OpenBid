# OpenBid

OpenBid is a lightweight, self-hosted, multi-tenant tender aggregation platform for engineering-focused South African opportunities.

This README is the single source of truth for installation, setup, deployment, updates, backups, restore, auto-start, and troubleshooting. Other setup documents in this repo should point here instead of repeating full installation steps.

## What Runs

OpenBid runs four services in Docker Compose:

- `app`: Go web application
- `worker`: source sync, queue processing, and extraction orchestration
- `extractor`: Python document text extraction service
- `proxy`: nginx reverse proxy exposed on port `8088` by default

Runtime data is stored in SQLite. The default root Compose setup stores important host files in:

- `./data/store.db`
- `./backups`
- `./secrets`

## Documentation Structure

- Main installation and operations guide: this `README.md`
- Authorization model: [docs/authorization-model.md](docs/authorization-model.md)
- Legacy Raspberry Pi bundle notes: [InstallationInstructions/README.md](InstallationInstructions/README.md)
- Redirect-only legacy setup pages:
  - [docs/raspberry-pi-docker-setup.md](docs/raspberry-pi-docker-setup.md)
  - [docs/production-operations.md](docs/production-operations.md)

Use the root project folder commands below for new installs. The `InstallationInstructions` folder is retained for older deployments that already used that bundle, but it is not the primary setup guide.

## Development Credentials

For local development only:

- Username: `admin`
- Password: `OpenBid!2026`

These are seeded only when `APP_ENV=development` and `BOOTSTRAP_ADMIN_PASSWORD` is empty. Production installs must set a strong secret key and bootstrap admin password before first startup.

## Local Development Quick Start

Use this for development on your workstation:

```bash
cp .env.example .env
go mod tidy
go test ./...
go run ./cmd/server
```

Open:

```text
http://localhost:8080
```

To run the full local Docker stack from source:

```bash
cp .env.example .env
docker compose --env-file .env up --build
```

Open:

```text
http://localhost:8088
```

## Raspberry Pi Production Install

### 1. Recommended Hardware And OS

Recommended:

- Raspberry Pi 5 with 4 GB or 8 GB RAM
- Raspberry Pi 4 with 4 GB or 8 GB RAM
- SSD storage preferred for the SQLite database and backups
- Raspberry Pi OS Lite 64-bit, Debian Bookworm or newer

Not recommended:

- 32-bit Raspberry Pi OS
- Raspberry Pi Zero
- Raspberry Pi 3 for long-running production use
- microSD-only production storage if you expect regular extraction workload

### 2. Prepare The Pi

Log in over SSH or directly on the Pi:

```bash
sudo apt update
sudo apt full-upgrade -y
sudo reboot
```

After reboot, install basic tools:

```bash
sudo apt update
sudo apt install -y git curl ca-certificates openssl nano
```

### 3. Install Docker

Install Docker:

```bash
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh
```

Allow your user to run Docker:

```bash
sudo usermod -aG docker "$USER"
newgrp docker
```

Verify Docker:

```bash
docker version
```

### 4. Confirm Docker Compose

Check the Compose plugin:

```bash
docker compose version
```

If that fails:

```bash
sudo apt install -y docker-compose-plugin
docker compose version
```

Enable Docker at boot:

```bash
sudo systemctl enable docker
sudo systemctl start docker
```

### 5. Clone Or Copy OpenBid

The recommended install path is:

```text
/opt/openbid
```

Clone the repo:

```bash
sudo mkdir -p /opt
cd /opt
sudo git clone YOUR_REPOSITORY_URL openbid
sudo chown -R "$USER":"$USER" /opt/openbid
cd /opt/openbid
```

If you copy files manually instead of using Git, copy the full repository to `/opt/openbid`.

### 6. Create Persistent Directories

From `/opt/openbid`:

```bash
mkdir -p data backups secrets
chmod 700 secrets
```

These host directories map into containers as:

- `./data` -> `/app/data`
- `./backups` -> `/app/backups`
- `./secrets` -> `/run/secrets`

Do not delete `data/store.db` unless you intend to erase the application database.

### 7. Create Secret Files

Generate the application secret key:

```bash
openssl rand -base64 48 > secrets/openbid_secret_key
chmod 600 secrets/openbid_secret_key
```

Create the first admin password:

```bash
nano secrets/openbid_bootstrap_admin_password
chmod 600 secrets/openbid_bootstrap_admin_password
```

Put one strong password on a single line. This password is used only when the database is empty and the initial admin account is created.

### 8. Create The Environment File

For production:

```bash
cp .env.production.example .env.production
nano .env.production
```

Recommended starting values:

```env
APP_ENV=production
SECURE_COOKIES=true
LOW_MEMORY_MODE=true
ANALYTICS_ENABLED=false
BOOTSTRAP_SYNC_ON_STARTUP=false
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
LOGIN_RATE_LIMIT_WINDOW_SECONDS=600
LOGIN_RATE_LIMIT_MAX_ATTEMPTS=10
TREASURY_FEED_URL=
BACKUP_DIR=/app/backups
ALERT_WEBHOOK_URL=
ALERT_EVAL_SECONDS=300
ALERT_BACKUP_MAX_AGE_MINUTES=1560
ALERT_BACKLOG_MAX_JOBS=25
ALERT_BACKLOG_MAX_AGE_MINUTES=60
ALERT_LOGIN_THROTTLE_THRESHOLD=3
ALERT_EXTRACTOR_FAILURE_THRESHOLD=5
SECRET_KEY=
SECRET_KEY_FILE=/run/secrets/openbid_secret_key
BOOTSTRAP_ADMIN_PASSWORD=
BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/openbid_bootstrap_admin_password
BOOTSTRAP_TENANT_NAME=KolaboSolutions
BOOTSTRAP_TENANT_SLUG=kolabosolutions
```

Optional port override for the root `docker-compose.yml` stack:

```env
OPENBID_HTTP_PORT=8088
```

Important settings:

- `APP_ENV=production`: enforces production-safe startup checks
- `SECURE_COOKIES=true`: required when users access the app through HTTPS
- `SECRET_KEY_FILE`: preferred production secret input
- `BOOTSTRAP_ADMIN_PASSWORD_FILE`: preferred first-admin password input
- `LOW_MEMORY_MODE=true`: recommended on Raspberry Pi
- `BOOTSTRAP_SYNC_ON_STARTUP=false`: keeps first boot light
- `WORKER_SYNC_MINUTES=360`: source check cadence
- `WORKER_LOOP_SECONDS=30`: worker polling interval
- `ALERT_WEBHOOK_URL`: optional Slack, Teams, ntfy, or webhook receiver URL

### 9. HTTPS And Reverse Proxy Notes

The bundled `proxy` container exposes HTTP on port `8088` by default.

For production, users should reach OpenBid over HTTPS. A common Raspberry Pi setup is:

- Browser -> Cloudflare or another TLS proxy over HTTPS
- Cloudflare/proxy -> Raspberry Pi over `http://PI_IP:8088`

Keep `SECURE_COOKIES=true` in production. If you are testing LAN-only without HTTPS, use development mode temporarily:

```env
APP_ENV=development
SECURE_COOKIES=false
```

Do not use that LAN-only mode for hardened public access.

### 10. Build And Start From Source

From `/opt/openbid`:

```bash
docker compose --env-file .env.production up --build -d
```

The first Raspberry Pi build can take several minutes.

Check status:

```bash
docker compose ps
```

Expected healthy services:

- `app`
- `worker`
- `extractor`
- `proxy`

### 11. Published GHCR Image Deploy

GitHub Actions publishes OpenBid images to GitHub Container Registry automatically when code is pushed to `main`. The same workflow also supports manual runs and `v*` release tags.

Published images:

- `ghcr.io/onellan/openbid/app:latest`
- `ghcr.io/onellan/openbid/extractor:latest`
- `ghcr.io/onellan/openbid/app:sha-<full-commit-sha>`
- `ghcr.io/onellan/openbid/extractor:sha-<full-commit-sha>`

Pull the current `main` images manually:

```bash
docker pull ghcr.io/onellan/openbid/app:latest
docker pull ghcr.io/onellan/openbid/extractor:latest
```

Run the GHCR-based Compose stack:

```bash
docker compose -f docker-compose.ghcr.yml --env-file .env.production pull
docker compose -f docker-compose.ghcr.yml --env-file .env.production up -d
```

Use `latest` for the newest successful `main` build:

```env
APP_IMAGE_TAG=latest
EXTRACTOR_IMAGE_TAG=latest
```

For a pinned deploy, set both tags to the matching commit-specific SHA tag:

```env
APP_IMAGE_TAG=sha-FULL_COMMIT_SHA
EXTRACTOR_IMAGE_TAG=sha-FULL_COMMIT_SHA
```

Before relying on GHCR from a fresh repository, confirm the package visibility in GitHub under the repository or organization packages. Public images can be pulled without authentication; private packages require a GitHub token with package read access.

### 12. Verify Health

On the Pi:

```bash
curl http://localhost:8088/healthz
```

Expected:

```json
{ "ok": true }
```

From another device on the same network, find the Pi address:

```bash
hostname -I
```

Open:

```text
http://PI_IP_ADDRESS:8088
```

### 13. First Login

Use:

- Username: `admin`
- Password: the value in `secrets/openbid_bootstrap_admin_password`

After login:

- Change the password if needed
- Configure MFA if desired
- Review `Settings`, `Tenant Admin`, and `Sources`

## Daily Operations

Run these commands from `/opt/openbid`.

### Status

```bash
docker compose ps
curl http://localhost:8088/healthz
```

### Logs

All services:

```bash
docker compose logs --tail=200
docker compose logs -f
```

One service:

```bash
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

What to check:

- `app`: startup, auth, database, and request errors
- `worker`: source sync, queue processing, extraction retries, skipped work
- `extractor`: document fetch and parsing errors
- `proxy`: upstream routing, 4xx/5xx traffic, rate limiting

### Restart

Restart all services:

```bash
docker compose restart
```

Restart one service:

```bash
docker compose restart app
docker compose restart worker
docker compose restart extractor
docker compose restart proxy
```

### Stop And Start

Stop without removing containers:

```bash
docker compose stop
```

Start again:

```bash
docker compose start
```

Stop and remove containers while keeping bind-mounted data:

```bash
docker compose down
```

Start again:

```bash
docker compose --env-file .env.production up -d
```

### Update From Source

Back up first:

```bash
./scripts/sqlite-backup.sh ./backups/store-$(date +%Y%m%d-%H%M%S).db
```

Then update:

```bash
cd /opt/openbid
git pull
docker compose --env-file .env.production up --build -d
docker compose ps
curl http://localhost:8088/healthz
```

### Update Published Images

Back up first:

```bash
COMPOSE_FILE=docker-compose.ghcr.yml ./scripts/sqlite-backup.sh ./backups/store-$(date +%Y%m%d-%H%M%S).db
```

For the newest `main` images, keep `APP_IMAGE_TAG=latest` and `EXTRACTOR_IMAGE_TAG=latest`, then:

```bash
docker compose -f docker-compose.ghcr.yml --env-file .env.production pull
docker compose -f docker-compose.ghcr.yml --env-file .env.production up -d
curl http://localhost:8088/healthz
```

For a pinned deploy or rollback, set `APP_IMAGE_TAG` and `EXTRACTOR_IMAGE_TAG` to the previous matching `sha-<full-commit-sha>` tags before running the same `pull` plus `up -d` commands.

## Backups And Restore

### Backup

Create a consistent SQLite backup through the app container:

```bash
./scripts/sqlite-backup.sh ./backups/store-$(date +%Y%m%d-%H%M%S).db
```

If you run the published-image stack, select that Compose file:

```bash
COMPOSE_FILE=docker-compose.ghcr.yml ./scripts/sqlite-backup.sh ./backups/store-$(date +%Y%m%d-%H%M%S).db
```

Verify backups:

```bash
ls -lh backups
```

Recommended habits:

- Back up before every update
- Back up before major configuration changes
- Keep copies off the Pi
- Periodically test restore on a non-production copy

### Validate

Validate the current runtime database:

```bash
./scripts/sqlite-validate.sh ./data/store.db
```

For the published-image stack:

```bash
COMPOSE_FILE=docker-compose.ghcr.yml ./scripts/sqlite-validate.sh ./data/store.db
```

### Restore

Restore only while the write-path containers are stopped. The restore script handles the normal Docker flow:

```bash
./scripts/sqlite-restore.sh ./backups/store-YYYYMMDD-HHMMSS.db ./data/store.db
```

For the published-image stack:

```bash
COMPOSE_FILE=docker-compose.ghcr.yml ./scripts/sqlite-restore.sh ./backups/store-YYYYMMDD-HHMMSS.db ./data/store.db
```

Then verify:

```bash
docker compose ps
curl http://localhost:8088/healthz
```

Keep the old or broken database file until you confirm the restore worked.

## Auto-Start After Reboot

The Compose files use:

```yaml
restart: unless-stopped
```

That is usually enough if Docker starts at boot:

```bash
sudo systemctl enable docker
```

Test:

```bash
sudo reboot
```

After the Pi comes back:

```bash
cd /opt/openbid
docker compose ps
curl http://localhost:8088/healthz
```

### Optional Systemd Unit

If you want systemd to manage the whole Compose stack explicitly:

```bash
sudo tee /etc/systemd/system/openbid-compose.service >/dev/null <<'EOF'
[Unit]
Description=OpenBid Docker Compose stack
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/openbid
ExecStart=/usr/bin/docker compose --env-file .env.production up -d
ExecStop=/usr/bin/docker compose down
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable openbid-compose.service
sudo systemctl start openbid-compose.service
sudo systemctl status openbid-compose.service --no-pager
```

Common commands:

```bash
sudo systemctl restart openbid-compose.service
sudo systemctl stop openbid-compose.service
sudo systemctl start openbid-compose.service
```

## Operational Alerts

The `/health` page shows application, database, queue, extractor, runtime, and alert state for admins.

Optional webhook alerts:

```env
ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts
```

Host-side container alerting can run from cron:

```bash
*/5 * * * * cd /opt/openbid && ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts ./scripts/docker-alert-check.sh >> /var/log/openbid-container-alerts.log 2>&1
```

## Troubleshooting

### Docker Compose Fails To Build Or Start

Check logs:

```bash
docker compose logs --tail=200
```

Check disk space:

```bash
df -h
docker system df
```

Common causes:

- Not enough disk space
- Docker group permissions not active
- Temporary network failure while pulling base images
- Wrong working directory
- Missing `.env.production`

### Docker Permission Denied

Confirm your user is in the Docker group:

```bash
groups
```

If needed:

```bash
sudo usermod -aG docker "$USER"
```

Log out and back in, or run:

```bash
newgrp docker
```

### App Does Not Open From Another Device

Check the proxy:

```bash
docker compose port proxy 80
curl http://localhost:8088/healthz
```

Check the Pi IP:

```bash
hostname -I
```

Common mistakes:

- Using the wrong Pi IP address
- Using the app container port instead of proxy port `8088`
- Router or firewall blocking the port
- Cloudflare origin pointing to the wrong internal address

### Container Shows Unhealthy

Inspect service logs:

```bash
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

Inspect health details:

```bash
docker inspect openbid-app-1 --format '{{json .State.Health}}'
docker inspect openbid-worker-1 --format '{{json .State.Health}}'
docker inspect openbid-extractor-1 --format '{{json .State.Health}}'
docker inspect openbid-proxy-1 --format '{{json .State.Health}}'
```

### Production Startup Refuses To Boot

Check `.env.production`:

- `APP_ENV=production`
- `SECURE_COOKIES=true`
- `SECRET_KEY_FILE=/run/secrets/openbid_secret_key`
- `BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/openbid_bootstrap_admin_password`

Check secret files:

```bash
ls -lah secrets
chmod 600 secrets/openbid_secret_key secrets/openbid_bootstrap_admin_password
```

### Login Works Locally But Not Through Cloudflare

Confirm:

- Public browser URL is HTTPS
- Cloudflare origin points to `http://PI_IP:8088`
- `SECURE_COOKIES=true`
- Cloudflare forwards `X-Forwarded-Proto`

### Low Memory Or Slow Raspberry Pi

Keep:

```env
LOW_MEMORY_MODE=true
BOOTSTRAP_SYNC_ON_STARTUP=false
```

Check resources:

```bash
free -h
docker stats --no-stream
```

Helpful steps:

- Prefer SSD storage
- Avoid running other heavy containers
- Increase `WORKER_LOOP_SECONDS` if background activity is too frequent
- Use a Pi 4 with 4 GB or better

### Out Of Disk Space

Check:

```bash
df -h
docker system df
```

Clean unused Docker data carefully:

```bash
docker image prune -a
docker builder prune
```

Do not delete:

- `data/store.db`
- `backups`
- `secrets`

### Backup Or Restore Fails

Check:

```bash
docker compose ps
docker compose logs --tail=100 app
ls -lah data backups
```

Validate the database:

```bash
./scripts/sqlite-validate.sh ./data/store.db
```

### Extractor Or Queue Problems

Check:

```bash
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
```

The worker skips extraction for expired tenders and records skipped status in the queue. Expired means the tender closing date/time has already passed.

## Quick Command Reference

Start from source:

```bash
docker compose --env-file .env.production up --build -d
```

Start with published images:

```bash
docker compose -f docker-compose.ghcr.yml --env-file .env.production up -d
```

Status:

```bash
docker compose ps
curl http://localhost:8088/healthz
```

Logs:

```bash
docker compose logs -f
```

Restart:

```bash
docker compose restart
```

Stop:

```bash
docker compose stop
```

Backup:

```bash
./scripts/sqlite-backup.sh ./backups/store-$(date +%Y%m%d-%H%M%S).db
```

Restore:

```bash
./scripts/sqlite-restore.sh ./backups/store-YYYYMMDD-HHMMSS.db ./data/store.db
```

Validate:

```bash
go test ./...
```
