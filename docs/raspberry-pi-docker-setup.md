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

Start from the example file:

```bash
cp .env.example .env
```

For a real deployment, set at least these values in `.env`:

```dotenv
APP_ENV=production
SECRET_KEY=replace-with-a-random-32-plus-character-secret
SECURE_COOKIES=true
LOW_MEMORY_MODE=true
ANALYTICS_ENABLED=false
BOOTSTRAP_ADMIN_PASSWORD=replace-with-a-strong-admin-password
BOOTSTRAP_SYNC_ON_STARTUP=false
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
TREASURY_FEED_URL=
```

Notes:

- `SECRET_KEY` must be strong and non-default in production.
- `BOOTSTRAP_ADMIN_PASSWORD` is required for the first production startup when the database is empty.
- `BOOTSTRAP_SYNC_ON_STARTUP=false` is a safer default on smaller Pis because first boot stays lighter.
- Leave `LOW_MEMORY_MODE=true` unless you have profiled a larger Pi and want to experiment.

## 5. Understand the HTTPS requirement

Production mode requires `SECURE_COOKIES=true`. That means the browser must access OpenBid over HTTPS or session cookies will not work correctly.

You have two practical options:

1. Recommended: place OpenBid behind a TLS terminator such as Caddy, Nginx Proxy Manager, Traefik, or a Tailscale/Cloudflare tunnel.
2. LAN-only quick start: use `APP_ENV=development` and `SECURE_COOKIES=false` temporarily, knowing this is not a hardened production setup.

The bundled `proxy` service only exposes plain HTTP on port `8088`. It is fine as an internal hop behind another HTTPS reverse proxy.

## 6. Start the stack

```bash
mkdir -p data
docker compose up --build -d
docker compose ps
```

OpenBid will be available through the bundled proxy on:

- `http://PI_IP_ADDRESS:8088`

If you put another reverse proxy in front, point it at `http://PI_IP_ADDRESS:8088`.

## 7. Verify the services

```bash
docker compose logs app --tail=100
docker compose logs worker --tail=100
docker compose logs extractor --tail=100
docker compose logs proxy --tail=100
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
docker compose up --build -d
```

If you changed only configuration and not code, `docker compose up -d` is enough.

## 10. Back up the SQLite database

The database lives at:

- host path: `/srv/openbid/data/store.db`
- container path: `/app/data/store.db`

Simple backup example:

```bash
cp /srv/openbid/data/store.db /srv/openbid/data/store.db.bak
```

For safer scheduled backups, stop the stack briefly or use the repository backup scripts after testing them in your environment.

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

If login works locally but not through your public URL:

- confirm your front-end proxy is serving HTTPS
- confirm it forwards traffic to `http://PI_IP_ADDRESS:8088`

If the Pi feels slow:

- inspect `docker compose logs worker --tail=100`
- check `docker stats`
- move the data directory to SSD storage
