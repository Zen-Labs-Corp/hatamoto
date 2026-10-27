package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const workflowDir = "../../../.github/workflows"

// hasWorkflow reports whether a workflow file exists. The channel workflows are
// export-ignore (they belong to the private ops repository), so the public
// repository has neither of them and their tests do not apply there.
func hasWorkflow(name string) bool {
	_, err := os.Stat(filepath.Join(workflowDir, name))
	return err == nil
}

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(workflowDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustContain(t *testing.T, file, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s: missing %q", file, w)
		}
	}
}

func mustNotContain(t *testing.T, file, text string, bans ...string) {
	t.Helper()
	for _, b := range bans {
		if strings.Contains(text, b) {
			t.Errorf("%s: must not contain %q", file, b)
		}
	}
}

// TestWorkflowsStatic checks the properties of the CI workflows that matter
// for release signing: where the write permission and the manifest secret
// may appear, and what release.yml does.
func TestWorkflowsStatic(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(workflowDir, "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		text := readWorkflow(t, name)
		if name != "release.yml" {
			mustNotContain(t, name, text, "contents: write")
		}
		if name != "channels.yml" {
			mustNotContain(t, name, text, "HATAMOTO_MANIFEST_KEY", "RELEASES_SSH_")
		}
		mustNotContain(t, name, text, "set -x", "PRIVATE KEY")
	}

	// Every action in the two signing workflows is pinned to a full commit SHA
	// with a version comment; a moving tag could run new code beside a secret.
	pinned := regexp.MustCompile(`^\s*(- )?uses: [\w./-]+@[0-9a-f]{40} # v\d+`)
	for _, name := range []string{"release.yml", "image.yml", "channels.yml", "channels-freshness.yml"} {
		if !hasWorkflow(name) {
			continue
		}
		for _, line := range strings.Split(readWorkflow(t, name), "\n") {
			if strings.Contains(line, "uses:") && !pinned.MatchString(line) {
				t.Errorf("%s: action is not pinned to a commit SHA with a version comment: %s", name, strings.TrimSpace(line))
			}
		}
	}

	rel := readWorkflow(t, "release.yml")
	mustContain(t, "release.yml", rel,
		"tags: ['v*']", "contents: write  # create the draft release", "--draft", "--verify-tag", "persist-credentials: false", "cache: false",
		"make release-build", "tools/release record")
	// The tag is validated before any other step, in particular before checkout.
	mustContain(t, "release.yml", rel, `^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)
	if !strings.Contains(rel, "Check the tag") || strings.Index(rel, "Check the tag") > strings.Index(rel, "uses:") {
		t.Error("release.yml must validate the tag before its first action")
	}
	// The tag signature is checked against the signers on main before anything is built.
	mustContain(t, "release.yml", rel, "Verify the tag signature", "scripts/verify-tag-signature.sh", "origin/main:.github/allowed_signers")
	if strings.Index(rel, "Verify the tag signature") > strings.Index(rel, "make release-build") {
		t.Error("release.yml must verify the tag signature before it builds")
	}
	// Least permissions: none for the workflow, and the write permissions only on
	// the job that needs them. contents: write is the draft job's alone.
	mustContain(t, "release.yml", rel, "permissions: {}", "id-token: write", "attestations: write")
	if strings.Count(rel, "contents: write") != 1 || !strings.Contains(workflowJob(t, rel, "draft"), "contents: write") {
		t.Error("contents: write must appear once, in the draft job")
	}
	for _, job := range []string{"build", "attest"} {
		if strings.Contains(workflowJob(t, rel, job), "contents: write") {
			t.Errorf("release.yml job %s must not hold contents: write", job)
		}
	}
	if strings.Contains(workflowJob(t, rel, "build"), "id-token") {
		t.Error("the build job must not hold id-token")
	}
	// Attestations: provenance and both SBOM formats per platform; SBOMs are made per binary.
	mustContain(t, "release.yml", rel, "actions/attest-build-provenance@", "actions/attest-sbom@", ".spdx.json", ".cdx.json", "anchore/sbom-action/download-syft@", "syft-version: v")
	// The image is not built by a tag run (a tag brings its own workflow file). It
	// lives in image.yml: dispatch only, main only, behind an environment, the only
	// holder of packages: write, verifying the tag against main's own scripts.
	if strings.Contains(rel, "packages: write") || strings.Contains(rel, "ghcr.io") || strings.Contains(rel, "cosign") {
		t.Error("release.yml must not publish an image: that is image.yml")
	}
	img := readWorkflow(t, "image.yml")
	mustContain(t, "image.yml", img, "workflow_dispatch:", "tag:", "environment: release-image", "packages: write", "id-token: write", "contents: read",
		"ghcr.io/zen-labs-corp/hatamoto", "linux/amd64,linux/arm64", "cosign sign --yes", "cosign attest --yes --type spdxjson", "cosign verify-attestation",
		"refs/heads/main", "--require", "merge-base --is-ancestor", "gh release view", "isDraft", "image.yml@refs/heads/main", "permissions: {}", "cancel-in-progress: false", "releases/latest", "release.json.sig", "-a \"version=$TAG\"")
	mustNotContain(t, "image.yml", img, "contents: write", "attestations: write", "\n  push:", "pull_request", "release:", "secrets.", "schedule:")
	if strings.Contains(img, "scripts/verify-tag-signature.sh") && strings.Index(img, "cp scripts/verify-tag-signature.sh") > strings.Index(img, "git checkout --quiet --detach") {
		t.Error("image.yml must copy main's verifier before the work tree moves to the tag")
	}
	if strings.Count(img, "on:\n  workflow_dispatch:") != 1 {
		t.Error("image.yml must trigger only on workflow_dispatch")
	}
	// The draft decides the run's conclusion; a failed attestation must not.
	mustContain(t, "release.yml (attest)", workflowJob(t, rel, "attest"), "continue-on-error: true", "fail-fast: false", "always() && job.status", "attestation failed", "dist/SHA256SUMS")
	if strings.Contains(workflowJob(t, rel, "draft"), "attest") {
		t.Error("the draft job must not depend on attest")
	}
	// A public repository passes --require on its own.
	mustContain(t, "release.yml", rel, "REPO_PRIVATE", "--require")
	if strings.Count(rel, "id-token: write") != 1 {
		t.Error("id-token: write belongs to the attest job alone")
	}
	// The kind comes from the tag, never a fixed word.
	mustContain(t, "release.yml", rel, `--kind "$kind"`, `kind="major"`, `kind="minor"`, `kind="patch"`)
	mustNotContain(t, "release.yml", rel, "--kind patch", "secrets.", "branches:", "go build", "-ldflags", "schedule:")
	// Only the v* tag trigger.
	if strings.Count(rel, "\n  push:") != 1 || strings.Contains(rel, "pull_request") {
		t.Error("release.yml must trigger only on a pushed v* tag")
	}

	if !hasWorkflow("channels.yml") {
		t.Log("channels.yml is not in this repository (public tree): channel workflow checks skipped")
		return
	}
	ch := readWorkflow(t, "channels.yml")
	mustContain(t, "channels.yml", ch,
		"schedule:", "workflow_dispatch:", "paths:", "releases/channels/**",
		"contents: read", "umask 077", "rm -f", "if: always()", "upload-artifact",
		"secrets.HATAMOTO_MANIFEST_KEY", "channel resign", "verify --chan", "environment: release-channels",
		"secrets.RELEASES_SSH_KEY", "secrets.RELEASES_SSH_HOST", "secrets.RELEASES_SSH_USER", "secrets.RELEASES_SSH_KNOWN_HOSTS",
		"StrictHostKeyChecking=yes", "IdentitiesOnly=yes", "rsync", "Publish channels",
		"for c in edge stable canary", "out/canary.json")
	mustNotContain(t, "channels.yml", ch, `echo "$HATAMOTO_MANIFEST_KEY"`, "echo $HATAMOTO_MANIFEST_KEY",
		"release-primary", "release-backup", "secrets.GITHUB_TOKEN",
		"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "--delete")
	// The manifest secret is used once, in the signing step's env block; each
	// publish secret once, in the publishing step's env block.
	if strings.Count(ch, "secrets.") != 5 {
		t.Errorf("channels.yml must reference exactly five secrets, got %d", strings.Count(ch, "secrets."))
	}
	sign, publish := workflowStep(t, ch, "Sign channels"), workflowStep(t, ch, "Publish channels")
	if strings.Count(sign, "secrets.") != 1 || !strings.Contains(sign, "secrets.HATAMOTO_MANIFEST_KEY") {
		t.Error("the signing step must hold the manifest secret and nothing else")
	}
	if strings.Contains(publish, "HATAMOTO_MANIFEST_KEY") {
		t.Error("the publishing step must not see the manifest key")
	}
	for _, s := range []string{"RELEASES_SSH_KEY", "RELEASES_SSH_HOST", "RELEASES_SSH_USER", "RELEASES_SSH_KNOWN_HOSTS"} {
		if strings.Count(ch, "secrets."+s) != 1 || !strings.Contains(publish, "secrets."+s) {
			t.Errorf("secrets.%s must appear once, in the Publish channels step", s)
		}
	}
	// Order: sign, publish, then remove the keys (on every outcome).
	// The signed files are made under umask 077; Caddy must be able to read them
	// on the box, so they are chmod 0644 after signing and rsync forces F644.
	mustContain(t, "channels.yml (Sign channels)", sign, `chmod 0644 "$RUNNER_TEMP"/out/*.json`)
	if strings.Index(sign, "chmod 0644") < strings.Index(sign, "channel resign") {
		t.Error("channels.yml: chmod 0644 must come after signing")
	}
	mustContain(t, "channels.yml (Publish channels)", publish, "--chmod=F644", "--no-perms", "--no-links")
	mustContain(t, "channels.yml", ch, "rrsync -wo -munge")
	mustNotContain(t, "channels.yml", ch, "chmod 0644 \"$RUNNER_TEMP/manifest.key", "chmod 644 \"$RUNNER_TEMP/manifest.key")
	// A stale run must not overwrite newer channel state: runs are serialised and
	// only the run for the head of main publishes.
	mustContain(t, "channels.yml", ch, "concurrency:", "group: ${{ github.workflow }}", "cancel-in-progress: false")
	mustNotContain(t, "channels.yml", ch, "cancel-in-progress: true")
	mustContain(t, "channels.yml (Publish channels)", publish, "git/ref/heads/main", `!= "$GITHUB_SHA"`)
	if strings.Index(publish, "git/ref/heads/main") > strings.Index(publish, "rsync") {
		t.Error("channels.yml: the main-head check must come before rsync")
	}
	i, j, k := strings.Index(ch, "Sign channels"), strings.Index(ch, "Publish channels"), strings.Index(ch, "Remove key")
	if !(i < j && j < k) {
		t.Error("channels.yml steps must run in the order sign, publish, remove key")
	}
	rm := workflowStep(t, ch, "Remove key")
	mustContain(t, "channels.yml (Remove key)", rm, "manifest.key", "deploy.key", "known_hosts", "if: always()")
}

// TestReleaseBuildGuard runs the real Makefile target: without the two
// release variables it must stop before touching DIST, and with them the
// recipe must pin the settings that change the binary whatever the caller
// exports.
func TestReleaseBuildGuard(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	root := filepath.Join(workflowDir, "..", "..")
	dist := filepath.Join(t.TempDir(), "dist")
	run := func(env []string, args ...string) (string, error) {
		cmd := exec.Command("make", append([]string{"-C", root, "release-build", "DIST=" + dist}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// VERSION and COMMIT are git describe values in the Makefile; they must not satisfy the guard.
	for _, args := range [][]string{nil, {"RELEASE_VERSION=v1.0.0"}, {"RELEASE_COMMIT=abc"}, {"VERSION=v1.0.0", "COMMIT=abc"}} {
		if out, err := run([]string{"RELEASE_VERSION=", "RELEASE_COMMIT="}, args...); err == nil || !strings.Contains(out, "set RELEASE_VERSION") {
			t.Errorf("make release-build %v: want the guard to fire, got err=%v\n%s", args, err, out)
		}
	}
	if _, err := os.Stat(dist); err == nil {
		t.Error("the guard fired after DIST was created")
	}
	out, err := run([]string{"GOAMD64=v3", "GOFLAGS=-tags=x", "GOEXPERIMENT=boringcrypto"}, "-n", "RELEASE_VERSION=v1.0.0", "RELEASE_COMMIT=abc")
	if err != nil {
		t.Fatalf("make -n: %v\n%s", err, out)
	}
	mustContain(t, "release-build recipe", out, "GOENV=off", "GOWORK=off", "GOAMD64=v1", "GOARM64=v8.0", "GOFLAGS=", "GOEXPERIMENT=", `GOTOOLCHAIN="$tc"`, "-trimpath", "-buildvcs=false")
	// The bundles ride along: the bare binaries stay the updater's files.
	mustContain(t, "release-build recipe", out, "release-bundles")
	out, err = run(nil, "-n", "release-bundles", "DIST=.")
	if err != nil {
		t.Fatalf("make -n release-bundles: %v\n%s", err, out)
	}
	mustContain(t, "release-bundles recipe", out, "THIRD_PARTY_LICENSES", "cp LICENSE NOTICE", ".tar.gz", "--mtime=@0", "gzip -n")
}

// workflowStep returns the text of the step named name (up to the next step).
func workflowStep(t *testing.T, text, name string) string {
	t.Helper()
	start := strings.Index(text, "- name: "+name)
	if start < 0 {
		t.Fatalf("step %q not found", name)
	}
	rest := text[start+1:]
	if end := strings.Index(rest, "\n      - "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// workflowJob returns the text of the job named name (up to the next job).
func workflowJob(t *testing.T, text, name string) string {
	t.Helper()
	start := strings.Index(text, "\n  "+name+":\n")
	if start < 0 {
		t.Fatalf("job %q not found", name)
	}
	rest := text[start+1:]
	if end := regexp.MustCompile(`\n  [a-z-]+:\n`).FindStringIndex(rest[len(name)+4:]); end != nil {
		return rest[:len(name)+4+end[0]]
	}
	return rest
}

// TestChannelsFreshnessWorkflow: the weekly alarm needs no secret, cannot write
// and fails through the script's exit code. It lives with channels.yml.
func TestChannelsFreshnessWorkflow(t *testing.T) {
	if !hasWorkflow("channels-freshness.yml") {
		t.Skip("channels-freshness.yml is not in this repository (public tree)")
	}
	f := readWorkflow(t, "channels-freshness.yml")
	mustContain(t, "channels-freshness.yml", f, "schedule:", "cron:", "workflow_dispatch:", "permissions: {}", "contents: read",
		"persist-credentials: false", "scripts/check-channel-freshness.sh --min-days 7")
	mustNotContain(t, "channels-freshness.yml", f, "secrets.", ": write", "continue-on-error", "|| true")
	if _, err := os.Stat("../../../scripts/check-channel-freshness.sh"); err != nil {
		t.Errorf("the script the alarm runs is missing: %v", err)
	}
}
