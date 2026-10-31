---
title: Install on a VPS
nav_title: Install on a VPS
description: Turn a blank Ubuntu or Debian VPS into a running Hatamoto with automatic HTTPS, using one command or a cloud-init file.
next: first-run.md, create-users.md
---

In this guide you install Hatamoto on a **VPS**, a small server you rent from a hosting company such as Vultr, DigitalOcean, Lightsail, Hetzner or Linode. One command, or one file pasted into the provider's launch form, installs everything. When you finish, a link in the server console opens the setup wizard with your setup code already filled in.

## Before you start

- A new VPS running Ubuntu 22.04, 24.04 or 26.04 LTS, or Debian 12 or 13, on amd64 or arm64 processors, with at least 1 GB of RAM. Other Ubuntu releases install with a warning. Any other system is refused. Hatamoto adds a swap file (disk space the server uses as extra memory) on small servers by itself.
- A way to run commands as root on it: the provider's web console, or SSH with `sudo`.
- Optional: a domain name. Without one, Hatamoto uses a free name of the form `203-0-113-7.sslip.io` built from your server's address.
- The provider's firewall must let in ports 80 and 443. On Lightsail, open 443 in the Lightsail networking tab of the instance. Other providers have a similar firewall page: allow 22, 80 and 443 there too.

The installer sets up Postgres, Hatamoto under systemd and Caddy, a web server that gets HTTPS certificates by itself. It also turns on nightly backups and signed automatic updates.

## Steps

### Choose how to install

There are two ways. Pick one.

- **Run one command.** Sign in to the new server and run it:

  ```bash
  curl -fsSL https://hatamoto.dev/install.sh | sudo sh
  ```

- **Paste a cloud-init file.** Many providers have a box named user-data, launch script or startup script, where you paste a file that runs once when the server first starts. The [VPS provider examples](vps-provider-examples.md) page has a ready file for each provider.

### Set your options (optional)

Without options the installer picks safe defaults. To change one, set it as an **environment variable**, a named setting given to a program when it starts. Put them after `sudo env`:

```bash
curl -fsSL https://hatamoto.dev/install.sh | sudo env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com sh
```

In a cloud-init file or launch script, put the same names after `env` in the install command, as the provider examples do.

| Name | What it does | Default |
|---|---|---|
| `HATAMOTO_DOMAIN` | Your own domain, for example `cms.example.com`. | the sslip.io name |
| `HATAMOTO_VERSION` | The Hatamoto version to install, such as `v1.0.2`. It cannot be older than the installer's minimum. | the newest stable version, but never below the installer's minimum |
| `HATAMOTO_PG_MAJOR` | The PostgreSQL major version to install: `16`, `17` or `18`. | `18` |
| `HATAMOTO_ADMIN_EMAIL` | Your email address, given to the certificate authority for expiry notices. | none |
| `HATAMOTO_SETUP_CODE` | Your own setup code, at least 12 letters or digits. | a random code |
| `HATAMOTO_BACKUPS` | `1` for nightly backups, `0` for none. | `1` |
| `HATAMOTO_TLS` | `auto` for real certificates, `internal` for test certificates, `off` for plain HTTP. | `auto` |
| `HATAMOTO_FIREWALL` | `0` leaves the server's firewall alone, for hosts with a provider firewall only. | `1` |
| `HATAMOTO_PUBLIC_IP` | The server's public address. Detected by asking an outside service and checking the answer against the addresses on the server. If they differ, the server's own public address wins. If the server has none (providers that put it behind NAT, such as Amazon Lightsail), the outside answer is used and a warning names both. Set this to decide yourself. | detected |

If you build a machine image for others to launch (`build --seal`), never put `HATAMOTO_SETUP_CODE` in it, not even in `/etc/default/hatamoto-install`. Everyone who launches the image would share that code, and the image's author could read it. Leave it out and each server generates its own at first start.

If you set `HATAMOTO_DOMAIN`, create a DNS record for it first. **DNS** is the system that maps a domain name to a server address: add an `A` record pointing the domain at the server's address. If the record is not ready yet, Hatamoto starts on the sslip.io name and switches to your domain within minutes of the record working.

The sslip.io names are shared by many servers and may hit shared certificate limits. Use your own domain for production.

### Which version is installed

Each installer carries a minimum Hatamoto version, the oldest release it was tested with. Without `HATAMOTO_VERSION`, it installs the newer of that minimum and the newest fully rolled-out stable release. If you set `HATAMOTO_VERSION` below the minimum, the installer stops and names the minimum.

### PostgreSQL

The installer installs PostgreSQL 18 from apt.postgresql.org, on Ubuntu as well as Debian. Set `HATAMOTO_PG_MAJOR` to `16` or `17` to choose another. If a PostgreSQL cluster already exists, it keeps its version, and the installer refuses a version outside 16 to 18. It does not move existing data between versions.

### Change the domain later

To switch a running server to a new domain, point its `A` record at the server's address, then download the installer again and run:

```bash
curl -fsSLO https://hatamoto.dev/install.sh
chmod +x install.sh
sudo ./install.sh set-domain cms.example.com --email you@example.com
```

When DNS already points at the server, the installer switches the site, restarts it and prints a new admin link. The old domain then redirects permanently to the new one. Only one old domain is kept, so a later change replaces it.

When DNS does not match yet, nothing changes. The server keeps serving the current domain and the DNS-switch timer retries until the record works. The command says it is waiting for DNS.

The command refuses to run on a Hatamoto older than the installer's minimum. Update Hatamoto first.

### Check the installer first (optional)

To read and verify the script before it runs, download it and check its signature with the [GitHub CLI](https://cli.github.com/):

```bash
curl -fsSLO https://hatamoto.dev/install.sh
gh attestation verify install.sh --repo Zen-Labs-Corp/hatamoto --signer-workflow Zen-Labs-Corp/hatamoto/.github/workflows/release.yml
chmod +x install.sh
sudo ./install.sh
```

The installer then checks the Hatamoto program against the signed release list before it installs it. How signing works: [Trust and signing](../updates/trust-and-signing.md).

### Open the setup link

When the install ends, the console prints a block like this:

```text
Hatamoto is ready.

Open this link to create your owner account
(the setup code is already filled in):

  https://203-0-113-7.sslip.io/k7qm-x2rf-9tdc/setup#code=K7M2-RT9P-QW4X

Setup code: K7M2-RT9P-QW4X
Domain:     cms.example.com (waiting for DNS -> 203.0.113.7;
            will switch automatically)
Updates:    automatic (stable channel)
Status:     systemctl status hatamoto-api

This message disappears once the owner account exists.
```

The `Domain:` line only appears while your domain does not point at the server yet; the site then moves to it by itself. Open the link. The setup wizard shows the code already filled in, because the part after `#` stays in your browser and is never sent to the server. The same message shows each time you sign in to the server, until you create the owner account. Continue with [First run](first-run.md).

## Check it worked

On the server, run:

```bash
curl -i http://127.0.0.1:8090/health
```

The first line of the answer is `HTTP/1.1 200 OK`. Then open your site's address in a browser: it shows a secure padlock and the starter home page. If the page does not load, check the provider's firewall for ports 80 and 443 first.

Two things to do soon:

- **Backups:** the installer saves a copy of your site every night on the server. That does not protect you if the server is lost, so also turn on the provider's snapshots, which copy the whole server. See [Backups](../operate/backups.md).
- **Updates:** Hatamoto installs signed updates by itself. See [Updates](../updates/index.md).

Your login user depends on the provider: often `ubuntu` on Lightsail and `root` on others. Hatamoto itself never runs as root.
