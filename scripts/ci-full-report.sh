#!/usr/bin/env bash
# ci-full-report.sh - keep exactly one open issue while ci-full is red on main.
#
# Env in:  RESULT (success|failure|cancelled|skipped), SHA, IS_MAIN, RUN_URL, GH_TOKEN.
# failure: comment on the open issue, else create it. success: comment and close it.
set -Eeuo pipefail

title="ci-full failing on main"
result=${RESULT:-}

if [ "${IS_MAIN:-}" != true ]; then
  echo "ci-full ran on a commit that is not the head of main: no issue change"
  exit 0
fi
case "$result" in
  success | failure) ;;
  *)
    echo "ci-full result is $result: no issue change"
    exit 0
    ;;
esac

number=$(gh issue list --state open --limit 100 --json number,title |
  jq -r --arg t "$title" '[.[] | select(.title == $t)][0].number // empty')
body="ci-full on ${SHA:-unknown}: $result. Run: ${RUN_URL:-unknown}"

if [ "$result" = failure ]; then
  if [ -n "$number" ]; then
    gh issue comment "$number" --body "$body"
  else
    gh issue create --title "$title" --body "$body"
  fi
elif [ -n "$number" ]; then
  gh issue comment "$number" --body "$body"
  gh issue close "$number"
else
  echo "ci-full green and no open issue"
fi
