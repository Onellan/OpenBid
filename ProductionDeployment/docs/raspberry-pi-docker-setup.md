# Raspberry Pi Docker Setup

This guide keeps the deployment portable. Clone OpenBid anywhere, then run production commands from `ProductionDeployment/`.

## Recommended Hardware

- Raspberry Pi 5 with 4 GB or 8 GB RAM
- Raspberry Pi 4 with 4 GB or 8 GB RAM
- SSD storage for the SQLite database and backups
- Raspberry Pi OS Lite 64-bit, Debian Bookworm or newer

Avoid 32-bit OS images and microSD-only production storage for regular extraction workloads.

## Prepare The Pi

```bash
sudo apt update
sudo apt full-upgrade -y
sudo reboot
```

After reboot:

```bash
sudo apt update
sudo apt install -y git curl ca-certificates openssl nano
```

## Install Docker

```bash
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh
sudo usermod -aG docker "$USER"
newgrp docker
docker version
docker compose version
```

If the Compose plugin is missing:

```bash
sudo apt install -y docker-compose-plugin
docker compose version
```

Enable Docker at boot:

```bash
sudo systemctl enable docker
sudo systemctl start docker
```

## Clone And Start

Clone the repository wherever you keep applications, then:

```bash
cd ProductionDeployment
./setup.sh
docker compose up -d --build
```

The first Raspberry Pi source build can take several minutes.

For GHCR images:

```bash
cd ProductionDeployment
./setup.sh
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
```

## Runtime

Runtime directories are created automatically:

```text
runtime/data
runtime/backups
runtime/secrets
```

Do not delete `runtime/data/store.db` unless you intend to erase the application database.

Secret files are created by `setup.sh` when missing:

```text
runtime/secrets/openbid_secret_key
runtime/secrets/openbid_bootstrap_admin_password
```

Docker Compose injects those host files into the `app` and `worker` containers as Compose secrets at:

```text
/run/secrets/openbid_secret_key
/run/secrets/openbid_bootstrap_admin_password
```

Read the generated first-login password:

```bash
cat runtime/secrets/openbid_bootstrap_admin_password
```

## Access

On the Pi:

```bash
curl http://localhost:8088/healthz
hostname -I
```

Open from another device:

```text
http://PI_IP_ADDRESS:8088
```

For public access, terminate HTTPS in front of the Pi and keep:

```env
APP_ENV=production
SECURE_COOKIES=true
```

## Low Memory Settings

Recommended Raspberry Pi defaults:

```env
LOW_MEMORY_MODE=true
BOOTSTRAP_SYNC_ON_STARTUP=false
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
```

Put overrides in `.env` inside `ProductionDeployment/`:

```bash
cp .env.example .env
nano .env
```

## Operations

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

See [production-operations.md](production-operations.md) for backup, restore, update, and alert commands.
