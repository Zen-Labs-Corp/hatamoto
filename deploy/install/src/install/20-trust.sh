
# Trust: pick a release and fetch its binary only after the signed chain checks out.
#
# Chain (the same one the updater runs, backend/internal/release/verify.go):
#   channel index  -> signed by a manifest key from TRUSTED_MANIFEST_KEYS, fresh
#   release record -> signed by the release key named in the index entry
#   binary         -> size and sha256 from the record
# The keys are the generated block above: nothing in the environment can change them.
# Every refusal prints "FAILED CHECK: <name>: ..." and exits non-zero.

TRUST_TMP=""
TRUST_MAX_SIZE=268435456
TRUST_CHANNEL=stable

# trust_die removes the work directory, then dies naming the failed check.
trust_die() {
  [ -z "$TRUST_TMP" ] || rm -rf "$TRUST_TMP"
  TRUST_TMP=""
  die "FAILED CHECK: $1: $2"
}

# trust_valid_version succeeds for vMAJOR.MINOR.PATCH[-prerelease], one line only.
trust_valid_version() {
  case "$1" in *'
'*) return 1 ;; esac
  printf '%s\n' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$'
}

# trust_get URL OUTFILE downloads over https (or http on loopback, for tests).
trust_get() {
  case "$RELEASE_BASE" in
    https://*)
      curl --fail --silent --show-error --retry 3 --proto '=https' --proto-redir '=https' --tlsv1.2 --location \
        --output "$2" "$1" ;;
    http://127.0.0.1 | http://127.0.0.1/* | http://127.0.0.1:*)
      curl --fail --silent --show-error --retry 3 --proto '=http' --output "$2" "$1" ;;
    *)
      die "release base must be an https URL: $RELEASE_BASE" ;;
  esac
}

# trust_get_max URL OUTFILE MAXBYTES is trust_get with a size cap.
trust_get_max() {
  case "$RELEASE_BASE" in
    https://*)
      curl --fail --silent --show-error --retry 3 --proto '=https' --proto-redir '=https' --tlsv1.2 --location \
        --max-filesize "$3" --output "$2" "$1" ;;
    *)
      curl --fail --silent --show-error --retry 3 --proto '=http' \
        --max-filesize "$3" --output "$2" "$1" ;;
  esac
}

# trust_check_tools dies early when a command the chain needs is missing.
trust_check_tools() {
  need_cmd curl
  need_cmd jq
  need_cmd openssl
  need_cmd base64
  need_cmd sha256sum
  need_cmd awk
  need_cmd wc
  need_cmd tr
  case "$RELEASE_BASE" in
    https://* | http://127.0.0.1 | http://127.0.0.1/* | http://127.0.0.1:*) ;;
    *) die "release base must be an https URL: $RELEASE_BASE" ;;
  esac
}

# trust_key_b64 SET ID prints the base64 public key of ID in SET, or fails.
trust_key_b64() {
  printf '%s\n' "$1" | awk -v id="$2" '$1 == id { print $2; found = 1 } END { exit !found }'
}

# trust_verify SET ID MSGFILE SIGB64 succeeds when the Ed25519 signature is valid.
trust_verify() {
  tv_b64="$(trust_key_b64 "$1" "$2")" || return 1
  [ -n "$TRUST_TMP" ] || return 1
  # Raw key to DER with the fixed header (octal escapes: dash printf has no \x).
  { printf '\060\052\060\005\006\003\053\145\160\003\041\000'; printf '%s' "$tv_b64" | base64 -d; } |
    openssl pkey -pubin -inform DER -out "$TRUST_TMP/k.pem" 2>/dev/null || return 1
  printf '%s' "$4" | base64 -d > "$TRUST_TMP/sig.bin" 2>/dev/null || return 1
  [ -s "$TRUST_TMP/sig.bin" ] || return 1
  openssl pkeyutl -verify -pubin -inkey "$TRUST_TMP/k.pem" -rawin -in "$3" -sigfile "$TRUST_TMP/sig.bin" >/dev/null 2>&1
}

# trust_load_index fetches and verifies the channel index. It leaves the
# compact signed index in $TRUST_TMP/index.json and creates TRUST_TMP.
trust_load_index() {
  TRUST_TMP="$(mktemp -d)" || die "cannot create a work directory"
  chmod 0700 "$TRUST_TMP"
  trust_get "$RELEASE_BASE/channels/$TRUST_CHANNEL.json" "$TRUST_TMP/channel.json" ||
    trust_die index-download "cannot fetch $RELEASE_BASE/channels/$TRUST_CHANNEL.json"
  jq -e 'type == "object" and (.index | type == "object")' "$TRUST_TMP/channel.json" >/dev/null 2>&1 ||
    trust_die index-signature "the channel file is not a signed index"
  ti_signer="$(jq -r '.signer // "" | strings' "$TRUST_TMP/channel.json")"
  ti_alg="$(jq -r '.algorithm // "" | strings' "$TRUST_TMP/channel.json")"
  ti_sig="$(jq -r '.signature // "" | strings' "$TRUST_TMP/channel.json")"
  printf '%s\n' "$ti_signer" | grep -Eq '^[0-9a-f]{16}$' || trust_die index-signer "bad signer id"
  trust_key_b64 "$TRUSTED_MANIFEST_KEYS" "$ti_signer" >/dev/null ||
    trust_die index-signer "signer $ti_signer is not a trusted manifest key"
  case "$ti_alg" in "" | ed25519) ;; *) trust_die index-signature "unsupported algorithm: $ti_alg" ;; esac
  [ -n "$ti_sig" ] || trust_die index-signature "the index is unsigned"
  # The signed bytes are the index with all insignificant whitespace removed.
  jq -c '.index' "$TRUST_TMP/channel.json" | tr -d '\n' > "$TRUST_TMP/index.json"
  trust_verify "$TRUSTED_MANIFEST_KEYS" "$ti_signer" "$TRUST_TMP/index.json" "$ti_sig" ||
    trust_die index-signature "the signature does not match the index"
  [ "$(jq -r '.channel // ""' "$TRUST_TMP/index.json")" = "$TRUST_CHANNEL" ] ||
    trust_die index-channel "the index is not for the $TRUST_CHANNEL channel"
  ti_now="$(date -u +%s)"
  ti_fresh="$(jq -r --argjson now "$ti_now" '
    try (
      (.issued_at | fromdateiso8601) as $i | (.expires_at | fromdateiso8601) as $e |
      if $e <= $i or ($e - $i) > 1296000 then "lifetime"
      elif $i > ($now + 600) then "future"
      elif $now >= $e then "expired"
      else "ok" end
    ) catch "format"' "$TRUST_TMP/index.json")"
  case "$ti_fresh" in
    ok) ;;
    future) trust_die index-freshness "the index is dated in the future: is the clock of this machine wrong?" ;;
    expired) trust_die index-freshness "the index has expired: is the clock of this machine wrong, or is the mirror stale?" ;;
    *) trust_die index-freshness "the index carries no usable issue and expiry time ($ti_fresh)" ;;
  esac
}

# trust_check_entry VERSION: the version is listed, not halted and not yanked.
trust_check_entry() {
  jq -e --arg v "$1" '[.releases[]? | select(.version == $v)] | length == 1' "$TRUST_TMP/index.json" >/dev/null 2>&1 ||
    trust_die index-entry "$1 is not a release on the $TRUST_CHANNEL channel"
  if jq -e --arg v "$1" 'any(.yanked[]?; .version == $v)' "$TRUST_TMP/index.json" >/dev/null 2>&1; then
    trust_die index-yanked "$1 was yanked"
  fi
  if jq -e --arg v "$1" 'any(.releases[]?; .version == $v and .halted == true)' "$TRUST_TMP/index.json" >/dev/null 2>&1; then
    trust_die index-halted "$1 is halted"
  fi
}

# trust_select prints the newest plain x.y.z release whose ramp is at least $1 percent now.
# $1 is 100 (fully rolled out) or 1 (rollout started).
trust_select() {
  jq -r --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson min "$1" '
    def at: try (.from | fromdateiso8601) catch null;
    def pct:
      ($now | fromdateiso8601) as $n
      | [.ramp[]? | select((at) != null and (at) <= $n)]
      | if length == 0 then 0 else (max_by(at) | .pct) end
      | if . > 100 then 100 elif . < 0 then 0 else . end;
    [.yanked[]?.version] as $yanked
    | [.releases[]?
       | select(.halted != true)
       | select(.version as $v | ($yanked | index($v)) == null)
       | select(.version | test("^v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$"))
       | select(pct >= $min)]
    | sort_by(.version | ltrimstr("v") | split(".") | map(tonumber))
    | last | .version // empty' "$TRUST_TMP/index.json"
}

# trust_pick_version prints the version to install: HATAMOTO_VERSION when set
# (it must be a listed, live release), else the newest fully rolled-out one.
trust_pick_version() {
  trust_check_tools
  if [ -n "$IN_VERSION" ]; then
    trust_valid_version "$IN_VERSION" || die "FAILED CHECK: version: HATAMOTO_VERSION is not a version like v1.2.3"
  fi
  trust_load_index
  if [ -n "$IN_VERSION" ]; then
    trust_check_entry "$IN_VERSION"
    tp_version="$IN_VERSION"
  else
    tp_version="$(trust_select 100)"
    if [ -z "$tp_version" ]; then
      tp_version="$(trust_select 1)"
      [ -z "$tp_version" ] || warn "no release is fully rolled out yet; using $tp_version, which is still ramping"
    fi
    [ -n "$tp_version" ] || trust_die index-entry "no usable release on the $TRUST_CHANNEL channel; set HATAMOTO_VERSION"
  fi
  rm -rf "$TRUST_TMP"
  TRUST_TMP=""
  printf '%s\n' "$tp_version"
}

# trust_fetch VERSION OUTFILE downloads the binary for this CPU and writes it to
# OUTFILE only after every check passed. On any failure OUTFILE is not created.
trust_fetch() {
  [ $# -eq 2 ] || die "usage: trust_fetch VERSION OUTFILE"
  tf_version="$1"
  tf_out="$2"
  trust_valid_version "$tf_version" || die "FAILED CHECK: version: not a version like v1.2.3"
  trust_check_tools
  tf_arch="$(arch)"
  trust_load_index
  trust_check_entry "$tf_version"

  tf_recsig="$(jq -r --arg v "$tf_version" '.releases[] | select(.version == $v) | .record_sig // "" | strings' "$TRUST_TMP/index.json")"
  tf_keyid="${tf_recsig%%:*}"
  tf_sig="${tf_recsig#*:}"
  case "$tf_recsig" in *:*) ;; *) trust_die record-signature "the index entry carries no record signature" ;; esac
  printf '%s\n' "$tf_keyid" | grep -Eq '^[0-9a-f]{16}$' || trust_die record-signature "bad key id in the record signature"
  trust_key_b64 "$TRUSTED_RELEASE_KEYS" "$tf_keyid" >/dev/null ||
    trust_die record-signature "key $tf_keyid is not a trusted release key"

  trust_get "$RELEASE_BASE/$tf_version/release.json" "$TRUST_TMP/release.json" ||
    trust_die record-download "cannot fetch the release record for $tf_version"
  # The signature covers the file exactly as downloaded.
  trust_verify "$TRUSTED_RELEASE_KEYS" "$tf_keyid" "$TRUST_TMP/release.json" "$tf_sig" ||
    trust_die record-signature "the release record does not match its signature"
  [ "$(jq -r '.version // ""' "$TRUST_TMP/release.json")" = "$tf_version" ] ||
    trust_die record-version "the signed record describes another version"

  tf_facts="$(jq -r --arg p "linux-$tf_arch" '.artifacts[$p] | "\(.sha256) \(.size)"' "$TRUST_TMP/release.json" 2>/dev/null)" ||
    trust_die record-version "the record has no linux-$tf_arch build"
  tf_sha="${tf_facts% *}"
  tf_size="${tf_facts#* }"
  printf '%s\n' "$tf_sha" | grep -Eq '^[0-9a-fA-F]{64}$' || trust_die artifact-sha256 "the record carries no valid sha256"
  printf '%s\n' "$tf_size" | grep -Eq '^[1-9][0-9]*$' || trust_die artifact-size "the record carries no valid size"
  [ "$tf_size" -le "$TRUST_MAX_SIZE" ] || trust_die artifact-size "the record size is above the limit"

  log "downloading hatamoto $tf_version (linux-$tf_arch)"
  trust_get_max "$RELEASE_BASE/$tf_version/hatamoto-linux-$tf_arch" "$TRUST_TMP/binary" "$tf_size" ||
    trust_die artifact-size "download failed or larger than the signed size of $tf_size bytes"
  tf_got="$(wc -c < "$TRUST_TMP/binary" | tr -d ' ')"
  [ "$tf_got" = "$tf_size" ] || trust_die artifact-size "got $tf_got bytes, the signed record says $tf_size"
  tf_sum="$(sha256sum "$TRUST_TMP/binary" | awk '{ print $1 }')"
  [ "$tf_sum" = "$(printf '%s' "$tf_sha" | tr 'A-F' 'a-f')" ] ||
    trust_die artifact-sha256 "the binary does not match the signed record"

  chmod 0755 "$TRUST_TMP/binary"
  mv -f "$TRUST_TMP/binary" "$tf_out" || trust_die artifact-install "cannot write $tf_out"
  rm -rf "$TRUST_TMP"
  TRUST_TMP=""
  log "verified hatamoto $tf_version"
}
