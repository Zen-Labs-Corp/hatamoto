package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// agentPage is the body of the agent page. A robots.txt name that never
// fetches (Google-Extended) is PolicyOnly: no figures, just the policy.
type agentPage struct {
	Operator, PurposeName string
	PolicyOnly            bool
	KPIs                  []ui.Stat
	Hero                  ui.AiaChart
	Pages                 ui.BarList
	Responses             *ui.CategoryBar
	Verification          *ui.CategoryBar
	Tracker               ui.Tracker
	TrackerCaption        string
	TrackerTable          ui.DataTable
	Policy                agentPolicy
	HasPolicy             bool   // the purpose is one the robots policy covers
	ledeText              string // what the agent's purpose is for, under its name
}

// Chips are the operator and purpose labels shown under the agent's name.
func (p *agentPage) Chips() []string {
	var c []string
	for _, s := range []string{p.Operator, p.PurposeName} {
		if s != "" {
			c = append(c, s)
		}
	}
	return c
}

// ChipBadges are the operator and purpose labels shown beside the agent's name: plain tags,
// the Training purpose in the info tone.
func (p *agentPage) ChipBadges() []ui.Badge {
	var c []ui.Badge
	if p.Operator != "" {
		c = append(c, ui.Badge{Label: p.Operator, Plain: true})
	}
	if p.PurposeName != "" {
		b := ui.Badge{Label: p.PurposeName, Plain: true}
		if p.PurposeName == purposeLabels["training"] {
			b.Kind = "info"
		}
		c = append(c, b)
	}
	return c
}

// agentPolicy is the Robots policy card. The form posts only this purpose, with
// scope=one, to the Crawlers save handler, which merges it into the saved policy.
type agentPolicy struct {
	Token, Purpose string
	Blocked        bool
	Form           ui.AiaPolicyForm
	Control        *ui.AiaPolicy // nil unless the signed-in user can change the policy
	Note           string
	Callout        *ui.Callout
}

// topAgentPages is how many pages the agent's most fetched list shows.
const topAgentPages = 6

// handleAgentPage serves /admin/analytics/agents/{id}; the sidebar keeps
// Crawlers current. An id with no activity that is not in the registry is a 404.
func (a *Admin) handleAgentPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rg, msg := parseRange(q)
	cmp := compareFor(rg, parseCompare(q))
	id := r.PathValue("id")
	detail, err := a.agentData(r.Context(), rg, id)
	switch {
	case errors.Is(err, errNoService):
		a.unavailable(w, r, "Analytics")
		return
	case err == nil && !detail.Found:
		a.ErrorBack(w, r, http.StatusNotFound, "Agent not found", "That agent has no activity and is not in the registry.", a.adminPath+"/analytics/crawlers", "Back to agents")
		return
	}
	status := http.StatusOK
	if msg != "" {
		status = http.StatusUnprocessableEntity
	}
	title := a.agentName(id)
	d := &sectionPage{Title: title, RangeErr: msg, Compare: cmp.On, Prev: cmp.Prev,
		Range: a.dateRangeFor(r.URL.Path, rg, map[string]string{}, &cmp)}
	if err == nil {
		d.Body, err = a.buildAgent(r.Context(), q, rg, cmp.On, id, detail)
	}
	if err != nil {
		a.logger().Error("analytics agent page failed", "agent", id, "err", err)
		d.Err = "This agent could not be loaded. Try again in a moment."
		d.Body = nil
		if status == http.StatusOK {
			status = http.StatusInternalServerError
		}
	}
	if l, ok := d.Body.(interface{ lede() string }); ok {
		d.Lede = l.lede()
	}
	v := a.NewView(w, r, title)
	v.Crumbs = a.areaCrumbs("ai-crawlers", Crumb{Label: title})
	for i, c := range v.Crumbs {
		if c.Href == a.adminPath+"/analytics/crawlers" { // back to the list keeps the range and compare
			v.Crumbs[i].Href = sectionHref(c.Href, q)
		}
	}
	v.AreaCarry = analyticsViewCarry(q)
	v.Data = d
	a.Render(w, r, status, "analytics/agent", v)
}

func (p *agentPage) lede() string { return p.ledeText }

func (a *Admin) buildAgent(ctx context.Context, q url.Values, rg agents.Range, cmp bool, id string, d *agentDetail) (*agentPage, error) {
	reg := a.registry().ByID(id)
	pid := purposeID(d.Purpose)
	owner := false
	if u := userFrom(ctx); u != nil {
		owner = u.Role == "owner"
	}
	form, err := a.robotsData(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	p := &agentPage{Operator: d.Operator, PurposeName: d.Purpose, PolicyOnly: reg != nil && len(reg.UA) == 0,
		ledeText: purposeDescriptions[pid]}
	p.HasPolicy = slices.Contains(robotsPurposes, agents.Purpose(pid))
	pol := &p.Policy
	pol.Token, pol.Purpose = d.RobotsToken, pid
	for _, r := range form.Rows {
		if r.Purpose == pid {
			pol.Blocked = r.Deny
		}
	}
	pol.Form = ui.AiaPolicyForm{Owner: owner, Return: sectionHref(a.adminPath+"/analytics/agents/"+url.PathEscape(id), q)}
	if owner && p.HasPolicy {
		pol.Control = &ui.AiaPolicy{Purpose: pid, Legend: robotsLegend(pid, d.Purpose), Deny: pol.Blocked, Owner: true}
		purposeName := lowerFirst(d.Purpose)
		if p.PolicyOnly {
			pol.Note = "Changes the policy for the whole " + purposeName + " purpose."
		} else {
			n := 0
			for _, r := range a.registry().Agents {
				if string(r.Purpose) == pid && len(r.UA) > 0 {
					n++
				}
			}
			pol.Note = "Changes the policy for all " + strconv.Itoa(n) + " " + purposeName + " agents, not only " + d.Name + "."
		}
	}
	if d.IgnoresRobots {
		pol.Callout = &ui.Callout{Kind: "warning", Title: "Ignores robots.txt",
			Body: d.Name + " is known to fetch pages that robots.txt tells it to stay away from. Blocking it needs a firewall or server rule."}
	}
	if p.PolicyOnly {
		return p, nil
	}

	// KPIs, with the previous period's numbers when comparing.
	var prev agentDetail
	if cmp {
		if pd, err := a.agentData(ctx, previousRange(rg), id); err == nil && pd.Found {
			prev = *pd
		}
	}
	agentURL := a.dataURL("agent.json", rg, false) + "&id=" + url.QueryEscape(id)
	// A phrase or a date is not a metric: it is a plain badge, so it never wraps at 30px or poses as a number.
	verified := ui.Stat{Label: "Verified", Badge: &ui.Badge{Label: "n/a", Plain: true}, Note: "No fetches in this period"}
	if d.Hits > 0 {
		verified = ui.Stat{Label: "Verified", Value: fixed(ratio(d.Verified, d.Hits), 0), Note: fmtNum(d.Spoofed) + " spoofed, " + fmtNum(d.Unverifiable) + " unverifiable"}
	}
	seen := ui.Stat{Label: "Last seen", Badge: &ui.Badge{Label: "Not seen", Plain: true}, Note: "No fetches recorded"}
	if f := strings.Fields(d.LastSeenAt); len(f) >= 3 {
		seen.Badge, seen.Note = &ui.Badge{Label: strings.Join(f[:3], " "), Plain: true}, strings.Join(f[3:], " ")
	}
	p.KPIs = []ui.Stat{
		{Label: "Fetches", Value: fmtNum(d.Hits), Change: kpiDelta(cmp, rg, float64(d.Hits), float64(prev.Hits), false, false), Spark: liveSpark("aia-spark-agent-fetches", 1, ui.SparkLive{Src: agentURL, Chart: "daily"})},
		verified,
		{Label: "Pages read", Value: fmtNum(int64(d.PagesRead)), Change: kpiDelta(cmp, rg, float64(d.PagesRead), float64(prev.PagesRead), false, false)},
		seen,
	}

	unit := per(rg)
	p.Hero = ui.AiaChart{ID: "w-ag-fetch", Title: "Fetches per " + unit, Description: "How often " + d.Name + " read your site over the selected range.",
		Src: a.dataURL("agent.json", rg, cmp) + "&id=" + url.QueryEscape(id), Chart: "daily", Hero: true, Compare: cmp, Table: d.daily}

	p.Pages = ui.BarList{Label: "Pages " + d.Name + " fetched most", Quiet: true, Mono: true, Empty: "No data for this period."}
	for i, pc := range d.TopPaths {
		if i == topAgentPages {
			break
		}
		p.Pages.Items = append(p.Pages.Items, ui.BarItem{Label: pc.Path, Value: fmtNum(pc.Hits), N: float64(pc.Hits)})
	}
	var s2, s3, s4, s5 int64
	for _, s := range d.ByStatus {
		switch s.Status / 100 {
		case 2:
			s2 += s.Hits
		case 3:
			s3 += s.Hits
		case 4:
			s4 += s.Hits
		case 5:
			s5 += s.Hits
		}
	}
	p.Responses = responsesBar(s2, s3, s4, s5)
	p.Verification = verificationBar(d.Verified, d.Unverifiable, d.Spoofed)

	// Activity tracker: one block per day, green when the agent fetched.
	days := d.dailyM.Days
	counts := d.dailyM.Vals["Fetches"]
	p.Tracker = ui.Tracker{Label: d.Name + " activity by day", States: true}
	if len(days) > 0 {
		p.Tracker.Start, p.Tracker.End = shortDay(days[0]), shortDay(days[len(days)-1])
	}
	active := 0
	for i, day := range days {
		blk := ui.TrackerBlock{Kind: "neutral", Tip: shortDay(day) + ": No fetches"}
		if counts[i] > 0 {
			active++
			blk = ui.TrackerBlock{Kind: "success", Tip: shortDay(day) + ": " + fmtNum(counts[i]) + " fetches"}
		}
		p.Tracker.Blocks = append(p.Tracker.Blocks, blk)
	}
	if active > 0 {
		p.Tracker.Legend = append(p.Tracker.Legend, ui.TrackerKey{Kind: "success", Label: "Fetched"})
	}
	if active < len(days) {
		p.Tracker.Legend = append(p.Tracker.Legend, ui.TrackerKey{Kind: "neutral", Label: "No fetches"})
	}
	p.TrackerCaption = "Active on " + strconv.Itoa(active) + " of " + strconv.Itoa(len(days)) + " days."
	p.TrackerTable = withoutTotal(d.dailyM.table("Fetches per day by "+d.Name, "Day"))
	return p, nil
}
