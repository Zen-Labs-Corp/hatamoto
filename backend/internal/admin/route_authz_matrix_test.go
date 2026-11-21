package admin

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var (
	matrixRestRe  = regexp.MustCompile(`\{[^}]*\.\.\.\}`)
	matrixParamRe = regexp.MustCompile(`\{[^}$]*\}`)
)

// adminAuthzMatrix requests every admin route as an anonymous visitor, an
// editor and an owner, each on a fresh session, and returns one sorted line per
// route with the status codes. A handler that dies on the rig's missing
// collaborators counts as "reached".
func adminAuthzMatrix(t *testing.T) []string {
	t.Helper()
	g := newRig(t)
	var lines []string
	for _, rt := range g.routes {
		method, path, _ := strings.Cut(rt.Pattern, " ")
		url := strings.ReplaceAll(path, "{$}", "")
		url = matrixRestRe.ReplaceAllString(url, "a/b")
		url = matrixParamRe.ReplaceAllString(url, "p1")
		line := rt.Pattern
		for _, p := range []struct {
			name  string
			token func() string
		}{
			{"anon", func() string { return "" }},
			{"editor", func() string { return g.freshToken(g.editor.ID) }},
			{"owner", func() string { return g.freshToken(g.owner.ID) }},
		} {
			cell := "reached"
			func() {
				defer func() { _ = recover() }()
				rec := g.do(method, url, p.token(), nil, nil)
				cell = strconv.Itoa(rec.Code)
			}()
			line += " " + p.name + "=" + cell
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return lines
}

// TestAdminRouteAuthzMatrixGolden pins the status of every admin route for an
// anonymous visitor, an editor and an owner. A gate refactor must leave the
// file untouched. Regenerate with UPDATE_GOLDEN=1 only for a deliberate
// behaviour change.
func TestAdminRouteAuthzMatrixGolden(t *testing.T) {
	lines := adminAuthzMatrix(t)
	got := strings.Join(lines, "\n") + "\n"
	if again := strings.Join(adminAuthzMatrix(t), "\n") + "\n"; again != got {
		gl, al := lines, strings.Split(strings.TrimSuffix(again, "\n"), "\n")
		for i := range gl {
			if i < len(al) && gl[i] != al[i] {
				t.Errorf("unstable:\n  %s\n  %s", gl[i], al[i])
			}
		}
		t.Fatal("matrix is not deterministic across two runs")
	}

	gated := 0
	for _, l := range lines {
		if strings.Contains(l, " editor=403") && !strings.Contains(l, " owner=403") {
			gated++
		}
	}
	if gated < 20 {
		t.Fatalf("only %d routes reject the editor but not the owner; the gates were not reached", gated)
	}

	const golden = "testdata/route_authz.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got == string(want) {
		return
	}
	inWant := map[string]bool{}
	wl := strings.Split(string(want), "\n")
	for _, l := range wl {
		inWant[l] = true
	}
	inGot := map[string]bool{}
	for _, l := range strings.Split(got, "\n") {
		inGot[l] = true
		if !inWant[l] {
			t.Errorf("new:     %s", l)
		}
	}
	for _, l := range wl {
		if !inGot[l] {
			t.Errorf("missing: %s", l)
		}
	}
}
