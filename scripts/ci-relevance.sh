#!/usr/bin/env bash
# ci-relevance.sh - decide inside a CI job whether its heavy steps apply.
# The job itself always runs (a required check must end SUCCESS, never SKIPPED);
# later steps are gated on the relevant output.
#
# Env in:  TIER (fast|full), EVENT_NAME, BASE_SHA, HEAD_SHA, BEFORE_SHA,
#          PATTERN (ERE matched against changed paths), FULL_ONLY (1 = only the
#          full tier runs this job), LABEL (for the log line).
# Out:     relevant=true|false appended to $GITHUB_OUTPUT (stdout when unset).
# Unknown event, unreachable base or a failed diff all fail safe to true.
set -Eeuo pipefail

out=${GITHUB_OUTPUT:-/dev/stdout}
label=${LABEL:-job}
emit() { echo "relevant=$1" >> "$out"; }

if [ "${TIER:-}" = full ]; then
  echo "full tier: $label runs"
  emit true
  exit 0
fi
if [ "${FULL_ONLY:-0}" = 1 ]; then
  echo "$label is moved to ci-full: skipped"
  emit false
  exit 0
fi

case "${EVENT_NAME:-}" in
  pull_request)
    a=${BASE_SHA:-}
    b=${HEAD_SHA:-}
    ;;
  push)
    a=${BEFORE_SHA:-}
    b=${HEAD_SHA:-}
    if [[ -z $a || $a =~ ^0+$ ]]; then
      echo "no previous commit to compare: $label runs"
      emit true
      exit 0
    fi
    ;;
  *)
    echo "event ${EVENT_NAME:-unknown}: $label runs"
    emit true
    exit 0
    ;;
esac

if files=$(git diff --name-only "$a" "$b" 2>/dev/null); then
  if grep -Eq "${PATTERN:?PATTERN is required}" <<<"$files"; then
    echo "relevant: $label changed, runs"
    emit true
  else
    echo "not relevant: no $label change, skipped"
    emit false
  fi
else
  echo "diff $a..$b failed, failing safe: $label runs"
  emit true
fi
