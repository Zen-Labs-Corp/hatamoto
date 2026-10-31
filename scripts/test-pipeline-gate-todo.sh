#!/usr/bin/env bash
# test-pipeline-gate-todo.sh - `pipeline-gate.sh todo` in a scratch repo with a stub `go`: packages under a
# nested go.mod are vetted/tested from their own module dir (never passed to the main module), nested
# modules under testdata are skipped with a note, and a failure in a nested module fails the gate.
set -euo pipefail
unset HATAMOTO_CHECK_SUITES HATAMOTO_FLAKES_FILE HATAMOTO_GATE_CACHE HATAMOTO_SKIP_INTEGRATION GO_FAIL_IN
top=$(git rev-parse --show-toplevel)
if [[ -L "$top/scripts/pipeline-gate.sh" && ! -e "$top/scripts/pipeline-gate.sh" ]]; then
  echo "test-pipeline-gate-todo: FAIL: scripts/pipeline-gate.sh is a dangling link (run .pi/bin/notes-link.sh)" >&2
  exit 1
fi
if [[ ! -e "$top/scripts/pipeline-gate.sh" ]]; then
  echo "test-pipeline-gate-todo: SKIP (scripts/pipeline-gate.sh absent: notes-clone tooling, not in the public tree)"
  exit 0
fi
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=T GIT_AUTHOR_EMAIL=t@example.com GIT_COMMITTER_NAME=T GIT_COMMITTER_EMAIL=t@example.com
git init -q -b main "$W/r"; cd "$W/r"
R=$(git rev-parse --show-toplevel)
mkdir -p scripts .githooks/lib backend/internal/form backend/pkg/sdk/guest \
  backend/internal/x/testdata/mod backend/internal/x/testdata/plain backend/pkg/sdk/testdata
cp "$top/scripts/pipeline-gate.sh" scripts/; cp "$top/.githooks/lib/gate-cache.sh" .githooks/lib/
printf '#!/usr/bin/env bash\nexit 0\n' >scripts/check-changed.sh; chmod +x scripts/check-changed.sh
echo 'module example.test/core' >backend/go.mod
echo 'module example.test/sdk' >backend/pkg/sdk/go.mod
echo 'module example.test/hostile' >backend/internal/x/testdata/mod/go.mod
for d in internal/form pkg/sdk pkg/sdk/guest pkg/sdk/testdata internal/x/testdata/mod internal/x/testdata/plain; do
  echo 'package p' >"backend/$d/f.go"
done
mkdir -p "$W/bin"
cat >"$W/bin/go" <<STUB
#!/usr/bin/env bash
echo "\$PWD \$*" >>"$W/go.log"
[[ -z "\${GO_FAIL_IN:-}" || "\$PWD" != *"\$GO_FAIL_IN"* ]] || exit 1
exit 0
STUB
chmod +x "$W/bin/go"
export PATH="$W/bin:$PATH"
git add -A; git commit -qm base

todo() { : >"$W/go.log"; bash scripts/pipeline-gate.sh todo "$@" >"$W/out" 2>"$W/err"; local rc=$?; cat "$W/out" "$W/err" >"$W/log"; return $rc; }
fail() { echo "test-pipeline-gate-todo: FAIL: $*" >&2; cat "$W/log" "$W/go.log" >&2; exit 1; }
has()  { grep -qxF -- "$1" "$W/go.log" || fail "go.log lacks: $1"; }
hasnt(){ ! grep -qF -- "$1" "$W/go.log" || fail "go.log unexpectedly has: $1"; }
only_build() { ! grep -v ' build \./\.\.\.$' "$W/go.log" | grep -q . || fail "unexpected go calls"; }

# 1. nested module only: vet+test inside it, nothing for the main module
todo backend/pkg/sdk/f.go || fail "case 1: gate exited non-zero"
has "$R/backend/pkg/sdk vet ."; has "$R/backend/pkg/sdk test ."
! grep -F "$R/backend " "$W/go.log" | grep -qF './pkg/sdk' || fail "case 1: ./pkg/sdk passed to the main module"
grep -q '^OK todo gate$' "$W/log" || fail "case 1: no OK todo gate"

# 2. mixed: each package in its own module, main module first
todo backend/internal/form/f.go backend/pkg/sdk/f.go || fail "case 2: gate exited non-zero"
has "$R/backend vet ./internal/form"; has "$R/backend test ./internal/form"
has "$R/backend/pkg/sdk vet ."; has "$R/backend/pkg/sdk test ."
m=$(grep -nxF "$R/backend vet ./internal/form" "$W/go.log" | cut -d: -f1)
n=$(grep -nxF "$R/backend/pkg/sdk vet ." "$W/go.log" | cut -d: -f1)
((m < n)) || fail "case 2: main-module run does not precede the nested one"

# 3. nested subpackage is relative to its module
todo backend/pkg/sdk/guest/f.go || fail "case 3: gate exited non-zero"
has "$R/backend/pkg/sdk vet ./guest"; has "$R/backend/pkg/sdk test ./guest"

# 4. nested module under testdata: skipped with a note
todo backend/internal/x/testdata/mod/f.go || fail "case 4: gate exited non-zero"
grep -q 'note.*testdata' "$W/out" || fail "case 4: skip note not on stdout"
only_build

# 5. plain testdata: skipped silently
todo backend/internal/x/testdata/plain/f.go || fail "case 5: gate exited non-zero"
! grep -q 'note' "$W/log" || fail "case 5: unexpected note"
only_build

# 5b. plain testdata inside a nested module: no note either
todo backend/pkg/sdk/testdata/f.go || fail "case 5b: gate exited non-zero"
! grep -q 'note' "$W/log" || fail "case 5b: unexpected note"
only_build

# 5c. main-module only: the original run line, no nested run
todo backend/internal/form/f.go || fail "case 5c: gate exited non-zero"
has "$R/backend vet ./internal/form"; has "$R/backend test ./internal/form"
hasnt "$R/backend/"

# 6. a failure inside a nested module fails the gate
rc=0; GO_FAIL_IN=backend/pkg/sdk todo backend/pkg/sdk/f.go || rc=$?
[[ $rc -eq 1 ]] || fail "case 6: exit $rc, want 1"
grep -q 'PIPELINE-GATE FAIL (todo)' "$W/log" || fail "case 6: no FAIL line"

echo "test-pipeline-gate-todo: ok"
