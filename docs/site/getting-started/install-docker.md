---
title: Install with Docker
nav_title: Install with Docker
description: Try Hatamoto on your own computer by starting it and its database together with Docker.
next: first-run.md, ../operate/deploy-docker.md
---

In this guide you create three small files and start Hatamoto and its database together with one Docker command. When you finish, your site answers on port 8090 and the server prints a setup code, which you use in the next guide to create your owner account. This setup is for trying Hatamoto on your own computer, not for a public site.

The Dockerfile below downloads the release binary and builds the container on your computer. A signed multi-arch image, `ghcr.io/zen-labs-corp/hatamoto:vX.Y.Z`, is also published for each release, built from the `Dockerfile` in the source tree; you can use it as the `image:` of the `hatamoto` service instead of the `build:` block.

## Before you start

- Docker with Compose version 2. Check with `docker compose version`, which prints `Docker Compose version v2` followed by more numbers.
- A 64-bit computer (x86 or ARM) and an internet connection: Docker downloads Hatamoto during the build.
- Port 8090 free on your computer. See [Before you install](before-you-install.md).

## Steps

### Create the files

1. Make an empty folder and go into it:

   ```bash
   mkdir hatamoto-demo
   cd hatamoto-demo
   ```

2. Find the current version. This command lists each release and whether it was withdrawn because of a problem:

   ```bash
   curl -fsSL https://hatamoto.dev/releases/channels/stable.json | grep -E '"(version|halted|yanked)"'
   ```

   ```text
           "version": "v1.1.0",
           "halted": false
           "version": "v1.3.0",
           "halted": false
           "version": "v1.3.1",
           "halted": false
       "yanked": []
   ```

   Skip a version followed by `"halted": true`. If the last part reads `"yanked": [` rather than `"yanked": []`, the versions after it were withdrawn too: skip them. Then take the highest number left (`v1.3.1` is higher than `v1.3.0`). The list is in release order, not number order, so the last version is not always it.

3. Create a file named `.env` with that version. Docker reads it for every command you run in this folder. Replace `v1.3.1` with the version you found:

   ```text
   VERSION=v1.3.1
   ```

4. Create a file named `Dockerfile`. It describes the **container**, a small, sealed Linux system that Docker runs, here with the Hatamoto program added.

   ```dockerfile
   # syntax=docker/dockerfile:1
   FROM debian:bookworm-slim
   ARG VERSION
   ARG TARGETARCH
   RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
       && curl -fsSL -o /usr/share/keyrings/postgresql-archive-keyring.asc https://www.postgresql.org/media/keys/ACCC4CF8.asc \
       && echo "deb [signed-by=/usr/share/keyrings/postgresql-archive-keyring.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" > /etc/apt/sources.list.d/pgdg.list \
       && apt-get update && apt-get install -y --no-install-recommends postgresql-client-16 \
       && apt-get purge -y --auto-remove curl \
       && rm -rf /var/lib/apt/lists/* \
       && useradd --system --uid 10001 --no-create-home --shell /usr/sbin/nologin app
   ADD --chmod=755 https://github.com/Zen-Labs-Corp/hatamoto/releases/download/${VERSION}/hatamoto-linux-${TARGETARCH} /usr/local/bin/hatamoto
   WORKDIR /app
   RUN mkdir -p /app/uploads /data/secrets && chown -R app:app /app /data
   ENV HATAMOTO_DATA=/data HATAMOTO_SECRETS_DIR=/data/secrets
   VOLUME /data
   USER app
   EXPOSE 8090
   HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
       CMD ["hatamoto", "healthcheck"]
   ENTRYPOINT ["hatamoto"]
   CMD ["supervise"]
   ```

   Besides Hatamoto, the container gets `pg_dump` 16, the tool that copies the database for a backup. Hatamoto takes a backup before every update and stops the update when it cannot take one, and the tool must have the same version as the database, which is 16 in the next file.

5. Create a file named `docker-compose.yml`. A **compose file** tells Docker which containers to start together: here, the Postgres database (`db`) and Hatamoto (`hatamoto`). **Postgres** is the database Hatamoto stores everything in. The `volumes` lines create **Docker volumes**, storage that survives when a container is replaced, so your data is kept.

   ```yaml
   services:
     db:
       image: postgres:16
       environment:
         POSTGRES_USER: hatamoto
         POSTGRES_PASSWORD: hatamoto
         POSTGRES_DB: hatamoto
       volumes:
         - pgdata:/var/lib/postgresql/data
       healthcheck:
         test: ["CMD-SHELL", "pg_isready -U hatamoto"]
         interval: 5s
         timeout: 5s
         retries: 10

     hatamoto:
       build:
         context: .
         args:
           VERSION: ${VERSION:?set VERSION to a release such as vX.Y.Z}
       depends_on:
         db:
           condition: service_healthy
       environment:
         ENVIRONMENT: development
         DATABASE_URL: postgres://hatamoto:hatamoto@db:5432/hatamoto?sslmode=disable
         API_HOST: 0.0.0.0
         API_PORT: 8090
         CSP_ENFORCE: "${CSP_ENFORCE:-}"
       restart: unless-stopped
       stop_grace_period: 45s
       volumes:
         - data:/data
         - secrets:/data/secrets
         - uploads:/app/uploads
       ports:
         - "8090:8090"

   volumes:
     pgdata:
     secrets:
     uploads:
     data:
   ```

   The database publishes no port on your computer, so a Postgres you already run there does not get in the way. Only Hatamoto reaches it, inside Docker; to open it yourself, run `docker compose exec db psql -U hatamoto`.

   A new install makes browsers block scripts from other sites, such as tag managers; the empty `CSP_ENFORCE` leaves that default alone. [Hardening](../operate/hardening.md) explains how to check your pages and what changes.

### Start Hatamoto

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`.

1. Build and start both containers:

   ```bash
   docker compose up -d
   ```

   The first run downloads Hatamoto and builds the container, in about a minute. Docker picks the right file for your processor by itself. Hatamoto waits until the database is ready, then starts on port 8090.

2. Find the setup code. The **setup code** is a one-time code the server prints to its log so only someone with access to the server can create the first owner. Read it from the log:

   ```bash
   docker compose logs hatamoto | grep -i setup
   ```

   You see a line that ends like this:

   ```text
   msg="Setup: open /k7qm-x2rf-9tdc/setup and enter code K7M2-RT9P" setup_code=K7M2-RT9P
   ```

   To choose the code yourself, add `HATAMOTO_SETUP_CODE` with at least 12 characters under `environment:` in the `hatamoto` service, then run `docker compose up -d` again. The log then reminds you to use that setting instead of printing a code.

### Stop and restart

1. Run these from the same folder:

   ```bash
   # follow the log, Ctrl+C to stop following
   docker compose logs -f hatamoto
   # restart Hatamoto; data, secret and sign-ins are kept
   docker compose restart hatamoto
   # stop everything and keep all data
   docker compose down
   # stop everything and DELETE all data, the database too
   docker compose down -v
   ```

   To run any other Hatamoto command, put it after `docker compose exec hatamoto`, for example `docker compose exec hatamoto hatamoto version`.

## Check it worked

Open `http://localhost:8090/` in a browser to see the starter home page, and open the setup address from the log line to reach the setup wizard. `hatamoto admin-url` prints your admin address if you need it again. Continue with [First run](first-run.md).

Open the admin on the computer that runs Docker, through `localhost`. From another device on your network, for example at `http://192.168.1.20:8090`, the public pages load but every admin page, the setup wizard included, answers with the site's not-found page. Browsers send the request headers the admin checks (`Sec-Fetch-*`) only over HTTPS or to `localhost`, and the admin refuses requests without them; the log says `admin request without fetch metadata refused`. To use the admin from another device, serve it over HTTPS, as in [Without a domain](../operate/reverse-proxy-tls.md#without-a-domain).

`docker compose ps` shows the `hatamoto` container as `healthy` within about a minute of starting.

If `docker compose up` stops with `set VERSION to a release such as vX.Y.Z`, the `.env` file is missing or is not in this folder. If the build fails while downloading, check that the version in `.env` exists and starts with `v`.

This setup is for trying Hatamoto. It runs in development mode, uses a well-known database password, serves plain HTTP without your domain name, sends no email until set up in **Settings > Email**, and relies on a generated secret. Before you put a site on the internet, follow [Deploy with Docker](../operate/deploy-docker.md), which covers the volumes and what to change for production.
