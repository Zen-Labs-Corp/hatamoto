package server

import (
	"os"
	"strings"
	"testing"
)

// restWriteAllow lists the non-read routes that may answer something other
// than 403 for the restricted roles, with the reason.
var restWriteAllow = map[string]string{
	"POST /api/v1/auth/logout": "own session",
	"PATCH /api/v1/me":         "own account",
}

// assertNoWrites fails when a non-read route answers a listed column with
// anything but 403. Public rows (anon cell is not 401) are skipped; every
// allow key must match a line, so the list cannot go stale.
func assertNoWrites(t *testing.T, lines, cols []string, allow map[string]string) {
	t.Helper()
	used := map[string]bool{}
	for _, l := range lines {
		f := strings.Fields(l)
		pattern := f[0] + " " + f[1]
		if _, ok := allow[pattern]; ok {
			used[pattern] = true
			continue
		}
		if f[0] == "GET" || f[0] == "HEAD" || f[0] == "OPTIONS" || !strings.Contains(l, " anon=401") {
			continue
		}
		for _, c := range cols {
			if !strings.Contains(l, " "+c+"=403") {
				t.Errorf("%s is not refused for %s: %s", pattern, c, l)
			}
		}
	}
	for k := range allow {
		if !used[k] {
			t.Errorf("stale allow entry %q", k)
		}
	}
}

// TestRESTRolesRouteMatrix pins every protected /api/v1 route for the four
// restricted built-in roles and proves none of them can write outside their own account.
func TestRESTRolesRouteMatrix(t *testing.T) {
	roles := []string{"read-only", "author", "contributor", "reviewer"}
	ps := []principal{{"anon", nil}}
	for _, r := range roles {
		ps = append(ps, principal{r, []string{"Authorization", "Bearer test-session-" + r}})
	}
	lines := routeAuthzMatrixFor(t, ps)
	got := strings.Join(lines, "\n") + "\n"
	if again := strings.Join(routeAuthzMatrixFor(t, ps), "\n") + "\n"; again != got {
		t.Fatal("matrix is not deterministic across two runs")
	}

	const golden = "testdata/route_authz_roles.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("matrix differs from %s; regenerate with UPDATE_GOLDEN=1 if deliberate", golden)
		}
	}

	assertNoWrites(t, lines, roles, restWriteAllow)
}

// Form submissions are personal data: the restricted roles are refused on both
// REST reads, while an editor and an owner still reach them.
func TestRESTFormSubmissionReadsNeedTheReadAction(t *testing.T) {
	ps := []principal{{"editor", []string{"Authorization", "Bearer test-session-editor"}}, {"owner", []string{"Authorization", "Bearer test-session-owner"}}}
	roles := []string{"read-only", "author", "contributor", "reviewer"}
	for _, r := range roles {
		ps = append(ps, principal{r, []string{"Authorization", "Bearer test-session-" + r}})
	}
	seen := 0
	for _, l := range routeAuthzMatrixFor(t, ps) {
		if !strings.HasPrefix(l, "GET /api/v1/forms/{id}/submissions") {
			continue
		}
		seen++
		for _, r := range roles {
			if !strings.Contains(l, " "+r+"=403") {
				t.Errorf("%s is not refused for %s: %s", strings.Fields(l)[1], r, l)
			}
		}
		for _, r := range []string{"editor", "owner"} {
			if !strings.Contains(l, " "+r+"=reached") {
				t.Errorf("%s no longer reaches %s: %s", r, strings.Fields(l)[1], l)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("found %d submission read routes, want 2", seen)
	}
}
