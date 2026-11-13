---
title: VPS provider examples
nav_title: VPS provider examples
description: Ready-to-paste launch scripts and cloud-init files that install Hatamoto on a new Vultr, DigitalOcean, Lightsail, Amazon EC2, Hetzner or Linode server.
---

This page has one example per hosting provider for the launch form of a new server, where you can paste a script or a cloud-init file that runs once when the server first starts. It supports the guide [Install on a VPS](install-vps.md), which explains the options and what the installer does. Every example installs the same thing, so pick the one for your provider.

**cloud-init** is the standard tool that reads such a file on the first start of a cloud server. A file for it starts with the line `#cloud-config`. A plain script starts with `#!/bin/sh`.

## Before you paste

- Launch a server with at least 1 GB of RAM and an LTS image: Ubuntu 22.04, 24.04 or 26.04, or Debian 12 or 13. Vultr's default image can be a short-term Ubuntu release (26.10 was). The installer accepts short-term releases with a warning, but an LTS image is the safer choice.
- The code in the launch box runs as root, so the examples have no `sudo`. The login user you use later depends on the provider: `root` on Vultr, DigitalOcean, Hetzner and Linode, `ubuntu` or `admin` on Lightsail and EC2 (see your provider's section). If you are not root, run later commands with `sudo`.
- Replace `cms.example.com` and `you@example.com` with your own values, or delete the whole `env` part to use the free sslip.io name. If you set a domain, point its `A` record at the server's address first (see the guide).
- To choose your own setup code, add `HATAMOTO_SETUP_CODE=CHANGE-ME` to the `env` part and replace `CHANGE-ME` with at least 12 letters or digits. Without it the installer makes a random code.
- The launch box is usually readable later in the provider's panel, so do not paste a real setup code you want to keep secret. Leave it out and read the generated one in the console.
- After the server starts, the install takes a few minutes. Open the provider's web console to see the setup link, or read `/var/log/cloud-init-output.log` over SSH.
- The provider's firewall must allow ports 80 and 443.

Each example downloads the installer to a file first, makes it executable, and runs it only if the download worked, so a failed download stops the step instead of being reported as success. If the image mounts `/tmp` as noexec, run the installer with `sh /tmp/hatamoto-install.sh` instead of the direct path.

## Vultr

In the deploy form, under **Additional Features**, turn on **Cloud-Init User Data** and paste this into the **User Data** field. This is not a startup script of type boot: on Linux, Vultr runs user data through cloud-init, once, as root, on the first boot.

```sh
#!/bin/sh
sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && chmod +x /tmp/hatamoto-install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com /tmp/hatamoto-install.sh'
```

## DigitalOcean

When you create the Droplet, expand **Additional Options**, turn on **Startup scripts**, and paste this into the box that appears:

```yaml
#cloud-config
runcmd:
  - sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && chmod +x /tmp/hatamoto-install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com /tmp/hatamoto-install.sh'
```

## Amazon Lightsail

In the launch form, open the launch script section and paste:

```sh
#!/bin/sh
sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && chmod +x /tmp/hatamoto-install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com /tmp/hatamoto-install.sh'
```

After it launches, open the networking tab of the instance and add a rule for port 443 (HTTPS). Lightsail opens only 22 and 80 by default. The login user is `ubuntu` on Ubuntu images and `admin` on Debian images.

## Amazon EC2

In the launch instance wizard, expand **Advanced details** at the bottom, and paste this into the **User data** field. Leave **User data has already been base64 encoded** unticked; the console encodes it for you.

```sh
#!/bin/sh
sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && chmod +x /tmp/hatamoto-install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com /tmp/hatamoto-install.sh'
```

The instance's security group must allow inbound TCP 22, 80 and 443. A security group made by the wizard opens only SSH (22), so add 80 and 443 from anywhere (`0.0.0.0/0`, and `::/0` if you use IPv6). The VPC's default security group opens none of them.

The public address EC2 assigns at launch changes every time the instance is stopped and started, so a DNS record pointing at it breaks. Use an **Elastic IP**, a fixed address that stays with your account until you release it. Allocate one, point your domain's `A` record at it, launch the instance with your domain in the script, then associate the Elastic IP with the instance. Hatamoto starts on the sslip.io name and switches to your domain within minutes of the record reaching the server. AWS charges for every public IPv4 address, including the one assigned at launch, so an attached Elastic IP costs no more than that address.

EC2 puts the public address in front of the instance (NAT), so the server only sees a private address. The installer asks an outside service for the public one and logs a warning naming both; that warning is normal here. The login user is `ubuntu` on Ubuntu images and `admin` on Debian images.

## Hetzner

When you create the server, paste this into the cloud config field:

```yaml
#cloud-config
runcmd:
  - sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && chmod +x /tmp/hatamoto-install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com /tmp/hatamoto-install.sh'
```

Hetzner has no firewall unless you attach one. If you attach one, allow ports 22, 80 and 443.

## Linode

When you create the Linode, expand **Add User Data** and paste this into the **User Data** field:

```yaml
#cloud-config
runcmd:
  - sh -c 'curl -fsSL -o /tmp/hatamoto-install.sh https://hatamoto.dev/install.sh && chmod +x /tmp/hatamoto-install.sh && env HATAMOTO_DOMAIN=cms.example.com HATAMOTO_ADMIN_EMAIL=you@example.com /tmp/hatamoto-install.sh'
```

If your Linode image does not offer user data, create the server, open its console and run the one-line command from the guide instead. The login user is `root` (the SSH key you add goes to root), so later commands need no `sudo`. The web console (Launch LISH Console) needs a root password, so set one on the create form if you want to use it.

## Any other provider

If the provider accepts cloud-init, use the DigitalOcean example. If it accepts a script, use the Vultr example. If it accepts neither, sign in once and run the command from [Install on a VPS](install-vps.md).
