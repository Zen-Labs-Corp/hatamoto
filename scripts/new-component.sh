#!/usr/bin/env bash
# new-component.sh <name> - scaffold an admin component with all five artefacts
# the design contract needs: partial + token-only CSS, ui struct, golden test,
# components.json entry, docs subsection and styleguide sample.
#   scripts/new-component.sh rating
#   scripts/new-component.sh --self-test   (scaffolds "demo" in a throwaway worktree)
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"

self_test() {
  tmp=$(mktemp -d)
  trap 'git -C "$ROOT" worktree remove --force "$tmp/wt" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT
  git -C "$ROOT" worktree add -q --detach "$tmp/wt"
  # carry over uncommitted work, so the test sees what the caller sees
  git -C "$ROOT" diff HEAD --binary | git -C "$tmp/wt" apply --allow-empty
  git -C "$ROOT" ls-files -o --exclude-standard -z | while IFS= read -r -d '' f; do
    mkdir -p "$tmp/wt/$(dirname "$f")"; cp "$ROOT/$f" "$tmp/wt/$f"
  done
  (cd "$tmp/wt" && bash scripts/new-component.sh demo)
  grep -q '^### Demo$' "$tmp/wt/docs/ADMIN_UI_RULES.md" || { echo "self-test FAILED: docs lack the ### Demo subsection" >&2; exit 1; }
  (cd "$tmp/wt/backend" && go test ./internal/admin ./internal/admin/designcheck ./internal/admin/a11y)
  echo "new-component self-test OK"
}

if [ "${1:-}" = "--self-test" ]; then self_test; exit 0; fi

name="${1:-}"
[[ "$name" =~ ^[a-z][a-z0-9-]*$ ]] || { echo "usage: new-component.sh <name>  (lowercase letters, digits, dashes)" >&2; exit 2; }
command -v jq >/dev/null || { echo "new-component.sh needs jq" >&2; exit 2; }

# CamelCase: rating-bar -> RatingBar
camel=""; IFS=- read -ra parts <<<"$name"
for p in "${parts[@]}"; do camel+="$(tr '[:lower:]' '[:upper:]' <<<"${p:0:1}")${p:1}"; done

A="$ROOT/backend/web/admin"; G="$ROOT/backend/internal/admin"
tpl="$A/partials/$name.html"; css="$A/css/partials/$name.css"
uigo="$G/ui/$name.go"; ptest="$G/partial_${name//-/_}_test.go"
spec="$A/components.json"; docs="$ROOT/docs/ADMIN_UI_RULES.md"; sg="$G/styleguide.go"

for f in "$tpl" "$css" "$uigo" "$ptest"; do
  [ ! -e "$f" ] || { echo "refusing: $f already exists" >&2; exit 1; }
done
! jq -e --arg n "$name" 'has($n)' "$spec" >/dev/null || { echo "refusing: components.json already has $name" >&2; exit 1; }
! grep -rqE "^type $camel " "$G/ui" || { echo "refusing: ui.$camel already exists" >&2; exit 1; }
grep -q '<!-- new-components-above -->' "$docs" || { echo "docs marker missing" >&2; exit 1; }
grep -q 'new-components-above (scripts' "$sg" || { echo "styleguide marker missing" >&2; exit 1; }

# From here on, any failure undoes this run, so a re-run is not refused.
bak=$(mktemp -d)
cp "$spec" "$bak/spec"; cp "$docs" "$bak/docs"; cp "$sg" "$bak/sg"
rollback() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "new-component.sh failed; removing what it created" >&2
    rm -f "$tpl" "$css" "$uigo" "$ptest" "$spec.new" "$docs.new" "$sg.new"
    rm -f "$G/testdata/golden/partials/hm_$name".*
    cp "$bak/spec" "$spec"; cp "$bak/docs" "$docs"; cp "$bak/sg" "$sg"
  fi
  rm -rf "$bak"
  return "$status"
}
trap rollback EXIT

cat >"$tpl" <<EOF
{{define "hm/$name"}}<div class="hm-$name">{{.Label}}</div>{{end}}
EOF

cat >"$css" <<EOF
/* $camel. Token values only; the contract is components.json. */
.hm-$name {
  border-radius: var(--radius-md); padding: var(--space-2) var(--space-3); font-size: var(--text-sm);
  background: var(--color-kasumi); color: var(--color-sumi);
}
EOF

cat >"$uigo" <<EOF
package ui

// $camel is the hm/$name partial.
type $camel struct {
	Label string
}
EOF

cat >"$ptest" <<EOF
package admin

import (
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartial$camel(t *testing.T) {
	a := newPartialAdmin(t)
	checkPartial(t, a, "hm/$name", "default", ui.$camel{Label: "Sample"})
}
EOF

# components.json: append textually so the existing hand-formatted entries stay as they are.
entry=$(jq -n --arg n "$name" --arg sel ".hm-$name" --arg own "css/partials/$name.css" --arg p "hm/$name" '{($n): {
  selector: $sel, owner: $own, partial: $p,
  props: {"border-top-left-radius": "var(--radius-md)", "font-size": "var(--text-sm)", "padding-left": "var(--space-3)", "padding-right": "var(--space-3)"}}}' |
  sed '1d;$d')
{ sed '$d' "$spec" | sed '$s/$/,/'; printf '%s\n}\n' "$entry"; } >"$spec.new"
# validate before replacing, so a bad edit never leaves a broken components.json behind
jq -e --arg n "$name" 'has($n)' "$spec.new" >/dev/null || { rm -f "$spec.new"; echo "generated components.json is invalid; $spec left unchanged" >&2; exit 1; }
mv "$spec.new" "$spec"

# docs subsection above the marker
sub=$(printf '### %s\n\nPartial: `hm/%s`. Contract: `components.json` key `%s`.\n\nRules: (describe when to use it and what it must never do)\n\n<!-- new-components-above -->' "$camel" "$name" "$name")
awk -v s="$sub" '/<!-- new-components-above -->/ { print s; next } { print }' "$docs" >"$docs.new" && mv "$docs.new" "$docs"

# styleguide sample above its marker
awk -v n="$name" -v c="$camel" '/new-components-above \(scripts/ {
  printf "\t\"%s\": {\n\t\t{Label: \"Default\", Data: ui.%s{Label: \"Sample\"}},\n\t},\n", n, c } { print }' "$sg" >"$sg.new" && mv "$sg.new" "$sg"
gofmt -w "$sg"

cd "$ROOT/backend"
go test ./internal/admin -run "TestPartial$camel\$" -update
go test ./internal/admin/designcheck -run 'TestDesignContractDocsTable' -update
echo "scaffolded $name: review git status, then fill in the partial, the contract and the docs."
