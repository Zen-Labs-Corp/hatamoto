#!/usr/bin/env bash
# test-backup-swap.sh - setup_swap and setup_backups against fake commands and a fixture root.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }
bash "$here/build-install.sh" --out "$tmp/asm" >/dev/null
fb="$tmp/asm/firstboot.sh"
bin="$tmp/bin"; mkdir -p "$bin"
mk() { cat > "$bin/$1"; chmod +x "$bin/$1"; }
mk fallocate <<'F'
#!/bin/sh
echo "fallocate $*" >> "$CALLS"; : > "$3"
F
mk mkswap <<'F'
#!/bin/sh
echo "mkswap $*" >> "$CALLS"
F
mk swapon <<'F'
#!/bin/sh
echo "swapon $*" >> "$CALLS"; [ -z "${FAKE_SWAPON_FAIL:-}" ]
F
mk systemctl <<'F'
#!/bin/sh
echo "systemctl $*" >> "$CALLS"
F
run() { # run ROOT FN [ENV...]
  local r="$1" fn="$2"; shift 2
  # shellcheck disable=SC2016 # the single-quoted script expands in the child shell
  env PATH="$bin:$PATH" CALLS="$tmp/calls" HATAMOTO_INSTALL_ROOT="$r" HATAMOTO_INSTALL_SOURCE_ONLY=1 "$@" sh -c '. "$1"; "$2"' _ "$fb" "$fn"
}
mkroot() { # mkroot NAME KB [swaps-lines]
  local r="$tmp/$1"; rm -rf "$r"; mkdir -p "$r/proc" "$r/etc"
  printf 'MemTotal:       %s kB\nMemFree: 1 kB\n' "$2" > "$r/proc/meminfo"
  printf 'Filename Type Size Used Priority\n' > "$r/proc/swaps"
  echo "$r"
}

for case in "1gb 1000000 1048576" "2gb 2097152 2097152" "mid 1500000 1500000"; do
  # shellcheck disable=SC2086 # $case is split on purpose into positional parameters
  set -- $case
  r="$(mkroot "$1" "$2")"; : > "$tmp/calls"
  run "$r" setup_swap >/dev/null 2>&1 || fail "$1: swap failed"
  grep -qx "fallocate -l ${3}K $r/swapfile" "$tmp/calls" || fail "$1: size: $(cat "$tmp/calls")"
  [ "$(stat -c %a "$r/swapfile")" = 600 ] || fail "$1: mode"
  grep -qx "swapon $r/swapfile" "$tmp/calls" || fail "$1: swapon"
  [ "$(grep -c '^/swapfile none swap sw 0 0$' "$r/etc/fstab")" = 1 ] || fail "$1: fstab"
  pass "swap $1: $3 KiB"
done
r="$(mkroot small 500000)"; : > "$tmp/calls"; run "$r" setup_swap >/dev/null 2>&1
grep -q 'fallocate -l 1048576K' "$tmp/calls" || fail "minimum is 1 GiB"
r="$(mkroot big 4000000)"; : > "$tmp/calls"; run "$r" setup_swap >/dev/null 2>&1
{ [ ! -s "$tmp/calls" ] && [ ! -e "$r/swapfile" ]; } || fail "4 GB machine got swap"
pass "swap minimum 1 GiB; none above 2 GiB RAM"
r="$(mkroot active 1000000)"; echo '/dev/vda2 partition 1 0 -2' >> "$r/proc/swaps"; : > "$tmp/calls"
run "$r" setup_swap >/dev/null 2>&1; [ ! -e "$r/swapfile" ] || fail "swap added though active"
r="$(mkroot twice 1000000)"; run "$r" setup_swap >/dev/null 2>&1; run "$r" setup_swap >/dev/null 2>&1
[ "$(grep -c swapfile "$r/etc/fstab")" = 1 ] || fail "fstab duplicated"
pass "active swap respected; rerun keeps one fstab line"
r="$(mkroot nocontainer 1000000)"
run "$r" setup_swap FAKE_SWAPON_FAIL=1 >"$tmp/o" 2>&1 || fail "swapon failure fatal"
{ [ ! -e "$r/swapfile" ] && [ ! -e "$r/etc/fstab" ]; } || fail "leftovers after swapon failure"
grep -q 'continuing without it' "$tmp/o" || fail "no warning"
pass "swapon failure warns and cleans up"

r="$(mkroot bk 4000000)"; : > "$tmp/calls"
run "$r" setup_backups HATAMOTO_BACKUPS=1 >/dev/null 2>&1 || fail "backups failed"
s="$r/etc/systemd/system/hatamoto-backup.service"; t="$r/etc/systemd/system/hatamoto-backup.timer"
for w in Type=oneshot User=hatamoto WorkingDirectory=/opt/hatamoto EnvironmentFile=/opt/hatamoto/config/hatamoto.env \
  'ExecStart=/opt/hatamoto/bin/hatamoto backup create'; do grep -qxF "$w" "$s" || fail "service lacks $w"; done
for w in 'OnCalendar=*-*-* 03:30' RandomizedDelaySec=30min Persistent=true; do grep -qxF "$w" "$t" || fail "timer lacks $w"; done
if grep -q 'User=root' "$s"; then fail "User=root"; fi
if grep -Eiq 'password|secret|setup_code|DATABASE_URL=' "$s" "$t"; then fail "secret-looking text in units"; fi
grep -qx 'systemctl enable --now hatamoto-backup.timer' "$tmp/calls" || fail "timer not enabled"
pass "backup units"
before="$(sha256sum "$s" "$t")"; run "$r" setup_backups HATAMOTO_BACKUPS=1 >/dev/null 2>&1
[ "$before" = "$(sha256sum "$s" "$t")" ] || fail "rerun changed units"
: > "$tmp/calls"; run "$r" setup_backups HATAMOTO_BACKUPS=0 >/dev/null 2>&1
{ [ ! -e "$s" ] && [ ! -e "$t" ]; } || fail "units kept with backups off"
grep -qx 'systemctl disable --now hatamoto-backup.timer' "$tmp/calls" || fail "timer not disabled"
r="$(mkroot off 4000000)"; run "$r" setup_backups HATAMOTO_BACKUPS=0 >/dev/null 2>&1
[ -z "$(find "$r/etc" -type f)" ] || fail "backups off wrote files"
pass "rerun idempotent; backups off writes nothing and removes the timer"
echo "ok: test-backup-swap"
