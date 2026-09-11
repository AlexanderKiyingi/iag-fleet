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
# Pin a commit SHA (not just "main") so standalone builds do not reuse a stale
# Docker layer from before iag-telemetry-gateway API changes.
#
# Bump this whenever fleet depends on new fleet-iot BEHAVIOUR, not just new
# symbols — a stale pin compiles fine and fails silently. This pin sat on
# 8dce711 while fleet shipped migration 0031 + the `lastFixSource` field on the
# live-map payload; the column is written by SyncVehicleFromPing inside
# fleet-iot, so the API served an empty string in production until the bump to
# 61b50de. Ingest authz hardening (6e6292b) was likewise undeployed.
#
# Bumped to c3a18db: migration 0043 made iot_devices.vehicle_id a uuid, and
# fleet-iot read it as COALESCE(vehicle_id, ''), which Postgres refuses to
# type-check. Every device read 500'd. The fix lives in fleet-iot, so leaving
# this pin behind would have kept the outage with a green fleet build.
#
# Bumped to d6178d1: CreateDeviceInput and UpdateDeviceInput gained Model and
# the fuel sensor mapping (FuelIOID/FuelScale/FuelOffset) so the device API can
# finally write iot_devices.model and configure analog / LLS fuel sensors. This
# one is new SYMBOLS rather than new behaviour, so it fails loudly at compile
# time instead of silently at runtime — the monorepo target COPYs
# edge/Fleet_IoT and so builds green against the working tree, which is exactly
# how a stale pin gets missed locally. If you changed fleet and fleet-iot
# together, bump this in the same commit.
# Bumped to 2d28bf1: the same 0043 fallout as c3a18db above, on the WRITE side
# this time. iot_devices.vehicle_id and device_commands.vehicle_id are uuid, and
# fleet-iot bound them with NULLIF($n, '') — comparing against a text literal
# pins the parameter to text, so the statement was assigning text to a uuid
# column and Postgres refused it outright:
#
#   column "vehicle_id" is of type uuid but expression is of type text
#   (SQLSTATE 42804)
#
# Registering a device, editing one, and queuing an immobilise all failed. No
# input value helped — the statement could not run. The fix lives entirely in
# fleet-iot, so leaving this pin behind would keep every device registration
# broken with a green fleet build, exactly as the c3a18db note warns.
# Bumped to 758bc4b: the HQ decoder now keeps the trailing fields it used to
# parse past — battery level and the serving cell (mcc/mnc/lac/cellId) — and
# carries them into telemetry_timeseries.raw. Device monitoring had nothing to
# read before this: the platform could say where a tracker was and nothing about
# the tracker itself.
#
# Behaviour, not symbols, so a stale pin would compile perfectly and simply keep
# discarding the data — exactly the failure mode the 61b50de note above warns
# about.
# Bumped to 2a88e94: fleet-iot now pins its own search_path and refuses to
# start if its pings table resolves to an unexpected schema.
#
# This is the fix for a silent outage. The services share one database and
# separate by schema; fleet-iot took its schema from a ?search_path= DSN param,
# and with that param missing it wrote pings to public.telemetry_timeseries
# while this service read iag_fleet.telemetry_timeseries. The table exists in
# both, so every insert succeeded, every read returned an empty array, and a
# vehicle reporting every twenty seconds had no history at all.
#
# Behaviour again, not symbols — a stale pin here compiles and keeps writing to
# the wrong schema.
# Bumped to aec4be8: the schema pin now honours a search_path named in the DSN,
# and the boot assertion treats a search_path FALLBACK as legitimate rather than
# fatal.
#
# 2a88e94 would have refused to start the gateway here. telemetry_timeseries
# lives in public on the deployed database — a scan found 455 rows there and
# none in iag_fleet — and the assertion demanded the leading schema. Fleet's
# tables are part-way through a move out of public, so resolving through the
# "iag_fleet, public" fallback is exactly what keeps this working.
#
# Still fatal: a table unreachable on this connection while existing in another
# schema. That is the original bug and it is distinguishable, because the name
# does not resolve at all.
ARG FLEET_IOT_REF=aec4be8
ARG FLEET_IOT_REPO=https://github.com/AlexanderKiyingi/iag-telemetry-gateway.git
RUN git clone --filter=blob:none --no-checkout "${FLEET_IOT_REPO}" "${FLEET_IOT_DEP}" \
    && cd "${FLEET_IOT_DEP}" \
    && git checkout "${FLEET_IOT_REF}"

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
    for cmd in . ./cmd/migrate ./cmd/seed ./cmd/fleet-jobs ./cmd/telemetry-aggregate ./cmd/telemetry-purge ./cmd/healthcheck; do \
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
    for cmd in . ./cmd/migrate ./cmd/seed ./cmd/fleet-jobs ./cmd/telemetry-aggregate ./cmd/telemetry-purge ./cmd/healthcheck; do \
        name=$(basename $cmd); [ "$name" = "." ] && name=api; \
        CGO_ENABLED=0 GOOS=linux go build -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

FROM gcr.io/distroless/static-debian12:nonroot AS monorepo
WORKDIR /app
COPY --from=build-monorepo /out/ /app/
# AUTO_MIGRATE=true is the image default because it is what an unconfigured
# deploy needs to come up with a current schema. It is INCOMPATIBLE with
# ENVIRONMENT=production, which fails validation unless AUTO_MIGRATE=false — set
# both together, out of band, per docs/RAILWAY.md. Do not flip this default to
# false on its own: that turns a loud boot failure into silent schema drift.
# GIN_MODE=release also marks this as a deployed runtime, which is what makes
# config.HardenedRuntime enforce fail-closed RBAC even with ENVIRONMENT unset.
ENV PORT=4008 AUTO_MIGRATE=true LOG_FORMAT=json AUTH_MODE=gateway GIN_MODE=release
EXPOSE 4008
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/api"]

FROM gcr.io/distroless/static-debian12:nonroot AS standalone
WORKDIR /app
COPY --from=build-standalone /out/ /app/
# AUTO_MIGRATE=true is the image default because it is what an unconfigured
# deploy needs to come up with a current schema. It is INCOMPATIBLE with
# ENVIRONMENT=production, which fails validation unless AUTO_MIGRATE=false — set
# both together, out of band, per docs/RAILWAY.md. Do not flip this default to
# false on its own: that turns a loud boot failure into silent schema drift.
# GIN_MODE=release also marks this as a deployed runtime, which is what makes
# config.HardenedRuntime enforce fail-closed RBAC even with ENVIRONMENT unset.
ENV PORT=4008 AUTO_MIGRATE=true LOG_FORMAT=json AUTH_MODE=gateway GIN_MODE=release
EXPOSE 4008
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/api"]
