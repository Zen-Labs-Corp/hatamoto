# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
#
# One Go-only image: the hatamoto binary serves the site, the admin, the API and
# runs the worker.
#   docker build -t hatamoto .

# ---- Go builder ------------------------------------------------------------
FROM golang:1.27-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS builder
WORKDIR /src/backend
RUN apk add --no-cache git bash
COPY backend/go.mod backend/go.sum ./
COPY backend/pkg/sdk/go.mod backend/pkg/sdk/go.sum ./pkg/sdk/
RUN go mod download
COPY backend/ ./
# LICENSE, NOTICE and THIRD_PARTY_LICENSES (the Go toolchain's licence, Go
# modules, vendored JS, font) for the runtime stage: Apache-2.0 and the
# BSD/MIT notices apply to this binary distribution as well.
COPY LICENSE NOTICE /src/
COPY scripts/third-party-licenses.sh /src/scripts/
RUN /src/scripts/third-party-licenses.sh /out/THIRD_PARTY_LICENSES
ARG VERSION=dev
ARG COMMIT=unknown
# Build tags; empty for every real image. Only the end-to-end test passes one.
ARG TAGS=""
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -tags "${TAGS}" \
    -ldflags "-X hatamoto.dev/core/app.Version=${VERSION} -X hatamoto.dev/core/app.Commit=${COMMIT}" \
    -o /out/hatamoto ./cmd/hatamoto

# ---- Final: hatamoto ---------------------------------------------------------
# One binary: `supervise` runs `serve` as its child, which migrates the
# database, generates and persists its secrets, renders the site and runs the
# worker. The supervisor keeps releases under $HATAMOTO_DATA/releases and rolls
# back a release that fails its health gate. Mount a volume on /data (secrets
# live in /data/secrets). No shell or entrypoint script is needed.
FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8
# pg_dump for backups (Settings > Backups and the backup before each update).
# Bookworm's own client is 15, and pg_dump refuses a newer server, so the client
# comes from the PostgreSQL apt repository (https://www.postgresql.org/download/linux/debian/).
# Its major must match the database server's major: docker-compose.yml runs
# postgres:16. If you run another major, build with --build-arg PG_MAJOR=<n>.
# curl is only needed to fetch the signing key and is removed again.
ARG PG_MAJOR=16
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && curl -fsSL -o /usr/share/keyrings/postgresql-archive-keyring.asc https://www.postgresql.org/media/keys/ACCC4CF8.asc \
    && echo "deb [signed-by=/usr/share/keyrings/postgresql-archive-keyring.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update && apt-get install -y --no-install-recommends "postgresql-client-${PG_MAJOR}" \
    && apt-get purge -y --auto-remove curl \
    && rm -rf /var/lib/apt/lists/* \
    && pg_dump --version \
    && useradd --system --uid 10001 --no-create-home --shell /usr/sbin/nologin app
WORKDIR /app
COPY --from=builder /out/hatamoto /usr/local/bin/hatamoto
COPY --from=builder /src/LICENSE /src/NOTICE /usr/share/doc/hatamoto/
COPY --from=builder /out/THIRD_PARTY_LICENSES /usr/share/doc/hatamoto/THIRD_PARTY_LICENSES
RUN mkdir -p /app/uploads /data/secrets \
    && chown -R app:app /app /data
ENV HATAMOTO_DATA=/data
ENV HATAMOTO_SECRETS_DIR=/data/secrets
VOLUME /data
USER app
EXPOSE 8090
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD ["hatamoto", "healthcheck"]
ENTRYPOINT ["hatamoto"]
CMD ["supervise"]
