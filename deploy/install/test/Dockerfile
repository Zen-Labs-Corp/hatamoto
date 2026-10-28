# Test image for the installer end-to-end test: a bare distro with systemd and
# only what a stock cloud image has. The installer adds everything else, so the
# test sees what a real blank server sees. No secrets, no Hatamoto files.
ARG BASE=ubuntu:24.04
FROM ${BASE}

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq \
 && apt-get install -y --no-install-recommends \
      systemd systemd-sysv sudo curl ca-certificates python3 iproute2 openssl \
 && rm -rf /var/lib/apt/lists/* \
 && systemctl mask getty.target console-getty.service systemd-logind.service >/dev/null 2>&1 || true

# Some hosts load an AppArmor profile for /usr/bin/hostname that breaks that
# one binary in every privileged container (dpkg scripts such as ssl-cert call
# it). A copy outside the profile's path, in a directory that
# apt puts before /usr/bin when it runs maintainer scripts, avoids it.
RUN if command -v hostname >/dev/null 2>&1; then cp "$(command -v hostname)" /usr/sbin/hostname; fi

STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
