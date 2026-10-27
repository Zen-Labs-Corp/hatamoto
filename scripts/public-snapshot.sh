#!/usr/bin/env bash
# public-snapshot.sh - build the public tree from one ref as a brand new
# repository with exactly ONE commit ("Initial public release").
#
# Usage:
#     scripts/public-snapshot.sh --author-name NAME --author-email EMAIL [--out DIR] [--force] [REF]
#
#   REF            commit to export (default HEAD)
#   --out DIR      target directory (default /tmp/hatamoto-public-snapshot);
#                  must be missing or empty unless --force (which deletes it)
#   --author-name / --author-email
#                  the identity of the one commit (author and committer). There
#                  is no default: pass the organisation's approved identity.
#                  Free-mail addresses are refused.
#
# The tree is `git archive REF`: every path marked export-ignore in
# .gitattributes (working notes, plans, design mockups, ...) is left out. No
# history, tags, remotes or hooks are carried over. Refuses to run when the
# working tree is dirty (tracked changes or untracked files), so what is
# exported is what is committed. Prints the file count and the tree size.
# The private repository is never modified. Nothing is pushed.
set -euo pipefail

die() { echo "public-snapshot: $*" >&2; exit 1; }
usage() { sed -n '2,24p' "$0"; exit 64; }

OUT=/tmp/hatamoto-public-snapshot
NAME="" EMAIL="" REF="" FORCE=0
while (($#)); do
  case "$1" in
    --author-name) NAME=${2:-}; shift 2 ;;
    --author-email) EMAIL=${2:-}; shift 2 ;;
    --out) OUT=${2:-}; shift 2 ;;
    --force) FORCE=1; shift ;;
    -h|--help) usage ;;
    -*) die "unknown option $1" ;;
    *) [[ -z "$REF" ]] || die "one REF only"; REF=$1; shift ;;
  esac
done
REF=${REF:-HEAD}

[[ -n "$NAME" && -n "$EMAIL" ]] || die "pass --author-name and --author-email (the approved identity of the public commit; there is no default)"
[[ "$EMAIL" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]] || die "--author-email is not an email address"
if [[ "${EMAIL,,}" =~ @(gmail|googlemail|yahoo|hotmail|outlook|live|icloud|me|aol|proton|protonmail|gmx)\. ]]; then
  die "--author-email is a personal (free-mail) address; use the organisation's address"
fi
[[ -n "$OUT" && "$OUT" != / ]] || die "bad --out"

ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || die "not inside a git repository"
cd "$ROOT"
if [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  git status --short >&2
  die "the working tree is dirty; commit or stash first"
fi
SHA=$(git rev-parse --verify --quiet "$REF^{commit}") || die "unknown ref: $REF"

if [[ -e "$OUT" ]]; then
  if [[ -n "$(ls -A "$OUT" 2>/dev/null)" ]]; then
    ((FORCE)) || die "$OUT exists and is not empty (pass --force to replace it)"
    rm -rf -- "$OUT"
  fi
fi
mkdir -p -- "$OUT"

git archive --format=tar "$SHA" | tar -x -C "$OUT"

# Belt and braces: the attributes of the exported tree say what must be gone.
for p in .pi CLAUDE.md design backend/.next .neutral-private; do
  [[ ! -e "$OUT/$p" ]] || die "$p is in the archive; check the export-ignore rules in .gitattributes"
done

g() { git -C "$OUT" -c core.hooksPath=/dev/null -c commit.gpgsign=false -c init.defaultBranch=main "$@"; }
g init -q
g add -A
GIT_AUTHOR_NAME="$NAME" GIT_AUTHOR_EMAIL="$EMAIL" GIT_COMMITTER_NAME="$NAME" GIT_COMMITTER_EMAIL="$EMAIL" \
  g -c user.name="$NAME" -c user.email="$EMAIL" commit -q --signoff -m "Initial public release"

files=$(g ls-files | wc -l | tr -d ' ')
bytes=$(g ls-files -z | (cd "$OUT" && xargs -0 cat) | wc -c | tr -d ' ')
human=$(du -sh --exclude=.git "$OUT" 2>/dev/null | cut -f1 || echo "?")
echo "snapshot: $OUT"
echo "source:   $SHA"
echo "commit:   $(g rev-parse HEAD) (1 commit, author $NAME)"
echo "files:    $files"
echo "size:     $bytes bytes of tracked content ($human on disk without .git)"
