#!/usr/bin/env bash
# test-trust.sh - the installer refuses anything the signed chain does not prove.
# Uses a local mirror signed with throwaway keys and an installer assembled with
# those keys (build-install.sh --keys-dir); no network, no root.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
cleanup() { mirror_stop 2>/dev/null || true; rm -rf "$tmp"; }
trap cleanup EXIT

# shellcheck source=/dev/null # mirror.sh is checked on its own
. "$here/testlib/mirror.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

case "$(uname -m)" in
  x86_64 | amd64) plat=linux-amd64 ;;
  aarch64 | arm64) plat=linux-arm64 ;;
  *) echo "SKIPPED: unsupported CPU $(uname -m)"; exit 0 ;;
esac

for c in jq openssl python3 curl go base64; do
  command -v "$c" >/dev/null || fail "missing $c"
done

mirror_init "$tmp/m"
bash "$here/build-install.sh" --keys-dir "$M_TRUST" --out "$tmp/test-asm" >/dev/null
bash "$here/build-install.sh" --out "$tmp/prod-asm" >/dev/null
script="$tmp/test-asm/install.sh"

# Releases: v0.2.0 is the expected default.
mirror_add v0.1.0 now:100
mirror_add v0.2.0 now:100
mirror_add v0.2.5 now:100
mirror_add v0.3.0 now:100
mirror_add v0.4.0 now:5,+2d:100
mirror_add v0.5.0 +2d:100
mirror_add v0.6.0-rc.1 now:100
mirror_add v1.0.0 +2d:100
mirror_tool channel halt stable v0.2.5 --reason testing --dir "$M_SRC" >/dev/null
mirror_tool channel yank stable v0.3.0 --reason "bad build" --dir "$M_SRC" >/dev/null
mirror_publish
mirror_serve
cp -a "$M_SITE" "$tmp/site.good"
restore() { rm -rf "${M_SITE:?}"/* "${M_SITE:?}"/.[!.]* 2>/dev/null || true; cp -a "$tmp/site.good/." "$M_SITE/"; }

# dl SCRIPT VERSION OUT [ENV...]: run the download seam, stderr to $tmp/err.
dl() {
  local s="$1" v="$2" out="$3"; shift 3
  env HATAMOTO_RELEASE_BASE="$M_URL" "$@" sh "$s" download "$v" "$out" 2>"$tmp/err" >"$tmp/stdout"
}

# refused NAME SCRIPT VERSION [ENV...]: must exit non-zero, name the check, leave no output file.
refused() {
  local name="$1" s="$2" v="$3"; shift 3
  rm -f "$tmp/out.bin"
  if dl "$s" "$v" "$tmp/out.bin" "$@"; then fail "$name: installer accepted it"; fi
  grep -q "FAILED CHECK: $name" "$tmp/err" || fail "$name: not named in: $(cat "$tmp/err")"
  [ ! -e "$tmp/out.bin" ] || fail "$name: output file exists"
  pass "refused: $name"
}

# shellcheck disable=SC2016 # the single-quoted script expands in the child shell
picked() { env HATAMOTO_RELEASE_BASE="$M_URL" HATAMOTO_INSTALL_SOURCE_ONLY=1 "$@" sh -c '. "$1"; trust_pick_version' _ "$script" 2>"$tmp/err"; }

# --- good paths
dl "$script" v0.2.0 "$tmp/out.bin" || fail "good download failed: $(cat "$tmp/err")"
cmp -s "$tmp/out.bin" "$M_SITE/releases/v0.2.0/hatamoto-$plat" || fail "downloaded bytes differ from the fixture"
[ -x "$tmp/out.bin" ] || fail "downloaded file is not executable"
pass "good download equals the fixture"

[ "$(picked)" = v0.2.0 ] || fail "default pick is not v0.2.0 (got $(picked || true))"
pass "default: highest fully ramped, not yanked/halted/future/prerelease"
[ "$(picked HATAMOTO_VERSION=v0.4.0)" = v0.4.0 ] || fail "explicit ramping version refused"
pass "explicit version honoured"

# --- fallback and no usable release
mirror_stop
mirror_init "$tmp/m2" "$tmp/m"
mirror_add v0.7.0 now:5
mirror_add v0.8.0 now:5
mirror_add v0.9.0 +2d:100
mirror_publish
mirror_serve
[ "$(picked)" = v0.8.0 ] || fail "fallback pick is not v0.8.0"
grep -q 'still ramping' "$tmp/err" || fail "fallback does not warn"
pass "fallback: highest started ramp, with a warning"
mirror_stop
mirror_init "$tmp/m3" "$tmp/m"
mirror_add v0.9.0 +2d:100
mirror_publish
mirror_serve
if picked >/dev/null; then fail "picked a release with nothing started"; fi
grep -q 'set HATAMOTO_VERSION' "$tmp/err" || fail "no hint to set HATAMOTO_VERSION"
pass "nothing usable: dies with a hint"
mirror_stop
# Back to the first mirror's files and key set.
rm -rf "$tmp/m2" "$tmp/m3"
M_DIR="$tmp/m"; M_SRC="$M_DIR/src"; M_SITE="$M_DIR/site"; M_TRUST="$M_DIR/trust"; M_PRIV="$M_DIR/priv"
# shellcheck disable=SC2034 # M_TOOL is read by testlib/mirror.sh
M_TOOL="$M_DIR/release-tool"
mirror_serve

# --- refusals
mirror_tamper_binary v0.2.0 "$plat"; refused artifact-sha256 "$script" v0.2.0; restore
mirror_truncate_binary v0.2.0 "$plat"; refused artifact-size "$script" v0.2.0; restore
mirror_tamper_record v0.2.0; refused record-signature "$script" v0.2.0; restore
mirror_tamper_index; refused index-signature "$script" v0.2.0; restore
mirror_sign_index "$M_PRIV/release.key" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(date -u -d '+10 days' +%Y-%m-%dT%H:%M:%SZ)"
refused index-signer "$script" v0.2.0; restore
mirror_sign_index "$M_PRIV/manifest.key" "$(date -u -d '-20 days' +%Y-%m-%dT%H:%M:%SZ)" "$(date -u -d '-6 days' +%Y-%m-%dT%H:%M:%SZ)"
refused index-freshness "$script" v0.2.0; grep -q 'expired' "$tmp/err" || fail "expiry not explained"; restore
mirror_sign_index "$M_PRIV/manifest.key" "$(date -u -d '+2 days' +%Y-%m-%dT%H:%M:%SZ)" "$(date -u -d '+9 days' +%Y-%m-%dT%H:%M:%SZ)"
refused index-freshness "$script" v0.2.0; restore
mirror_sign_index "$M_PRIV/manifest.key" "$(date -u -d '-1 day' +%Y-%m-%dT%H:%M:%SZ)" "$(date -u -d '+30 days' +%Y-%m-%dT%H:%M:%SZ)"
refused index-freshness "$script" v0.2.0; restore
# The index is signed, but the record of the entry is from another version's signer: swap two records.
cp "$M_SITE/releases/v0.1.0/release.json" "$M_SITE/releases/v0.2.0/release.json"
refused record-signature "$script" v0.2.0; restore

refused index-yanked "$script" v0.3.0
refused index-halted "$script" v0.2.5
refused index-entry "$script" v9.9.9
refused version "$script" '../x'
refused version "$script" 'v1.2'
# HATAMOTO_VERSION goes through the same check.
if picked HATAMOTO_VERSION='../x' >/dev/null; then fail "HATAMOTO_VERSION=../x accepted"; fi
grep -q 'FAILED CHECK: version' "$tmp/err" || fail "bad HATAMOTO_VERSION not named"
pass "refused: HATAMOTO_VERSION ../x"
if picked HATAMOTO_VERSION=v0.3.0 >/dev/null; then fail "yanked HATAMOTO_VERSION accepted"; fi
pass "refused: yanked HATAMOTO_VERSION"

# A plain-http base that is not loopback is refused before any request.
rm -f "$tmp/out.bin"
if env HATAMOTO_RELEASE_BASE=http://example.com/releases sh "$script" download v0.2.0 "$tmp/out.bin" 2>"$tmp/err"; then fail "non-loopback http accepted"; fi
grep -q 'https' "$tmp/err" || fail "http refusal not explained"
pass "refused: non-loopback http base"
for tb_bad in http://127.0.0.1:80@example.com/releases http://127.0.0.1:x@evil/ http://127.0.0.1.evil.com/; do
  if env HATAMOTO_RELEASE_BASE="$tb_bad" sh "$script" download v0.2.0 "$tmp/out.bin" 2>"$tmp/err"; then fail "base $tb_bad accepted"; fi
  grep -q 'https' "$tmp/err" || fail "base $tb_bad refusal not explained"
done
pass "refused: loopback look-alike http bases"

# Oversized index and record are refused (1 MiB cap).
head -c 2000000 /dev/zero | tr '\0' ' ' >> "$M_SITE/releases/channels/stable.json"
refused index-download "$script" v0.2.0; restore
head -c 2000000 /dev/zero | tr '\0' ' ' >> "$M_SITE/releases/v0.2.0/release.json"
refused record-download "$script" v0.2.0; restore

# Escaped & in a yank reason: jq re-encodes it, so the signature no longer matches. Documented fail-closed.
mirror_tool channel yank stable v0.1.0 --reason "fix & retest" --dir "$M_SRC" >/dev/null
mirror_publish
grep -q 'u0026' "$M_SITE/releases/channels/stable.json" || fail "fixture lost the escaped ampersand"
refused index-signature "$script" v0.2.0
pass "fail-closed on escaped characters (documented limit)"
restore

# --- production keys never accept a test-signed mirror (ISC-A-3)
dl "$tmp/prod-asm/install.sh" v0.2.0 "$tmp/out.bin" && fail "production script accepted a test-signed mirror"
rm -f "$tmp/out.bin"
refused index-signer "$tmp/prod-asm/install.sh" v0.2.0
refused index-signer "$tmp/prod-asm/install.sh" v0.2.0 \
  "TRUSTED_MANIFEST_KEYS=$(jq -r '.key_id + " " + .public' "$M_TRUST/manifest.pub")" \
  "TRUSTED_RELEASE_KEYS=$(jq -r '.key_id + " " + .public' "$M_TRUST/release-test.pub")" \
  HATAMOTO_TEST_TRUST_DIR="$M_TRUST"
grep -q 'HATAMOTO_TEST_TRUST_DIR\|HATAMOTO_TRUST' "$tmp/prod-asm/install.sh" && fail "the script reads a trust override from the environment"
pass "production keys ignore any environment override"

echo "ok: test-trust"
