#!/usr/bin/env bash
# test-img-check.sh - img-check.sh passes a clean tree and names every violation;
# stage_seal turns a dirty tree into one that passes.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
hd=home/dev
export hd
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

# clean_tree DIR: the shape of a sealed image.
clean_tree() {
  local r="$1"
  mkdir -p "$r"/etc/ssh "$r"/etc/ufw "$r"/root/.ssh "$r/$hd/.ssh" "$r"/tmp "$r"/var/tmp \
    "$r"/var/log/journal "$r"/var/lib/apt/lists/partial "$r"/var/cache/apt/archives \
    "$r"/opt/hatamoto/config "$r"/opt/hatamoto/data "$r"/var/lib/postgresql "$r"/var/lib/dbus
  : > "$r/etc/machine-id"
  printf 'ENABLED=yes\n' > "$r/etc/ufw/ufw.conf"
  printf 'root:x:0:0:root:/root:/bin/bash\ndev:x:1000:1000::/home/dev:/bin/bash\n' > "$r/etc/passwd"
  printf 'root:!:19000:0:99999:7:::\ndev:*:19000:0:99999:7:::\n' > "$r/etc/shadow"
  : > "$r/root/.ssh/authorized_keys"
  : > "$r/var/log/syslog"
  : > "$r/var/lib/apt/lists/lock"
  printf 'Port 22\n' > "$r/etc/ssh/sshd_config"
}

check() { bash "$here/img-check.sh" "$@"; }

clean="$tmp/clean"
clean_tree "$clean"
check "$clean" >"$tmp/out" || fail "clean tree failed: $(cat "$tmp/out")"
grep -q 'image is clean' "$tmp/out" || fail "no clean verdict"
pass "clean tree passes"

# violation NAME EXPECTED-LINE ACTION: copy the clean tree, break one thing, expect exit 1 naming it.
violation() {
  local name="$1" want="$2"; shift 2
  local r="$tmp/v-$name"
  rm -rf "$r"; cp -a "$clean" "$r"
  ( cd "$r" && "$@" )
  if IMG_CHECK_DENY="${DENY:-}" check "$r" >"$tmp/out"; then fail "$name: image passed"; fi
  grep -q "^\[FAIL\] $want" "$tmp/out" || fail "$name: not named ($want): $(cat "$tmp/out")"
  pass "violation: $name"
}

violation host-key ssh-host-keys touch etc/ssh/ssh_host_ed25519_key
violation authorized authorized-keys sh -c 'echo "ssh-ed25519 AAAA key" > "$hd/.ssh/authorized_keys"'
violation private-key ssh-private-keys touch root/.ssh/id_rsa
violation known-hosts ssh-private-keys touch "$hd/.ssh/known_hosts"
violation root-password password sh -c "printf 'root:\$6\$salt\$hash:19000:0:99999:7:::\ndev:*:19000:0:99999:7:::\n' > etc/shadow"
violation user-password password sh -c "printf 'root:!:19000:0:99999:7:::\ndev:\$6\$s\$h:19000:0:99999:7:::\n' > etc/shadow"
violation machine-id machine-id sh -c 'echo 0123456789abcdef > etc/machine-id'
violation history history sh -c 'echo "curl | sh" > root/.bash_history'
violation apt-list apt-lists touch var/lib/apt/lists/archive.ubuntu.com_dists_noble_InRelease
violation apt-cache apt-cache touch var/cache/apt/archives/foo.deb
violation tmp-file tmp touch tmp/leftover
violation log-content logs sh -c 'head -c 100 /dev/zero | tr "\0" x > var/log/auth.log'
violation rotated rotated-logs touch var/log/syslog.1
violation env-file hatamoto-secrets touch opt/hatamoto/config/hatamoto.env
violation secrets-dir hatamoto-secrets mkdir opt/hatamoto/data/secrets
violation install-id hatamoto-secrets touch opt/hatamoto/data/install_id
violation state-dir hatamoto-secrets sh -c 'mkdir -p var/lib/hatamoto-install && touch var/lib/hatamoto-install/firstboot.done'
violation acme caddy-state sh -c 'mkdir -p var/lib/caddy/.local/share/caddy && touch var/lib/caddy/.local/share/caddy/acme'
violation cloud-init cloud-init mkdir -p var/lib/cloud/instances
violation firewall firewall sh -c 'echo ENABLED=no > etc/ufw/ufw.conf'
DENY=decoy-secret-value violation decoy secret-scan sh -c 'echo "token=decoy-secret-value" > etc/foo.conf'
DENY=decoy-secret-value violation decoy-pg secret-scan sh -c 'echo "CREATE ROLE x PASSWORD decoy-secret-value" > var/lib/postgresql/wal'
unset DENY

# The firewall item can be switched off for hosts that rely on the provider firewall.
r="$tmp/v-nofw"; rm -rf "$r"; cp -a "$clean" "$r"; echo ENABLED=no > "$r/etc/ufw/ufw.conf"
IMG_CHECK_FIREWALL=0 check "$r" >"$tmp/out" || fail "IMG_CHECK_FIREWALL=0 still failed"
pass "firewall item can be skipped"

# --- stage_seal on a dirty fixture produces a tree that passes.
bash "$here/build-install.sh" --out "$tmp/asm" >/dev/null
dirty="$tmp/dirty"
clean_tree "$dirty"
printf 'root:$6$salt$hash:19000:0:99999:7:::\ndev:$6$s$h:19000:0:99999:7:::\n' > "$dirty/etc/shadow"
touch "$dirty/etc/ssh/ssh_host_rsa_key" "$dirty/etc/ssh/ssh_host_rsa_key.pub" "$dirty/root/.ssh/id_ed25519" "$dirty/$hd/.ssh/known_hosts"
echo "ssh-ed25519 AAAA key" > "$dirty/root/.ssh/authorized_keys"
echo "ssh-ed25519 AAAA key" > "$dirty/$hd/.ssh/authorized_keys"
echo 0123456789abcdef > "$dirty/etc/machine-id"
echo "curl | sh" > "$dirty/root/.bash_history"
echo "ls" > "$dirty/$hd/.bash_history"
mkdir -p "$dirty/var/lib/cloud/instances/i-1"
touch "$dirty/var/lib/apt/lists/some_InRelease" "$dirty/var/cache/apt/archives/x.deb" "$dirty/var/cache/apt/pkgcache.bin" "$dirty/tmp/old" "$dirty/var/log/syslog.1" "$dirty/var/log/auth.log.2.gz"
mkdir -p "$dirty/var/lib/hatamoto-install" "$dirty/opt/hatamoto/data/secrets" "$dirty/var/lib/caddy/.local/share/caddy/x" "$dirty/var/log/journal/abc"
touch "$dirty/var/lib/hatamoto-install/firstboot.done" "$dirty/opt/hatamoto/data/install_id" "$dirty/var/log/journal/abc/system.journal"
printf 'DATABASE_URL=postgres://hatamoto:decoy-secret-value@127.0.0.1/hatamoto\n' > "$dirty/opt/hatamoto/config/hatamoto.env"
head -c 500 /dev/zero | tr '\0' x > "$dirty/var/log/syslog"
if IMG_CHECK_DENY=decoy-secret-value check "$dirty" >/dev/null; then fail "dirty fixture passed"; fi
env HATAMOTO_INSTALL_ROOT="$dirty" HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; stage_seal' _ "$tmp/asm/install.sh" >"$tmp/seal.out" 2>&1 ||
  fail "stage_seal failed: $(cat "$tmp/seal.out")"
IMG_CHECK_DENY=decoy-secret-value check "$dirty" >"$tmp/out" || fail "sealed tree fails the check: $(cat "$tmp/out")"
pass "stage_seal output passes img-check (decoy secret gone with the env file)"
grep -q '^root:!:' "$dirty/etc/shadow" || fail "root password not locked"

# Sealing twice is harmless.
env HATAMOTO_INSTALL_ROOT="$dirty" HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; stage_seal' _ "$tmp/asm/install.sh" >/dev/null 2>&1 || fail "second seal failed"
check "$dirty" >/dev/null || fail "twice-sealed tree fails"
pass "sealing twice is harmless"

echo "ok: test-img-check"
