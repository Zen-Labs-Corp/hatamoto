#!/usr/bin/env bash
# Sourced by pre-push.
# hm_go_targets <worktree> <git diff range>: for the Go and migration files changed in the
# range, print "<module dir>\t<package>" lines (sorted, unique). The module is the nearest
# go.mod above the file (backend/, backend/pkg/sdk, backend/tools/docs-publish,
# examples/plugins/*), the package is relative to it ("./internal/server", "."). Paths with
# a testdata segment, files outside any module and directories without Go files are dropped.
hm_go_targets() {
  local wt=$1 range=$2 f d m rel
  git diff -z --name-only --diff-filter=ACMRD "$range" -- '*.go' 'backend/migrations/*.sql' |
    while IFS= read -r -d '' f; do
      [[ /$f == */testdata/* ]] && continue
      d=$(dirname "$f")
      [[ -d $wt/$d ]] || continue
      compgen -G "$wt/$d/*.go" >/dev/null || continue
      m=$d
      while [[ ! -f $wt/$m/go.mod ]]; do
        [[ $m == . ]] && break
        m=$(dirname "$m")
      done
      [[ -f $wt/$m/go.mod ]] || continue
      rel=${d#"$m"}; rel=${rel#/}
      if [[ -z $rel ]]; then printf '%s\t.\n' "$m"; else printf '%s\t./%s\n' "$m" "$rel"; fi
    done | sort -u
}
