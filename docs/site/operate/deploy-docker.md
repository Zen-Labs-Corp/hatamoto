---
title: Deploy with Docker
nav_title: Deploy with Docker
description: Turn the Docker demo stack into a production setup with a real database password, a secret, your domain and only the proxy able to reach it.
next: reverse-proxy-tls.md, hardening.md, configuration-reference.md
---

In this guide you make the Docker stack from the getting-started guide fit for production: a real database password, a fixed secret, your public address and a proxy-only port. You add an override file.

## Before you start

- Docker with Compose 2.24.4 or later (`docker compose version`).
- The `Dockerfile`, `.env` and `docker-compose.yml` from [Install with Docker](../getting-started/install-docker.md), in one directory. `VERSION` in `.env` selects the release that is downloaded and is what `hatamoto version` prints. It must have the form `vX.Y.Z`, with the leading `v`; find the current one as described in [Install the binary](../getting-started/install-binary.md).
- A domain name. You add HTTPS next, in [Put a reverse proxy in front](reverse-proxy-tls.md).
- SMTP credentials, if the site sends email. Enter them at Settings > Email, or as `SMTP_*` variables in the override file below. See [Set up email](email.md).

## Steps

### Know what you are running

1. Read what the image sets. It is `debian:bookworm-slim` with CA certificates and the `hatamoto` program added, and the source tree's own `Dockerfile` builds the same image from source. The command is `supervise`: the **supervisor** is the process that starts the server, watches it and switches to a new release after an update. It runs as PID 1 with no shell or entrypoint script, so if it exits, Docker restarts the container.

   | Item | Value |
   |---|---|
   | Program | `/usr/local/bin/hatamoto` |
   | Entry point and command | `ENTRYPOINT ["hatamoto"]`, `CMD ["supervise"]` |
   | User | system user `app`, uid `10001`, no shell, no home, so the process never runs as root |
   | Working directory | `/app` |
   | Data directory | `HATAMOTO_DATA=/data`, a declared `VOLUME` |
   | Secrets directory | `HATAMOTO_SECRETS_DIR=/data/secrets` |
   | Uploads | `/app/uploads` (the default `MEDIA_UPLOAD_DIR` is `./uploads`, relative to `/app`) |
   | Port | `EXPOSE 8090` |
   | Health check | `hatamoto healthcheck` every 30 seconds, 5 second timeout, 30 second start period, 3 retries |

2. Know the four named volumes, and back them all up (see [Back up and restore](backups.md)). The `secrets` volume sits inside `/data` so the secret survives if `data` is recreated. Do not remove it.

   | Volume | Mounted at | Holds |
   |---|---|---|
   | `pgdata` | `/var/lib/postgresql/data` (in `db`) | The Postgres data files. |
   | `data` | `/data` | Releases, update state and backups, compiled plugin cache. |
   | `secrets` | `/data/secrets` | `secrets.env` with the generated server secret. |
   | `uploads` | `/app/uploads` | Uploaded media files. |

### Make it production

1. Put real values in `.env` next to the compose file, beside `VERSION`, and keep the file out of version control. `HATAMOTO_SECRET` is the **server secret**, a long random value the server uses to sign links and protect stored credentials; it must stay the same across restarts. Avoid `@`, `:`, `/`, `?` and `#` in the database password, because it is pasted into a URL:

   ```text
   VERSION=vX.Y.Z
   DB_PASSWORD=<database-password>
   HATAMOTO_SECRET=<at-least-32-random-characters>
   ```

2. Create `production.yml` beside `docker-compose.yml`. Compose joins `ports` lists from several files: `!override` replaces the base list, so Hatamoto listens on loopback only, for the proxy, and `!reset []` empties it, so the database has no published port. `ENVIRONMENT: production` turns on JSON logs and the HSTS header, so only use it behind HTTPS.

   ```yaml
   services:
     db:
       environment:
         POSTGRES_PASSWORD: ${DB_PASSWORD}
       ports: !reset []
     hatamoto:
       environment:
         ENVIRONMENT: production
         DATABASE_URL: postgres://hatamoto:${DB_PASSWORD}@db:5432/hatamoto?sslmode=disable
         PUBLIC_BASE_URL: https://example.com
         HATAMOTO_SECRET: ${HATAMOTO_SECRET}
         SMTP_HOST: smtp.example.com
         SMTP_FROM: noreply@example.com
         HATAMOTO_TRUSTED_PROXIES: 172.18.0.1
       ports: !override
         - "127.0.0.1:8090:8090"
   ```

3. If you already ran the demo, change the database password in place, because `POSTGRES_PASSWORD` only takes effect when `pgdata` is created. Or start from an empty volume with `docker compose down -v`, which deletes the demo data.

   ```bash
   docker compose exec db psql -U hatamoto -c "ALTER ROLE hatamoto PASSWORD '<database-password>'"
   ```

   `HATAMOTO_TRUSTED_PROXIES` must be the address your proxy connects from. A host Caddy or nginx reaching the published port arrives from the bridge gateway, not loopback, so without it every visitor looks like one client and `X-Forwarded-*` is ignored (the log shows a one-time warning). Trust the gateway only while the port is published on `127.0.0.1`, as `production.yml` above does. If the port is open on every interface (`"8090:8090"`, as in the getting-started file), remote clients also arrive from the gateway and can forge `X-Forwarded-For`. Read the gateway with `docker network inspect <project>_default --format '{{(index .IPAM.Config 0).Gateway}}'`, and use the proxy container's address instead if the proxy is a Compose service. See [Reverse proxy and TLS](reverse-proxy-tls.md).

4. Check the merged configuration. `hatamoto` must list only `host_ip: 127.0.0.1`, and `db` must have no `ports` at all:

   ```bash
   docker compose -f docker-compose.yml -f production.yml config
   ```

5. Start it. Pass both files to every later command (`exec`, `logs`, `down`); Compose reads `.env` each time. Keep `stop_grace_period` at 45 seconds or more: the supervisor stops the server within 40 seconds.

   ```bash
   docker compose -f docker-compose.yml -f production.yml up -d
   ```

6. Leave `CSP_ENFORCE: "false"` from the base file for now, so pages send a report-only policy. Set it to `"true"` in `production.yml` once your pages show no violations.

### Run commands and update

1. Run operator commands inside the container. They run as `app`, so a bind-mounted `/data` must be owned by uid `10001`:

   ```bash
   docker compose -f docker-compose.yml -f production.yml exec hatamoto hatamoto migrate status
   ```

2. To update, let the supervisor install signed releases into the `data` volume, or set a newer `VERSION` and run `up -d --build`. `HATAMOTO_UPDATES` decides the policy; see [How updates work](../updates/index.md). An image built from a source checkout takes `--build-arg VERSION=vX.Y.Z --build-arg COMMIT=$(git rev-parse HEAD)`; without a proper `vX.Y.Z` it counts as a development build and never updates itself.

## Check it worked

On the server, ask the readiness endpoint, which also checks the database:

```bash
curl -s http://127.0.0.1:8090/ready
```

```text
ok
```

`docker compose ps` shows the `hatamoto` service as `healthy` after the start period.

If the log says `password authentication failed`, the role still has the demo password: change it as in step 3 of [Make it production](#make-it-production), then start again. If the log says `HATAMOTO_SECRET must be at least 32 characters`, make the value in `.env` longer.
