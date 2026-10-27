#!/usr/bin/env bash
# Sourced by the hooks. core.hooksPath points at the dispatchers made by `make hooks`, which run
# a snapshot of .githooks (with check-neutral.sh and .gitleaks.toml beside it), so every worktree runs these installed copies, including
# worktrees of old branches that have no .githooks or scripts of their own.
hooks_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
installed_root=$(dirname "$hooks_dir")

# hm_file <repo-relative path>: the file in the current checkout if it has one,
# else the installed (snapshot) copy; fails loudly if neither exists.
hm_file() {
  if [[ -e $PWD/$1 ]]; then echo "$PWD/$1"
  elif [[ -e $installed_root/$1 ]]; then echo "$installed_root/$1"
  else echo "hooks: $1 exists neither in this checkout nor in $installed_root" >&2; return 1
  fi
}
