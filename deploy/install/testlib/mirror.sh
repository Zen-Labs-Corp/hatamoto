#!/usr/bin/env bash
# mirror.sh - a local release site signed with throwaway keys, for the installer tests.
#
# Source it (it defines functions only):
#   . deploy/install/testlib/mirror.sh
#   mirror_init "$tmp/m"                  # build the tool, mint keys, empty stable channel
#   mirror_add v0.2.0 now:100             # fake binaries + signed record + channel entry
#   mirror_publish                        # sign the channel index with the manifest key
#   mirror_serve                          # sets M_URL (the release base) and M_PID
#   ... run the installer built with --keys-dir "$M_TRUST" against "$M_URL" ...
#   mirror_stop
#
# Nothing here touches the network. The signing tool is backend/tools/release
# built with -tags hatamoto_testkeys; it trusts the keys in $M_TRUST. After the
# first mirror_init, MIRROR_TOOL names the built tool so later mirrors reuse it.

# mirror_init DIR [KEYSDIR] (KEYSDIR: reuse the keys of an earlier mirror) sets M_DIR M_SRC M_SITE M_TRUST M_PRIV M_TOOL.
mirror_init() {
  local repo
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
  M_DIR="$1"
  M_SRC="$M_DIR/src"
  M_SITE="$M_DIR/site"
  M_TRUST="$M_DIR/trust"
  M_PRIV="$M_DIR/priv"
  M_TOOL="$M_DIR/release-tool"
  mkdir -p "$M_SRC" "$M_SITE/releases/channels" "$M_TRUST" "$M_PRIV"
  if [ -n "${MIRROR_TOOL:-}" ] && [ -x "$MIRROR_TOOL" ]; then
    cp "$MIRROR_TOOL" "$M_TOOL"
  else
    (cd "$repo/backend" && CGO_ENABLED=0 go build -tags hatamoto_testkeys -o "$M_TOOL" ./tools/release) ||
      { echo "mirror: cannot build tools/release" >&2; return 1; }
    MIRROR_TOOL="$M_TOOL"
  fi
  if [ -n "${2:-}" ]; then
    cp "$2"/priv/* "$M_PRIV/"
    cp "$2"/trust/* "$M_TRUST/"
  else
    (
      unset HATAMOTO_TEST_TRUST_DIR
      "$M_TOOL" keygen --kind release --out "$M_PRIV" >/dev/null
      "$M_TOOL" keygen --kind manifest --out "$M_PRIV" >/dev/null
    )
    cp "$M_PRIV/release.pub" "$M_TRUST/release-test.pub"
    cp "$M_PRIV/manifest.pub" "$M_TRUST/manifest.pub"
  fi
  printf '{"channel":"stable","issued_at":"","expires_at":"","releases":[],"yanked":[]}\n' > "$M_SRC/stable.json"
  M_PID=""
}

# mirror_tool runs the release tool with the test trust directory.
mirror_tool() {
  HATAMOTO_TEST_TRUST_DIR="$M_TRUST" "$M_TOOL" "$@"
}

# mirror_add VERSION RAMP adds a release (RAMP like now:100 or now:5,+2d:100).
# Each platform gets different small fake bytes.
mirror_add() {
  local v="$1" ramp="$2" rel p core kind patch minor
  rel="$M_SITE/releases/$v"
  mkdir -p "$rel"
  for p in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
    printf 'fake hatamoto %s %s\n' "$v" "$p" > "$rel/hatamoto-$p"
  done
  core="${v#v}"; core="${core%%-*}"
  patch="${core##*.}"; minor="${core#*.}"; minor="${minor%%.*}"
  if [ "$patch" != 0 ]; then kind="patch"; elif [ "$minor" != 0 ]; then kind="minor"; else kind="major"; fi
  printf '{"version":"%s","commit":"test","sdk":"1.0.0","schema_head":1}\n' "$v" > "$M_DIR/facts-$v.json"
  mirror_tool record --version "$v" --kind "$kind" --facts "$M_DIR/facts-$v.json" \
    --rollback-floor "$v" --min-from "$v" --allow-same --notes-url https://example.test/notes \
    --dir "$rel" --out "$rel/release.json" >/dev/null
  mirror_tool sign-record "$rel/release.json" --key "$M_PRIV/release.key" --out "$rel/release.json.sig" --allow-same >/dev/null
  mirror_tool channel add stable "$v" --record "$rel/release.json" --sig "$rel/release.json.sig" \
    --ramp "$ramp" --dir "$M_SRC" >/dev/null
}

# mirror_publish signs the channel source with the manifest key (valid 14 days).
mirror_publish() {
  mirror_tool channel resign --channel stable --in "$M_SRC" --out "$M_SITE/releases/channels" \
    --key "$M_PRIV/manifest.key" >/dev/null
}

# mirror_serve starts the site on a free loopback port: M_URL, M_PID.
mirror_serve() {
  local port
  port="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')"
  python3 -m http.server --bind 127.0.0.1 --directory "$M_SITE" "$port" >/dev/null 2>&1 &
  M_PID=$!
  M_URL="http://127.0.0.1:$port/releases"
  for _ in $(seq 1 40); do
    if curl -fsS -o /dev/null "$M_URL/channels/stable.json" 2>/dev/null; then return 0; fi
    sleep 0.25
  done
  echo "mirror: site did not come up" >&2
  return 1
}

mirror_stop() {
  [ -z "${M_PID:-}" ] || kill "$M_PID" 2>/dev/null || true
  M_PID=""
}

# mirror_tamper_binary VERSION PLATFORM: same size, different bytes.
mirror_tamper_binary() {
  local f="$M_SITE/releases/$1/hatamoto-$2"
  { head -c "$(( $(wc -c < "$f") - 1 ))" "$f"; printf 'X'; } > "$f.new"
  mv "$f.new" "$f"
}

# mirror_truncate_binary VERSION PLATFORM: one byte short.
mirror_truncate_binary() {
  local f="$M_SITE/releases/$1/hatamoto-$2"
  head -c "$(( $(wc -c < "$f") - 1 ))" "$f" > "$f.new"
  mv "$f.new" "$f"
}

# mirror_tamper_record VERSION: edit the record after it was signed.
mirror_tamper_record() {
  local f="$M_SITE/releases/$1/release.json"
  jq '.notes_url = "https://example.test/other"' "$f" > "$f.new"
  mv "$f.new" "$f"
}

# mirror_tamper_index: edit the published index after it was signed.
mirror_tamper_index() {
  local f="$M_SITE/releases/channels/stable.json"
  jq '.index.releases[0].halted = false | .index.yanked = []' "$f" > "$f.new"
  # Make sure the bytes really differ even when the edit was a no-op.
  jq '.index.expires_at = "2099-01-01T00:00:00Z"' "$f.new" > "$f"
  rm -f "$f.new"
}

# mirror_sign_index KEYFILE ISSUED EXPIRES: publish the channel source signed
# with any key (a release key, say) and any dates, using jq's compact form.
mirror_sign_index() {
  local keyfile="$1" issued="$2" expires="$3" out id seed
  out="$M_SITE/releases/channels/stable.json"
  id="$(jq -r .key_id "$keyfile")"
  jq --arg i "$issued" --arg e "$expires" --arg id "$id" \
    '{index: (.issued_at = $i | .expires_at = $e), signer: $id, algorithm: "ed25519", signature: ""}' \
    "$M_SRC/stable.json" > "$M_DIR/env.json"
  jq -c .index "$M_DIR/env.json" | tr -d '\n' > "$M_DIR/msg"
  seed="$M_DIR/seed.der"
  { printf '\060\056\002\001\000\060\005\006\003\053\145\160\004\042\004\040'
    jq -r .private "$keyfile" | base64 -d | head -c 32; } > "$seed"
  openssl pkey -inform DER -in "$seed" -out "$M_DIR/signer.pem"
  openssl pkeyutl -sign -rawin -inkey "$M_DIR/signer.pem" -in "$M_DIR/msg" -out "$M_DIR/msg.sig"
  jq --arg s "$(base64 -w0 < "$M_DIR/msg.sig")" '.signature = $s' "$M_DIR/env.json" > "$out"
  rm -f "$seed" "$M_DIR/signer.pem"
}
