package admin

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestShellCarriesNoncedSpeculationRules(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", "/admin/entities", "owner-token", nil, nil)
	body := rec.Body.String()
	m := regexp.MustCompile(`<script type="speculationrules" nonce="([^"]+)">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("shell lacks the speculationrules script with a nonce")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "'nonce-"+m[1]+"'") {
		t.Error("the speculationrules nonce is not the CSP nonce")
	}
	var rules struct {
		Prerender []struct {
			Where struct {
				And []map[string]json.RawMessage `json:"and"`
			} `json:"where"`
			Eagerness string `json:"eagerness"`
		} `json:"prerender"`
	}
	if err := json.Unmarshal([]byte(m[2]), &rules); err != nil || len(rules.Prerender) != 1 {
		t.Fatalf("rules JSON: %v %q", err, m[2])
	}
	if rules.Prerender[0].Eagerness != "moderate" || !strings.Contains(m[2], `"/admin/*"`) {
		t.Errorf("rules: %s", m[2])
	}
	for _, want := range []string{
		"/admin/login*", "/admin/setup*", "/admin/logout*", "/admin/theme", "/admin/*/delete*", "/admin/*/confirm*",
		"/admin/*/export.csv*", "/admin/*/preview*", "/admin/preview/*", "/admin/plugins/*/ui*", "/admin/plugins/*/settings*", "/admin/*/bulk*",
		"/admin/*/unsigned*", "/admin/*/check-updates*",
		"/admin/settings/submissions/*", "/admin/entities/*/edit*", "/admin/entities/new*", "/admin/entities/drafts*",
	} {
		if !strings.Contains(m[2], `"`+want+`"`) {
			t.Errorf("rules do not exclude %s", want)
		}
	}
	if strings.Contains(m[2], "/security") {
		t.Error("the Package security page must stay prerenderable like any other page")
	}
	login := g.do("GET", "/admin/login", "", nil, nil).Body.String()
	if strings.Contains(login, "speculationrules") {
		t.Error("the bare layout must not carry speculation rules")
	}
}
