package app

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// composeList returns the "- item" lines under the key at indent, inside the
// block that starts at the line "<blockIndent>name:".
func composeList(t *testing.T, lines []string, block, key string) []string {
	t.Helper()
	in, listing := false, false
	var out []string
	for _, l := range lines {
		trim := strings.TrimSpace(l)
		indent := len(l) - len(strings.TrimLeft(l, " "))
		switch {
		case trim == "" || strings.HasPrefix(trim, "#"):
		case indent == 0:
			in, listing = trim == block+":", false
		case in && trim == key+":":
			listing = true
		case in && listing && strings.HasPrefix(trim, "- "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(trim, "- ")))
		case in && listing && !strings.HasPrefix(trim, "-"):
			listing = false
		}
	}
	return out
}

// An existing stack that pulls this compose file and runs `up` must keep the
// volume its server secret lives in, at the path it was mounted at before.
func TestComposeKeepsSecretsVolumeWhereItWas(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	var svc []string
	inSvc, inHat := false, false
	for _, l := range lines {
		trim := strings.TrimSpace(l)
		indent := len(l) - len(strings.TrimLeft(l, " "))
		if indent == 0 && trim != "" {
			inSvc = trim == "services:"
		}
		if inSvc && indent == 2 && trim != "" {
			inHat = trim == "hatamoto:"
		}
		if inSvc && inHat {
			svc = append(svc, strings.TrimPrefix(l, "  "))
		}
	}
	vols := composeList(t, append([]string{"hatamoto:"}, svc[1:]...), "hatamoto", "volumes")
	has := func(want string) bool {
		for _, v := range vols {
			if v == want {
				return true
			}
		}
		return false
	}
	if !strings.Contains(strings.Join(svc, "\n"), "\n  restart: unless-stopped") {
		t.Error("the hatamoto service needs `restart: unless-stopped`: the supervisor is PID 1 and nothing else restarts it")
	}
	for _, want := range []string{"secrets:/data/secrets", "data:/data"} {
		if !has(want) {
			t.Errorf("hatamoto service lacks volume %q; has %v", want, vols)
		}
	}
	for _, v := range vols {
		if strings.HasSuffix(v, ":/app/data") {
			t.Errorf("stale /app/data mount %q", v)
		}
	}
	declared := false
	inTop := false
	for _, l := range lines {
		if len(l) > 0 && l[0] != ' ' && l[0] != '#' {
			inTop = strings.TrimSpace(l) == "volumes:"
		}
		if inTop && strings.TrimSpace(l) == "secrets:" {
			declared = true
		}
	}
	if !declared {
		t.Error("top-level volumes: no longer declares secrets")
	}
}

// The mail catcher shows every message in full and has no sign-in, so its web
// UI must stay on the loopback address, and its image must not float.
func TestComposeMailpitIsLocalAndPinned(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	i := strings.Index(text, "\n  mailpit:")
	if i < 0 {
		t.Fatal("no mailpit service")
	}
	block := text[i+1:]
	if j := strings.Index(block, "\n\n"); j >= 0 {
		block = block[:j]
	}
	if !strings.Contains(block, `- "127.0.0.1:8025:8025"`) {
		t.Errorf("mailpit UI must be published on 127.0.0.1 only:\n%s", block)
	}
	for _, l := range strings.Split(block, "\n") {
		if line := strings.TrimSpace(l); strings.HasPrefix(line, "image:") {
			if ref := strings.TrimSpace(strings.TrimPrefix(line, "image:")); !strings.Contains(ref, ":v") {
				t.Errorf("mailpit image %q must be pinned to a version tag", ref)
			}
		}
	}
}

// The compose file pulls a published release image; the source build is opt-in.
func TestComposePullsPinnedReleaseImage(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	// Same tag rule as scripts/bump-compose-image.sh: vX.Y.Z, no leading zeros.
	const semver = `v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)`
	re := regexp.MustCompile(`(?m)^    image: ghcr\.io/zen-labs-corp/hatamoto:(` + semver + `)$`)
	m := re.FindAllSubmatch(raw, -1)
	if len(m) != 1 {
		t.Fatalf("want one pinned ghcr hatamoto image line (vX.Y.Z), got %d", len(m))
	}
	tag := string(m[0][1])
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	tags := regexp.MustCompile(`ghcr\.io/zen-labs-corp/hatamoto:(v\d+\.\d+\.\d+)`).FindAllSubmatch(readme, -1)
	if len(tags) == 0 {
		t.Error("README.md names no hatamoto image tag")
	}
	for _, g := range tags {
		if string(g[1]) != tag {
			t.Errorf("README.md names image tag %s, docker-compose.yml pins %s (scripts/bump-compose-image.sh rewrites both)", g[1], tag)
		}
	}
	for _, p := range []struct{ path, re string }{
		{"../../render.yaml", `(?m)^      url: ghcr\.io/zen-labs-corp/hatamoto:(` + semver + `)$`},
		{"../../.do/deploy.template.yaml", `(?m)^        tag: (` + semver + `)$`},
	} {
		b, err := os.ReadFile(p.path)
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(p.re).FindAllSubmatch(b, -1)
		if len(m) != 1 {
			t.Errorf("%s: want one pinned hatamoto image tag line, got %d", p.path, len(m))
			continue
		}
		if string(m[0][1]) != tag {
			t.Errorf("%s pins %s, docker-compose.yml pins %s (scripts/bump-compose-image.sh rewrites both)", p.path, m[0][1], tag)
		}
	}
	if regexp.MustCompile(`(?m)^    build:`).Match(raw) {
		t.Fatal("docker-compose.yml must not build from source; that is docker-compose.build.yml")
	}
	b, err := os.ReadFile("../../docker-compose.build.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^  hatamoto:\n    build: \.$`).Match(b) {
		t.Fatal("docker-compose.build.yml must build the hatamoto service from the checkout")
	}
}
