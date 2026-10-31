#!/usr/bin/env bash
# test-assemble.sh - the assembler produces valid, deterministic, key-pinned scripts.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

bash "$here/build-install.sh" --out "$tmp/a" >/dev/null
bash "$here/build-install.sh" --out "$tmp/b" >/dev/null

for f in install.sh firstboot.sh; do
  [ -x "$tmp/a/$f" ] || fail "$f is not executable"
  sh -n "$tmp/a/$f" || fail "sh -n $f"
  if command -v dash >/dev/null; then dash -n "$tmp/a/$f" || fail "dash -n $f"; fi
  [ "$(tail -n1 "$tmp/a/$f")" = 'main "$@"' ] || fail "$f: last line is not main \"\$@\""
  [ "$(head -n1 "$tmp/a/$f")" = '#!/bin/sh' ] || fail "$f: first line is not #!/bin/sh"
  cmp -s "$tmp/a/$f" "$tmp/b/$f" || fail "$f differs between two assemblies"
done

grep -q '^TRUSTED_MANIFEST_KEYS=' "$tmp/a/install.sh" || fail "no manifest key block"
grep -q '^TRUSTED_RELEASE_KEYS=' "$tmp/a/install.sh" || fail "no release key block"
if grep -q 'TRUSTED_.*KEYS' "$tmp/a/firstboot.sh"; then fail "firstboot.sh carries trust keys"; fi

# The floor comes from MIN_CORE_VERSION, in install.sh only.
want="$(cat "$here/MIN_CORE_VERSION")"
[ "$(grep -c '^INSTALLER_MIN_VERSION=' "$tmp/a/install.sh")" = 1 ] || fail "install.sh needs exactly one floor line"
[ "$(grep '^INSTALLER_MIN_VERSION=' "$tmp/a/install.sh")" = "INSTALLER_MIN_VERSION='$want'" ] || fail "floor line differs from MIN_CORE_VERSION"
if grep -q 'INSTALLER_MIN_VERSION=' "$tmp/a/firstboot.sh"; then fail "firstboot.sh carries the floor"; fi

# --floor-file changes the floor; bad floor files are refused.
printf 'v2.3.4\n' > "$tmp/floor"
bash "$here/build-install.sh" --floor-file "$tmp/floor" --out "$tmp/f" >/dev/null
grep -qx "INSTALLER_MIN_VERSION='v2.3.4'" "$tmp/f/install.sh" || fail "--floor-file not honoured"
printf 'v2.3.4' > "$tmp/floor"
bash "$here/build-install.sh" --floor-file "$tmp/floor" --out "$tmp/f2" >/dev/null
cmp -s "$tmp/f/install.sh" "$tmp/f2/install.sh" || fail "floor file without a trailing newline changed the output"
: > "$tmp/floor.empty"
printf '1.0.2\n' > "$tmp/floor.nov"
printf 'v1.0\n' > "$tmp/floor.short"
printf 'v1.0.2\nv1.0.3\n' > "$tmp/floor.multi"
for b in missing empty nov short multi; do
  case "$b" in missing) ff="$tmp/floor.none" ;; *) ff="$tmp/floor.$b" ;; esac
  if bash "$here/build-install.sh" --floor-file "$ff" --out "$tmp/g" 2>"$tmp/err"; then fail "bad floor file accepted: $b"; fi
  grep -q 'floor file' "$tmp/err" || fail "bad floor error not named for $b: $(cat "$tmp/err")"
done

# Source-only mode defines functions and runs nothing.
HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; command -v payload_unit >/dev/null && command -v main >/dev/null' _ "$tmp/a/install.sh" ||
  fail "source-only mode did not define the functions"

# The unit payload is the tracked unit, byte for byte.
HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; payload_unit' _ "$tmp/a/install.sh" > "$tmp/unit"
cmp -s "$tmp/unit" "$repo/deploy/systemd/hatamoto-api.service" || fail "unit payload differs from the tracked unit"

# A key file whose name fits no set is refused, as trust.go refuses it.
mkdir "$tmp/keys"
cp "$repo"/backend/internal/release/keys/*.pub "$tmp/keys/"
cp "$tmp/keys/manifest.pub" "$tmp/keys/stray.pub"
if bash "$here/build-install.sh" --keys-dir "$tmp/keys" --out "$tmp/c" 2>"$tmp/err"; then fail "stray key file accepted"; fi
grep -q 'unexpected key file name: stray.pub' "$tmp/err" || fail "stray key error not named: $(cat "$tmp/err")"
rm "$tmp/keys/stray.pub"

# Files that are not *.pub are invisible, as to the Go loader: the output does not change.
echo 'not a key' > "$tmp/keys/README"
echo 'junk' > "$tmp/keys/release-extra"
bash "$here/build-install.sh" --keys-dir "$tmp/keys" --out "$tmp/e" || fail "non-.pub files broke the build"
cmp -s "$tmp/a/install.sh" "$tmp/e/install.sh" || fail "non-.pub files changed install.sh"
rm "$tmp/keys/README" "$tmp/keys/release-extra"

# A key_id that does not match the key is refused.
jq '.key_id = "0000000000000000"' "$tmp/keys/manifest.pub" > "$tmp/manifest.new"
mv "$tmp/manifest.new" "$tmp/keys/manifest.pub"
if bash "$here/build-install.sh" --keys-dir "$tmp/keys" --out "$tmp/d" 2>"$tmp/err"; then fail "wrong key_id accepted"; fi
grep -q 'key_id does not match' "$tmp/err" || fail "wrong key_id error not named"

echo "ok: test-assemble"
