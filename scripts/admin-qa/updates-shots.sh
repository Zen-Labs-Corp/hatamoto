#!/usr/bin/env bash
# Shoot every Settings > Updates status, the owner banner and one untouched legacy
# settings page into scripts/admin-qa/shots-out/updates (gitignored; or $SHOTS).
# Four throwaway instances through run-local.sh: a release-stamped build for the
# statuses and banners, one with HATAMOTO_UPDATES=auto (locked mode), one with
# HATAMOTO_UPDATE_CHANNEL=canary (channel locked to canary), and one with an
# unusable update URL (the unavailable state). Nothing leaves the machine.
set -Eeuo pipefail
ROOT=$(git rev-parse --show-toplevel)
export SHOTS=${SHOTS:-$ROOT/scripts/admin-qa/shots-out/updates}
export QA_VERSION=${QA_VERSION:-v1.4.1}
RL=$ROOT/scripts/admin-qa/run-local.sh
QA_VARIANTS="up_to_date scheduled blocked blocked_long major_available update_failed rolled_back rollback_pending off check_failed check_failed_long notify no_check_yet pinned history banner_rolled_back banner_rolled_back_yanked banner_rollback_pending banner_blocked legacy" "$RL" updates-shots.mjs
QA_VARIANTS=env_locked HATAMOTO_UPDATES=auto "$RL" updates-shots.mjs
QA_VARIANTS=env_locked_canary HATAMOTO_UPDATE_CHANNEL=canary "$RL" updates-shots.mjs
QA_VARIANTS=unavailable HATAMOTO_UPDATE_URL=not-a-url "$RL" updates-shots.mjs
