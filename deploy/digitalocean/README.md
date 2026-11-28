# Hatamoto droplet image for DigitalOcean

This Packer template builds a DigitalOcean snapshot you can create droplets from. The snapshot is
Ubuntu 24.04 with Hatamoto already built and sealed by `install.sh build --seal`, and checked by
`img-check.sh`. Nothing is configured yet: on its first boot each droplet creates its own
database, secrets and site, then prints the setup address.

This is the persistent DigitalOcean option. App Platform storage is ephemeral, so use this image
when you want your data to survive restarts and deploys.

## Prerequisites

- Packer 1.11 or newer.
- A DigitalOcean API token with write access, in the `DIGITALOCEAN_TOKEN` environment variable.

## Build the snapshot

Run these from the repository root:

```sh
packer init deploy/digitalocean
packer validate deploy/digitalocean
packer build deploy/digitalocean
```

Packer starts a temporary droplet, builds and seals Hatamoto on it, then snapshots it. The
snapshot is named `hatamoto-` plus the build time. You can change the region, droplet size and
installer address with `-var region=...`, `-var size=...` and `-var install_url=...`.

To pin the installer, download `install.sh` and verify it as described in
[Install on a VPS](../../docs/site/getting-started/install-vps.md), then pass its checksum. The
build stops if the downloaded file differs:

```sh
packer build -var install_sha256=<sha256 of the verified install.sh> deploy/digitalocean
```

## Why the check runs last

The seal removes the build SSH key and the server's SSH host keys, so Packer cannot open a second
connection after it. For that reason the seal, the log clean-up and `img-check.sh` run in one
final step, and nothing follows it. `img-check.sh` is the last command: if it reports a failure
the step exits non-zero and the build fails, so no snapshot is made from a dirty image. The
firewall stays on during the build, and `img-check.sh` requires it.

`install.sh` and `img-check.sh` are kept in `/run`, which is not part of the snapshot.

Every droplet you create from the snapshot gets new SSH host keys and its own login key on first
boot.

## Create a droplet

In the DigitalOcean control panel, open the snapshot under Backups & Snapshots and choose Create
Droplet. Add your SSH key, then wait for the first boot to finish. The setup address and the
login message are described in
[Install on a VPS](../../docs/site/getting-started/install-vps.md).

Submitting the image to the DigitalOcean Marketplace is not covered here.
