package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// ---- robots policy ----

type robotsRow struct {
	Purpose, Label string
	Deny           bool
	Tokens         string
	Ignores        string
	// TokenList and IgnoreList are the same two columns as lists (robots.json).
	TokenList, IgnoreList []string
}

type robotsForm struct {
	Rows  []robotsRow
	Owner bool
	Saved bool
}

var robotsPurposes = []agents.Purpose{
	agents.PurposeTraining, agents.PurposeSearchIndex, agents.PurposeUserFetch, agents.PurposeOtherAI,
	agents.PurposeSearch, agents.PurposeSocial, agents.PurposeSEO, agents.PurposeArchive,
}

// robotsData builds the form; denied overrides the stored policy (a re-render
// after a failed save keeps what the owner chose).
func (a *Admin) robotsData(ctx context.Context, denied []agents.Purpose, override map[string]bool) (*robotsForm, error) {
	stats, ok := a.agentStats()
	if !ok {
		return nil, errNoService
	}
	if override == nil {
		var err error
		if denied, err = stats.DeniedPurposes(ctx); err != nil {
			return nil, err
		}
	}
	reg := a.registry()
	f := &robotsForm{}
	for _, p := range robotsPurposes {
		row := robotsRow{Purpose: string(p), Label: purposeLabel(string(p))}
		if override != nil {
			row.Deny = override[string(p)]
		} else {
			for _, d := range denied {
				row.Deny = row.Deny || d == p
			}
		}
		row.TokenList = agents.DenyTokens(reg, []agents.Purpose{p})
		row.Tokens = strings.Join(row.TokenList, ", ")
		ig := []string{}
		for _, ag := range reg.Agents {
			if ag.Purpose == p && ag.IgnoresRobots {
				ig = append(ig, ag.Name)
			}
		}
		sort.Strings(ig)
		row.IgnoreList = ig
		row.Ignores = strings.Join(ig, ", ")
		f.Rows = append(f.Rows, row)
	}
	return f, nil
}

// robotsReturnsFor matches the pages that carry the policy form; the form's
// hidden "return" field names one of them so the save lands where it came from.
func robotsReturnsFor(prefix string) *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + `/analytics/(crawlers|health|agents/[^/]+)$`)
}

// robotsReturn is where to go after a save: the form's "return" path when it is
// one of a.robotsReturns (never an absolute URL), else Crawlers. The range and
// compare switch of the page the form was on are kept.
func (a *Admin) robotsReturn(r *http.Request) string {
	u, err := url.Parse(r.PostFormValue("return"))
	if err != nil || u.Scheme != "" || u.Host != "" || !a.robotsReturns.MatchString(u.Path) || path.Base(u.Path) == ".." || path.Base(u.Path) == "." {
		return a.url("analytics", "crawlers")
	}
	// u.Path is decoded: write it back escaped, as the agent route accepts any id.
	esc := (&url.URL{Path: u.Path}).EscapedPath()
	keep := url.Values{}
	for _, k := range []string{"from", "to", "compare"} {
		if v := u.Query().Get(k); v != "" {
			keep.Set(k, v)
		}
	}
	if len(keep) == 0 {
		return esc
	}
	return esc + "?" + keep.Encode()
}

func (a *Admin) handleRobotsSave(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u == nil || u.Role != "owner" {
		a.Error(w, r, http.StatusForbidden, "Not allowed", "Only the site owner can change the robots policy.")
		return
	}
	stats, ok := a.agentStats()
	if !ok {
		a.unavailable(w, r, "Analytics")
		return
	}
	denied, ok := a.robotsPosted(w, r, stats)
	if !ok {
		return
	}
	if err := stats.SetDenied(r.Context(), denied); err != nil {
		a.logger().Error("save robots policy failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The robots policy could not be saved.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Robots policy saved. robots.txt now tells these crawlers to stay away.")
	http.Redirect(w, r, a.robotsReturn(r), http.StatusSeeOther)
}

// robotsPosted is the policy a save post asks for. The Crawlers and Health
// forms post all eight purposes. The agent page posts scope=one and only its
// own purpose, which is merged into the stored policy so the other seven are
// left as they are. Anything else, or a value other than allow or deny, is a
// 400 and ok is false.
func (a *Admin) robotsPosted(w http.ResponseWriter, r *http.Request, stats AgentStats) (denied []agents.Purpose, ok bool) {
	bad := func() ([]agents.Purpose, bool) {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The robots policy form was incomplete. Reload the page and try again.")
		return nil, false
	}
	valid := func(v string) bool { return v == agents.RobotsAllow || v == agents.RobotsDeny }
	switch r.PostFormValue("scope") {
	case "":
		for _, p := range robotsPurposes {
			v := r.PostFormValue("purpose." + string(p))
			if !valid(v) {
				return bad()
			}
			if v == agents.RobotsDeny {
				denied = append(denied, p)
			}
		}
		return denied, true
	case "one":
		var one agents.Purpose
		n := 0
		for k := range r.PostForm {
			if strings.HasPrefix(k, "purpose.") {
				n++
				one = agents.Purpose(strings.TrimPrefix(k, "purpose."))
			}
		}
		if n != 1 || !slices.Contains(robotsPurposes, one) || !valid(r.PostFormValue("purpose."+string(one))) {
			return bad()
		}
		stored, err := stats.DeniedPurposes(r.Context())
		if err != nil {
			a.logger().Error("load robots policy failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The robots policy could not be saved.")
			return nil, false
		}
		for _, p := range stored {
			if p != one {
				denied = append(denied, p)
			}
		}
		if r.PostFormValue("purpose."+string(one)) == agents.RobotsDeny {
			denied = append(denied, one)
		}
		return denied, true
	}
	return bad()
}

type robotsPurposeJSON struct {
	Purpose             string   `json:"purpose"`
	Label               string   `json:"label"`
	Action              string   `json:"action"`
	Tokens              []string `json:"tokens"`
	IgnoresRobotsAgents []string `json:"ignores_robots_agents"`
}

// robotsJSON is the current policy for any signed-in role. It is the state of
// the site, not of a date range, so it does not parse one. policy.js reads it
// when the browser restores a page from its back/forward cache.
func (a *Admin) robotsJSON(w http.ResponseWriter, r *http.Request) {
	form, err := a.robotsData(r.Context(), nil, nil)
	if err != nil {
		if errors.Is(err, errNoService) {
			a.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "analytics is not available"})
			return
		}
		a.logger().Error("load robots policy failed", "err", err)
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load the robots policy"})
		return
	}
	u := userFrom(r.Context())
	body := struct {
		Owner    bool                `json:"owner"`
		Purposes []robotsPurposeJSON `json:"purposes"`
	}{Owner: u != nil && u.Role == "owner", Purposes: []robotsPurposeJSON{}}
	for _, row := range form.Rows {
		action := agents.RobotsAllow
		if row.Deny {
			action = agents.RobotsDeny
		}
		body.Purposes = append(body.Purposes, robotsPurposeJSON{Purpose: row.Purpose, Label: row.Label, Action: action, Tokens: row.TokenList, IgnoresRobotsAgents: row.IgnoreList})
	}
	a.writeJSON(w, http.StatusOK, body)
}

// robotsLegend names one purpose's Allow / Block pair for screen readers.
func robotsLegend(purpose, label string) string {
	switch agents.Purpose(purpose) {
	case agents.PurposeTraining, agents.PurposeSearchIndex, agents.PurposeUserFetch, agents.PurposeOtherAI:
		return "Robots policy for all " + lowerFirst(label) + " agents"
	}
	return "Robots policy for " + lowerFirst(label) + " crawlers"
}

// lowerFirst is label for the middle of a sentence: its first letter in lower
// case, unless the first word is an acronym ("SEO", "AI").
func lowerFirst(label string) string {
	word, _, _ := strings.Cut(label, " ")
	if len(word) > 1 && word == strings.ToUpper(word) {
		return label
	}
	return strings.ToLower(label[:1]) + label[1:]
}

// aiaPolicies are the policy controls of the form, by purpose: radios for the
// owner, badges for everyone else.
func aiaPolicies(f *robotsForm, owner bool) map[string]*ui.AiaPolicy {
	out := make(map[string]*ui.AiaPolicy, len(f.Rows))
	for _, r := range f.Rows {
		out[r.Purpose] = &ui.AiaPolicy{Purpose: r.Purpose, Legend: robotsLegend(r.Purpose, r.Label), Deny: r.Deny, Owner: owner}
	}
	return out
}
