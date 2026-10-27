package app

import (
	"os"
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
