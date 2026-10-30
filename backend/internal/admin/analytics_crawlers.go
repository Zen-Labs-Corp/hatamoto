package admin

import (
	"context"
	"net/url"
	"sort"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// purposeDescriptions say what each purpose is for, as the prototype words them.
var purposeDescriptions = map[string]string{
	"training": "Read pages to train AI models.", "search_index": "Build the index behind AI search answers.",
	"user_fetch": "Fetch a page when a person asks an assistant about it.", "other_ai": "Other AI crawling that fits no group above.",
	"search": "Search engines such as Google and Bing.", "social": "Link previews on social networks and chat apps.",
	"seo": "SEO and site audit tools.", "archive": "Web archives such as the Internet Archive.",
}

// aiPurposes are the groups of the agents table, in the order shown; the other
// robotsPurposes are listed under "Other crawlers".
var aiPurposes = []agents.Purpose{agents.PurposeTraining, agents.PurposeSearchIndex, agents.PurposeUserFetch, agents.PurposeOtherAI}

// purposeID is the purpose id of a display label ("Search index" is "search_index").
func purposeID(label string) string {
	for id, l := range purposeLabels {
		if l == label {
			return id
		}
	}
	return label
}

// crawlersPage is the body of the Crawlers section.
type crawlersPage struct {
	Lede string
	// Verify is the verification banner above the chart; nil shows none.
	Verify *verifyBanner
	Hero   ui.AiaChart
	Agents ui.AiaAgents
	// Owner and Return feed the one policy form around the agents table.
	Owner  bool
	Return string
	// NoFetches is true when no AI agent fetched in the range.
	NoFetches bool
}

func (p *crawlersPage) lede() string { return p.Lede }

// verificationBadge is the verification state of one agent, as verBadge in the prototype.
func verificationBadge(ag agentJSON) ui.Badge {
	switch {
	case ag.Hits == 0:
		return ui.Badge{Label: "No fetches"}
	case ag.Spoofed > 0:
		return ui.Badge{Label: fmtNum(ag.Spoofed) + " spoofed", Kind: "danger"}
	case ag.Unverifiable == ag.Hits:
		return ui.Badge{Label: "Unverifiable"}
	case ag.Verified == ag.Hits:
		return ui.Badge{Label: "All verified", Kind: "success"}
	}
	return ui.Badge{Label: fixed(ratio(ag.Verified, ag.Hits), 0) + " verified"}
}

// lastSeen is the day an agent last fetched, "23 Oct".
func lastSeen(day string) string {
	if day == "" {
		return "Not seen"
	}
	return shortDay(day)
}

// agentsByPurpose is every known agent of the AI purposes with what it fetched
// in the range (an agent that did not is listed with no fetches), grouped by
// purpose id, busiest first.
func (a *Admin) agentsByPurpose(list []agentJSON) map[string][]agentJSON {
	seen := map[string]bool{}
	out := map[string][]agentJSON{}
	for _, ag := range list {
		seen[ag.ID] = true
		out[purposeID(ag.Purpose)] = append(out[purposeID(ag.Purpose)], ag)
	}
	for _, r := range a.registry().Agents {
		if len(r.UA) == 0 || seen[r.ID] {
			continue
		}
		out[string(r.Purpose)] = append(out[string(r.Purpose)], agentJSON{ID: r.ID, Name: r.Name, Operator: r.Operator, Purpose: purposeLabel(string(r.Purpose)),
			IgnoresRobots: r.IgnoresRobots, VerifiedPct: "n/a"})
	}
	for _, rows := range out {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Hits != rows[j].Hits {
				return rows[i].Hits > rows[j].Hits
			}
			return rows[i].Name < rows[j].Name
		})
	}
	return out
}

func (a *Admin) buildCrawlers(ctx context.Context, q url.Values, rg agents.Range, cmp bool) (any, error) {
	ov, err := a.overviewData(ctx, rg, "purpose")
	if err != nil {
		return nil, err
	}
	list, err := a.agentsData(ctx, rg)
	if err != nil {
		return nil, err
	}
	form, err := a.robotsData(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	owner := false
	if u := userFrom(ctx); u != nil {
		owner = u.Role == "owner"
	}
	policies := aiaPolicies(form, owner)
	denied := map[string]bool{}
	for _, r := range form.Rows {
		denied[r.Purpose] = r.Deny
	}

	byPurpose := a.agentsByPurpose(list.Agents)
	total := ov.Totals.AIFetches
	var known, fetched int
	groups := make([]ui.AiaAgentGroup, 0, len(aiPurposes))
	for _, p := range aiPurposes {
		id := string(p)
		rows := byPurpose[id]
		var groupHits, busiest int64
		for _, ag := range rows {
			groupHits += ag.Hits
			busiest = max(busiest, ag.Hits)
		}
		g := ui.AiaAgentGroup{Purpose: purposeLabel(id), Description: purposeDescriptions[id], Policy: policies[id],
			Summary: fmtNum(groupHits) + " fetches, " + fixed(ratio(groupHits, total), 0) + " of all AI fetches."}
		for _, ag := range rows {
			known++
			if ag.Hits > 0 {
				fetched++
			}
			href := sectionHref(a.adminPath+"/analytics/agents/"+url.PathEscape(ag.ID), q)
			g.Rows = append(g.Rows, ui.AiaAgentRow{Name: ag.Name, Href: href, Operator: ag.Operator, Fetches: fmtNum(ag.Hits), Share: int(ratio(ag.Hits, busiest) + 0.5),
				Verification: verificationBadge(ag), Blocked: denied[id], Ignores: ag.IgnoresRobots, LastSeen: lastSeen(ag.LastSeen), Purpose: id, Fetched: ag.Hits > 0})
		}
		for _, r := range a.registry().Agents {
			if r.Purpose == p && len(r.UA) == 0 {
				g.PolicyOnly = append(g.PolicyOnly, ui.AiaPolicyOnly{Name: r.Name, Href: sectionHref(a.adminPath+"/analytics/agents/"+url.PathEscape(r.ID), q), Operator: r.Operator})
			}
		}
		groups = append(groups, g)
	}

	var other []ui.AiaOtherPurpose
	for _, r := range form.Rows {
		switch agents.Purpose(r.Purpose) {
		case agents.PurposeSearch, agents.PurposeSocial, agents.PurposeSEO, agents.PurposeArchive:
			other = append(other, ui.AiaOtherPurpose{Purpose: r.Purpose, Name: r.Label, Description: purposeDescriptions[r.Purpose], Policy: policies[r.Purpose]})
		}
	}

	return &crawlersPage{
		Verify: a.verifyBanner(ctx, q, rg),
		Lede:   strconv.Itoa(fetched) + " of " + strconv.Itoa(known) + " known AI agents fetched your site " + rangeWords(rg) + ". Allow or block a whole purpose at the top of each group.",
		Hero: ui.AiaChart{ID: "w-cr-hero", Title: "AI fetches by purpose", Description: "Stacked by what the agent reads pages for.",
			Src: a.dataURL("overview.json", rg, cmp) + "&group=purpose", Chart: "fetches", Hero: true, Compare: cmp,
			Table: ov.fetchM.table("AI fetches per day by purpose", "Day")},
		Agents:    ui.AiaAgents{Caption: "AI agents by purpose", Groups: groups, Other: other},
		Owner:     owner,
		Return:    sectionHref(a.adminPath+"/analytics/crawlers", q),
		NoFetches: total == 0,
	}, nil
}
