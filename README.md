# OpenBid

OpenBid is a lightweight, self-hosted, multi-tenant tender aggregation platform for engineering-focused South African opportunities.

The repository root is for application source code and local development. Production deployment assets live under `ProductionDeployment/`.

## What Runs

OpenBid runs four services in Docker Compose:

- `app`: Go web application
- `worker`: source sync, queue processing, and extraction orchestration
- `extractor`: Python document text extraction service
- `proxy`: nginx reverse proxy exposed on port `8088` by default

## Local Development

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

For the full Docker stack from source:

```bash
cd ProductionDeployment
./setup.sh
APP_ENV=development SECURE_COOKIES=false docker compose up --build
```

Open:

```text
http://localhost:8088
```

The Docker stack reads its first admin password from `ProductionDeployment/runtime/secrets/openbid_bootstrap_admin_password`.

## Development Credentials

For local development only:

- Username: `admin`
- Password: `OpenBid!2026-YK4j3z39CEfu0kbFHcEzM8yI`

These are seeded by `go run ./cmd/server` only when `APP_ENV=development` and no bootstrap password file is configured. Docker and production installs use secret files created by `ProductionDeployment/setup.sh`.

## Production Deployment

All deployment files are located in `ProductionDeployment/`.

Start here:
- `ProductionDeployment/README.md`
- `ProductionDeployment/setup.sh`

⚠️ NOTE: InstallationInstructions has been renamed to ProductionDeployment.
Runtime data is now stored inside ProductionDeployment/runtime/ and is created automatically.

## Documentation

- Authorization model: [docs/authorization-model.md](docs/authorization-model.md)
- Production deployment guide: [ProductionDeployment/README.md](ProductionDeployment/README.md)
- Production operations: [ProductionDeployment/docs/production-operations.md](ProductionDeployment/docs/production-operations.md)
- Raspberry Pi Docker setup: [ProductionDeployment/docs/raspberry-pi-docker-setup.md](ProductionDeployment/docs/raspberry-pi-docker-setup.md)
