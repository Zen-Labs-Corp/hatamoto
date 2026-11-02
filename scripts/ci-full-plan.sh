#!/usr/bin/env bash
# ci-full-plan.sh - decide what the ci-full workflow tests and whether it runs.
# Run in a fetch-depth 0 checkout, with gh on PATH.
#
# Env in:  EVENT_NAME (schedule|workflow_dispatch), INPUT_SHA (optional, dispatch
#          only), GH_TOKEN, GITHUB_OUTPUT.
# Out:     sha=<tested commit>, is_main=true|false, run=true|false.
#
# A dispatch always runs. A scheduled run is skipped when the newest green ci-full
# run already tested the checked-out commit. The tested sha of a run is carried in its
# run name ("ci-full @ <sha>"): a dispatch can only name a branch, so headSha of
# the run is the branch head, not necessarily the tested commit.
set -Eeuo pipefail

out=${GITHUB_OUTPUT:-/dev/stdout}
event=${EVENT_NAME:-}

# The commit under test is the checked-out HEAD (github.sha, the commit the run name
# shows), never origin/main, which may have moved while the run was queued. origin/main
# only decides is_main and the nightly skip.
main=$(git rev-parse --verify --quiet origin/main || git rev-parse HEAD)
sha=$(git rev-parse HEAD)
if [ "$event" = workflow_dispatch ] && [ -n "${INPUT_SHA:-}" ]; then
  if [[ ! $INPUT_SHA =~ ^[0-9a-f]{40}$ ]] || ! git cat-file -e "$INPUT_SHA^{commit}" 2>/dev/null; then
    echo "sha input is not a 40-hex commit of this repository: $INPUT_SHA" >&2
    exit 1
  fi
  sha=$INPUT_SHA
fi

run=true
if [ "$event" = schedule ]; then
  last=$(gh run list --workflow ci-full.yml --status success --limit 20 --json displayTitle,headSha 2>/dev/null |
    jq -r 'if length == 0 then "" else .[0] | ((.displayTitle | capture("@ (?<s>[0-9a-f]{40})").s) // .headSha) end' 2>/dev/null || true)
  if [ -n "$last" ] && [ "$last" = "$sha" ]; then
    echo "ci-full already green on ${sha:0:12}: skipping"
    run=false
  fi
fi

{
  echo "sha=$sha"
  echo "is_main=$([ "$sha" = "$main" ] && echo true || echo false)"
  echo "run=$run"
} >> "$out"
echo "ci-full: sha=${sha:0:12} run=$run"
