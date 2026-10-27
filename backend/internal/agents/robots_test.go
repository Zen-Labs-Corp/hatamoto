package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestDenyTokens(t *testing.T) {
	reg := Default()
	training := DenyTokens(reg, []Purpose{PurposeTraining})
	for _, want := range []string{"GPTBot", "ClaudeBot", "CCBot", "Google-Extended", "Bytespider", "Meta-ExternalAgent", "Applebot-Extended", "FacebookBot"} {
		if !slices.Contains(training, want) {
			t.Errorf("deny training missing %s: %v", want, training)
		}
	}
	for _, not := range []string{"ChatGPT-User", "OAI-SearchBot", "Googlebot", "facebookexternalhit"} {
		if slices.Contains(training, not) {
			t.Errorf("deny training must not include %s", not)
		}
	}
	if !slices.IsSorted(training) {
		t.Errorf("not sorted: %v", training)
	}
	// Distinct and no empties across every purpose.
	all := DenyTokens(reg, allPurposes)
	seen := map[string]bool{}
	for _, tok := range all {
		if tok == "" || seen[tok] {
			t.Fatalf("empty or duplicate token %q in %v", tok, all)
		}
		seen[tok] = true
	}
	if got := DenyTokens(reg, nil); got == nil || len(got) != 0 {
		t.Errorf("empty policy = %#v, want []", got)
	}
}

func putPolicy(h http.Handler, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/robots-policy", strings.NewReader(body)))
	return rr
}

func TestRobotsPolicyPut(t *testing.T) {
	f := &fakeAdminStore{}
	h := adminRouter(newTestAdmin(f))

	rr := putPolicy(h, `{"purposes":[{"purpose":"training","action":"deny"},{"purpose":"search_index","action":"allow"}]}`)
	if rr.Code != 200 || !f.setCalled || len(f.setDenied) != 1 || f.setDenied[0] != PurposeTraining {
		t.Fatalf("deny training: %d %s denied=%v", rr.Code, rr.Body, f.setDenied)
	}
	var resp robotsPolicyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Purposes) != len(allPurposes) || resp.Purposes[0].Purpose != "training" || resp.Purposes[0].Action != "deny" {
		t.Errorf("response = %+v", resp.Purposes[0])
	}
	var ignoring int
	for _, p := range resp.Purposes {
		if p.Tokens == nil || p.IgnoresRobotsAgents == nil {
			t.Errorf("%s: null array", p.Purpose)
		}
		ignoring += len(p.IgnoresRobotsAgents)
	}
	if ignoring == 0 {
		t.Error("no ignores_robots agents reported")
	}

	// All-allow (or empty) clears the policy.
	f.setCalled = false
	rr = putPolicy(h, `{"purposes":[{"purpose":"training","action":"allow"}]}`)
	if rr.Code != 200 || !f.setCalled || len(f.setDenied) != 0 {
		t.Errorf("all allow: %d denied=%v called=%v", rr.Code, f.setDenied, f.setCalled)
	}
	if rr = putPolicy(h, `{}`); rr.Code != 200 || len(f.setDenied) != 0 {
		t.Errorf("empty body: %d", rr.Code)
	}
}

func TestRobotsPolicyPutValidation(t *testing.T) {
	f := &fakeAdminStore{}
	h := adminRouter(newTestAdmin(f))
	for name, body := range map[string]string{
		"unknown purpose": `{"purposes":[{"purpose":"nope","action":"deny"}]}`,
		"bad action":      `{"purposes":[{"purpose":"training","action":"block"}]}`,
		"duplicate":       `{"purposes":[{"purpose":"training","action":"deny"},{"purpose":"training","action":"allow"}]}`,
		"not json":        `deny training`,
	} {
		if rr := putPolicy(h, body); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rr.Code)
		}
	}
	if f.setCalled {
		t.Error("store written despite validation errors")
	}
}

func TestRobotsPolicyGetDefaultsToAllow(t *testing.T) {
	rr := get(t, adminRouter(newTestAdmin(&fakeAdminStore{})), "/robots-policy")
	var resp robotsPolicyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.Purposes {
		if p.Action != "allow" {
			t.Errorf("%s = %s with empty store, want allow", p.Purpose, p.Action)
		}
	}
}

// STOP-ROBOTS: migrations must never seed the policy table.
func TestMigrationsDoNotSeedRobotsPolicy(t *testing.T) {
	data, err := os.ReadFile("../../migrations/013_agent_first_crawl_robots.sql")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(l, "--") {
			continue
		}
		if strings.Contains(l, "insert into") && strings.Contains(l, "robots_policy") {
			t.Fatalf("migration seeds the robots policy: %s", line)
		}
	}
}
