# syntax=docker/dockerfile:1.7

# ─── Build stage ────────────────────────────────────────────────────────────
# golang:alpine keeps the toolchain tight; we only emit static binaries
# (CGO_ENABLED=0) so the final image can be distroless/static.
FROM golang:1.25-alpine AS build

WORKDIR /src

# Cache mounts intentionally omitted. Railway's Metal builder requires
# every cache mount id to be prefixed with the per-service cacheKey
# (`id=s/<railway-service-id>-…`) — env vars aren't allowed in mount
# flags, so the id has to be hardcoded. Until that prefix is wired in,
# we run without --mount=type=cache; cold builds redownload Go modules
# and recompile from scratch (adds ~30–60s) but builds reliably across
# local Docker, Railway, Depot, etc. To restore caching, replace
# `id=haula-gomod` / `id=haula-gobuild` below with your service-specific
# values, e.g. `id=s/abc123-haula-gomod`.
# Local replace for iag-authclient (pkg/authclient) must exist before go mod download.
COPY go.mod go.sum ./
COPY pkg/authclient ./pkg/authclient
RUN go mod download

COPY . .

# Build all four binaries in one pass. -trimpath strips local paths from
# debug info; -ldflags="-s -w" removes the symbol table and DWARF for a
# leaner image. Each cmd is a separate `go build` so every binary is its
# own self-contained artifact in /out.
ARG VERSION=dev
RUN set -eu; \
    mkdir -p /out; \
    for cmd in . ./cmd/seed ./cmd/iot-gateway ./cmd/fleet-jobs ./cmd/telemetry-aggregate ./cmd/telemetry-purge ./cmd/healthcheck; do \
        name=$(basename $cmd); [ "$name" = "." ] && name=api; \
        CGO_ENABLED=0 GOOS=linux go build \
            -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

# ─── Runtime stage ──────────────────────────────────────────────────────────
# distroless/static is minimal: glibc-free, no shell, no package manager.
# Includes ca-certificates so SMTP-over-TLS and outbound HTTPS work without
# a separate stage. Run as the nonroot uid by default.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/ /app/

ENV PORT=4008 \
    AUTO_MIGRATE=true \
    LOG_FORMAT=json \
    AUTH_MODE=gateway \
    GIN_MODE=release

EXPOSE 4008
EXPOSE 5027

HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
  CMD ["/app/healthcheck"]

# The default entrypoint is the API server. For other binaries:
#   docker run --entrypoint /app/seed              haula-fleet --reset
#   docker run --entrypoint /app/iot-gateway       haula-fleet
#   docker run --entrypoint /app/fleet-jobs            haula-fleet --all
#   docker run --entrypoint /app/telemetry-purge       haula-fleet --days 365
#   docker run --entrypoint /app/telemetry-aggregate   haula-fleet
ENTRYPOINT ["/app/api"]
