# OpenBid Raspberry Pi Docker Deployment

This folder contains a self-contained Raspberry Pi deployment bundle for OpenBid.

It is designed for:

- Raspberry Pi 4 with 4 GB RAM or more
- Raspberry Pi 5 with 4 GB or 8 GB RAM
- Raspberry Pi OS Lite 64-bit
- Docker-based deployment
- low-maintenance, always-on operation
- plain HTTP inside your network, with optional Cloudflare or another reverse proxy handling public HTTPS

This bundle matches the current app architecture:

- `app`: Go web server
- `worker`: Go background worker
- `extractor`: Python document extraction service
- `proxy`: nginx reverse proxy on port `8088`

## Folder contents

- `Dockerfile.app`: production build for the Go app and worker
- `Dockerfile.extractor`: production build for the Python extractor
- `docker-compose.yml`: recommended Raspberry Pi deployment stack
- `.env.example`: environment template for the Pi deployment
- `nginx.raspberry-pi.conf`: nginx config used by the proxy container
- `openbid-compose.service`: optional systemd service for starting the stack at boot

## Supported Raspberry Pi assumptions

Recommended:

- Raspberry Pi 5, 4 GB or 8 GB RAM
- Raspberry Pi 4, 4 GB or 8 GB RAM

Will often work, but not recommended for comfortable production use:

- Raspberry Pi 4, 2 GB RAM

Not recommended for this deployment method:

- 32-bit Raspberry Pi OS
- Raspberry Pi Zero family
- Raspberry Pi 3 for long-running production use

Why:

- OpenBid builds Go binaries locally in Docker
- the extractor uses `pdftotext`
- SQLite, nginx, the Go app, the worker, and the extractor all run together
- a 64-bit OS gives better compatibility and headroom

## OS recommendation

Use:

- Raspberry Pi OS Lite (64-bit)
- current stable release based on Debian Bookworm or newer

Why this is recommended:

- low RAM overhead
- no desktop environment
- best fit for always-on Docker workloads

## Recommended install path

This guide assumes you place the repo here:

```bash
/opt/openbid
```

That means this deployment bundle will live here:

```bash
/opt/openbid/InstallationInstructions
```

The optional systemd service file in this folder is already written for that path.

## Step 1: Prepare the Raspberry Pi

Log in to the Pi directly or over SSH.

Update package indexes and install system updates:

```bash
sudo apt update
sudo apt full-upgrade -y
sudo reboot
```

What this does:

- refreshes package metadata
- installs the latest security and system updates
- reboots into the updated kernel and userspace

Expected result:

- the Pi reboots cleanly
- you can log back in

## Step 2: Install useful base tools

After the reboot, install common tools used throughout this guide:

```bash
sudo apt update
sudo apt install -y git curl ca-certificates openssl nano
```

What this does:

- `git`: lets you clone and update the project
- `curl`: used to download Docker installer and test endpoints
- `ca-certificates`: required for TLS downloads
- `openssl`: used to create a strong secret key
- `nano`: simple text editor for `.env`

Expected result:

- the packages install without errors

## Step 3: Install Docker

Install Docker using the official Docker convenience installer:

```bash
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh
```

Add your user to the Docker group:

```bash
sudo usermod -aG docker "$USER"
newgrp docker
```

Why this matters:

- without the group change, you would need `sudo` for every Docker command

Verify Docker:

```bash
docker version
```

Expected result:

- you see both client and server version output

## Step 4: Confirm Docker Compose support

Check whether the Docker Compose plugin is already available:

```bash
docker compose version
```

If that command works, continue to the next step.

If it does not work, install the Compose plugin:

```bash
sudo apt install -y docker-compose-plugin
docker compose version
```

Expected result:

- `docker compose version` prints a version string

## Step 5: Make Docker start automatically after reboot

Enable Docker itself at boot:

```bash
sudo systemctl enable docker
sudo systemctl start docker
sudo systemctl status docker --no-pager
```

Expected result:

- Docker shows as active and enabled

This matters because even with container restart policies, Docker must itself start after a reboot.

## Step 6: Clone or copy the project onto the Pi

If your project is hosted in Git, clone it:

```bash
sudo mkdir -p /opt
cd /opt
sudo git clone YOUR_REPOSITORY_URL openbid
sudo chown -R "$USER":"$USER" /opt/openbid
```

If you are copying the project manually from another machine instead of using Git:

- copy the full repository to `/opt/openbid`
- make sure the `InstallationInstructions` folder is present inside it

Move into the deployment folder:

```bash
cd /opt/openbid/InstallationInstructions
```

Expected result:

- `pwd` should print `/opt/openbid/InstallationInstructions`

## Step 7: Review the deployment files

You should now see these files:

```bash
ls -la
```

Expected result:

- `docker-compose.yml`
- `.env.example`
- `Dockerfile.app`
- `Dockerfile.extractor`
- `nginx.raspberry-pi.conf`
- `openbid-compose.service`
- `README.md`

## Step 8: Create runtime directories

Create the persistent storage and secrets directories used by Docker Compose:

```bash
mkdir -p runtime/data
mkdir -p runtime/backups
mkdir -p runtime/secrets
```

What these directories are for:

- `runtime/data`: SQLite database and runtime app data
- `runtime/backups`: SQLite backup files
- `runtime/secrets`: secret key and bootstrap admin password files

Verify them:

```bash
find runtime -maxdepth 2 -type d | sort
```

Expected result:

- `runtime`
- `runtime/backups`
- `runtime/data`
- `runtime/secrets`

## Step 9: Create the secret key file

Generate a strong application secret:

```bash
openssl rand -base64 48 > runtime/secrets/openbid_secret_key
chmod 600 runtime/secrets/openbid_secret_key
```

What this does:

- creates a strong random key used to sign sessions and security-sensitive values
- locks the file down so only your user can read it

Verify the file exists:

```bash
ls -l runtime/secrets
```

Expected result:

- you see `openbid_secret_key`

Important:

- do not commit this file to Git
- do not share this file
- do not rotate it casually after users exist, unless you intend to invalidate active sessions

## Step 10: Create the initial admin password file

Create a strong first admin password file:

```bash
nano runtime/secrets/openbid_bootstrap_admin_password
```

Enter one strong password on a single line, save, then lock permissions:

```bash
chmod 600 runtime/secrets/openbid_bootstrap_admin_password
```

What this password is for:

- first startup on an empty production database
- initial admin account bootstrap

Important:

- use a strong unique password
- store it somewhere safe
- after first login, change it inside the app if needed

## Step 11: Create the environment file

Copy the example environment file:

```bash
cp .env.example .env
```

Open it:

```bash
nano .env
```

Recommended starting values:

```env
APP_ENV=production
OPENBID_HTTP_PORT=8088
SECURE_COOKIES=true
LOW_MEMORY_MODE=true
ANALYTICS_ENABLED=false
BOOTSTRAP_TENANT_NAME=KolaboSolutions
BOOTSTRAP_TENANT_SLUG=kolabosolutions
BOOTSTRAP_SYNC_ON_STARTUP=false
TREASURY_FEED_URL=
ALERT_WEBHOOK_URL=
ALERT_EVAL_SECONDS=300
ALERT_BACKUP_MAX_AGE_MINUTES=1560
ALERT_BACKLOG_MAX_JOBS=25
ALERT_BACKLOG_MAX_AGE_MINUTES=60
ALERT_LOGIN_THROTTLE_THRESHOLD=3
ALERT_EXTRACTOR_FAILURE_THRESHOLD=5
WORKER_SYNC_MINUTES=360
WORKER_LOOP_SECONDS=30
LOGIN_RATE_LIMIT_WINDOW_SECONDS=600
LOGIN_RATE_LIMIT_MAX_ATTEMPTS=10
```

What the important settings mean:

- `APP_ENV=production`: enables production-safe behavior
- `OPENBID_HTTP_PORT=8088`: exposes OpenBid on Pi port `8088`
- `SECURE_COOKIES=true`: required when users access the app over HTTPS through Cloudflare or another TLS-terminating proxy
- `LOW_MEMORY_MODE=true`: recommended for Raspberry Pi
- `BOOTSTRAP_TENANT_NAME=KolaboSolutions`: seeds the default workspace and links built-in sources to it
- `BOOTSTRAP_SYNC_ON_STARTUP=false`: avoids expensive first-start ingestion during bootstrap
- `ALERT_WEBHOOK_URL`: optional webhook receiver for operational alerts
- `WORKER_SYNC_MINUTES=360`: source sync cadence
- `WORKER_LOOP_SECONDS=30`: worker polling loop

Cloudflare note:

- this deployment intentionally serves HTTP on the Pi
- if Cloudflare sits in front, let Cloudflare present HTTPS to the user and point it at `http://PI_IP:8088`

## Step 12: Build and start OpenBid

From `/opt/openbid/InstallationInstructions`, run:

```bash
docker compose --env-file .env up --build -d
```

What this does:

- builds the Go app image for the Pi
- builds the Python extractor image
- creates the `app`, `worker`, `extractor`, and `proxy` containers
- starts them in the background

Important for Raspberry Pi:

- the first build can take several minutes
- a Pi 4 with less RAM will be slower
- this is normal

Expected result:

- the command finishes without errors

## Step 13: Verify that the containers are running

Check container status:

```bash
docker compose ps
```

Expected result:

- `app` becomes `healthy`
- `worker` becomes `healthy`
- `extractor` becomes `healthy`
- `proxy` becomes `healthy`

If one service is still starting, wait 30 to 60 seconds and run the command again.

## Step 14: Verify the app health endpoint

Test the proxy health endpoint locally on the Pi:

```bash
curl http://localhost:8088/healthz
```

Expected result:

```json
{"ok":true}
```

If you changed `OPENBID_HTTP_PORT`, use that port instead.

## Step 15: Access the app from another device on your network

Find the Pi's IP address:

```bash
hostname -I
```

Pick the main LAN address from the output, for example:

```text
192.168.1.50
```

From another device on the same network, open:

```text
http://192.168.1.50:8088
```

Expected result:

- the OpenBid login page loads

If you put Cloudflare or another reverse proxy in front later:

- keep the origin on `http://PI_IP:8088`
- let the external proxy provide HTTPS to the browser

## Step 16: First login

Use:

- username: `admin`
- password: the value you placed in `runtime/secrets/openbid_bootstrap_admin_password`

After login:

- confirm the app loads normally
- change the password if desired
- set up MFA if you want it

## Step 17: How persistent storage works

This deployment uses bind mounts:

- `./runtime/data` -> `/app/data`
- `./runtime/backups` -> `/app/backups`
- `./runtime/secrets` -> `/run/secrets`

That means your important files live on the Pi host here:

```bash
/opt/openbid/InstallationInstructions/runtime/data
/opt/openbid/InstallationInstructions/runtime/backups
/opt/openbid/InstallationInstructions/runtime/secrets
```

This is why the app survives container recreation.

## Step 18: How to restart the app

Restart all services:

```bash
docker compose restart
```

Restart only one service:

```bash
docker compose restart app
docker compose restart worker
docker compose restart extractor
docker compose restart proxy
```

Expected result:

- containers stop and come back up
- `docker compose ps` eventually shows healthy services again

## Step 19: How to stop the app

Stop containers but keep them defined:

```bash
docker compose stop
```

Start them again:

```bash
docker compose start
```

Stop and remove the containers while keeping data on disk:

```bash
docker compose down
```

Important:

- `docker compose down` does not delete your bind-mounted `runtime` data
- do not manually delete `runtime/data` unless you really want to erase the database

## Step 20: How to update the app later

Before updating, make a backup:

```bash
timestamp=$(date +%F-%H%M%S)
docker compose exec -T app openbid-sqlite-backup "/app/backups/openbid-$timestamp.db"
ls -lh runtime/backups
```

Then update the repo:

```bash
cd /opt/openbid
git pull
cd /opt/openbid/InstallationInstructions
docker compose --env-file .env up --build -d
```

What this does:

- pulls the latest code
- rebuilds the containers
- restarts the stack with the updated app

After the update:

```bash
docker compose ps
curl http://localhost:8088/healthz
```

## Step 21: How to view logs

View logs from all containers:

```bash
docker compose logs --tail=200
```

Follow logs live:

```bash
docker compose logs -f
```

View one service only:

```bash
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

What to look for:

- `app`: startup errors, DB problems, auth errors
- `worker`: queue processing, source sync, extraction retries
- `extractor`: parsing and fetch errors
- `proxy`: bad upstream routing, 4xx/5xx traffic, rate limiting

## Step 22: How to check container health

Quick health view:

```bash
docker compose ps
```

Detailed health inspection for a single container:

```bash
docker inspect openbid-app-1 --format '{{json .State.Health}}'
docker inspect openbid-worker-1 --format '{{json .State.Health}}'
docker inspect openbid-extractor-1 --format '{{json .State.Health}}'
docker inspect openbid-proxy-1 --format '{{json .State.Health}}'
```

Expected result:

- health status should be `healthy`

You can also inspect active in-app operational alerts from the browser as an admin on `/health`, or from an authenticated admin session with:

```bash
curl http://localhost:8088/health/alerts.json
```

## Step 23: How to confirm ports are exposed correctly

Check which port Docker published:

```bash
docker compose port proxy 80
```

Expected result:

- output similar to `0.0.0.0:8088`

Check locally with `ss`:

```bash
sudo ss -tulpn | grep 8088
```

Expected result:

- you see something listening on the configured HTTP port

## Step 24: How to inspect mounted volumes and bind mounts

Inspect the app container mounts:

```bash
docker inspect openbid-app-1 --format '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}'
```

Expected result:

- the host `runtime` directories map to the expected in-container paths

You can also verify the files directly on disk:

```bash
ls -lah runtime
ls -lah runtime/data
ls -lah runtime/backups
ls -lah runtime/secrets
```

## Step 25: How to back up important data

The critical data is the SQLite database in `runtime/data/store.db`.

Create a backup using the built-in SQLite backup helper:

```bash
timestamp=$(date +%F-%H%M%S)
docker compose exec -T app openbid-sqlite-backup "/app/backups/openbid-$timestamp.db"
```

Then verify:

```bash
ls -lh runtime/backups
```

Recommended backup practice:

- back up before upgrades
- back up before major configuration changes
- keep copies off the Pi as well
- periodically copy `runtime/backups` to another machine or cloud storage
- if `ALERT_WEBHOOK_URL` is configured, backup command failures can be surfaced to your webhook receiver

## Step 26A: Optional alert webhook and container monitoring

If you want OpenBid to push operational alerts to Slack, Teams, ntfy, or another webhook-compatible receiver, set:

```env
ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts
```

The app will then emit alerts for:

- stale or missing backups
- repeated login throttling
- extractor health failures
- accumulated extraction failures
- worker backlog growth

For Docker container health, schedule the host-side check script every 5 minutes:

```bash
cd /opt/openbid
ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts ./scripts/docker-alert-check.sh
```

Example cron entry:

```bash
*/5 * * * * cd /opt/openbid && ALERT_WEBHOOK_URL=https://your-webhook-endpoint.example/alerts ./scripts/docker-alert-check.sh >> /var/log/openbid-container-alerts.log 2>&1
```

## Step 26: How to restore from backup

First stop the stack:

```bash
docker compose down
```

List available backups:

```bash
ls -lh runtime/backups
```

Restore a backup:

```bash
cp runtime/backups/YOUR_BACKUP_FILE.db runtime/data/store.db
```

Start the app again:

```bash
docker compose --env-file .env up -d
```

Verify:

```bash
docker compose ps
curl http://localhost:8088/healthz
```

Important:

- restore only when the stack is down
- always keep the broken database file until you confirm the restore worked

## Step 27: How to change configuration later

Edit the environment file:

```bash
nano .env
```

Common changes:

- change `OPENBID_HTTP_PORT`
- keep `LOW_MEMORY_MODE=true`
- change worker timings
- set or change `TREASURY_FEED_URL`

After editing:

```bash
docker compose --env-file .env up -d
```

If the Dockerfiles or code changed too:

```bash
docker compose --env-file .env up --build -d
```

## Step 28: How to recover after a Raspberry Pi reboot

This Compose file already uses:

- `restart: unless-stopped`

That tells Docker to restart the containers automatically after reboot, as long as Docker itself starts.

To make sure Docker starts at boot:

```bash
sudo systemctl enable docker
```

Then reboot and test:

```bash
sudo reboot
```

After the Pi comes back:

```bash
cd /opt/openbid/InstallationInstructions
docker compose ps
curl http://localhost:8088/healthz
```

## Step 29: Optional systemd service for the Compose stack

If you want systemd to manage the Compose stack explicitly, install the included service file:

```bash
sudo cp /opt/openbid/InstallationInstructions/openbid-compose.service /etc/systemd/system/openbid-compose.service
sudo systemctl daemon-reload
sudo systemctl enable openbid-compose.service
sudo systemctl start openbid-compose.service
sudo systemctl status openbid-compose.service --no-pager
```

Expected result:

- the service is enabled and active

When to use this:

- you want the stack managed as a named Linux service
- you want a simple `systemctl` interface

Common commands:

```bash
sudo systemctl restart openbid-compose.service
sudo systemctl stop openbid-compose.service
sudo systemctl start openbid-compose.service
```

## Troubleshooting

### Problem: `docker compose up --build -d` fails

Check logs and build output carefully:

```bash
docker compose logs --tail=200
```

Also check disk space:

```bash
df -h
docker system df
```

Common causes:

- not enough disk space
- missing Docker permissions
- temporary network issue while pulling base images

### Problem: the app does not open from another device

Check the proxy is listening:

```bash
docker compose port proxy 80
curl http://localhost:8088/healthz
```

Then check the Pi IP address:

```bash
hostname -I
```

Common mistakes:

- using the wrong IP address
- connecting to the app container port instead of the proxy port
- router or firewall blocking the chosen port

### Problem: a container shows `unhealthy`

Inspect logs for that specific service:

```bash
docker compose logs --tail=200 app
docker compose logs --tail=200 worker
docker compose logs --tail=200 extractor
docker compose logs --tail=200 proxy
```

Then inspect the health data:

```bash
docker inspect openbid-app-1 --format '{{json .State.Health}}'
```

### Problem: permission denied on runtime folders

Check ownership:

```bash
ls -lah runtime
ls -lah runtime/secrets
```

Fix it:

```bash
sudo chown -R "$USER":"$USER" runtime
chmod 700 runtime/secrets
chmod 600 runtime/secrets/openbid_secret_key
chmod 600 runtime/secrets/openbid_bootstrap_admin_password
```

### Problem: low-memory issues on Raspberry Pi

Symptoms:

- very slow builds
- containers restarting unexpectedly
- the Pi becomes sluggish

What to do:

- keep `LOW_MEMORY_MODE=true`
- keep `BOOTSTRAP_SYNC_ON_STARTUP=false`
- do not run a desktop environment on the same Pi
- if possible, use a Pi 4 with 4 GB or a Pi 5
- increase `WORKER_LOOP_SECONDS` if you want slightly lighter background activity
- avoid running other heavy containers on the same Pi

Useful checks:

```bash
free -h
docker stats --no-stream
```

### Problem: out of disk space

Check space:

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

- `runtime/data/store.db`
- `runtime/backups`
- `runtime/secrets`

### Problem: the app works locally but not through Cloudflare

Check:

- Cloudflare should point to `http://PI_IP:8088`
- the Pi itself should remain on HTTP
- end users should see HTTPS only at the Cloudflare edge

Also confirm:

- `SECURE_COOKIES=true`
- the proxy container is healthy

## Recommended operating habits

- keep the Pi powered from a stable supply
- store the database on a reliable SSD if possible
- keep backups off-device
- update the app only after taking a backup
- run `docker compose ps` and `curl http://localhost:8088/healthz` after every update

## Quick command reference

Start:

```bash
docker compose --env-file .env up --build -d
```

Stop:

```bash
docker compose stop
```

Restart:

```bash
docker compose restart
```

Status:

```bash
docker compose ps
```

Logs:

```bash
docker compose logs -f
```

Health:

```bash
curl http://localhost:8088/healthz
```

Backup:

```bash
timestamp=$(date +%F-%H%M%S)
docker compose exec -T app openbid-sqlite-backup "/app/backups/openbid-$timestamp.db"
```

Update:

```bash
cd /opt/openbid
git pull
cd /opt/openbid/InstallationInstructions
docker compose --env-file .env up --build -d
```
