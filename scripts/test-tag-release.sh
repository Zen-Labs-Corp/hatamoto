#!/usr/bin/env bash
# test-tag-release.sh - tests scripts/tag-release.sh and scripts/verify-tag-signature.sh
# in a scratch repository with SSH keys generated here (no network, no real key).
# Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
TAG=$PWD/scripts/tag-release.sh VERIFY=$PWD/scripts/verify-tag-signature.sh VERSION=$PWD/scripts/version.sh
bash -n "$TAG"; bash -n "$VERIFY"
command -v ssh-keygen >/dev/null || { echo "SKIP: ssh-keygen not installed"; exit 0; }

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }
refuses() { # refuses NAME PATTERN CMD...: the command must fail and its output match PATTERN
  local name=$1 pat=$2 out; shift 2
  if out=$("$@" 2>&1); then fail "$name: expected a refusal, got success: $out"; fi
  grep -q -- "$pat" <<<"$out" || fail "$name: refusal does not mention '$pat': $out"
  pass "$name"
}

export GIT_CONFIG_GLOBAL=$W/gitconfig GIT_CONFIG_NOSYSTEM=1
git config --global user.name "Test Releaser"
git config --global user.email "releaser@example.com"
git config --global init.defaultBranch main
ssh-keygen -q -t ed25519 -N '' -C owner -f "$W/owner"
ssh-keygen -q -t ed25519 -N '' -C other -f "$W/other"
printf 'releaser@example.com namespaces="git" %s\n' "$(cut -d' ' -f1,2 "$W/owner.pub")" > "$W/allowed_signers"
printf '# placeholder only\n' > "$W/empty_signers"

# A "remote" and a clone, so origin/main exists.
git init -q --bare "$W/remote.git"
git init -q "$W/repo"; cd "$W/repo"
git remote add origin "$W/remote.git"
echo a > f; git add f; git commit -q -m "first"; git push -q origin main
cp "$VERSION" . 2>/dev/null || true
git config commit.gpgsign false

# --- refusals before any tag exists ------------------------------------------
refuses "no signing key" "no signing key" bash "$TAG" v1.2.3
git config user.signingkey "$W/owner.pub"
# gpg.format is deliberately NOT set: tag-release.sh signs with SSH by itself.
for bad in 1.2.3 v1.2 v1.2.3+meta "v1.2.3;rm" v01.2.3; do refuses "bad version $bad" "bad version" bash "$TAG" "$bad"; done
refuses "unknown rev" "unknown revision" bash "$TAG" v1.2.3 nope
echo b > f; git commit -q -am "local only, not on origin/main"
refuses "rev not on origin/main" "not on origin/main" bash "$TAG" v1.2.3
git push -q origin main

# --- the happy path ----------------------------------------------------------
out=$(bash "$TAG" --allowed-signers "$W/allowed_signers" v1.2.3) || fail "tag-release failed: $out"
for t in v1.2.3 backend/v1.2.3; do
  [ "$(git cat-file -t "refs/tags/$t")" = tag ] || fail "$t is not an annotated tag"
  [ "$(git rev-parse "$t^{commit}")" = "$(git rev-parse HEAD)" ] || fail "$t is not at HEAD"
  git cat-file tag "$t" | grep -q 'BEGIN SSH SIGNATURE' || fail "$t carries no SSH signature"
done
pass "v1.2.3 and backend/v1.2.3 created, annotated, SSH-signed, on HEAD"
for t in v1.2.3 backend/v1.2.3; do
  git cat-file tag "$t" | grep -q '^tagger Zen Labs Corporation <release@hatamoto.dev> ' || fail "$t tagger is not the public identity: $(git cat-file tag "$t" | grep '^tagger')"
done
pass "tagger is Zen Labs Corporation <release@hatamoto.dev>, not the local git user"
git ls-remote --tags origin | grep -q v1.2.3 && fail "tags were pushed without --push"
pass "no push without --push"
[ -z "$(git tag -l 'backend/pkg/sdk/*')" ] || fail "an SDK tag was created here"
grep -q "publish-sdk.sh" <<<"$(sed -n '2,31p' "$TAG")" || fail "the usage text does not point the SDK to publish-sdk.sh"
pass "no SDK tag (documented: scripts/publish-sdk.sh in hatamoto-sdk)"
refuses "existing tag" "already exists" bash "$TAG" v1.2.3
# scripts/version.sh counts only vN.N.N tags: the module tag must not confuse it.
cp "$VERSION" "$W/version.sh"; v=$(sh "$W/version.sh"); [ "$v" = v1.2.3 ] || fail "version.sh says $v with both tags present, want v1.2.3"
pass "version.sh ignores backend/v1.2.3"

# --- the push -----------------------------------------------------------------
bash "$TAG" --push --allowed-signers "$W/allowed_signers" v1.2.4-rc.1 >/dev/null 2>&1 || fail "tag-release --push failed"
[ "$(git ls-remote --tags origin | grep -cE 'refs/tags/(backend/)?v1\.2\.4-rc\.1$')" = 2 ] || fail "--push did not push both tags"
pass "--push pushes both tags (prerelease accepted)"

# --- a signature that does not verify must not leave a tag ---------------------
git config user.signingkey "$W/other.pub"
refuses "signed by a key that is not allowed" "failed verification" bash "$TAG" --allowed-signers "$W/allowed_signers" v1.2.5
[ -z "$(git tag -l 'v1.2.5' 'backend/v1.2.5')" ] || fail "a failed tag was kept"
pass "wrong signer: refused and nothing kept"

# A relative --allowed-signers is relative to the caller's directory, not the
# checkout root: from a subdirectory the wrong signer must still be refused.
mkdir -p sub/dir
rel=$(realpath --relative-to="$PWD/sub/dir" "$W/allowed_signers")
(cd sub/dir && refuses "relative --allowed-signers from a subdirectory" "failed verification" bash "$TAG" --allowed-signers "$rel" v1.2.6)
[ -z "$(git tag -l 'v1.2.6' 'backend/v1.2.6')" ] || fail "a failed tag was kept"

# The on-main rule is never skipped: a remote without main (or an unreachable
# one) is an error, and a stale origin/main is fetched before the check.
git init -q --bare "$W/nomain.git"; git remote add nomain "$W/nomain.git"
refuses "remote without main" "cannot fetch main" bash "$TAG" --remote nomain --allowed-signers "$W/allowed_signers" v1.2.7
refuses "unreachable remote" "cannot fetch main" bash "$TAG" --remote nowhere --allowed-signers "$W/allowed_signers" v1.2.7
git config user.signingkey "$W/owner.pub"
git update-ref refs/remotes/origin/main "$(git rev-list --max-parents=0 HEAD | head -1)"   # stale tracking ref
bash "$TAG" --allowed-signers "$W/allowed_signers" "v1.2.8" origin/main >/dev/null || fail "a stale origin/main was not refreshed by the fetch"
pass "relative signers path, missing remote main, stale origin/main"
git config user.signingkey "$W/other.pub"
HATAMOTO_TAG_SIGNING_KEY="$W/owner.pub" bash "$TAG" --allowed-signers "$W/allowed_signers" v1.2.9 >/dev/null || fail "HATAMOTO_TAG_SIGNING_KEY did not override git config user.signingkey"
pass "HATAMOTO_TAG_SIGNING_KEY picks the release tag key over git config"

# --- verify-tag-signature.sh ---------------------------------------------------
git config gpg.format ssh   # the direct `git tag -s` calls below sign with SSH too
git config user.signingkey "$W/owner.pub"
git tag -a unsigned-1 -m x HEAD; git tag light-1 HEAD
git config user.signingkey "$W/other.pub"; git tag -s other-1 -m x HEAD
git config user.signingkey "$W/owner.pub"; git tag -s good-1 -m x HEAD
bash "$VERIFY" --allowed-signers "$W/allowed_signers" good-1 >/dev/null || fail "good tag does not verify"
pass "verify: signed by an allowed signer passes"
refuses "verify: unsigned annotated tag" "no valid signature" bash "$VERIFY" --allowed-signers "$W/allowed_signers" unsigned-1
refuses "verify: lightweight tag" "lightweight" bash "$VERIFY" --allowed-signers "$W/allowed_signers" light-1
refuses "verify: signed by another key" "no valid signature" bash "$VERIFY" --allowed-signers "$W/allowed_signers" other-1
refuses "verify: no such tag" "no tag" bash "$VERIFY" --allowed-signers "$W/allowed_signers" nope
out=$(bash "$VERIFY" --allowed-signers "$W/empty_signers" unsigned-1) || fail "placeholder allowed_signers must not block"
grep -q 'NOT enforced' <<<"$out" || fail "placeholder mode did not warn: $out"
pass "verify: placeholder allowed_signers warns and passes (enforcement is off until a key is added)"
refuses "verify --require with the placeholder" "lists no signer" bash "$VERIFY" --require --allowed-signers "$W/empty_signers" good-1
refuses "verify: missing file with --require" "lists no signer" bash "$VERIFY" --require --allowed-signers "$W/none" good-1
ROOT=$OLDPWD

# --- the release.yml step, run for real ---------------------------------------
# Extract the "Verify the tag signature" run block and run it in a fresh
# checkout of a scratch remote (its https auth header is ignored for file://).
step() { awk '/- name: Verify the tag signature/{f=1} f&&/run: \|/{r=1;next} r&&/^          /{sub(/^          /,"");print;next} r{exit}' "$ROOT/.github/workflows/release.yml"; }
step > "$W/step.sh"; [ -s "$W/step.sh" ] || fail "could not extract the release.yml verify step"
mkdir -p "$W/signers-repo"; cd "$W/signers-repo"; git init -q; git remote add origin "$W/remote.git"
mkdir -p .github scripts; cp "$VERIFY" scripts/; cp "$W/allowed_signers" .github/allowed_signers
git add -A; git commit -q -m "signers"; git branch -M main; git push -q -f origin main
git config user.signingkey "$W/owner.pub"
git config gpg.format ssh; git config user.signingkey "$W/owner.pub"; git tag -s v3.0.0 -m v3.0.0; git tag -a v3.0.1 -m v3.0.1; git config user.signingkey "$W/other.pub"; git tag -s v3.0.2 -m v3.0.2
git push -q origin refs/tags/v3.0.0 refs/tags/v3.0.1 refs/tags/v3.0.2
run_step() { # run_step TAG: a fresh checkout at the tag, then the extracted step
  rm -rf "$W/co"; git clone -q "${REMOTE:-$W/remote.git}" "$W/co"; (cd "$W/co" && git checkout -q --detach "$1^{commit}" \
    && GH_TOKEN=x REPO_PRIVATE=${REPO_PRIVATE:-false} GITHUB_REF_NAME=$1 GITHUB_SHA=$(git rev-parse HEAD) RUNNER_TEMP=$W bash -e "$W/step.sh"); }
run_step v3.0.0 >/dev/null 2>&1 || fail "release.yml step rejected a tag signed by an allowed signer"
pass "release.yml step: tag signed by an allowed signer builds"
refuses "release.yml step: unsigned tag" "no valid signature" run_step v3.0.1
refuses "release.yml step: tag signed by another key" "no valid signature" run_step v3.0.2

# A public repository fails closed: a main whose allowed_signers is still the
# placeholder must not build an unsigned tag there, but may in the private repo.
git init -q --bare "$W/ph.git"; mkdir -p "$W/ph-repo"; cd "$W/ph-repo"; git init -q; git remote add origin "$W/ph.git"
mkdir -p .github scripts; cp "$VERIFY" scripts/; cp "$W/empty_signers" .github/allowed_signers
git add -A; git commit -q -m "placeholder"; git branch -M main; git tag -a v4.0.0 -m v4.0.0; git push -q origin main refs/tags/v4.0.0
REMOTE=$W/ph.git REPO_PRIVATE=true run_step v4.0.0 >/dev/null 2>&1 || fail "private repo: the placeholder must warn and pass"
pass "release.yml step: private repo with the placeholder warns and builds"
REMOTE=$W/ph.git refuses "release.yml step: public repo with the placeholder (a forgotten key)" "lists no signer" run_step v4.0.0
REMOTE=$W/ph.git REPO_PRIVATE=false refuses "release.yml step: public repo, explicit false" "lists no signer" run_step v4.0.0
echo "all tag-release tests passed"
