# OpenBid

OpenBid is a lightweight, self-hosted, multi-tenant tender aggregation platform for engineering-focused South African opportunities.

## What is implemented

- Go web app with standard-library server rendering
- HTMX-style server-side workflow with reusable template partials
- SQLite runtime store at `./data/store.db`
- Go worker for sync and extraction queue processing
- Python extractor service for document text extraction
- Multi-tenant workflow, bookmarks, saved searches, audit log, queue views, and tender detail pages
- Docker Compose deployment
- Unit and integration-style tests for the current runtime path

## Default credentials

- Username: `admin`
- Password: `TenderHub!2026`

## Local run

```bash
cp .env.example .env
go mod tidy
go test ./...
go run ./cmd/server
```

Open `http://localhost:8080`.

## Docker run

```bash
cp .env.example .env
docker compose up --build
```

Open `http://localhost:8088`.

## Runtime notes

- Default local database path: `./data/store.db`
- Docker database path: `/app/data/store.db`
- The app and worker both expect SQLite and no longer support the old JSON runtime store.

## Validation

```bash
go mod tidy
go test ./...
docker compose up --build
```

## Useful files

- `cmd/server`
- `cmd/worker`
- `cmd/sqlite_check`
- `internal/app`
- `internal/store`
- `web/templates`
- `scripts/run-local.sh`
- `scripts/sqlite-validate.sh`
