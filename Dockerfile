# syntax=docker/dockerfile:1.7
#
# Targets:
#   standalone (default) — iag-fleet repo root on Railway; clones Fleet_IoT at build
#   monorepo             — IAG_multi_backend root context (deploy/docker-compose)
#
# Monorepo:  docker build -f services/operations/fleet/Dockerfile --target monorepo .
# Standalone: docker build -f Dockerfile --target standalone .   (iag-fleet repo root)

FROM golang:1.25-alpine AS base
RUN apk add --no-cache git
ENV FLEET_IOT_DEP=/deps/fleet-iot \
    PLATFORM_GO_DEP=/deps/platform-go

FROM base AS fleet-iot-clone
ARG FLEET_IOT_REF=main
ARG FLEET_IOT_REPO=https://github.com/AlexanderKiyingi/iag-telemetry-gateway.git
RUN git clone --depth 1 --branch "${FLEET_IOT_REF}" "${FLEET_IOT_REPO}" "${FLEET_IOT_DEP}"

FROM base AS fleet-iot-copy
COPY edge/Fleet_IoT ${FLEET_IOT_DEP}

FROM base AS platform-go-copy
COPY shared/platform-go ${PLATFORM_GO_DEP}

# ─── Standalone iag-fleet (repo root = service root) ───────────────────────
FROM base AS build-standalone
# Standalone (iag-fleet repo root): the meta-repo is private, so Railway
# can't clone it at build time. The standalone repo carries a committed
# snapshot at third_party/platform-go (refreshed via
# scripts/sync-platform-go.sh). fleet-iot is still cloned from the
# (public) iag-telemetry-gateway repo above.
COPY --from=fleet-iot-clone ${FLEET_IOT_DEP} ${FLEET_IOT_DEP}
WORKDIR /src
COPY third_party/platform-go ${PLATFORM_GO_DEP}
COPY go.mod go.sum ./
RUN go mod edit \
        -replace=github.com/iag/fleet-iot=${FLEET_IOT_DEP} \
        -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP} \
    && go mod download
COPY . .
ARG VERSION=dev
# `COPY . .` restored go.mod from the build context, which still carries the
# meta-repo-only replaces (`../../../edge/Fleet_IoT` and
# `../../../shared/platform-go`). Neither path exists inside the build
# container, so re-apply the vendored replaces before invoking `go build`.
RUN set -eu; \
    go mod edit \
        -replace=github.com/iag/fleet-iot=${FLEET_IOT_DEP} \
        -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP}; \
    mkdir -p /out; \
    for cmd in . ./cmd/seed ./cmd/fleet-jobs ./cmd/telemetry-aggregate ./cmd/telemetry-purge ./cmd/healthcheck; do \
        name=$(basename $cmd); [ "$name" = "." ] && name=api; \
        CGO_ENABLED=0 GOOS=linux go build -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

# ─── Monorepo (context = repo root) ────────────────────────────────────────
FROM base AS build-monorepo
COPY --from=fleet-iot-copy ${FLEET_IOT_DEP} ${FLEET_IOT_DEP}
COPY --from=platform-go-copy ${PLATFORM_GO_DEP} ${PLATFORM_GO_DEP}
WORKDIR /src/services/operations/fleet
COPY services/operations/fleet/go.mod services/operations/fleet/go.sum ./
RUN go mod edit \
        -replace=github.com/iag/fleet-iot=${FLEET_IOT_DEP} \
        -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP} \
    && go mod download
COPY services/operations/fleet/ .
ARG VERSION=dev
RUN set -eu; \
    go mod edit \
        -replace=github.com/iag/fleet-iot=${FLEET_IOT_DEP} \
        -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP}; \
    mkdir -p /out; \
    for cmd in . ./cmd/seed ./cmd/fleet-jobs ./cmd/telemetry-aggregate ./cmd/telemetry-purge ./cmd/healthcheck; do \
        name=$(basename $cmd); [ "$name" = "." ] && name=api; \
        CGO_ENABLED=0 GOOS=linux go build -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

FROM gcr.io/distroless/static-debian12:nonroot AS monorepo
WORKDIR /app
COPY --from=build-monorepo /out/ /app/
ENV PORT=4008 AUTO_MIGRATE=true LOG_FORMAT=json AUTH_MODE=gateway GIN_MODE=release
EXPOSE 4008
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/api"]

FROM gcr.io/distroless/static-debian12:nonroot AS standalone
WORKDIR /app
COPY --from=build-standalone /out/ /app/
ENV PORT=4008 AUTO_MIGRATE=true LOG_FORMAT=json AUTH_MODE=gateway GIN_MODE=release
EXPOSE 4008
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/api"]
