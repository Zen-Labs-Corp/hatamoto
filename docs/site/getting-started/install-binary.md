---
title: Install the binary
nav_title: Install the binary
description: Download Hatamoto, connect it to a Postgres database and start your site.
next: first-run.md, create-users.md
---

In this guide you download Hatamoto, connect it to a Postgres database and start it. When you finish, your site answers on port 8090 and the server prints a setup code, which you use in the next guide to create your owner account.

## Before you start

- You have done the checks in [Before you install](before-you-install.md).
- A Linux or macOS computer or server with a terminal, and `curl` installed.
- Postgres 16, or Docker to start a test database. **Postgres** is the database Hatamoto stores everything in.

## Steps

### Download Hatamoto

1. Find the current version. Hatamoto lists its releases in one file for the `stable` release channel, the default stream of tested releases. This command shows each version in it and whether it was withdrawn because of a problem:

   ```bash
   curl -fsSL https://hatamoto.dev/releases/channels/stable.json | grep -E '"(version|halted|yanked)"'
   ```

   ```text
           "version": "v0.26.0",
           "halted": false
           "version": "v0.27.1",
           "halted": false
           "version": "v0.28.0",
           "halted": false
       "yanked": []
   ```

   Read it like this:

   - Skip a version followed by `"halted": true`. It was withdrawn.
   - If the last part reads `"yanked": [` rather than `"yanked": []`, the versions after it were withdrawn too: skip them.
   - Then take the highest number left (`v0.28.0` is higher than `v0.27.1`). The list is in release order, not number order, so the last version is not always it.

   Above, that is `v0.28.0`. [Requirements and secrets](requirements-and-secrets.md) explains the file in full.

   The [GitHub releases](https://github.com/Zen-Labs-Corp/hatamoto/releases) page offers, from the first public release, the same program as a `hatamoto-<os>-<arch>.tar.gz` bundle that also holds the licences, for example `hatamoto-linux-amd64.tar.gz`. Inside the bundle the program is `hatamoto-<os>-<arch>/hatamoto`: move it to `./hatamoto` to follow the steps below (for example `tar -xzf hatamoto-linux-amd64.tar.gz --strip-components=1 hatamoto-linux-amd64/hatamoto`). How releases are signed: [Trust and signing](../updates/trust-and-signing.md).

2. Download the program file for your system. The file name ends with your system and processor type, from `uname -sm`:

   | `uname -sm` prints | File |
   |---|---|
   | `Linux x86_64` | `hatamoto-linux-amd64` |
   | `Linux aarch64` | `hatamoto-linux-arm64` |
   | `Darwin x86_64` | `hatamoto-darwin-amd64` |
   | `Darwin arm64` | `hatamoto-darwin-arm64` |

   For Linux on x86, replace `v0.28.0` with the version you found:

   ```bash
   VERSION=v0.28.0
   curl -fLo hatamoto "https://hatamoto.dev/releases/$VERSION/hatamoto-linux-amd64"
   chmod +x hatamoto
   ```

   `chmod +x` lets the file run as a program.

3. Check that the program runs:

   ```bash
   ./hatamoto version
   ```

   ```text
   hatamoto v0.31.0 (commit 15887307b221388a8a86d939775cdaa10ad0f8ca, sdk 0.12.0)
   ```

### Prepare the database

1. Create a database user and an empty database, both named `hatamoto`. Run these on the database server, as a Postgres administrator (on most Linux servers, put `sudo -u postgres` in front of each command). The first command asks you to choose a password:

   ```bash
   createuser --pwprompt hatamoto
   createdb --owner hatamoto hatamoto
   ```

   To try Hatamoto without installing Postgres, start a temporary Postgres 16 with Docker instead. Run it in a terminal of its own and leave it open. Its user, password and database are all `hatamoto`, and everything is deleted when you stop it:

   ```bash
   docker run --rm -e POSTGRES_USER=hatamoto -e POSTGRES_PASSWORD=hatamoto -e POSTGRES_DB=hatamoto -p 127.0.0.1:5432:5432 postgres:16
   ```

2. Write the **database address**, which tells Hatamoto where the database is and how to sign in. Put your password in place of `your-password`:

   ```text
   postgres://hatamoto:your-password@localhost:5432/hatamoto?sslmode=disable
   ```

   For the temporary Docker database, the address is `postgres://hatamoto:hatamoto@localhost:5432/hatamoto?sslmode=disable`. If the database is on another computer, end the address with `sslmode=require` so the connection is encrypted.

### Start Hatamoto

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`.

1. Start the server. Hatamoto reads the address from an **environment variable**, a named setting you give a program when it starts, called `DATABASE_URL`. In the folder where you saved the file, run:

   ```bash
   DATABASE_URL='postgres://hatamoto:your-password@localhost:5432/hatamoto?sslmode=disable' ./hatamoto serve
   ```

   On its first start Hatamoto prepares everything by itself. It creates the **server secret**, a long random value the server uses to sign links and protect stored credentials, and saves it in `data/secrets`. It sets up the tables in the database, creates a starter home page and starts listening on port 8090. [Requirements and secrets](requirements-and-secrets.md) lists each step.

2. Find the setup code in the output. The **setup code** is a one-time code the server prints to its log so only someone with access to the server can create the first owner. Look for a line like this:

   ```text
   time=2026-10-20T09:15:02.418Z level=WARN msg="Setup: open /k7qm-x2rf-9tdc/setup and enter code K7M2-RT9P" setup_code=K7M2-RT9P
   ```

   Leave the server running in this terminal. Pressing Ctrl+C stops it.

To listen on another address or port, set `API_HOST` and `API_PORT` the same way as `DATABASE_URL`. To keep Hatamoto running after you close the terminal and start it when the server boots, set it up as a service managed by **systemd**, the service manager on most Linux servers, with [Deploy with systemd](../operate/deploy-systemd.md). That guide uses `hatamoto supervise`, which runs the server and installs signed updates; the difference and every other command are in the [CLI reference](../operate/cli-reference.md).

## Check it worked

In a second terminal on the same machine, run:

```bash
curl -i http://localhost:8090/health
```

The first line of the answer is `HTTP/1.1 200 OK`. `./hatamoto healthcheck` makes the same request and exits without a message when the server is up.

Then open `http://localhost:8090/` in a browser to see the starter home page, and open the setup address from the log line to reach the setup wizard. `hatamoto admin-url` prints your admin address if you need it again. Continue with [First run](first-run.md).

If the program stops at once, read its last line:

- `DATABASE_URL is required`: the variable is missing. Check that `DATABASE_URL=` is on the same command line as `./hatamoto serve`.
- `connect to database`: Postgres is not running, or the user, password or database name in the address is wrong.

The other start errors, with their fixes, are listed at the end of [Requirements and secrets](requirements-and-secrets.md).
