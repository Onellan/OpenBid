# OpenBid Production Deployment Guide

This README is the primary production deployment source of truth for OpenBid.
Run production commands from this directory:

```bash
cd ProductionDeployment
```

If another document gives a shorter command list, use this README as the
definitive guide and return here for the full sequence, checks, explanations,
backup procedure, restore procedure, and troubleshooting steps.

## 1. Introduction

### What this guide is for

This guide explains how to deploy OpenBid with Docker Compose from start to
finish. It covers host checks, project setup, environment configuration, first
startup, verification, access, updates, backups, restores, troubleshooting,
security, maintenance, and a command reference.

### What OpenBid is

OpenBid is a lightweight, self-hosted, multi-tenant tender aggregation platform
for engineering-focused South African opportunities. It provides a web
application, source synchronization, document extraction orchestration, tenant
and user administration, queue monitoring, and operational health pages.

### Who this guide is for

This guide is written for a new operator who may not have deployed this project
before. It assumes you can use a terminal, but it does not assume that you
already know the OpenBid deployment flow.

### Environment assumed by this guide

The production deployment in this directory assumes:

- A Linux server or Linux-like environment that can run Docker and Docker
  Compose.
- A POSIX shell such as `sh` or `bash`.
- Docker Engine with the Docker Compose plugin available as `docker compose`.
- Git access to this repository if building from source.
- Optional internet access to GHCR if deploying prebuilt images with
  `docker-compose.ghcr.yml`.
- Optional domain and HTTPS reverse proxy in front of the included OpenBid
  nginx proxy for real production browser access.

The commands are shown for Linux. They also work on Raspberry Pi OS, Debian, or
Ubuntu when Docker is installed. Windows users should run the production stack
from WSL, Git Bash, or a Linux server rather than plain PowerShell because
`setup.sh` and several backup examples use POSIX shell syntax.

### What you will have when finished

After a successful deployment you will have:

- Four Docker services running under the Compose project name `openbid`.
- OpenBid served through the included nginx `proxy` container.
- A SQLite database stored at `ProductionDeployment/runtime/data/store.db`.
- Database backups stored under `ProductionDeployment/runtime/backups/`.
- Production secret files stored under `ProductionDeployment/runtime/secrets/`.
- A first admin user created automatically when the database is empty.
- A health endpoint available at `/healthz`.
- A login page available at `/login`.

## 2. Deployment Overview

### Architecture summary

OpenBid production uses Docker Compose. The stack has one public entry point and
three internal services:

```text
Browser or external reverse proxy
        |
        v
host port OPENBID_HTTP_PORT, default 8088
        |
        v
proxy container, nginx on container port 80
        |
        v
app container, Go web app on container port 8080
        |
        +--> runtime/data/store.db, SQLite database bind mount
        +--> runtime/backups, backup bind mount
        +--> extractor container, Python extraction API on container port 9090
        +--> worker container, background source sync and queue processor
```

The `app` and `worker` containers both open the same SQLite database. The app
handles web requests. The worker handles scheduled source checks, manual source
checks, queued document extraction jobs, and background maintenance work. The
extractor receives document URLs from the worker and extracts text/facts from
documents.

### Containers and services

The source build compose file is:

```text
docker-compose.yml
```

The GHCR image compose file is:

```text
docker-compose.ghcr.yml
```

Both compose files define the same services:

| Service | Image/build source | Purpose | Exposed to host |
| --- | --- | --- | --- |
| `proxy` | `nginx:stable-alpine3.23` | Public HTTP entry point, reverse proxy, rate limiting, forwarding headers | Yes, default host port `8088` |
| `app` | `Dockerfile.app` or `ghcr.io/onellan/openbid/app` | Go web application, login, pages, API-like health endpoints, automatic database migration/seed | No |
| `worker` | `Dockerfile.app` or `ghcr.io/onellan/openbid/app` | Background source sync, document extraction queue, worker heartbeat | No |
| `extractor` | `Dockerfile.extractor` or `ghcr.io/onellan/openbid/extractor` | Python document text extraction service using Poppler tools | No |

### Ports used

| Port | Where | Purpose |
| --- | --- | --- |
| `OPENBID_HTTP_PORT`, default `8088` | Host | Port you open in the browser or point an external reverse proxy at |
| `80` | `proxy` container | nginx listens here inside Docker |
| `8080` | `app` container | Go web application listens here inside Docker |
| `9090` | `extractor` container | Python extractor listens here inside Docker |

Only the proxy service publishes a host port. The app, worker, and extractor are
private to the Docker Compose network.

### Volumes and bind mounts used

The compose files use host bind mounts relative to this directory:

```yaml
./runtime/data:/app/data
./runtime/backups:/app/backups
./nginx/nginx.conf:/etc/nginx/conf.d/default.conf:ro
```

The app reads and writes:

```text
/app/data/store.db
/app/backups/
```

On the host, those paths are:

```text
ProductionDeployment/runtime/data/store.db
ProductionDeployment/runtime/backups/
```

Secrets are mounted as Docker Compose secrets, not as a direct directory bind:

```yaml
runtime/secrets/openbid_secret_key
runtime/secrets/openbid_bootstrap_admin_password
```

Inside the `app` and `worker` containers, those secret files appear as:

```text
/run/secrets/openbid_secret_key
/run/secrets/openbid_bootstrap_admin_password
```

### Required files

These files must exist before the stack starts:

```text
ProductionDeployment/docker-compose.yml
ProductionDeployment/docker-compose.ghcr.yml
ProductionDeployment/setup.sh
ProductionDeployment/nginx/nginx.conf
ProductionDeployment/runtime/secrets/openbid_secret_key
ProductionDeployment/runtime/secrets/openbid_bootstrap_admin_password
```

The two secret files are created by:

```bash
./setup.sh
```

Do not create empty secret files. If either secret file exists but is empty,
`setup.sh` fails intentionally so the app does not start with unsafe secrets.

### Environment files involved

The deployment can run without a `.env` file because the compose files include
production-safe defaults for the variables they pass into containers.

Use `.env` only when you need to override defaults:

```bash
cp .env.example .env
nano .env
```

Docker Compose automatically reads `.env` from this directory when you run
`docker compose` from `ProductionDeployment/`.

Important: `.env.example` also contains some application variables that are not
currently injected by the production compose files. This README calls out which
variables are actually wired by the compose files so you do not assume an unused
override is active.

### Where data is stored

Runtime state is local to this directory:

```text
runtime/
runtime/data/
runtime/backups/
runtime/secrets/
```

Important files:

| Host path | Purpose | Delete casually? |
| --- | --- | --- |
| `runtime/data/store.db` | Main SQLite database | No |
| `runtime/data/store.db-wal` | SQLite WAL sidecar while database is active | No |
| `runtime/data/store.db-shm` | SQLite shared-memory sidecar while database is active | No |
| `runtime/backups/*.db` | Database backups created by OpenBid backup command | No, unless you are applying a retention policy |
| `runtime/secrets/openbid_secret_key` | Production signing/encryption secret | No |
| `runtime/secrets/openbid_bootstrap_admin_password` | Initial admin password for an empty database | No, keep secure |

### How the app is accessed after deployment

For health checks on the host:

```text
http://localhost:8088/healthz
```

For real production browser login, place HTTPS in front of the included proxy
and access OpenBid through your domain, for example:

```text
https://openbid.example.com/login
```

The production compose defaults use:

```env
APP_ENV=production
SECURE_COOKIES=true
```

Secure cookies are the right production setting. They also mean browser login is
expected to happen over HTTPS. Plain HTTP is fine for `curl` health checks, but
do not rely on plain HTTP for production user sessions.

## 3. Prerequisites

### Supported host assumptions

Use a host where Docker can run reliably:

- Ubuntu Server 22.04 or newer.
- Debian 12 or newer.
- Raspberry Pi OS Lite 64-bit, Debian Bookworm or newer.
- Another Linux distribution with Docker Engine and the Compose plugin.

The stack is path-agnostic. You can clone the repository anywhere, as long as
you run production commands from `ProductionDeployment/`.

### Operating system assumptions

This guide assumes:

- 64-bit OS.
- Shell tools: `sh`, `mkdir`, `chmod`, `cat`, `cp`, `rm`, `df`, `ss` or `lsof`.
- `openssl` is preferred for secret generation. If unavailable, `setup.sh` can
  fall back to `/dev/urandom`.

For Raspberry Pi production use:

- Prefer Raspberry Pi 5 with 4 GB or 8 GB RAM, or Raspberry Pi 4 with 4 GB or
  8 GB RAM.
- Prefer SSD storage for `runtime/data` and `runtime/backups`.
- Avoid 32-bit OS images and microSD-only production storage for regular
  extraction workloads.

### Docker requirements

Install Docker Engine and confirm it works before deploying OpenBid.

On Debian, Ubuntu, or Raspberry Pi OS, this common installer path is:

```bash
sudo apt update
sudo apt install -y git curl ca-certificates openssl nano
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh
sudo usermod -aG docker "$USER"
newgrp docker
```

Success looks like this:

- The install command exits without an error.
- `docker version` prints both `Client` and `Server` sections.
- You can run `docker run --rm hello-world`.

If your organization manages Docker another way, use that approved install path.
The final requirement is the same: `docker` and `docker compose` must work for
the user who will operate OpenBid.

### Docker Compose requirements

OpenBid uses the Docker Compose plugin command:

```bash
docker compose version
```

Use `docker compose`, not the older standalone `docker-compose` command.

### Git requirements

You need Git if you will clone or update from source:

```bash
git --version
```

You do not need Git on the production server if you copy a prepared release
archive to the server, but the directory must still contain the
`ProductionDeployment/` files and the source files needed by `docker-compose.yml`
if you build locally.

### Access requirements

The deployment operator needs:

- Shell access to the host.
- Permission to run Docker commands.
- Permission to bind the chosen host port, default `8088`.
- Permission to create and write files under the clone directory.
- Internet access if building images from source for the first time, because
  Docker must download base images and Go/Python dependencies during the build.
- Internet access to `ghcr.io` if using prebuilt GHCR images.

### Domain and reverse proxy assumptions

The included `proxy` container is an internal nginx reverse proxy for OpenBid.
It listens on HTTP inside the deployment. It does not create TLS certificates.

For production user access, put a real HTTPS reverse proxy in front of the
OpenBid proxy. That external proxy can be Cloudflare, Traefik, Caddy, nginx,
Apache, a load balancer, or another approved TLS terminator.

Your external proxy should forward traffic to:

```text
http://OPENBID_HOST:8088
```

It must preserve or set:

```text
Host
X-Forwarded-Host
X-Forwarded-For
X-Forwarded-Proto: https
```

The included nginx config also understands Cloudflare `CF-Visitor` scheme
headers.

### Registry and login requirements

Source builds use local Dockerfiles and do not need a GHCR login.

GHCR image deployments use:

```text
ghcr.io/onellan/openbid/app
ghcr.io/onellan/openbid/extractor
```

If those images are public in your environment, `docker compose -f
docker-compose.ghcr.yml pull` works without login. If GHCR returns
`unauthorized` or `denied`, log in with a GitHub token that has package read
access:

```bash
docker login ghcr.io
```

Success looks like:

```text
Login Succeeded
```

### Hardware and resource assumptions

Minimum practical starting point:

- 2 CPU cores.
- 2 GB RAM for light use.
- 4 GB RAM or more recommended for production and document extraction.
- 10 GB free disk to start.
- More disk if you expect many tenders, extraction jobs, or backups.

The default deployment sets:

```env
LOW_MEMORY_MODE=true
BOOTSTRAP_SYNC_ON_STARTUP=false
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
```

Those defaults are conservative and work well for small servers.

## 4. Pre-install Checks

Run these checks before starting OpenBid. Do not skip them on a new host.

### Confirm Docker is installed

```bash
docker version
```

Expected result:

- A `Client:` section.
- A `Server:` section.
- No message like `Cannot connect to the Docker daemon`.

If the server section is missing or Docker cannot connect, start Docker:

```bash
sudo systemctl enable docker
sudo systemctl start docker
docker version
```

### Confirm Docker Compose is installed

```bash
docker compose version
```

Expected result:

```text
Docker Compose version v2.x.x
```

Any v2 Compose plugin should work. If the command is missing on Debian/Ubuntu:

```bash
sudo apt update
sudo apt install -y docker-compose-plugin
docker compose version
```

### Confirm your user can run Docker

```bash
docker run --rm hello-world
```

Expected result:

- Docker downloads the test image if needed.
- The output includes `Hello from Docker!`.
- The command exits successfully.

If you see `permission denied` while connecting to the Docker socket:

```bash
sudo usermod -aG docker "$USER"
newgrp docker
docker run --rm hello-world
```

If your organization does not allow non-root Docker access, prefix the OpenBid
commands with `sudo`. Keep that choice consistent.

### Confirm required ports are available

OpenBid publishes one host port. The default is `8088`.

Check with `ss`:

```bash
sudo ss -ltnp | grep ':8088 ' || true
```

Expected result if the port is free: no output. No output means nothing is
listening on `8088`.

If you see a line with `LISTEN`, the port is already in use. Choose another port
in `.env`, for example:

```env
OPENBID_HTTP_PORT=8090
```

Then later access:

```text
http://localhost:8090/healthz
```

If `ss` is unavailable, use `lsof`:

```bash
sudo lsof -iTCP:8088 -sTCP:LISTEN
```

No output means the port is free.

### Confirm enough disk space exists

From the directory where you plan to clone or run OpenBid:

```bash
df -h .
```

Expected result:

- The `Avail` column should be at least `10G` for a small deployment.
- More is better if you will keep many backups.

Check Docker's storage area too:

```bash
docker system df
```

Expected result:

- The command completes.
- You can see current image, container, and build-cache usage.

If disk is low, stop and free space before deploying. Do not put a production
SQLite database on a nearly full disk.

### Confirm the shell can run `setup.sh`

From `ProductionDeployment/` after cloning:

```bash
sh ./setup.sh
```

Expected result:

- It prints `Preparing OpenBid production runtime directories...`.
- It creates or keeps the runtime directories.
- It creates or keeps secret files.
- It ends with `Runtime is ready under ProductionDeployment/runtime.`

If you see `Permission denied`, run:

```bash
chmod +x setup.sh
./setup.sh
```

Or keep using:

```bash
sh ./setup.sh
```

## 5. Project Setup

### Choose where to place the repo

Pick a stable location. Examples:

```text
/opt/openbid
/srv/openbid
$HOME/apps/openbid
```

Do not deploy from a temporary directory that may be cleaned automatically.

Example using `/opt`:

```bash
sudo mkdir -p /opt/openbid
sudo chown "$USER":"$USER" /opt/openbid
cd /opt/openbid
```

Success looks like:

- `pwd` prints `/opt/openbid`.
- `ls -la` works without permission errors.

### Clone the repo

Replace the URL if your repository remote is different:

```bash
git clone https://github.com/onellan/openbid.git .
```

Success looks like:

- Git prints clone progress.
- The directory contains `README.md`, `go.mod`, `cmd/`, `internal/`, `web/`,
  and `ProductionDeployment/`.

### Navigate into the deployment folder

```bash
cd ProductionDeployment
pwd
```

Expected result:

```text
/opt/openbid/ProductionDeployment
```

Your path may differ, but it must end with:

```text
ProductionDeployment
```

### Confirm the files you should see

```bash
ls -la
```

Expected important entries:

```text
README.md
setup.sh
docker-compose.yml
docker-compose.ghcr.yml
Dockerfile.app
Dockerfile.extractor
.env.example
.env.production.example
nginx/
runtime/
systemd/
docs/
```

If `docker-compose.yml` is missing, you are not in the right folder or the copy
of the repository is incomplete.

### Expected deployment directory structure

The deployment-relevant structure is:

```text
OpenBid/
  cmd/
  internal/
  web/
  extractor/
  go.mod
  go.sum
  ProductionDeployment/
    README.md
    setup.sh
    docker-compose.yml
    docker-compose.ghcr.yml
    Dockerfile.app
    Dockerfile.extractor
    .env.example
    nginx/
      nginx.conf
      nginx.raspberry-pi.conf
    runtime/
      data/
      backups/
      secrets/
    systemd/
      openbid-compose.service
```

The source build compose file uses `context: ..`, so the parent repository
contents must exist when using `docker-compose.yml`.

### Pulling a release instead of cloning `main`

If you want a tagged release from Git:

```bash
git fetch --tags
git checkout vX.Y.Z
cd ProductionDeployment
```

Replace `vX.Y.Z` with the release tag you intend to deploy.

Success looks like:

- `git status --short` does not show unexpected local changes.
- `git describe --tags --always` prints the tag or commit you intended.

If you use GHCR images, pin the image tags in `.env` to the matching release or
commit tag when available:

```env
APP_IMAGE_TAG=vX.Y.Z
EXTRACTOR_IMAGE_TAG=vX.Y.Z
```

The release workflow can also publish immutable commit tags in this form:

```env
APP_IMAGE_TAG=sha-FULL_COMMIT_SHA
EXTRACTOR_IMAGE_TAG=sha-FULL_COMMIT_SHA
```

Use matching tags for app and extractor unless you intentionally know why they
should differ.

## 6. Environment Configuration

### Start with runtime setup

Run:

```bash
./setup.sh
```

If executable permissions are missing:

```bash
sh ./setup.sh
```

What this does:

- Creates `runtime/`.
- Creates `runtime/data/`.
- Creates `runtime/backups/`.
- Creates `runtime/secrets/`.
- Creates `runtime/data/store.db` if missing.
- Creates `runtime/secrets/openbid_secret_key` if missing.
- Creates `runtime/secrets/openbid_bootstrap_admin_password` if missing.
- Leaves existing data and existing secrets unchanged.

Success looks like:

```text
Preparing OpenBid production runtime directories...
Created runtime/secrets/openbid_secret_key
Created runtime/secrets/openbid_bootstrap_admin_password
Runtime is ready under ProductionDeployment/runtime.
```

If files already exist, success may say `Kept existing ...` instead of
`Created ...`.

### Verify the generated files

```bash
ls -la runtime runtime/data runtime/backups runtime/secrets
```

Expected result:

- `runtime/data` exists.
- `runtime/backups` exists.
- `runtime/secrets` exists.
- `runtime/secrets/openbid_secret_key` exists.
- `runtime/secrets/openbid_bootstrap_admin_password` exists.

Check the secret files are not empty:

```bash
wc -c runtime/secrets/openbid_secret_key
wc -c runtime/secrets/openbid_bootstrap_admin_password
```

Expected result:

- `openbid_secret_key` is greater than `32` bytes.
- `openbid_bootstrap_admin_password` is greater than `12` bytes.

Read the generated first-login password:

```bash
cat runtime/secrets/openbid_bootstrap_admin_password
```

Expected result:

- `OpenBid!2026-YK4j3z39CEfu0kbFHcEzM8yI`.

Store that password in your password manager. It is used only to create the
bootstrap admin account when the database has no users.

### Create `.env` only when you need overrides

The compose files include defaults. For many deployments, this is enough:

```bash
./setup.sh
docker compose up -d --build
```

Create `.env` when you need to change the host port, use GHCR image tags, set a
Treasury feed URL, or tune worker timing:

```bash
cp .env.example .env
nano .env
```

Success looks like:

- `.env` exists in `ProductionDeployment/`.
- It is not empty.
- It contains only values you understand and intend to use.

### Production-ready `.env` example

This example keeps the safe defaults and sets the most common values:

```env
APP_ENV=production
SECURE_COOKIES=true
LOW_MEMORY_MODE=true
ANALYTICS_ENABLED=false
BOOTSTRAP_SYNC_ON_STARTUP=false
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
OPENBID_HTTP_PORT=8088
LOGIN_RATE_LIMIT_WINDOW_SECONDS=600
LOGIN_RATE_LIMIT_MAX_ATTEMPTS=10
TREASURY_FEED_URL=
APP_IMAGE_TAG=latest
EXTRACTOR_IMAGE_TAG=latest
SECRET_KEY=
SECRET_KEY_FILE=/run/secrets/openbid_secret_key
BOOTSTRAP_ADMIN_PASSWORD=
BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/openbid_bootstrap_admin_password
```

Important security warning:

- Do not put production secret values directly in `.env`.
- Keep `SECRET_KEY` blank and use `SECRET_KEY_FILE`.
- Keep `BOOTSTRAP_ADMIN_PASSWORD` blank and use
  `BOOTSTRAP_ADMIN_PASSWORD_FILE`.
- Do not set `SECRET_KEY_FILE=` to an empty value.
- Do not set `BOOTSTRAP_ADMIN_PASSWORD_FILE=` to an empty value.

The compose files use the file-backed secrets by default:

```env
SECRET_KEY_FILE=/run/secrets/openbid_secret_key
BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/openbid_bootstrap_admin_password
```

### Variables passed by the production compose files

These variables are actively used by `docker-compose.yml` and
`docker-compose.ghcr.yml`.

| Variable | Required? | Default | Meaning | Production guidance |
| --- | --- | --- | --- | --- |
| `APP_ENV` | Yes | `production` | Application environment | Keep `production` for real deployments |
| `APP_ADDR` | No | `:8080` inside compose | App listen address inside container | Do not change unless changing compose networking |
| `DATA_PATH` | Yes | `/app/data/store.db` inside compose | SQLite database path inside container | Do not change in normal deployments |
| `SECRET_KEY` | Production requires a strong secret by value or file | blank in compose | Direct secret value | Leave blank in production |
| `SECRET_KEY_FILE` | Yes in production unless `SECRET_KEY` is set | `/run/secrets/openbid_secret_key` | Secret file path inside container | Keep default |
| `SECURE_COOKIES` | Yes in production | `true` | Adds Secure flag to cookies and HSTS header | Keep `true`; use HTTPS for browser access |
| `LOW_MEMORY_MODE` | No | `true` | Reduces memory-heavy dashboard behavior | Keep `true` on small hosts |
| `ANALYTICS_ENABLED` | No | `false` | Enables heavier analytics views | Keep `false` unless you need analytics and have resources |
| `BOOTSTRAP_ADMIN_PASSWORD` | Required only when DB is empty and no password file exists | blank in compose | Direct initial admin password | Leave blank in production |
| `BOOTSTRAP_ADMIN_PASSWORD_FILE` | Required for empty production DB unless direct password is set | `/run/secrets/openbid_bootstrap_admin_password` | Initial admin password file | Keep default |
| `BOOTSTRAP_SYNC_ON_STARTUP` | No | `false` | Runs source sync immediately on first seed | Keep `false` for controlled first startup |
| `EXTRACTOR_URL` | Yes | `http://extractor:9090` inside compose | Internal extractor URL | Do not change in normal deployments |
| `TREASURY_FEED_URL` | No | blank | JSON feed URL for the Treasury source | Set to a real feed URL if available; blank uses embedded sample data for that source |
| `BACKUP_DIR` | Yes | `/app/backups` inside compose | Backup destination inside container | Do not change in normal deployments |
| `WORKER_SYNC_MINUTES` | No | `360` | Default interval for automatic source checks | Use a positive whole number |
| `WORKER_LOOP_SECONDS` | No | `30` | Worker loop/heartbeat cadence | Use a positive whole number |
| `LOGIN_RATE_LIMIT_WINDOW_SECONDS` | No | `600` | Login rate limit window | Use a positive whole number |
| `LOGIN_RATE_LIMIT_MAX_ATTEMPTS` | No | `10` | Max login attempts in the rate limit window | Use a positive whole number |
| `OPENBID_HTTP_PORT` | No | `8088` | Host port published by proxy | Change if `8088` is already used |
| `APP_IMAGE_REF` | GHCR only | `ghcr.io/onellan/openbid/app:${APP_IMAGE_TAG:-latest}` | Full app image reference override | Usually leave unset |
| `APP_IMAGE_TAG` | GHCR only | `latest` | App image tag | Pin for predictable deploys |
| `EXTRACTOR_IMAGE_REF` | GHCR only | `ghcr.io/onellan/openbid/extractor:${EXTRACTOR_IMAGE_TAG:-latest}` | Full extractor image reference override | Usually leave unset |
| `EXTRACTOR_IMAGE_TAG` | GHCR only | `latest` | Extractor image tag | Pin to same release/commit as app |

### Variables present in examples but not injected by compose

The application code supports additional variables such as:

```text
SESSION_HOURS
ALERT_WEBHOOK_URL
ALERT_EVAL_SECONDS
ALERT_BACKUP_MAX_AGE_MINUTES
ALERT_BACKLOG_MAX_JOBS
ALERT_BACKLOG_MAX_AGE_MINUTES
ALERT_LOGIN_THROTTLE_THRESHOLD
ALERT_EXTRACTOR_FAILURE_THRESHOLD
BOOTSTRAP_ADMIN_USERNAME
BOOTSTRAP_ADMIN_EMAIL
BOOTSTRAP_TENANT_NAME
BOOTSTRAP_TENANT_SLUG
```

At the time this README was written, the production compose files do not pass
those variables into `app` or `worker`. Changing them in `.env` alone does not
change the running containers unless the compose files are also updated to
include them.

The current effective defaults from the application are:

| Variable | Effective application default |
| --- | --- |
| `SESSION_HOURS` | `12` |
| `BOOTSTRAP_ADMIN_USERNAME` | `admin` |
| `BOOTSTRAP_ADMIN_EMAIL` | `admin@localhost` |
| `BOOTSTRAP_TENANT_NAME` | `KolaboSolutions` |
| `BOOTSTRAP_TENANT_SLUG` | derived from tenant name, currently `kolabosolutions` |
| Alert thresholds | Application defaults from `internal/app/config.go` |

The container alert helper script can still use `ALERT_WEBHOOK_URL` as a shell
environment variable when you run it manually. See the maintenance section.

### How secrets should be generated

Use `setup.sh` unless you have a policy that requires generating secrets
outside the script:

```bash
./setup.sh
```

If you must replace the bootstrap admin password before first startup:

```bash
openssl rand -base64 48
```

Then write a strong password to:

```text
runtime/secrets/openbid_bootstrap_admin_password
```

The password must pass the application's password strength checks. A safe shape
is:

```text
OpenBid!2026-long-random-text
```

Do not rotate `runtime/secrets/openbid_secret_key` casually after users exist.
It signs sessions and protects sensitive user security data such as MFA and
recovery values. Changing it without a migration plan can invalidate sessions
and make previously encrypted sensitive values unreadable.

### Verify config before starting

From `ProductionDeployment/`:

```bash
docker compose config
```

Expected result:

- Compose prints the fully rendered configuration.
- It exits successfully.
- There is no message about missing secret files.
- The `proxy` port shows your expected host port, for example `8088:80`.

For GHCR deployments:

```bash
docker compose -f docker-compose.ghcr.yml config
```

Expected result is the same, except `app` and `extractor` use `image:` entries
instead of local `build:` entries.

## 7. Docker / Compose Setup

### Choose a deployment mode

Use one of these modes.

Source build mode:

- Uses `docker-compose.yml`.
- Builds app and extractor images on the server.
- Best when deploying local source changes or when GHCR images are unavailable.

GHCR image mode:

- Uses `docker-compose.ghcr.yml`.
- Pulls prebuilt app and extractor images.
- Best when deploying published images from CI.

Do not mix compose files for day-to-day operations. Pick one mode and use the
matching commands consistently.

### Source build first start

From `ProductionDeployment/`:

```bash
./setup.sh
docker compose up -d --build
```

What this does:

- `./setup.sh` prepares runtime directories and secrets.
- `docker compose up` creates the Compose project if needed.
- `-d` runs containers in the background.
- `--build` builds the app and extractor images before starting containers.

Expected output:

- Docker builds the Go app image from `Dockerfile.app`.
- Docker builds the Python extractor image from `Dockerfile.extractor`.
- Docker starts `extractor`, `app`, `worker`, and `proxy`.
- The command returns to the shell without an error.

The first source build can take several minutes, especially on Raspberry Pi.

### GHCR image first start

From `ProductionDeployment/`:

```bash
./setup.sh
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
```

What this does:

- `./setup.sh` prepares runtime directories and secrets.
- `pull` downloads the configured app and extractor images.
- `up -d` creates and starts containers in the background.

Expected output:

- Compose pulls `ghcr.io/onellan/openbid/app`.
- Compose pulls `ghcr.io/onellan/openbid/extractor`.
- Compose starts all four services.
- No `unauthorized`, `manifest unknown`, or `not found` error appears.

### When to use common Compose commands

| Command | Use it when | Effect |
| --- | --- | --- |
| `docker compose up -d` | Start existing images/containers | Creates missing containers and starts the stack |
| `docker compose up -d --build` | Source code changed or first source deploy | Rebuilds local images and starts the stack |
| `docker compose pull` | GHCR image deploy/update | Downloads newer configured images |
| `docker compose down` | Stop and remove stack containers/networks | Does not delete bind-mounted runtime data |
| `docker compose stop` | Pause containers without removing them | Keeps containers for later start |
| `docker compose restart` | Restart services after config or runtime issue | Stops and starts containers |
| `docker compose logs` | Inspect service output | Reads recent or streaming logs |
| `docker compose ps` | Check status and health | Shows service state |

For GHCR mode, include:

```bash
-f docker-compose.ghcr.yml
```

Example:

```bash
docker compose -f docker-compose.ghcr.yml ps
```

### What successful startup looks like

Run:

```bash
docker compose ps
```

Expected result in source build mode:

```text
NAME                  SERVICE     STATUS
openbid-app-1         app         Up ... (healthy)
openbid-worker-1      worker      Up ... (healthy)
openbid-extractor-1   extractor   Up ... (healthy)
openbid-proxy-1       proxy       Up ... (healthy)
```

Names may vary slightly by Docker Compose version. The important parts are:

- All four services are present.
- Each service is `Up`.
- Health is `healthy` after startup completes.

Then run:

```bash
curl -i http://localhost:8088/healthz
```

Expected result:

```text
HTTP/1.1 200 OK
...
{"ok":true}
```

### Common startup errors

Missing secret file:

```text
secret openbid_secret_key: file ... does not exist
```

Fix:

```bash
./setup.sh
docker compose up -d
```

Port already allocated:

```text
Bind for 0.0.0.0:8088 failed: port is already allocated
```

Fix:

```bash
cp .env.example .env
nano .env
```

Set:

```env
OPENBID_HTTP_PORT=8090
```

Then:

```bash
docker compose up -d
```

Weak production secret:

```text
SECRET_KEY must be a strong non-default value in production
```

Fix:

```bash
./setup.sh
wc -c runtime/secrets/openbid_secret_key
docker compose up -d
```

Secure cookies disabled in production:

```text
SECURE_COOKIES must be true in production
```

Fix `.env`:

```env
SECURE_COOKIES=true
```

Then:

```bash
docker compose up -d
```

## 8. First Startup Procedure

### First-run order

Follow this order exactly for a new production deployment:

1. Move into the deployment folder.
2. Run `setup.sh`.
3. Verify secret files exist and are non-empty.
4. Validate Compose config.
5. Start the stack.
6. Wait for all containers to become healthy.
7. Check `/healthz`.
8. Read the bootstrap admin password.
9. Open the login page over HTTPS.
10. Log in as `admin`.
11. Change the admin password in the UI.
12. Review sources and trigger source checks when ready.

Commands:

```bash
cd ProductionDeployment
./setup.sh
wc -c runtime/secrets/openbid_secret_key
wc -c runtime/secrets/openbid_bootstrap_admin_password
docker compose config
docker compose up -d --build
docker compose ps
curl -i http://localhost:8088/healthz
cat runtime/secrets/openbid_bootstrap_admin_password
```

Success looks like:

- Secret file sizes are non-zero.
- `docker compose config` succeeds.
- All services become `healthy`.
- `/healthz` returns `{"ok":true}`.
- You have the generated bootstrap password.

For GHCR, replace the start commands with:

```bash
docker compose -f docker-compose.ghcr.yml config
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
docker compose -f docker-compose.ghcr.yml ps
```

### Migrations and bootstrap

There is no separate manual migration command for normal deployment.

The `app` and `worker` containers open the SQLite database at:

```text
/app/data/store.db
```

The application automatically:

- Creates the SQLite schema if the database is empty.
- Migrates the SQLite schema when the binary supports the current database.
- Validates runtime tables through `/healthz`.
- Seeds the first admin user when no users exist.
- Seeds the default tenant and admin membership.
- Seeds default source configurations.
- Seeds source health state.
- Seeds smart keyword data when seed data is available.

If the database schema is newer than the running binary supports, startup fails.
That protects you from accidentally running an older OpenBid binary against a
newer database.

### First admin setup

On an empty production database, OpenBid creates:

```text
Username: admin
Email: admin@localhost
Role: Platform Super Admin
Tenant role: Tenant Owner
```

The password comes from:

```text
runtime/secrets/openbid_bootstrap_admin_password
```

Read it:

```bash
cat runtime/secrets/openbid_bootstrap_admin_password
```

After logging in, change the admin password from the UI. The bootstrap password
file is not a recurring password reset mechanism. Once the user exists, changing
the file does not reset that user's password.

### Default tenant notes

The application default tenant is:

```text
KolaboSolutions
```

The effective default slug is:

```text
kolabosolutions
```

The current production compose files do not pass `BOOTSTRAP_TENANT_NAME` or
`BOOTSTRAP_TENANT_SLUG`, so `.env` changes to those values are not applied
unless the compose files are also updated.

### Source setup notes

OpenBid seeds default source configurations on startup. The default sources
include National Treasury, eTenders, Eskom, Transnet, OnlineTenders, eThekwini,
City of Johannesburg, and several web page sources.

Important Treasury behavior:

- If `TREASURY_FEED_URL` is blank, the National Treasury JSON feed adapter uses
  embedded sample data when it runs.
- For a production Treasury feed, set `TREASURY_FEED_URL` to a real JSON feed
  URL before source checks run.
- You can also manage source settings from the OpenBid UI after first login.

Startup sync is disabled by default:

```env
BOOTSTRAP_SYNC_ON_STARTUP=false
```

That means first startup should be calm. The worker will run scheduled source
checks according to source schedule settings, and admins can trigger manual
source checks from the UI.

### First login instructions

Use the HTTPS domain configured in front of the OpenBid proxy:

```text
https://openbid.example.com/login
```

Login with:

```text
Username: admin
Password: value from runtime/secrets/openbid_bootstrap_admin_password
```

Expected result:

- The login page accepts the credentials.
- You land on the OpenBid home/dashboard.
- Navigation includes items such as Tenders, Queue, Sources, Settings, and
  Health depending on your role.

If you are testing only on the server:

```bash
curl -i http://localhost:8088/login
```

Expected result:

- HTTP `200 OK`.
- HTML for the login page.

For browser login, use HTTPS because production secure cookies are enabled.

### Configure outbound email after first login

OpenBid does not run a public mail server. It does not host inboxes, MX
records, POP3, IMAP, spam filtering, or inbound email. Outbound email is a
lightweight SMTP client feature that sends through the SMTP provider you
configure in the Admin UI.

Email is optional and disabled by default. The app, worker, Smart Keywords, and
source sync continue to work when email is not configured.

To configure email:

1. Log in as a Platform Admin or Platform Super Admin.
2. Open:

```text
Settings -> Email
```

Or go directly to:

```text
https://openbid.example.com/admin/email
```

Minimum required settings:

| Field | Required when | Example | Notes |
| --- | --- | --- | --- |
| Global outbound email enabled | You want OpenBid to send mail | On | Leave off until the other fields are complete |
| SMTP host | Always | `smtp.sendgrid.net` | Use the host supplied by your email provider |
| SMTP port | Always | `587` | Common ports are `587` for STARTTLS, `465` for TLS, and `25` for plain/provider-specific use |
| Security mode | Always | `STARTTLS` | Choose `STARTTLS`, `TLS`, or `plain` to match your provider |
| From email | Always | `alerts@example.com` | Must be an email address your provider allows |
| SMTP authentication required | When your provider requires login | On | Most production providers require this |
| SMTP username | When authentication is on | `apikey` or account username | Depends on provider |
| SMTP password/app password | When authentication is on | provider app password | The UI never displays the stored password |

Optional settings:

| Field | Example | Notes |
| --- | --- | --- |
| From display name | `OpenBid Alerts` | Friendly sender name shown by mail clients |
| Reply-to address | `support@example.com` | Leave blank to omit Reply-To |
| Timeout seconds | `10` | Increase only if your SMTP provider is slow |
| Default test recipient | `you@example.com` | Convenience value for the test email form |

Success looks like:

- The readiness panel says `Email ready`.
- Missing required field warnings are gone.
- Invalid field warnings are gone.
- The password status says whether a password is stored, but the password value
  is not shown.
- Clicking `Send test email` redirects back with a success message.
- The test recipient receives an email with subject `OpenBid test email`.

If readiness says `Email not configured` or `Email partially configured`, read
the missing/invalid field list at the top of the Email settings page, fix those
fields, save again, then send another test email.

Smart Keywords uses this central Admin email configuration. Smart Keywords does
not ask for SMTP host, port, password, TLS mode, or provider details. In Smart
Keywords, the only email-specific choice is:

```text
Send email alerts: on/off
```

When `Send email alerts` is off, Smart Keyword email alert channels do not send
email. When it is on, Smart Keywords sends through the Admin-configured email
service. If Smart Keywords tries to send email while Admin email is not ready,
OpenBid records the alert attempt as failed/skipped and continues processing
matches without crashing the app or worker.

### Confirm the app is healthy

```bash
curl http://localhost:8088/healthz
```

Expected result:

```json
{"ok":true}
```

The health endpoint checks the database runtime state. If the database schema,
tables, or runtime validation fails, it returns an unhealthy response.

## 9. Verification Steps

### Check container status

Source build mode:

```bash
docker compose ps
```

GHCR mode:

```bash
docker compose -f docker-compose.ghcr.yml ps
```

Expected result:

- `app` is `Up` and `healthy`.
- `worker` is `Up` and `healthy`.
- `extractor` is `Up` and `healthy`.
- `proxy` is `Up` and `healthy`.

If a service is `starting`, wait 30 to 90 seconds and check again.

### Inspect logs

All services:

```bash
docker compose logs --tail=200
```

One service:

```bash
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

Follow logs live:

```bash
docker compose logs -f
```

Expected healthy signs:

- `app` does not repeatedly exit.
- `worker` prints `worker starting`.
- `worker` writes heartbeat and source/job log events over time.
- `extractor` responds to its healthcheck.
- `proxy` access logs show requests and upstream status codes.

### Confirm health checks pass

Host-level health:

```bash
curl -i http://localhost:8088/healthz
```

Expected result:

```text
HTTP/1.1 200 OK
{"ok":true}
```

Container-level health:

```bash
docker inspect --format '{{.State.Health.Status}}' openbid-app-1
docker inspect --format '{{.State.Health.Status}}' openbid-worker-1
docker inspect --format '{{.State.Health.Status}}' openbid-extractor-1
docker inspect --format '{{.State.Health.Status}}' openbid-proxy-1
```

Expected result for each:

```text
healthy
```

If your Compose-generated container names differ, get the names with:

```bash
docker compose ps
```

### Confirm the web app is reachable

```bash
curl -i http://localhost:8088/login
```

Expected result:

- HTTP `200 OK`.
- Response contains HTML for the login page.

If using a domain:

```bash
curl -I https://openbid.example.com/login
```

Expected result:

- HTTP `200 OK`.
- TLS certificate is valid in your browser.
- No redirect loop.

### Confirm worker/background services are running

```bash
docker compose logs --tail=100 worker
```

Expected result:

- Logs include `worker starting`.
- No repeated fatal error.
- `docker compose ps` shows `worker` as `healthy`.

The worker healthcheck reads:

```text
/tmp/openbid-worker-heartbeat
```

inside the worker container. If the worker loop stops updating that heartbeat,
the healthcheck becomes unhealthy.

### Confirm extractor is running

```bash
docker compose logs --tail=100 extractor
docker compose ps extractor
```

Expected result:

- `extractor` is `Up` and `healthy`.
- No repeated Python traceback.

The extractor is not exposed to the host. Its healthcheck runs inside the
container against:

```text
http://127.0.0.1:9090/healthz
```

### Confirm persistence is working

Create a backup:

```bash
docker compose exec -T app openbid-sqlite-backup /app/backups/verify-persistence.db
```

Check the backup exists on the host:

```bash
ls -lh runtime/backups/verify-persistence.db
```

Validate the current database:

```bash
docker compose run --rm --no-deps app openbid-sqlite-check
```

Expected result:

- The backup file exists and has a non-zero size.
- The SQLite check command exits successfully.

Clean up the verification backup only if you do not need it:

```bash
rm -f runtime/backups/verify-persistence.db
```

## 10. Accessing OpenBid

### Local URL examples

On the deployment host:

```text
http://localhost:8088/healthz
http://localhost:8088/login
```

If you changed the port:

```text
http://localhost:8090/healthz
http://localhost:8090/login
```

### LAN access guidance

Find the server IP:

```bash
hostname -I
```

Example LAN URL:

```text
http://192.168.1.50:8088/healthz
```

Plain HTTP LAN access is acceptable for health testing on a trusted network, but
production browser login should use HTTPS because `SECURE_COOKIES=true`.

### Domain and reverse proxy access

Recommended production shape:

```text
https://openbid.example.com
        |
        v
external HTTPS reverse proxy
        |
        v
http://127.0.0.1:8088 or http://SERVER_LAN_IP:8088
        |
        v
OpenBid proxy container
```

External reverse proxy requirements:

- Forward to `http://SERVER:8088`.
- Preserve the original `Host`.
- Set `X-Forwarded-Proto: https`.
- Set `X-Forwarded-For`.
- Allow request bodies up to at least `10m`, matching the included nginx config.

### Included nginx config

The default compose files mount:

```text
nginx/nginx.conf
```

That config provides the internal OpenBid proxy, structured access logs,
forwarded header handling, basic request limits, static asset caching, and a
`10m` client body limit.

A Raspberry Pi-specific nginx variant is available:

```text
nginx/nginx.raspberry-pi.conf
```

To use it, edit the `proxy` volume mount in the compose file you use and replace:

```yaml
./nginx/nginx.conf:/etc/nginx/conf.d/default.conf:ro
```

with:

```yaml
./nginx/nginx.raspberry-pi.conf:/etc/nginx/conf.d/default.conf:ro
```

Then validate and restart:

```bash
docker compose config
docker compose up -d
docker compose ps proxy
curl http://localhost:8088/healthz
```

Success looks like:

- Compose config renders without a YAML error.
- `proxy` is `Up` and `healthy`.
- `/healthz` returns `{"ok":true}`.

### HTTPS guidance

Keep these production values:

```env
APP_ENV=production
SECURE_COOKIES=true
```

Do not set `SECURE_COOKIES=false` for a real production deployment. If you need
temporary non-HTTPS browser testing, use a non-production environment and
understand that this is not the production security posture.

### What login page to expect

Open:

```text
https://openbid.example.com/login
```

Expected result:

- Page title/content for OpenBid login.
- Username and password fields.
- No demo credential note in production.

Login with:

```text
admin
```

and the bootstrap password from:

```bash
cat runtime/secrets/openbid_bootstrap_admin_password
```

### What to do on first login

After first login:

- Change the admin password immediately.
- Store the new admin password in a password manager.
- Review user administration and create named accounts for real users.
- Keep the bootstrap `admin` account tightly controlled.
- Review source configuration at `/sources`.
- Review platform health at `/health`.
- Trigger a manual source check only after you are comfortable with the source
  settings.

## 11. Day-2 Operations

### Restart services

Source build mode:

```bash
docker compose restart
docker compose ps
```

GHCR mode:

```bash
docker compose -f docker-compose.ghcr.yml restart
docker compose -f docker-compose.ghcr.yml ps
```

Success looks like:

- Services restart.
- All four return to `Up` and `healthy`.
- `/healthz` returns `{"ok":true}`.

### Stop services

```bash
docker compose down
```

What it does:

- Stops and removes the OpenBid containers and the Compose network.
- Does not delete `runtime/data`, `runtime/backups`, or `runtime/secrets`
  because those are host bind mounts.

Success looks like:

- Compose reports containers removed.
- `docker compose ps` shows no running services for this project.

Start again:

```bash
docker compose up -d
```

### Rebuild after code changes

Use source build mode:

```bash
git pull
cd ProductionDeployment
./setup.sh
docker compose up -d --build
docker compose ps
curl http://localhost:8088/healthz
```

What success looks like:

- Git updates the source.
- Docker rebuilds images.
- Containers become healthy.
- Health returns `{"ok":true}`.

### Pull newer GHCR images

Use GHCR mode:

```bash
cd ProductionDeployment
./setup.sh
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
docker compose -f docker-compose.ghcr.yml ps
curl http://localhost:8088/healthz
```

Success looks like:

- Compose downloads newer images when available.
- Containers are recreated if their image changed.
- Services become healthy.

### Update the deployment safely

Recommended update sequence:

1. Confirm current stack is healthy.
2. Create a database backup.
3. Record the current Git commit or image tag.
4. Pull source or images.
5. Start the updated stack.
6. Check logs and health.
7. Log in and inspect `/health`.

Commands for source mode:

```bash
docker compose ps
curl http://localhost:8088/healthz
docker compose exec -T app openbid-sqlite-backup /app/backups/pre-update-$(date +%Y%m%d-%H%M%S).db
git rev-parse HEAD
git pull
docker compose up -d --build
docker compose ps
curl http://localhost:8088/healthz
docker compose logs --tail=200
```

Commands for GHCR mode:

```bash
docker compose -f docker-compose.ghcr.yml ps
curl http://localhost:8088/healthz
docker compose -f docker-compose.ghcr.yml exec -T app openbid-sqlite-backup /app/backups/pre-update-$(date +%Y%m%d-%H%M%S).db
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
docker compose -f docker-compose.ghcr.yml ps
curl http://localhost:8088/healthz
docker compose -f docker-compose.ghcr.yml logs --tail=200
```

### Roll back if an update fails

If an update fails before the database schema changed:

Source mode:

```bash
git checkout PREVIOUS_COMMIT_OR_TAG
cd ProductionDeployment
docker compose up -d --build
docker compose ps
curl http://localhost:8088/healthz
```

GHCR mode:

```bash
nano .env
```

Set the previous tags:

```env
APP_IMAGE_TAG=previous-tag
EXTRACTOR_IMAGE_TAG=previous-tag
```

Then:

```bash
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
docker compose -f docker-compose.ghcr.yml ps
curl http://localhost:8088/healthz
```

If the updated application already migrated the database and the older binary
cannot read it, restore the pre-update database backup. Follow the restore steps
in the data and persistence section.

### Re-run migrations safely

OpenBid migrations run automatically when `app` or `worker` starts. There is no
normal manual migration command.

To force startup validation after a failed attempt:

```bash
docker compose down
docker compose run --rm --no-deps app openbid-sqlite-check
docker compose up -d
docker compose ps
curl http://localhost:8088/healthz
```

If `openbid-sqlite-check` fails, do not repeatedly restart the app. Inspect logs
and restore from backup if the database is corrupted.

### Check logs during updates

```bash
docker compose logs -f app worker proxy extractor
```

Expected result:

- No repeated panic, fatal error, or restart loop.
- `app` becomes healthy.
- `worker` becomes healthy.
- `proxy` serves `/healthz`.

Stop following logs with `Ctrl+C`. This does not stop the containers.

### Optional systemd unit

The compose services already use:

```yaml
restart: unless-stopped
```

For many hosts, that is enough when Docker itself starts on boot:

```bash
sudo systemctl enable docker
sudo systemctl start docker
```

If your host requires a systemd unit for the Compose stack, use this template:

```text
systemd/openbid-compose.service
```

The template intentionally does not contain a fixed clone path. Before installing
it, set `OPENBID_DEPLOY_DIR` to the absolute path of this directory.

Example install flow:

```bash
pwd
sudo cp systemd/openbid-compose.service /etc/systemd/system/openbid-compose.service
sudo nano /etc/systemd/system/openbid-compose.service
sudo systemctl daemon-reload
sudo systemctl enable openbid-compose
sudo systemctl start openbid-compose
sudo systemctl status openbid-compose
```

In the unit file, change:

```text
Environment=OPENBID_DEPLOY_DIR=.
```

to your real deployment path, for example:

```text
Environment=OPENBID_DEPLOY_DIR=/opt/openbid/ProductionDeployment
```

Success looks like:

- `systemctl status openbid-compose` reports active/exited for the oneshot unit.
- `docker compose ps` from `ProductionDeployment/` shows OpenBid services
  running and healthy.

## 12. Data and Persistence

### What data is persisted

Persistent data includes:

- Users.
- Tenants and memberships.
- Sessions.
- Tender/opportunity records.
- Workflow records.
- Bookmarks.
- Saved searches.
- Keyword and smart keyword data.
- Source configuration.
- Source sync history and health.
- Queue jobs and extraction state.
- Audit entries.
- SQLite schema metadata.
- Backups.
- Production secrets.

### Where data lives

Host paths:

```text
runtime/data/store.db
runtime/data/store.db-wal
runtime/data/store.db-shm
runtime/backups/
runtime/secrets/openbid_secret_key
runtime/secrets/openbid_bootstrap_admin_password
```

Container paths:

```text
/app/data/store.db
/app/backups/
/run/secrets/openbid_secret_key
/run/secrets/openbid_bootstrap_admin_password
```

### How volumes are mounted

From the compose files:

```yaml
volumes:
  - "./runtime/data:/app/data"
  - "./runtime/backups:/app/backups"
```

These are bind mounts. Docker named volume commands are not the primary way to
manage OpenBid data. Inspect and back up the host directories directly.

### Inspect runtime files

```bash
ls -lah runtime
ls -lah runtime/data
ls -lah runtime/backups
ls -lah runtime/secrets
```

Expected result:

- `store.db` exists after `setup.sh` and startup.
- WAL/SHM files may exist while the app is running.
- Backups appear in `runtime/backups` after backup commands.
- Secret files exist and should have restrictive permissions on Linux.

### Create a database backup

Source mode:

```bash
docker compose exec -T app openbid-sqlite-backup /app/backups/store-$(date +%Y%m%d-%H%M%S).db
ls -lh runtime/backups
```

GHCR mode:

```bash
docker compose -f docker-compose.ghcr.yml exec -T app openbid-sqlite-backup /app/backups/store-$(date +%Y%m%d-%H%M%S).db
ls -lh runtime/backups
```

What the backup command does:

- Asks the running app container to run `openbid-sqlite-backup`.
- The backup tool checkpoints SQLite WAL state.
- It writes a `.db` file into `/app/backups`.
- The host sees that file in `runtime/backups`.

Success looks like:

- A new file appears in `runtime/backups`.
- The file has a non-zero size.
- The command exits without an error.

### Verify a backup is valid

Copy the backup to a temporary validation path:

```bash
cp runtime/backups/store-YYYYMMDD-HHMMSS.db runtime/data/restore-test.db
```

Run the SQLite check against that file:

```bash
docker compose run --rm --no-deps -e DATA_PATH=/app/data/restore-test.db app openbid-sqlite-check
```

Expected result:

- The command exits successfully.
- No schema or runtime validation error appears.

Remove the temporary validation copy:

```bash
rm -f runtime/data/restore-test.db
```

### Restore a database backup

Warning: Restore replaces the current database. Create a backup of the current
state first unless the current state is unusable.

1. Stop write-path containers:

```bash
docker compose stop proxy app worker
```

The extractor can keep running, but stopping the full stack is also acceptable:

```bash
docker compose down
```

2. Copy the selected backup into place:

```bash
cp runtime/backups/store-YYYYMMDD-HHMMSS.db runtime/data/store.db
```

3. Remove stale SQLite sidecar files:

```bash
rm -f runtime/data/store.db-wal runtime/data/store.db-shm
```

4. Validate the restored database:

```bash
docker compose run --rm --no-deps app openbid-sqlite-check
```

5. Start the stack:

```bash
docker compose up -d
docker compose ps
curl http://localhost:8088/healthz
```

Success looks like:

- SQLite check passes.
- Services become healthy.
- `/healthz` returns `{"ok":true}`.
- Login works with the users/passwords stored in the restored database.

For GHCR mode, add `-f docker-compose.ghcr.yml` to every compose command in the
restore sequence.

### What should never be deleted casually

Do not casually delete:

```text
runtime/data/store.db
runtime/data/store.db-wal
runtime/data/store.db-shm
runtime/secrets/openbid_secret_key
runtime/secrets/openbid_bootstrap_admin_password
runtime/backups/
```

Deleting `store.db` erases the application database.

Deleting `openbid_secret_key` and creating a new one can make existing sensitive
security data unreadable and invalidates existing signed sessions.

Deleting backups removes your recovery path.

## 13. Troubleshooting

Use this pattern for every issue:

1. Check `docker compose ps`.
2. Check logs for the affected service.
3. Check `/healthz`.
4. Fix the root cause.
5. Verify health again.

For GHCR deployments, add `-f docker-compose.ghcr.yml` to compose commands.

### Container will not start

Symptoms:

- `docker compose ps` shows `Exited`.
- `docker compose up -d` returns an error.
- Logs show fatal startup messages.

Likely causes:

- Missing or empty secret files.
- Invalid `.env` value.
- Database permission issue.
- Image build or pull failed.

Diagnose:

```bash
docker compose ps
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
docker compose config
```

Fix:

```bash
./setup.sh
docker compose config
docker compose up -d
```

If the app log names a specific invalid variable, edit `.env`, save it, then:

```bash
docker compose up -d
```

Verify:

```bash
docker compose ps
curl http://localhost:8088/healthz
```

### Port already in use

Symptoms:

- Compose reports `port is already allocated`.
- `proxy` will not start.

Likely cause:

- Another process already listens on `8088`.

Diagnose:

```bash
sudo ss -ltnp | grep ':8088 ' || true
docker compose logs --tail=100 proxy
```

Fix:

```bash
cp .env.example .env
nano .env
```

Set a free port:

```env
OPENBID_HTTP_PORT=8090
```

Restart:

```bash
docker compose up -d
```

Verify:

```bash
curl http://localhost:8090/healthz
docker compose ps proxy
```

### Permissions problems

Symptoms:

- Logs mention `permission denied`.
- App cannot open `/app/data/store.db`.
- Backup command cannot write to `/app/backups`.

Likely cause:

- Host runtime directories or files are not writable by the container user.
- Files were copied with restrictive ownership.

Diagnose:

```bash
ls -lah runtime runtime/data runtime/backups runtime/secrets
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
```

Fix:

```bash
./setup.sh
chmod 777 runtime/data runtime/backups
chmod 666 runtime/data/store.db
chmod 700 runtime/secrets
chmod 600 runtime/secrets/openbid_secret_key runtime/secrets/openbid_bootstrap_admin_password
docker compose restart app worker
```

Verify:

```bash
docker compose ps
docker compose exec -T app openbid-sqlite-backup /app/backups/permission-check.db
ls -lh runtime/backups/permission-check.db
rm -f runtime/backups/permission-check.db
```

### Environment or config missing

Symptoms:

- Logs mention required values such as `DATA_PATH must not be empty`.
- Logs mention `SECRET_KEY must be a strong non-default value in production`.
- Logs mention `SECURE_COOKIES must be true in production`.

Likely cause:

- `.env` overrides broke safe defaults.
- `SECRET_KEY_FILE` or `BOOTSTRAP_ADMIN_PASSWORD_FILE` was set to blank.

Diagnose:

```bash
docker compose config
grep -n 'SECRET_KEY\|BOOTSTRAP_ADMIN_PASSWORD\|SECURE_COOKIES\|DATA_PATH' .env || true
wc -c runtime/secrets/openbid_secret_key
wc -c runtime/secrets/openbid_bootstrap_admin_password
```

Fix:

Edit `.env` so these are true:

```env
APP_ENV=production
SECURE_COOKIES=true
SECRET_KEY=
SECRET_KEY_FILE=/run/secrets/openbid_secret_key
BOOTSTRAP_ADMIN_PASSWORD=
BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/openbid_bootstrap_admin_password
```

Then:

```bash
./setup.sh
docker compose up -d
```

Verify:

```bash
docker compose ps
curl http://localhost:8088/healthz
```

### App unhealthy

Symptoms:

- `app` status is `unhealthy`.
- `/healthz` returns non-200 or `{"ok":false}`.
- Logs mention database validation failure.

Likely cause:

- Database schema/runtime validation failed.
- Database file is corrupted or inaccessible.
- App cannot read secrets.

Diagnose:

```bash
docker compose ps app
docker compose logs --tail=300 app
curl -i http://localhost:8088/healthz
docker compose run --rm --no-deps app openbid-sqlite-check
```

Fix:

- If the check reports missing tables or corruption, restore a known-good
  backup.
- If the logs mention secrets, rerun `./setup.sh` and verify secret files.
- If the logs mention schema newer than binary, deploy the matching newer
  OpenBid version instead of rolling back the binary.

Verify:

```bash
docker compose up -d
docker compose ps app
curl http://localhost:8088/healthz
```

### Cannot log in

Symptoms:

- Login page loads but credentials fail.
- Login appears to succeed but redirects back to `/login`.
- Browser session does not stick.

Likely causes:

- Wrong bootstrap password.
- Bootstrap password changed after the admin user was already created.
- Account locked after repeated failed attempts.
- Browser is using plain HTTP while `SECURE_COOKIES=true`.
- External proxy is not forwarding the correct scheme/host headers.

Diagnose:

```bash
cat runtime/secrets/openbid_bootstrap_admin_password
docker compose logs --tail=200 app
curl -I http://localhost:8088/login
```

Fix:

- Use username `admin` only for the initial bootstrap user.
- Use the password that existed when the empty database first started.
- If the account is locked, wait at least 15 minutes or use another admin user
  if one exists.
- Access through HTTPS for browser login.
- Configure your external reverse proxy to send `X-Forwarded-Proto: https` and
  preserve `Host`.

Verify:

- Browser shows a logged-in OpenBid page after submitting credentials.
- App logs do not show repeated invalid credential attempts.

### Outbound email or SMTP test fails

Symptoms:

- `/admin/email` shows `Email not configured` or `Email partially configured`.
- `Send test email` redirects back with an error.
- Smart Keyword email alert history shows `failed` or `skipped`.
- The recipient does not receive the test message.

Likely causes:

- Global outbound email is disabled.
- A required SMTP field is missing.
- Security mode or port does not match the provider.
- SMTP username/password or app password is wrong.
- The provider rejected the from address.
- The host firewall, network, or provider blocks the SMTP port.
- Smart Keywords email alerts are off even though a Saved Smart View has an
  email channel.

Diagnose from the UI:

1. Log in as a Platform Admin.
2. Open `/admin/email`.
3. Read the readiness status.
4. Read the missing and invalid field lists.
5. Confirm `Password stored` says `Yes` when SMTP authentication is required.
6. Send a test email to an address you control.

Diagnose from the host:

```bash
docker compose logs --tail=300 app
docker compose logs --tail=300 worker
docker compose exec -T app sh -lc 'nc -vz smtp.example.com 587'
```

Replace `smtp.example.com` and `587` with your provider host and port.

Fix:

- In `/admin/email`, enable outbound email globally.
- Fill required fields: SMTP host, SMTP port, security mode, from email, and
  username/password when authentication is required.
- Use `STARTTLS` with port `587` unless your provider specifically tells you to
  use `TLS` on port `465` or `plain`.
- Use an app password or provider token when the provider requires it.
- Use a from address that your provider has verified.
- Save settings, then send a test email before relying on Smart Keywords.
- In `/smart-keywords`, turn on `Send email alerts` only after Admin email says
  `Email ready`.

Verify:

- `/admin/email` says `Email ready`.
- `Send test email` shows a success message.
- The test recipient receives the message.
- App logs show `email send succeeded` and do not show SMTP auth or TLS errors.
- Smart Keyword email alert deliveries show `sent` when a matching Saved Smart
  View has an enabled email channel and recipient.

### Reverse proxy issues

Symptoms:

- Domain returns 502/503.
- Login redirects loop.
- CSRF or same-origin actions fail.
- App works locally but not through the domain.

Likely causes:

- External reverse proxy points to the wrong host or port.
- Missing `X-Forwarded-Proto`.
- Missing or rewritten `Host`.
- TLS terminates before OpenBid but scheme is forwarded as `http`.

Diagnose:

```bash
curl -I http://localhost:8088/healthz
curl -I https://openbid.example.com/healthz
docker compose logs --tail=200 proxy
```

Fix:

Configure the external proxy to forward:

```text
Host: original host
X-Forwarded-Host: original host
X-Forwarded-For: client IP chain
X-Forwarded-Proto: https
```

Verify:

```bash
curl -I https://openbid.example.com/healthz
```

Expected:

- HTTP `200`.
- Browser login over HTTPS works.

### Database or storage issues

Symptoms:

- `/healthz` reports store unhealthy.
- Logs mention SQLite errors.
- Backups fail.
- Disk is full.

Likely causes:

- Disk full.
- Database file corrupted.
- Runtime directory permissions changed.
- Host storage is unreliable.

Diagnose:

```bash
df -h .
ls -lah runtime/data
docker compose logs --tail=300 app
docker compose run --rm --no-deps app openbid-sqlite-check
```

Fix:

- Free disk space if `df` shows low availability.
- Fix permissions with `./setup.sh`.
- Restore from the latest valid backup if SQLite check fails.

Verify:

```bash
docker compose up -d
docker compose ps
curl http://localhost:8088/healthz
```

### Worker not processing

Symptoms:

- `worker` is unhealthy.
- Queue items remain queued.
- Source checks do not run.
- Logs stop after startup.

Likely causes:

- Worker cannot access the database.
- Worker cannot reach extractor.
- Worker heartbeat is stale.
- Invalid worker timing values.

Diagnose:

```bash
docker compose ps worker extractor
docker compose logs --tail=300 worker
docker compose logs --tail=100 extractor
grep -n 'WORKER_' .env || true
```

Fix:

- Ensure `WORKER_SYNC_MINUTES` and `WORKER_LOOP_SECONDS` are positive whole
  numbers.
- Restart worker and extractor:

```bash
docker compose restart extractor worker
```

Verify:

```bash
docker compose ps worker
docker compose logs --tail=100 worker
```

Expected:

- `worker` is healthy.
- Logs include worker events and no repeated fatal error.

### Source sync not running

Symptoms:

- `/sources` shows no recent source checks.
- Source health stays at waiting/configured.
- Manual source check does not appear to run.

Likely causes:

- `BOOTSTRAP_SYNC_ON_STARTUP=false` means no immediate startup sync.
- Next scheduled check time has not arrived.
- Source disabled or manual/auto checks disabled in UI.
- Worker is not healthy.
- Source website blocks automated reads or is unavailable.

Diagnose:

```bash
docker compose ps worker
docker compose logs --tail=300 worker
curl http://localhost:8088/healthz
```

Then log in and inspect:

```text
/sources
/health
```

Fix:

- Use `/sources` to trigger a manual check for one source.
- Confirm the source is enabled.
- Confirm worker is healthy.
- If a source fails due to upstream website behavior, review the source message
  in `/sources` and adjust/disable that source as needed.

Verify:

- `/sources` shows a recent run.
- Worker logs include `worker_source_check_started` and
  `worker_source_check_finished`.

### Low-memory problems

Symptoms:

- Containers are killed or restart under load.
- Host logs show out-of-memory events.
- Builds fail on small devices.

Likely causes:

- Host has too little RAM.
- Source build is memory-heavy.
- Too many extraction jobs or source checks run at once for the host.

Diagnose:

```bash
free -h
docker stats --no-stream
docker compose ps
docker compose logs --tail=200 app worker extractor
```

Fix:

- Keep `LOW_MEMORY_MODE=true`.
- Keep `BOOTSTRAP_SYNC_ON_STARTUP=false`.
- Increase `WORKER_SYNC_MINUTES`.
- Use GHCR images instead of building locally on very small hosts.
- Add swap or move to a larger host if OOM continues.

Verify:

```bash
docker compose ps
free -h
curl http://localhost:8088/healthz
```

### Image pull failures

Symptoms:

- `docker compose -f docker-compose.ghcr.yml pull` fails.
- Error mentions `unauthorized`, `denied`, `not found`, or `manifest unknown`.

Likely causes:

- Not logged into GHCR when required.
- Image tag does not exist.
- Network/DNS problem.

Diagnose:

```bash
docker compose -f docker-compose.ghcr.yml pull
grep -n 'APP_IMAGE\|EXTRACTOR_IMAGE' .env || true
docker login ghcr.io
```

Fix:

- Log in to GHCR if required:

```bash
docker login ghcr.io
```

- Use a tag that exists, such as `latest`, a release tag, or a matching
  `sha-FULL_COMMIT_SHA` tag.
- If GHCR is unavailable, use source build mode:

```bash
docker compose up -d --build
```

Verify:

```bash
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
docker compose -f docker-compose.ghcr.yml ps
```

### Compose errors

Symptoms:

- Compose says a variable, file, or secret is invalid.
- Compose config fails before containers start.

Likely causes:

- Running commands from the wrong directory.
- Missing `runtime/secrets` files.
- YAML edited incorrectly.

Diagnose:

```bash
pwd
ls -la
docker compose config
```

Fix:

```bash
cd /path/to/OpenBid/ProductionDeployment
./setup.sh
docker compose config
```

If you edited YAML and broke syntax, compare with source control:

```bash
git diff -- docker-compose.yml docker-compose.ghcr.yml
```

Verify:

```bash
docker compose up -d
docker compose ps
```

### Volume ownership issues

Symptoms:

- App starts on one host but fails after moving files.
- Runtime files are owned by another user.
- Backup or database writes fail.

Likely causes:

- Files copied with root ownership.
- Files restored from backup with restrictive permissions.

Diagnose:

```bash
ls -lan runtime runtime/data runtime/backups runtime/secrets
docker compose logs --tail=200 app
```

Fix:

For a simple single-host deployment:

```bash
./setup.sh
chmod 777 runtime/data runtime/backups
chmod 666 runtime/data/store.db
chmod 700 runtime/secrets
chmod 600 runtime/secrets/openbid_secret_key runtime/secrets/openbid_bootstrap_admin_password
docker compose restart app worker
```

Verify:

```bash
docker compose exec -T app openbid-sqlite-backup /app/backups/ownership-check.db
ls -lh runtime/backups/ownership-check.db
rm -f runtime/backups/ownership-check.db
```

### Restart loop issues

Symptoms:

- `docker compose ps` shows containers repeatedly restarting.
- Logs repeat the same startup error.

Likely causes:

- Fatal app configuration error.
- Database migration/runtime error.
- Extractor dependency failure.

Diagnose:

```bash
docker compose ps
docker compose logs --tail=300 app
docker compose logs --tail=300 worker
docker compose logs --tail=300 extractor
```

Fix:

- Fix the first fatal error in logs.
- Do not chase later errors until the first fatal error is corrected.
- Common fixes are `./setup.sh`, `.env` correction, port correction, or database
  restore.

Verify:

```bash
docker compose up -d
docker compose ps
curl http://localhost:8088/healthz
```

### Network access issues

Symptoms:

- Host cannot reach `/healthz`.
- Other LAN devices cannot reach OpenBid.
- Source checks fail to fetch upstream sites.

Likely causes:

- Firewall blocks `OPENBID_HTTP_PORT`.
- External reverse proxy points to wrong host.
- Docker cannot resolve or reach the internet.
- Upstream source blocks automated reads.

Diagnose:

```bash
curl -i http://localhost:8088/healthz
hostname -I
sudo ss -ltnp | grep ':8088 ' || true
docker compose logs --tail=200 proxy
docker compose logs --tail=300 worker
```

Fix:

- Open the selected host port only to the networks that need it.
- Point your external reverse proxy to the correct host and port.
- Confirm outbound DNS/network access from the host.
- Disable or adjust failing sources from `/sources` if an upstream blocks reads.

Verify:

```bash
curl http://localhost:8088/healthz
curl http://SERVER_LAN_IP:8088/healthz
curl https://openbid.example.com/healthz
```

Use the URLs that match your deployment.

## 14. Security Guidance

### Secrets handling

- Keep production secrets in `runtime/secrets/`.
- Do not commit `.env`, runtime data, backups, or secret files.
- Do not paste production secrets into tickets, logs, or chat.
- Do not put production `SECRET_KEY` or `BOOTSTRAP_ADMIN_PASSWORD` values
  directly in `.env`.
- Restrict filesystem access to the deployment directory.
- Back up secrets together with the database. A database backup without the
  matching `openbid_secret_key` may not be enough for full recovery of sensitive
  user security values.
- SMTP passwords and app passwords are stored in the OpenBid database through
  the Admin Email page. The UI reports whether a password is stored but never
  displays it after saving.
- Treat SMTP app passwords like production secrets. Rotate them in your email
  provider if they are copied into chat, logs, screenshots, or support tickets.

### Password hygiene

- Change the bootstrap admin password after first login.
- Use named accounts for real users.
- Use long, unique passwords stored in a password manager.
- Keep the bootstrap `admin` account tightly controlled.
- Review user roles and tenant memberships after setup.
- Treat repeated login failures as a security signal.

### Admin account guidance

The first seeded user is a Platform Super Admin. Use it to complete setup, then:

- Create at least one named administrator account.
- Confirm the named administrator can log in.
- Store emergency credentials securely.
- Do not share a single admin password among operators.

### Firewall and exposure notes

Only expose what is required:

- Expose the external HTTPS reverse proxy publicly.
- Keep direct Docker host port `8088` private if possible.
- Do not publish app port `8080`.
- Do not publish extractor port `9090`.
- Do not expose Docker socket or Docker API publicly.
- Do not expose `runtime/data`, `runtime/backups`, or `runtime/secrets` through
  any web server.

### Reverse proxy and HTTPS notes

Production expects:

```env
APP_ENV=production
SECURE_COOKIES=true
```

Use HTTPS for browser access. Configure the external proxy to forward scheme and
host headers correctly. A bad forwarded scheme can cause login, CSRF, or session
problems.

### Safe production defaults

The compose defaults are intentionally conservative:

```env
LOW_MEMORY_MODE=true
ANALYTICS_ENABLED=false
BOOTSTRAP_SYNC_ON_STARTUP=false
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
LOGIN_RATE_LIMIT_WINDOW_SECONDS=600
LOGIN_RATE_LIMIT_MAX_ATTEMPTS=10
```

Keep them until you have measured the host under real load.

### What not to expose publicly without protection

Do not publicly expose:

- `/app/data/store.db`.
- `runtime/backups`.
- `runtime/secrets`.
- Docker socket.
- Internal `app` service.
- Internal `extractor` service.
- SMTP provider credentials or screenshots that reveal provider secrets.
- Any admin UI without HTTPS and strong passwords.

## 15. Maintenance Guidance

### Log review

Review app and worker logs regularly:

```bash
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
```

For outbound email, look for:

- `email send succeeded`, which confirms the SMTP provider accepted a message.
- `email send failed`, which indicates configuration, TLS, auth, network, or
  provider rejection problems.
- Smart Keyword alert delivery rows in the UI that show `failed` or `skipped`.

Do not paste logs into public support channels without checking for hostnames,
email addresses, usernames, and other operational details first. OpenBid does
not log SMTP passwords.

Review logs regularly:

```bash
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

Look for:

- Repeated login failures.
- Source failures.
- Extraction failures.
- Database validation errors.
- Restart loops.
- 5xx responses in proxy logs.

### Disk usage checks

Weekly:

```bash
df -h .
du -sh runtime/data runtime/backups
docker system df
```

Healthy result:

- Disk has enough free space for database growth and new backups.
- Backups are growing according to expectations.
- Docker image/cache usage is understood.

### Image cleanup

After confirming a deployment is healthy, you can remove unused Docker objects:

```bash
docker system prune
```

Read Docker's prompt carefully. This removes unused objects, not active OpenBid
containers or bind-mounted runtime data.

For build cache cleanup:

```bash
docker builder prune
```

Do not run broad destructive cleanup commands unless you understand what Docker
will remove.

### Backup schedule guidance

Recommended minimum:

- Daily database backup.
- Backup before every update.
- Keep at least 7 daily backups.
- Keep at least 4 weekly backups.
- Store copies off the host if this is important production data.

Backup command:

```bash
docker compose exec -T app openbid-sqlite-backup /app/backups/store-$(date +%Y%m%d-%H%M%S).db
```

Also back up:

```text
runtime/secrets/openbid_secret_key
runtime/secrets/openbid_bootstrap_admin_password
```

### Update cadence guidance

Recommended:

- Update deliberately, not automatically, unless you have tested automated
  rollback and backup restore.
- Read changes before updating production.
- Back up first.
- Pin GHCR image tags for predictable production deployments.
- Avoid relying on `latest` when you need repeatable rollbacks.

### Monitoring suggestions

At minimum, monitor:

- `http://localhost:8088/healthz`.
- Container health from `docker compose ps`.
- Disk free space.
- Backup age.
- Worker health.
- Source sync status in the UI.
- Queue backlog in the UI.

The app includes an authenticated `/health` page for platform health details.
Log in as an admin and open:

```text
/health
```

Optional container alert check:

```bash
ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts ../scripts/docker-alert-check.sh
```

For GHCR deployments:

```bash
COMPOSE_FILE=docker-compose.ghcr.yml ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts ../scripts/docker-alert-check.sh
```

Run those commands from `ProductionDeployment/`. The alert helper checks Docker
container states and can post firing/resolved notifications through
`scripts/post-alert.sh`.

### Keep the deployment healthy over time

Monthly checklist:

- Confirm backups exist and at least one backup validates.
- Confirm `/healthz` returns `{"ok":true}`.
- Log in and review `/health`.
- Review `/sources` for failing or stale sources.
- Review queue state.
- Review disk usage.
- Review Docker image/cache usage.
- Confirm the external HTTPS certificate is valid and not near expiry.
- Confirm the admin/user list is still correct.

## 16. Command Reference

Run commands from:

```bash
cd ProductionDeployment
```

For GHCR mode, add:

```bash
-f docker-compose.ghcr.yml
```

to compose commands.

### Start

Source build:

```bash
./setup.sh
docker compose up -d --build
```

Existing source-built images:

```bash
docker compose up -d
```

GHCR:

```bash
./setup.sh
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
```

### Stop

```bash
docker compose down
```

### Restart

```bash
docker compose restart
```

### Rebuild

```bash
docker compose up -d --build
```

### Logs

```bash
docker compose logs --tail=200
docker compose logs -f
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

### Status

```bash
docker compose ps
curl http://localhost:8088/healthz
```

### Email readiness

Email settings are managed in the web UI:

```text
https://openbid.example.com/admin/email
```

Useful log checks:

```bash
docker compose logs --tail=200 app | grep -i 'email send' || true
docker compose logs --tail=200 worker | grep -i 'smart alert\|email send' || true
```

### Pull images

```bash
docker compose -f docker-compose.ghcr.yml pull
```

### Update from source

```bash
docker compose exec -T app openbid-sqlite-backup /app/backups/pre-update-$(date +%Y%m%d-%H%M%S).db
git pull
docker compose up -d --build
docker compose ps
curl http://localhost:8088/healthz
```

### Update from GHCR

```bash
docker compose -f docker-compose.ghcr.yml exec -T app openbid-sqlite-backup /app/backups/pre-update-$(date +%Y%m%d-%H%M%S).db
docker compose -f docker-compose.ghcr.yml pull
docker compose -f docker-compose.ghcr.yml up -d
docker compose -f docker-compose.ghcr.yml ps
curl http://localhost:8088/healthz
```

### Backup

```bash
docker compose exec -T app openbid-sqlite-backup /app/backups/store-$(date +%Y%m%d-%H%M%S).db
ls -lh runtime/backups
```

### Validate current database

```bash
docker compose run --rm --no-deps app openbid-sqlite-check
```

### Restore

```bash
docker compose stop proxy app worker
cp runtime/backups/store-YYYYMMDD-HHMMSS.db runtime/data/store.db
rm -f runtime/data/store.db-wal runtime/data/store.db-shm
docker compose run --rm --no-deps app openbid-sqlite-check
docker compose up -d
docker compose ps
curl http://localhost:8088/healthz
```

### Troubleshooting starter pack

```bash
pwd
ls -la
./setup.sh
docker compose config
docker compose ps
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
curl -i http://localhost:8088/healthz
df -h .
docker system df
```
