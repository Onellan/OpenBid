FROM golang:1.23-alpine AS build
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
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tenderhub-server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tenderhub-worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tenderhub-sqlite-check ./cmd/sqlite_check && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tenderhub-sqlite-backup ./cmd/sqlite_backup && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tenderhub-worker-health ./cmd/worker_health
FROM alpine:3.20
ARG VERSION=dev
ARG VCS_REF=local
ARG BUILD_DATE=unknown
RUN apk add --no-cache ca-certificates
RUN addgroup -S app && adduser -S app -G app
LABEL org.opencontainers.image.title="OpenBid App" \
      org.opencontainers.image.description="OpenBid application and worker runtime" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}"
WORKDIR /app
COPY --from=build /out/tenderhub-server /usr/local/bin/tenderhub-server
COPY --from=build /out/tenderhub-worker /usr/local/bin/tenderhub-worker
COPY --from=build /out/tenderhub-sqlite-check /usr/local/bin/tenderhub-sqlite-check
COPY --from=build /out/tenderhub-sqlite-backup /usr/local/bin/tenderhub-sqlite-backup
COPY --from=build /out/tenderhub-worker-health /usr/local/bin/tenderhub-worker-health
COPY web ./web
RUN mkdir -p /app/data && chown -R app:app /app
USER app
EXPOSE 8080
CMD ["tenderhub-server"]
