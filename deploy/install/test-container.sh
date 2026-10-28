#!/usr/bin/env bash
# test-container.sh - end-to-end test of install.sh on a blank distro container.
#
#   bash deploy/install/test-container.sh [ubuntu2404|debian12|all]
#
# Per distro: a systemd container (privileged, like a small VM) runs the
# installer against a local release mirror signed with throwaway keys, so
# nothing touches the real release site or a real certificate authority. Checks:
#   1. a tampered binary is refused before /opt/hatamoto/bin/hatamoto exists
#   2. `build --seal`, then the image hygiene check passes, and fails on a decoy secret
#   3. first boot: /health through Caddy is 200, /setup serves the form with the
#      setupcode module, the login message shows the link, the backup timer is
#      enabled, nothing but loopback listens on :8090
#   4. the sealed image is booted for real: hatamoto-firstboot.service runs by itself
#      while Caddy is already up, and the same checks pass
#   5. a second fresh container runs the all-in-one path (`sh < install.sh`) with the same checks
#
# Needs: docker (also through `sg docker -c`), go, python3, jq, openssl, network
# access for apt. Without docker it prints SKIP and exits 0, unless
# HATAMOTO_REQUIRE_DOCKER=1 (CI), which makes it an error.
# Not part of the fast check suites (like scripts/e2e-supervisor.sh).
set -euo pipefail

want="${1:-all}"
case "$want" in ubuntu2404 | debian12 | all) ;; *) echo "usage: $0 [ubuntu2404|debian12|all]" >&2; exit 2 ;; esac

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"

DOCKER_MODE=""
if docker info >/dev/null 2>&1; then DOCKER_MODE=plain
elif sg docker -c 'docker info' >/dev/null 2>&1; then DOCKER_MODE=sg
fi
if [ -z "$DOCKER_MODE" ]; then
  echo "SKIP: docker is not usable here (docker info fails, also through sg docker): container test not run" >&2
  [ "${HATAMOTO_REQUIRE_DOCKER:-0}" != 1 ] || exit 1
  exit 0
fi
dk() {
  if [ "$DOCKER_MODE" = sg ]; then sg docker -c "$(printf '%q ' docker "$@")"; else docker "$@"; fi
}

# The snap-packaged docker cannot read /tmp or hidden dirs: stage under $HOME.
stage_root="${INSTALL_E2E_TMP:-$HOME/hatamoto-install-e2e-tmp}"
mkdir -p "$stage_root"
E="$(mktemp -d "$stage_root/run.XXXXXX")"
CONTAINERS=() IMAGES=()
FAILS=0

pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1"; FAILS=$((FAILS + 1)); }
check() { # check DESCRIPTION COMMAND...
  local what="$1"; shift
  if "$@"; then pass "$what"; else fail "$what"; fi
}

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  set +e
  for c in "${CONTAINERS[@]:-}"; do [ -n "$c" ] && dk rm -f -v "$c" >/dev/null 2>&1; done
  for i in "${IMAGES[@]:-}"; do [ -n "$i" ] && dk image rm -f "$i" >/dev/null 2>&1; done
  rm -rf "$E"
  exit $rc
}
trap cleanup EXIT INT TERM

for t in go python3 jq openssl; do command -v "$t" >/dev/null || { echo "missing tool: $t" >&2; exit 2; }; done

# ---- release mirror with a real binary, throwaway keys, tampered copy -------
# shellcheck source=testlib/mirror.sh
. "$here/testlib/mirror.sh"
arch="$(cd "$repo/backend" && go env GOARCH)"
VER=v1.0.1
echo "== building the test binary (tags hatamoto_testkeys) and the mirror"
mirror_init "$E/m"
(cd "$repo/backend" && CGO_ENABLED=0 go build -tags hatamoto_testkeys -trimpath \
  -ldflags "-X hatamoto.dev/core/app.Version=$VER -X hatamoto.dev/core/app.Commit=e2e" \
  -o "$E/hatamoto" ./cmd/hatamoto)
mirror_add "$VER" now:100
# Replace the fake binary of this machine's platform by the real one and sign again.
rel="$M_SITE/releases/$VER"
cp "$E/hatamoto" "$rel/hatamoto-linux-$arch"
"$E/hatamoto" version --json > "$M_DIR/facts-$VER.json"
printf '{"channel":"stable","issued_at":"","expires_at":"","releases":[],"yanked":[]}\n' > "$M_SRC/stable.json"
mirror_tool record --version "$VER" --kind patch --facts "$M_DIR/facts-$VER.json" \
  --rollback-floor "$VER" --min-from "$VER" --allow-same --notes-url https://example.test/notes \
  --dir "$rel" --out "$rel/release.json" >/dev/null
mirror_tool sign-record "$rel/release.json" --key "$M_PRIV/release.key" --out "$rel/release.json.sig" --allow-same >/dev/null
mirror_tool channel add stable "$VER" --record "$rel/release.json" --sig "$rel/release.json.sig" \
  --ramp now:100 --dir "$M_SRC" >/dev/null
mirror_publish
cp -r "$M_SITE" "$E/site-good"
cp -r "$M_SITE" "$E/site-bad"
M_SITE="$E/site-bad" mirror_tamper_binary "$VER" "linux-$arch"

bash "$here/build-install.sh" --keys-dir "$M_TRUST" --out "$E/asm" >/dev/null
cp "$here/img-check.sh" "$E/img-check.sh"

GOOD=http://127.0.0.1:8765/releases
BAD=http://127.0.0.1:8766/releases

# ---- per-container helpers -----------------------------------------------------
C=""
ex() { dk exec "$C" "$@"; }

start_container() { # start_container IMAGE NAME
  C="$2"
  CONTAINERS+=("$C")
  dk run -d --name "$C" --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock "$1" >/dev/null
  local i
  for i in $(seq 1 60); do
    if ex systemctl is-system-running 2>/dev/null | grep -Eq 'running|degraded'; then break; fi
    sleep 1
  done
  ex systemctl is-system-running 2>/dev/null | grep -Eq 'running|degraded' || { echo "container did not boot systemd" >&2; ex journalctl -b --no-pager -u hatamoto-firstboot -u postgresql -u "postgresql@*" 2>&1 | tail -n 40 >&2; return 1; }
  dk cp "$E/asm/install.sh" "$C:/root/install.sh"
  dk cp "$E/img-check.sh" "$C:/root/img-check.sh"
  dk cp "$E/site-good" "$C:/srv-good"
  dk cp "$E/site-bad" "$C:/srv-bad"
  dk exec -d "$C" python3 -m http.server --bind 127.0.0.1 --directory /srv-good 8765
  dk exec -d "$C" python3 -m http.server --bind 127.0.0.1 --directory /srv-bad 8766
  for i in $(seq 1 20); do
    ex curl -fsS -o /dev/null "$GOOD/channels/stable.json" 2>/dev/null && return 0
    sleep 0.5
  done
  echo "mirror inside the container did not come up" >&2
  return 1
}

# inputs for a container first boot: test TLS, no firewall, local mirror.
fb_env() { echo "HATAMOTO_TLS=internal" "HATAMOTO_PUBLIC_IP=$1" "HATAMOTO_FIREWALL=0" "HATAMOTO_RELEASE_BASE=$GOOD"; }

# assert_stack LABEL IP: the running install answers like a real one.
assert_stack() {
  local label="$1" ip="$2" host link motd code page
  host="$(echo "$ip" | tr . -).sslip.io"
  check "$label: /health through Caddy is 200" \
    test "$(ex curl -sk -o /dev/null -w '%{http_code}' --resolve "$host:443:127.0.0.1" "https://$host/health")" = 200
  link="$(ex cat /var/lib/hatamoto-install/link 2>/dev/null | head -n1)"
  case "$link" in http*) pass "$label: admin address recorded ($link)" ;; *) fail "$label: admin address not recorded" ;; esac
  # The admin only serves browser navigations: send the headers a browser sends.
  page="$(ex curl -sk --resolve "$host:443:127.0.0.1" \
    -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document' -H 'Sec-Fetch-Site: none' -H 'Sec-Fetch-User: ?1' \
    "$link/setup" || true)"
  check "$label: /setup shows the code field" grep -q 'id="code"' <<<"$page"
  check "$label: /setup loads the setupcode module" grep -q 'setupcode' <<<"$page"
  motd="$(ex /etc/update-motd.d/99-hatamoto 2>&1 || true)"
  check "$label: login message shows the setup link" grep -q "$link/setup#code=" <<<"$motd"
  code="$(sed -n 's/.*#code=\([A-Z0-9-]*\).*/\1/p' <<<"$motd" | head -n1)"
  check "$label: setup code has the expected shape" grep -Eq '^[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$' <<<"$code"
  check "$label: backup timer is enabled" test "$(ex systemctl is-enabled hatamoto-backup.timer 2>/dev/null)" = enabled
  check "$label: :8090 is listening" test -n "$(ex ss -Hltn 'sport = :8090')"
  check "$label: :8090 listens on loopback only" \
    test -z "$(ex ss -Hltn 'sport = :8090' | awk '$4 !~ /^127\.0\.0\.1:/ && $4 !~ /^\[::1\]:/')"
}

run_distro() { # run_distro NAME BASE
  local name="$1" base="$2" img="hatamoto-install-test:$1-$$" ip out rc
  echo "== $name ($base)"
  dk build -q --build-arg "BASE=$base" -t "$img" "$here/test" >/dev/null
  IMAGES+=("$img")

  # Container 1: tamper refusal, sealed image, first boot.
  start_container "$img" "hm-inst-$name-a-$$"
  ip="$(dk inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$C")"
  out="$(ex env HATAMOTO_FIREWALL=0 "HATAMOTO_RELEASE_BASE=$BAD" sh /root/install.sh build 2>&1)" && rc=0 || rc=$?
  check "$name: tampered binary makes the build fail" test "$rc" -ne 0
  check "$name: the failed check is named" grep -Eqi 'sha256|size' <<<"$out" || printf '%s\n' "$out" | tail -n 15
  check "$name: nothing was installed from the tampered binary" ex test ! -e /opt/hatamoto/bin/hatamoto

  if ex env HATAMOTO_FIREWALL=0 "HATAMOTO_RELEASE_BASE=$GOOD" sh /root/install.sh build --seal >"$E/build-$name.log" 2>&1; then
    pass "$name: build --seal succeeds"
  else
    fail "$name: build --seal failed (tail of the log below)"; tail -n 30 "$E/build-$name.log"; return 0
  fi
  # A real seal is followed by power off; here journald keeps running and rewrites its journal.
  ex systemctl stop systemd-journald.socket systemd-journald-dev-log.socket systemd-journald-audit.socket systemd-journald.service >/dev/null 2>&1 || true
  ex sh -c 'find /var/log/journal -type f -delete 2>/dev/null; true'
  check "$name: sealed image passes the hygiene check" ex env IMG_CHECK_FIREWALL=0 bash /root/img-check.sh /
  ex sh -c 'echo HATAMOTO_SETUP_CODE=ABCD-EFGH-JKMN > /opt/hatamoto/config/hatamoto.env'
  if ex env IMG_CHECK_FIREWALL=0 bash /root/img-check.sh / >/dev/null 2>&1; then
    fail "$name: hygiene check missed a decoy secret"
  else
    pass "$name: hygiene check fails on a decoy secret"
  fi
  ex rm -f /opt/hatamoto/config/hatamoto.env

  # Snapshot the sealed machine with the inputs a marketplace image author would bake in,
  # to boot it for real below (Caddy is already running by then, as on a provider).
  ex sh -c 'printf "%s\n" HATAMOTO_TLS=internal HATAMOTO_PUBLIC_IP=203.0.113.50 HATAMOTO_FIREWALL=0 > /etc/default/hatamoto-install'
  sealed_img="hatamoto-install-sealed:$name-$$"
  dk commit "$C" "$sealed_img" >/dev/null && IMAGES+=("$sealed_img") || { fail "$name: cannot snapshot the sealed container"; return 0; }
  ex rm -f /etc/default/hatamoto-install

  # shellcheck disable=SC2046 # fb_env prints words on purpose
  if ex env $(fb_env "$ip") sh /root/install.sh first-boot >"$E/first-boot-$name.log" 2>&1; then
    pass "$name: first boot succeeds"
  else
    fail "$name: first boot failed (tail of the log below)"; tail -n 30 "$E/first-boot-$name.log"; return 0
  fi
  assert_stack "$name/sealed+first-boot" "$ip"
  dk rm -f -v "$C" >/dev/null 2>&1 || true

  # Container 3: the sealed image boots, hatamoto-firstboot.service runs by itself and must
  # replace the package Caddy config of the already-running Caddy.
  start_container "$sealed_img" "hm-inst-$name-c-$$" || { fail "$name: the sealed image did not boot"; return 0; }
  local i
  for i in $(seq 1 120); do
    ex test -e /var/lib/hatamoto-install/firstboot.done && break
    sleep 2
  done
  if ex test -e /var/lib/hatamoto-install/firstboot.done; then
    pass "$name: hatamoto-firstboot.service finished on the sealed image"
  else
    fail "$name: first boot did not finish on the sealed image"; ex journalctl -u hatamoto-firstboot --no-pager | tail -n 30; return 0
  fi
  assert_stack "$name/sealed-boot" 203.0.113.50
  check "$name/sealed-boot: first boot made a new PostgreSQL TLS key" ex test -s /etc/ssl/private/ssl-cert-snakeoil.key
  dk rm -f -v "$C" >/dev/null 2>&1 || true

  # Container 2: the all-in-one path, script read from stdin like curl | sh.
  start_container "$img" "hm-inst-$name-b-$$"
  ip="$(dk inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$C")"
  # shellcheck disable=SC2046
  if ex env $(fb_env "$ip") sh -c 'sh < /root/install.sh' >"$E/all-$name.log" 2>&1; then
    pass "$name: all-in-one install succeeds"
  else
    fail "$name: all-in-one install failed (tail of the log below)"; tail -n 30 "$E/all-$name.log"; return 0
  fi
  assert_stack "$name/all-in-one" "$ip"
  dk rm -f -v "$C" >/dev/null 2>&1 || true
}

case "$want" in
  ubuntu2404) run_distro ubuntu2404 ubuntu:24.04 ;;
  debian12) run_distro debian12 debian:12 ;;
  all) run_distro ubuntu2404 ubuntu:24.04; run_distro debian12 debian:12 ;;
esac

if [ "$FAILS" -ne 0 ]; then echo "CONTAINER E2E RESULT: FAIL ($FAILS)"; exit 1; fi
echo "CONTAINER E2E RESULT: PASS"
