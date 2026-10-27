#!/usr/bin/env bash
# test-new-component.sh - scaffold a demo component in a throwaway worktree and
# run the admin Go suite on it. Slow (the whole admin package): not part of `make check`.
set -euo pipefail
exec bash "$(git rev-parse --show-toplevel)/scripts/new-component.sh" --self-test
