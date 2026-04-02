FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tenderhub-server ./cmd/server &&     CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tenderhub-worker ./cmd/worker
FROM alpine:3.20
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=build /out/tenderhub-server /usr/local/bin/tenderhub-server
COPY --from=build /out/tenderhub-worker /usr/local/bin/tenderhub-worker
COPY web ./web
RUN mkdir -p /app/data && chown -R app:app /app
USER app
EXPOSE 8080
CMD ["tenderhub-server"]
