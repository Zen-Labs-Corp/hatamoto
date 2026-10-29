---
title: VPS provider examples
nav_title: VPS provider examples
description: Ready-to-paste launch scripts and cloud-init files that install Hatamoto on a new Vultr, DigitalOcean, Lightsail, Hetzner or Linode server.
---

This page has one example per hosting provider for the launch form of a new server, where you can paste a script or a cloud-init file that runs once when the server first starts. It supports the guide [Install on a VPS](install-vps.md), which explains the options and what the installer does. Every example installs the same thing, so pick the one for your provider.

**cloud-init** is the standard tool that reads such a file on the first start of a cloud server. A file for it starts with the line `#cloud-config`. A plain script starts with `#!/bin/sh`.

## Before you paste

- Launch an Ubuntu 24.04 or Debian 12 server with at least 1 GB of RAM.
- The code in the launch box runs as root, so the examples have no `sudo`. The login user you use later is often `ubuntu` or `admin` (see your provider's page), not root. Run later commands with `sudo`.
- Replace `cms.example.com` and `you@example.com` with your own values, or delete the whole `env` part to use the free sslip.io name. If you set a domain, point its `A` record at the server's address first (see the guide).
- To choose your own setup code, add `HATAMOTO_SETUP_CODE=CHANGE-ME` to the `env` part and replace `CHANGE-ME` with at least 12 letters or digits. Without it the installer makes a random code.
- The launch box is usually readable later in the provider's panel, so do not paste a real setup code you want to keep secret. Leave it out and read the generated one in the console.
- After the server starts, the install takes a few minutes. Open the provider's web console to see the setup link, or read `/var/log/cloud-init-output.log` over SSH.
- The provider's firewall must allow ports 80 and 443.

Each example downloads the installer to a file first and runs it only if the download worked, so a failed download stops the step instead of being reported as success.

## Vultr

In the deploy form, open the startup script section, add a new script of type boot, and paste:

```sh
#!/bin/sh
sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com sh /tmp/hatamoto-install.sh'
```

## DigitalOcean

When you create the Droplet, open the advanced options, turn on user data, and paste:

```yaml
#cloud-config
runcmd:
  - sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com sh /tmp/hatamoto-install.sh'
```

## Amazon Lightsail

In the launch form, open the launch script section and paste:

```sh
#!/bin/sh
sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com sh /tmp/hatamoto-install.sh'
```

After it launches, open the networking tab of the instance and add a rule for port 443 (HTTPS). Lightsail opens only 22 and 80 by default. The login user is `ubuntu` on Ubuntu images and `admin` on Debian images.

## Hetzner

When you create the server, paste this into the cloud config field:

```yaml
#cloud-config
runcmd:
  - sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com sh /tmp/hatamoto-install.sh'
```

Hetzner has no firewall unless you attach one. If you attach one, allow ports 22, 80 and 443.

## Linode

When you create the Linode, open the add user data section (Akamai Cloud) and paste:

```yaml
#cloud-config
runcmd:
  - sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com sh /tmp/hatamoto-install.sh'
```

If your Linode image does not offer user data, create the server, open its console and run the one-line command from the guide instead.

## Any other provider

If the provider accepts cloud-init, use the DigitalOcean example. If it accepts a script, use the Vultr example. If it accepts neither, sign in once and run the command from [Install on a VPS](install-vps.md).
