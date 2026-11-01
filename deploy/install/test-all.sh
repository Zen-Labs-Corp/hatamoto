#!/usr/bin/env bash
# test-all.sh - every offline installer test, in order; stops at the first failure.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
top="$(cd "$here/../.." && pwd)"
for t in test-assemble.sh test-floor.sh test-shellcheck.sh test-trust.sh test-build-stage.sh test-img-check.sh \
  test-firstboot.sh test-motd-dns.sh test-backup-swap.sh test-docs-examples.sh; do
  echo "== $t"
  bash "$here/$t"
done
if command -v node >/dev/null 2>&1; then
  echo "== setupcode.test.mjs"
  (cd "$top" && node --test scripts/admin-qa/unit/setupcode.test.mjs)
else
  echo "SKIP: node not found, setupcode.test.mjs not run"
fi
echo "installer tests: OK"
