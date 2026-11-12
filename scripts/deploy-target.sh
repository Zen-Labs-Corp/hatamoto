#!/usr/bin/env bash
# deploy-target.sh - print the deploy target: `box` or `hmctl <slug>`.
#
# DEPLOY_TARGET from the environment when it is set at all (empty or `box` =
# box), else the first readable .deploy-target in this checkout, then the main
# checkout (same search order as scripts/deploy-host.sh). A cell target is
# `hmctl:<slug>`; no setting means the classic box. Exit 1 on a malformed value.
set -Eeuo pipefail

if [[ -n "${DEPLOY_TARGET+x}" ]]; then
  target=$DEPLOY_TARGET
else
  target=
  repo=$(git rev-parse --show-toplevel)
  main=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
  for f in "$repo/.deploy-target" "$main/.deploy-target"; do
    if [[ -r "$f" ]]; then target=$(tr -d '[:space:]' < "$f"); break; fi
  done
fi

case $target in
  ''|box) echo box; exit 0 ;;
esac
slug=${target#hmctl:}
if [[ "$target" != "hmctl:$slug" || ! "$slug" =~ ^[a-z][a-z0-9-]{1,28}$ || "$slug" == *- || "$slug" == *--* ]]; then
  echo "deploy-target: bad DEPLOY_TARGET (want box or hmctl:<slug>)" >&2
  exit 1
fi
printf 'hmctl %s\n' "$slug"
