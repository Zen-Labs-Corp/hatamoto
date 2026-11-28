packer {
  required_plugins {
    digitalocean = {
      source  = "github.com/digitalocean/digitalocean"
      version = "~> 1.4"
    }
  }
}

variable "do_token" {
  type      = string
  sensitive = true
  default   = env("DIGITALOCEAN_TOKEN")
}

variable "region" {
  type    = string
  default = "nyc3"
}

variable "size" {
  type    = string
  default = "s-1vcpu-1gb"
}

variable "install_url" {
  type    = string
  default = "https://hatamoto.dev/install.sh"
}

variable "install_sha256" {
  type        = string
  default     = ""
  description = "Optional sha256 of install.sh; the build stops when it differs."
}

locals {
  snapshot_name = "hatamoto-${formatdate("YYYYMMDD-hhmm", timestamp())}"
}

source "digitalocean" "hatamoto" {
  api_token        = var.do_token
  image            = "ubuntu-24-04-x64"
  region           = var.region
  size             = var.size
  ssh_username     = "root"
  snapshot_name    = local.snapshot_name
  snapshot_regions = [var.region]
}

build {
  sources = ["source.digitalocean.hatamoto"]

  provisioner "shell" {
    inline = ["cloud-init status --wait || true"]
  }

  # img-check goes to /run (tmpfs): not in the snapshot, untouched by the seal's /tmp scrub.
  provisioner "file" {
    source      = "${path.root}/../install/img-check.sh"
    destination = "/run/img-check.sh"
  }

  # Seal and check in ONE provisioner, and nothing after it: the seal removes the ssh host
  # keys and the build key in authorized_keys, so no new connection could be opened.
  provisioner "shell" {
    environment_vars = ["INSTALL_URL=${var.install_url}", "INSTALL_SHA256=${var.install_sha256}"]
    inline_shebang   = "/bin/bash -e"
    skip_clean       = true
    inline = [
      "set -euo pipefail",
      "curl -fsSL --proto '=https' --tlsv1.2 -o /run/install.sh \"$INSTALL_URL\"",
      "if [ -n \"$INSTALL_SHA256\" ]; then echo \"$INSTALL_SHA256  /run/install.sh\" | sha256sum -c -; fi",
      "sh /run/install.sh build --seal",
      "systemctl stop rsyslog.service syslog.socket systemd-journald.socket systemd-journald-dev-log.socket systemd-journald-audit.socket systemd-journald.service >/dev/null 2>&1 || true",
      "find /var/log/journal -type f -delete 2>/dev/null || true",
      "find /var/log -type f -exec truncate -s 0 {} +",
      "find /tmp /var/tmp -mindepth 1 -delete 2>/dev/null || true",
      "bash /run/img-check.sh /",
    ]
  }
}
