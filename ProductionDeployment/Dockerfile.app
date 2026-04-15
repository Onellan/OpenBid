FROM golang:1.26.2-alpine AS build
ARG VERSION=dev
ARG VCS_REF=local
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openbid-server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openbid-worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openbid-sqlite-check ./cmd/sqlite_check && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openbid-sqlite-backup ./cmd/sqlite_backup && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openbid-worker-health ./cmd/worker_health
FROM alpine:3.20
ARG VERSION=dev
ARG VCS_REF=local
ARG BUILD_DATE=unknown
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates
RUN addgroup -S app && adduser -S app -G app
LABEL org.opencontainers.image.title="OpenBid App" \
      org.opencontainers.image.description="OpenBid application and worker runtime" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}"
WORKDIR /app
COPY --from=build /out/openbid-server /usr/local/bin/openbid-server
COPY --from=build /out/openbid-worker /usr/local/bin/openbid-worker
COPY --from=build /out/openbid-sqlite-check /usr/local/bin/openbid-sqlite-check
COPY --from=build /out/openbid-sqlite-backup /usr/local/bin/openbid-sqlite-backup
COPY --from=build /out/openbid-worker-health /usr/local/bin/openbid-worker-health
COPY internal/seeddata ./internal/seeddata
COPY web ./web
RUN mkdir -p /app/data && chown -R app:app /app
USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s --retries=3 CMD wget -qO- "http://127.0.0.1:${PORT:-8080}/healthz" || exit 1
CMD ["openbid-server"]
