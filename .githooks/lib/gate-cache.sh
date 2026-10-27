#!/usr/bin/env bash
# Sourced by pre-push and scripts/pipeline-gate.sh (scripts/ship.sh inlines the lookups of
# `ui-gate` and `full-suite`).
#
# Gate results, cached per TREE: a gate that passed on some content passes on the same
# content again, so a re-push of the same tree (a rebase that changed nothing, an amended
# message, `pipeline-gate.sh full` followed by `git push`, the merge gate in ship.sh) does
# not re-run minutes of tests. Only passes are recorded. Gates that depend on more than the
# tree (DCO and gitleaks look at commits) are never cached; a gate whose scope depends on
# the base (the integration gate runs only the changed packages) passes that scope as the
# context, so a different package set is a different entry.
#
#   <git-common-dir>/hatamoto-gate-cache/<tree>/<gate>[.<context hash>]
#
# Cache names: the pre-push gates (context = mode and package list, so an affected pass never
# satisfies a full lookup), ui-gate, and full-suite (no context): recorded only by a FULL pre-push
# run in which every gate passed and none was skipped for the environment; scripts/ship.sh looks
# it up before merging.
#
# HATAMOTO_GATE_CACHE=off  ignore recorded passes (still records new ones).

hm_gc_dir() {
  local c
  c=$(git rev-parse --git-common-dir 2>/dev/null) || return 1
  c=$(cd "$c" && pwd) || return 1
  printf '%s/hatamoto-gate-cache\n' "$c"
}

# hm_gc_key <commit> <gate> [context]
hm_gc_key() {
  local tree dir name=$2
  tree=$(git rev-parse -q --verify "$1^{tree}" 2>/dev/null) || return 1
  dir=$(hm_gc_dir) || return 1
  [[ -z ${3:-} ]] || name+=".$(printf '%s' "$3" | sha1sum | cut -c1-12)"
  printf '%s/%s/%s\n' "$dir" "$tree" "${name//[^A-Za-z0-9._-]/_}"
}

# hm_gc_hit <commit> <gate> [context]: 0 when this gate already passed on the commit's tree.
hm_gc_hit() {
  [[ ${HATAMOTO_GATE_CACHE:-on} != off ]] || return 1
  local k
  k=$(hm_gc_key "$@") || return 1
  [[ -f $k ]]
}

# hm_gc_record <commit> <gate> [context]
hm_gc_record() {
  local k
  k=$(hm_gc_key "$@") || return 0
  mkdir -p "$(dirname "$k")" 2>/dev/null && date -u +%FT%TZ >"$k" 2>/dev/null
  return 0
}

# hm_gc_prune: drop entries older than 14 days (called once per pre-push).
hm_gc_prune() {
  local d
  d=$(hm_gc_dir) || return 0
  [[ -d $d ]] && find "$d" -mindepth 1 -maxdepth 1 -type d -mtime +14 -exec rm -rf {} + 2>/dev/null
  return 0
}

# Paths whose change can alter the admin UI: the admin-QA browser gate runs only for these.
# scripts/ship.sh keeps a copy of this pattern (it never sources files from a PR tree).
HM_UI_PATHS_RE='^backend/web/admin/|^backend/internal/admin/|^scripts/admin-qa/'

# hm_ui_changed <range>: 0 when the diff touches an admin-UI path.
hm_ui_changed() {
  git diff --name-only "$1" | grep -qE "$HM_UI_PATHS_RE"
}
