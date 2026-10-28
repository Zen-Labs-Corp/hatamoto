#!/usr/bin/env bash
# test-publish-install.sh - tests scripts/publish-install.sh in a throwaway git
# repo with a fake assembler and fake gh/ssh/scp on PATH; a local directory
# stands in for the server. No real host, no network.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
ROOT=$PWD
bash -n "$ROOT/scripts/publish-install.sh"

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }

# Throwaway checkout at tag v1.2.3 with a fake assembler.
R="$W/repo"; mkdir -p "$R/scripts" "$R/deploy/install" "$W/stubs" "$W/box" "$W/asset"
cp "$ROOT/scripts/publish-install.sh" "$ROOT/scripts/deploy-host.sh" "$R/scripts/"
cat > "$R/deploy/install/build-install.sh" <<'B'
#!/usr/bin/env bash
# fake assembler: --out DIR
mkdir -p "$2"; printf '#!/bin/sh\necho installer\n' > "$2/install.sh"
B
git -C "$R" init -q
git -C "$R" add -A
git -C "$R" -c user.name=t -c user.email=t@example.test commit -q -m init
git -C "$R" tag v1.2.3
printf '#!/bin/sh\necho installer\n' > "$W/asset/install.sh"

cat > "$W/stubs/gh" <<'S'
#!/usr/bin/env bash
echo "gh $*" >> "$STUB_LOG"
dir=""; while [[ $# -gt 0 ]]; do [[ $1 == --dir ]] && dir=$2; shift; done
mkdir -p "$dir"; cp "$GH_ASSET/install.sh" "$dir/install.sh"
S
cat > "$W/stubs/scp" <<'S'
#!/usr/bin/env bash
echo "scp $*" >> "$STUB_LOG"
args=("$@"); dest=${args[-1]#*:}; src=${args[-2]}
cp "$src" "$dest"
[[ -z ${STUB_CORRUPT:-} ]] || echo extra >> "$dest"
S
cat > "$W/stubs/ssh" <<'S'
#!/usr/bin/env bash
echo "ssh $*" >> "$STUB_LOG"
sh -c "$2"
S
chmod +x "$W"/stubs/*
export STUB_LOG="$W/log" GH_ASSET="$W/asset" DEPLOY_HOST=stubhost REMOTE_PUBLIC="$W/box"

run() { # run <args...>: sets OUT and RC
  : > "$STUB_LOG"
  set +e
  OUT=$(cd "$R" && PATH="$W/stubs:$PATH" scripts/publish-install.sh "$@" 2>&1)
  RC=$?
  set -e
}

# 1. Happy path: gh, then scp, then ssh hash, then ssh mv; file is in place.
run v1.2.3
[[ $RC == 0 ]] || { echo "$OUT"; fail "happy path exited $RC"; }
cmp -s "$W/asset/install.sh" "$W/box/install.sh" || fail "install.sh missing or different on the box"
[[ ! -e "$W/box/.install.sh.new" ]] || fail "temporary file left behind"
order=$(awk '{print $1 ($1=="ssh" ? ($0 ~ /mv -f/ ? ":mv" : ":hash") : "")}' "$STUB_LOG" | tr '\n' ' ')
[[ "$order" == "gh scp ssh:hash ssh:mv " ]] || fail "wrong call order: $order"
grep -q 'scp.* stubhost:.*/\.install\.sh\.new' "$STUB_LOG" || fail "upload did not use the temporary name"
pass "happy path; gh, scp, remote hash, atomic mv"
rm -f "$W/box/install.sh"

# 2. Built file differs from the release asset: abort before any scp/ssh.
printf 'tampered\n' > "$W/asset/install.sh"
run v1.2.3
[[ $RC != 0 ]] || fail "sha mismatch accepted"
if grep -qE '^(scp|ssh)' "$STUB_LOG"; then fail "remote call after a sha mismatch"; fi
[[ ! -e "$W/box/install.sh" ]] || fail "install.sh written despite mismatch"
pass "sha mismatch aborts before any scp"
printf '#!/bin/sh\necho installer\n' > "$W/asset/install.sh"

# 3. Tag mismatch and dirty tree refuse before anything runs.
run v1.2.4
[[ $RC != 0 ]] || fail "wrong tag accepted"
[[ ! -s "$STUB_LOG" ]] || fail "tools ran for a wrong tag"
echo dirty > "$R/scripts/stray"
run v1.2.3
[[ $RC != 0 ]] || fail "dirty tree accepted"
[[ ! -s "$STUB_LOG" ]] || fail "tools ran on a dirty tree"
rm -f "$R/scripts/stray"
run bad-tag
[[ $RC != 0 ]] || fail "malformed tag accepted"
pass "wrong tag, dirty tree and malformed tag refused"

# 4. Dry run: prints the steps, calls nothing.
run v1.2.3 --dry-run
[[ $RC == 0 ]] || { echo "$OUT"; fail "dry-run exited $RC"; }
[[ ! -s "$STUB_LOG" ]] || fail "dry-run called a stub"
[[ ! -e "$W/box/install.sh" ]] || fail "dry-run wrote install.sh"
grep -q 'DRY: ' <<<"$OUT" || fail "dry-run printed no steps"
if grep -q stubhost <<<"$OUT"; then fail "dry-run printed the host"; fi
pass "dry-run makes no remote call"

# 5. Remote hash mismatch: the served install.sh is not replaced.
printf 'old\n' > "$W/box/install.sh"
STUB_CORRUPT=1 run v1.2.3
[[ $RC != 0 ]] || fail "remote hash mismatch accepted"
if grep -q 'mv -f' "$STUB_LOG"; then fail "mv ran after a remote hash mismatch"; fi
[[ $(cat "$W/box/install.sh") == old ]] || fail "served install.sh was replaced"
[[ ! -e "$W/box/.install.sh.new" ]] || fail "temporary file left on the box"
pass "remote hash mismatch leaves install.sh untouched"
echo "publish-install tests ok"
