# Raspberry Pi Docker Setup

This guide walks through running OpenBid on a Raspberry Pi with Docker Compose.

## Recommended hardware

- Raspberry Pi 4 with 4 GB RAM minimum
- Raspberry Pi 5 preferred if you expect regular source checks and document extraction
- 32 GB or larger SSD strongly preferred over microSD for the SQLite database and extracted document workload

## What this deployment runs

OpenBid uses four containers:

- `app`: the Go web application
- `worker`: background source sync and extraction orchestration
- `extractor`: the Python PDF/text extraction service
- `proxy`: the bundled nginx reverse proxy on port `8088`

## 1. Prepare Raspberry Pi OS

Use a current 64-bit Raspberry Pi OS install.

```bash
sudo apt update
sudo apt upgrade -y
sudo apt install -y ca-certificates curl git
```

If you are using an SSD, mount it before continuing and choose a stable path such as `/srv/openbid`.

## 2. Install Docker and Compose

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
newgrp docker
docker version
docker compose version
```

If `newgrp docker` does not pick up the group change, log out and back in once.

## 3. Clone the project

```bash
sudo mkdir -p /srv/openbid
sudo chown -R $USER:$USER /srv/openbid
git clone https://github.com/OWNER/REPO.git /srv/openbid
cd /srv/openbid
```

Replace `OWNER/REPO` with your actual repository.

## 4. Create the environment file

Start from the production example file:

```bash
cp .env.production.example .env.production
mkdir -p secrets data backups
```

Prefer mounted secret files instead of inline secrets:

```bash
printf '%s' 'replace-with-a-random-32-plus-character-secret' > secrets/openbid_secret_key
printf '%s' 'replace-with-a-strong-admin-password' > secrets/openbid_bootstrap_admin_password
chmod 600 secrets/openbid_secret_key secrets/openbid_bootstrap_admin_password
```

For a real deployment, set at least these values in `.env.production`:

```dotenv
APP_ENV=production
SECURE_COOKIES=true
LOW_MEMORY_MODE=true
ANALYTICS_ENABLED=false
BOOTSTRAP_SYNC_ON_STARTUP=false
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
TREASURY_FEED_URL=
APP_IMAGE_TAG=v1.0.0
EXTRACTOR_IMAGE_TAG=v1.0.0
SECRET_KEY_FILE=/run/secrets/openbid_secret_key
BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/openbid_bootstrap_admin_password
```

Notes:

- `SECRET_KEY_FILE` and `BOOTSTRAP_ADMIN_PASSWORD_FILE` are the preferred production inputs.
- If you do use inline values, `SECRET_KEY` must still be strong and non-default.
- `BOOTSTRAP_SYNC_ON_STARTUP=false` is a safer default on smaller Pis because first boot stays lighter.
- Leave `LOW_MEMORY_MODE=true` unless you have profiled a larger Pi and want to experiment.

## 5. Understand the HTTPS requirement

Production mode requires `SECURE_COOKIES=true`. The important part is that the end user's browser must reach OpenBid over HTTPS.

If you are putting OpenBid behind Cloudflare, the OpenBid origin can still stay on plain HTTP. That setup works because:

- the browser talks to Cloudflare over `https://`
- Cloudflare talks to your Raspberry Pi over HTTP
- the app still sets secure cookies correctly for the browser-facing HTTPS session

For your setup, the bundled `proxy` service on port `8088` is the HTTP origin that Cloudflare should connect to.

Practical options:

1. Cloudflare in front of OpenBid: keep OpenBid on HTTP internally and expose `http://PI_IP_ADDRESS:8088` to Cloudflare.
2. Another local reverse proxy in front of OpenBid: same idea, OpenBid can remain HTTP behind it.
3. LAN-only quick start without any HTTPS edge: use `APP_ENV=development` and `SECURE_COOKIES=false` temporarily, knowing this is not a hardened production setup.

## 6. Start the stack

```bash
docker compose -f docker-compose.ghcr.yml --env-file .env.production pull
docker compose -f docker-compose.ghcr.yml --env-file .env.production up -d
docker compose -f docker-compose.ghcr.yml --env-file .env.production ps
```

OpenBid will be available through the bundled proxy on:

- `http://PI_IP_ADDRESS:8088`

If you use Cloudflare, point Cloudflare at `http://PI_IP_ADDRESS:8088` as the origin and let Cloudflare present `https://your-domain` to end users.

## 7. Verify the services

```bash
docker compose -f docker-compose.ghcr.yml --env-file .env.production logs app --tail=100
docker compose -f docker-compose.ghcr.yml --env-file .env.production logs worker --tail=100
docker compose -f docker-compose.ghcr.yml --env-file .env.production logs extractor --tail=100
docker compose -f docker-compose.ghcr.yml --env-file .env.production logs proxy --tail=100
```

Healthy signs:

- `app` responds on `/healthz`
- `extractor` responds on `/healthz`
- `proxy` is listening on port `8088`
- the first login succeeds with your bootstrap admin password

## 8. First login

Once the stack is up:

1. Open the site in a browser.
2. Sign in with username `admin`.
3. Use the password from `BOOTSTRAP_ADMIN_PASSWORD`.
4. Go to `Settings` and `Tenant Admin` to create additional tenants or switch workspaces.

## 9. Updating OpenBid later

```bash
cd /srv/openbid
git pull
docker compose -f docker-compose.ghcr.yml --env-file .env.production pull
docker compose -f docker-compose.ghcr.yml --env-file .env.production up -d
```

Rollback is the reverse: set `APP_IMAGE_TAG` and `EXTRACTOR_IMAGE_TAG` back to the last known good release and run the same `pull` plus `up -d`.

## 10. Back up the SQLite database

The database lives at:

- host path: `/srv/openbid/data/store.db`
- container path: `/app/data/store.db`

Consistent backup example:

```bash
COMPOSE_FILE=docker-compose.ghcr.yml ./scripts/sqlite-backup.sh ./backups/store-$(date +%Y%m%d-%H%M%S).db
```

Validate or restore later with:

```bash
COMPOSE_FILE=docker-compose.ghcr.yml ./scripts/sqlite-validate.sh ./data/store.db
COMPOSE_FILE=docker-compose.ghcr.yml ./scripts/sqlite-restore.sh ./backups/store-YYYYMMDD-HHMMSS.db ./data/store.db
```

For the full upgrade, rollback, backup, and observability runbook, see `docs/production-operations.md`.

## 11. Resource tips for smaller Pis

- Prefer SSD storage over microSD.
- Keep `BOOTSTRAP_SYNC_ON_STARTUP=false`.
- Avoid running other heavy services on the same Pi.
- Watch memory with `docker stats`.
- If document extraction feels slow, reduce how often the worker runs before increasing hardware.

## Troubleshooting

If the app does not start in production:

- confirm `.env` has a strong `SECRET_KEY`
- confirm `.env` has `SECURE_COOKIES=true`
- confirm `.env` has a strong `BOOTSTRAP_ADMIN_PASSWORD`

If login works locally but not through your public Cloudflare URL:

- confirm the public site is actually loading over `https://`
- confirm Cloudflare is forwarding to `http://PI_IP_ADDRESS:8088`
- confirm `SECURE_COOKIES=true` is still enabled in production

If the Pi feels slow:

- inspect `docker compose logs worker --tail=100`
- check `docker stats`
- move the data directory to SSD storage
