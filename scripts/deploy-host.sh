#!/usr/bin/env bash
# deploy-host.sh - print the validated deploy host (an ssh host alias).
#
# DEPLOY_HOST from the environment, else the first readable .deploy-host in
# this checkout, then the main checkout. Used by `make registry-pull-archive`
# and scripts/publish-registry.sh so both resolve and validate it identically.
set -Eeuo pipefail

host=${DEPLOY_HOST:-}
if [[ -z "$host" ]]; then
  repo=$(git rev-parse --show-toplevel)
  main=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
  for f in "$repo/.deploy-host" "$main/.deploy-host"; do
    if [[ -r "$f" ]]; then host=$(tr -d '[:space:]' < "$f"); break; fi
  done
fi
[[ -n "$host" ]] || { echo "deploy-host: set DEPLOY_HOST (ssh host alias) or write it to .deploy-host" >&2; exit 1; }
[[ "$host" =~ ^[A-Za-z0-9._@][A-Za-z0-9._@-]*$ ]] || { echo "deploy-host: bad DEPLOY_HOST" >&2; exit 1; }
printf '%s\n' "$host"
