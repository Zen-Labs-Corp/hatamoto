#!/usr/bin/env bash
# Seed Settings > Backups for the admin QA harness: one ok backup (a sidecar plus a
# small tar) and one failed record under $HATAMOTO_DATA/backups, so strict mode has
# rows to check. The files are 0600 like the real ones. Run it after the server is
# up (the page reads the directory on every load); run-local.sh does.
# Usage: HATAMOTO_DATA=/tmp/hm-data scripts/admin-qa/backups-seed.sh
set -Eeuo pipefail
: "${HATAMOTO_DATA:?set HATAMOTO_DATA to the instance data directory}"
dir=$HATAMOTO_DATA/backups
mkdir -p "$dir"
chmod 700 "$dir"

ok=20260101T030000Z-manual
fail=20260102T030000Z-pre_update
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf 'qa fixture, not a real dump\n' > "$work/core.dump"
tar -C "$work" -cf "$dir/$ok.tar" core.dump
size=$(stat -c %s "$dir/$ok.tar")

cat > "$dir/$ok.json" <<JSON
{"id":"$ok","kind":"manual","state":"ok","percent":100,"created":"2026-01-01T03:00:00Z","size":$size,"parts":["core.dump"]}
JSON
cat > "$dir/$fail.json" <<JSON
{"id":"$fail","kind":"pre_update","target":"v1.5.0","state":"failed","percent":40,"error":"not enough free space","created":"2026-01-02T03:00:00Z"}
JSON
chmod 600 "$dir/$ok.tar" "$dir/$ok.json" "$dir/$fail.json"
echo "backups-seed: $ok (ok), $fail (failed) in $dir"
