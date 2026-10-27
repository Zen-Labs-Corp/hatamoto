---
title: Deploy with systemd
nav_title: Deploy with systemd
description: Run Hatamoto as a systemd service on a Linux server, with its own user, an environment file and the unit the project ships.
next: reverse-proxy-tls.md, backups.md, cli-reference.md
---

In this guide you install the `hatamoto` program under `/opt/hatamoto`, give it a service user and an environment file, and start it with the systemd unit the repository ships, `deploy/systemd/hatamoto-api.service`. At the end the service starts at boot, restarts when it stops and answers on port 8090.

## Before you start

- A Linux server with systemd and a shell with `sudo`.
- The `hatamoto` program for your platform in the current directory. See [Install the binary](../getting-started/install-binary.md).
- A Postgres database and its connection string, the `DATABASE_URL`.
- The unit file `deploy/systemd/hatamoto-api.service` from the repository.

## Steps

### Create the user and files

1. Create a system user and the layout the unit expects, and give the user the data and upload directories:

   ```bash
   sudo useradd --system --home /opt/hatamoto --shell /usr/sbin/nologin hatamoto
   sudo install -d -o hatamoto -g hatamoto /opt/hatamoto/bin /opt/hatamoto/data /opt/hatamoto/uploads
   sudo install -d -o root -g hatamoto -m 0750 /opt/hatamoto/config
   sudo install -m 0755 hatamoto /opt/hatamoto/bin/hatamoto
   ```

   | Path | Holds |
   |---|---|
   | `/opt/hatamoto/bin/hatamoto` | The program you installed. Updates switch releases under the data directory, not here. |
   | `/opt/hatamoto/config/hatamoto.env` | The environment file. |
   | `/opt/hatamoto/data` | `HATAMOTO_DATA`: releases, update backups, compiled plugin cache, `install_id`. |
   | `/opt/hatamoto/data/secrets` | The generated secret, unless you set `HATAMOTO_SECRET` yourself. |
   | `/opt/hatamoto/uploads` | Uploaded media, set by `MEDIA_UPLOAD_DIR` (the default `./uploads` is relative to the working directory). |

2. Create `/opt/hatamoto/config/hatamoto.env`. Systemd reads it as plain `KEY=value` lines, with no `export` and no shell expansion. `HATAMOTO_SECRET` is the **server secret**, a long random value the server uses to sign links and protect stored credentials; it must stay the same across restarts. Without it, the server generates one into the secrets directory, which works if you back that directory up. Every variable is in the [configuration reference](configuration-reference.md).

   ```ini
   DATABASE_URL=postgres://hatamoto:CHANGE-ME@127.0.0.1:5432/hatamoto?sslmode=disable
   ENVIRONMENT=production
   PUBLIC_BASE_URL=https://example.com
   MEDIA_UPLOAD_DIR=/opt/hatamoto/uploads
   HATAMOTO_SECRET=replace-with-at-least-32-random-characters
   SMTP_HOST=smtp.example.com
   SMTP_FROM=noreply@example.com
   ```

3. Make it readable by root and the service group only:

   ```bash
   sudo chown root:hatamoto /opt/hatamoto/config/hatamoto.env
   sudo chmod 0640 /opt/hatamoto/config/hatamoto.env
   ```

### Install the unit

1. Read what the shipped unit sets. Its `[Unit]` section has `After=network-online.target postgresql.service`, `Wants=network-online.target` and `StartLimitIntervalSec=0`, and `[Install]` has `WantedBy=multi-user.target`. Its `[Service]` section:

   ```ini
   User=hatamoto
   Group=hatamoto
   WorkingDirectory=/opt/hatamoto
   Environment=HATAMOTO_DATA=/opt/hatamoto/data
   EnvironmentFile=/opt/hatamoto/config/hatamoto.env
   ExecStart=/opt/hatamoto/bin/hatamoto supervise
   KillMode=mixed
   TimeoutStopSec=60
   Restart=always
   RestartSec=5
   LimitNOFILE=65536
   NoNewPrivileges=true
   PrivateTmp=true
   ```

   | Directive | Why |
   |---|---|
   | `ExecStart=... supervise` | The **supervisor** starts the server as its child, switches to a new release only after it starts and answers, and rolls back on its own. Use `hatamoto serve` only if you want no update machinery. |
   | `KillMode=mixed`, `TimeoutStopSec=60` | Systemd signals only the supervisor, which forwards `SIGTERM` to the server and waits up to 40 seconds. The stop timeout stays above that. |
   | `StartLimitIntervalSec=0`, `Restart=always` | Keep retrying, for example while the database is down at boot, instead of giving up after five quick failures. |
   | `Environment=HATAMOTO_DATA=...` | The data directory does not depend on the working directory. A value in the environment file wins. |
   | `NoNewPrivileges`, `PrivateTmp` | Basic sandboxing. You can add `ProtectSystem=full` and `ReadWritePaths=/opt/hatamoto`; keep the data and upload directories writable. |

2. Install, enable and start it. The name `hatamoto-api` is historical and stays. While the database has no users, the server logs the **setup code**, a one-time code that lets only someone with access to the server create the first owner, at warn level. Or set `HATAMOTO_SETUP_CODE` (12 characters or more) before you start.

   ```bash
   sudo cp deploy/systemd/hatamoto-api.service /etc/systemd/system/hatamoto-api.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now hatamoto-api
   journalctl -u hatamoto-api -f
   ```

3. Run operator commands as the service user, with the same environment, or they look for `./data` in the current directory. The `update` commands refuse to run as anyone but the owner of the data directory. See the [command-line reference](cli-reference.md).

   ```bash
   sudo -u hatamoto bash -c 'set -a; . /opt/hatamoto/config/hatamoto.env; set +a
     cd /opt/hatamoto && HATAMOTO_DATA=/opt/hatamoto/data exec bin/hatamoto migrate status'
   ```

### Change the unit later

1. Put your own settings in the environment file or a drop-in, not in the unit.
2. To replace the unit on a server, upload the new file beside it as `$UNIT.new` and run `deploy/install-unit.sh` on the server with `UNIT` set to the unit path. The maintainers use this script. It keeps the `User=` and `Group=` already on the server, prints a diff, and refuses (exit 1, nothing changed) when the installed unit has drop-in files or `Environment=` lines the new one lacks. `FORCE_UNIT=1` installs anyway, and `CHECK_ONLY=1` stops after the checks. Otherwise it installs the file and runs `systemctl daemon-reload`.
3. Apply a changed unit with `sudo systemctl restart hatamoto-api`.

## Check it worked

```bash
systemctl is-active hatamoto-api
curl -s http://127.0.0.1:8090/ready
```

```text
active
ok
```

If the service restarts in a loop, read the last lines of `journalctl -u hatamoto-api`: the error names the missing variable or the database problem. If an `update` command says `no supervisor state under` a `data` directory in your current directory, run it as in step 3 of [Install the unit](#install-the-unit).
