#!/usr/bin/env bash
# baseline-ref.sh - print the ref the previous release is judged from.
# Used by check-migrations.sh and n-1.sh (docs/UPGRADES.md, Schema rules).
#
#   $OVERRIDE if set (MIGRATIONS_BASE / N1_BASE in the callers), else
#   the newest release tag (vX.Y.Z, no prerelease suffix) reachable from HEAD,
#   semver-sorted, else
#   the merge-base of HEAD and origin/$GITHUB_BASE_REF (pull requests; else
#   origin/main). When that merge-base is HEAD itself (a push to main), the
#   first parent HEAD^1 instead, so a merge is judged against the previous main;
#   HEAD itself when it is a root commit (a fresh one-commit snapshot).
#
# Prints one ref on stdout. Exit 2 with a message on stderr when none resolves.
# A depth-1 clone has no usable history: fetch with depth 0 or set the override.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

if [[ -n "${OVERRIDE:-}" ]]; then
  if ! git rev-parse --verify --quiet "$OVERRIDE^{commit}" >/dev/null; then
    echo "baseline override '$OVERRIDE' is not a commit" >&2
    exit 2
  fi
  echo "$OVERRIDE"
  exit 0
fi

tag=$(git tag --merged HEAD -l 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1 || true)
if [[ -n "$tag" ]]; then
  echo "$tag"
  exit 0
fi

target=origin/${GITHUB_BASE_REF:-main}
if mb=$(git merge-base HEAD "$target" 2>/dev/null) && [[ -n "$mb" ]]; then
  if [[ "$mb" == "$(git rev-parse HEAD)" ]]; then
    if first=$(git rev-parse --verify --quiet "HEAD^1^{commit}"); then
      echo "$first"
      exit 0
    fi
    # A root commit (the one-commit public snapshot, before its first tag):
    # everything in it has already shipped, so it is its own baseline.
    echo "$mb"
    exit 0
  fi
  echo "$mb"
  exit 0
fi

echo "cannot resolve a baseline (no reachable v* release tag, no $target; fetch with depth 0 or set MIGRATIONS_BASE)" >&2
exit 2
