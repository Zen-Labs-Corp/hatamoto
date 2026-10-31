package admin

import (
	"context"
	"net/url"
	"slices"
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

// aiPurposeIDs are aiPurposes as ids.
var aiPurposeIDs = func() []string {
	ids := make([]string, len(aiPurposes))
	for i, p := range aiPurposes {
		ids[i] = string(p)
	}
	return ids
}()

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
	KPIs   []ui.Stat
	Hero   ui.AiaChart
	Agents ui.AiaAgents
	// Owner and Return feed the one policy form around the agents table.
	Owner  bool
	Return string
	// NoFetches is true when nothing fetched in the range, AI agents or search engines.
	NoFetches bool
}

// searchPurpose is the purpose of the search engines, which get a group of their own.
const searchPurpose = "search"

// searchNote says what the search engines group is, and what else the Googlebot crawl feeds.
const searchNote = "The index behind Google, Bing and Apple search. Google AI Overviews and Gemini also use what Googlebot fetches."

// summaryPurposes are the non-AI purposes that get one line instead of a group.
var summaryPurposes = []string{"social", "seo", "archive"}

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

	// Search engines: every one seen in the range, with the group's own Allow / Block.
	var searchRows []agentJSON
	var searchHits int64
	for _, ag := range byPurpose[searchPurpose] {
		if ag.Hits > 0 {
			searchRows = append(searchRows, ag)
			searchHits += ag.Hits
		}
	}
	sg := ui.AiaAgentGroup{Purpose: "Search engines", Description: searchNote, Policy: policies[searchPurpose], Summary: fmtNum(searchHits) + " fetches."}
	for _, ag := range searchRows {
		href := sectionHref(a.adminPath+"/analytics/agents/"+url.PathEscape(ag.ID), q)
		sg.Rows = append(sg.Rows, ui.AiaAgentRow{Name: ag.Name, Href: href, Operator: ag.Operator, Fetches: fmtNum(ag.Hits), Share: int(ratio(ag.Hits, searchRows[0].Hits) + 0.5),
			Verification: verificationBadge(ag), Blocked: denied[searchPurpose], Ignores: ag.IgnoresRobots, LastSeen: lastSeen(ag.LastSeen), Purpose: searchPurpose, Fetched: true})
	}
	groups = append(groups, sg)

	// Verified share over everything the KPI row counts: the AI agents and the search engines.
	var ver, spo, unv int64
	for _, ag := range list.Agents {
		if id := purposeID(ag.Purpose); id == searchPurpose || slices.Contains(aiPurposeIDs, id) {
			ver, spo, unv = ver+ag.Verified, spo+ag.Spoofed, unv+ag.Unverifiable
		}
	}

	var summary []ui.AiaSummaryItem
	for _, id := range summaryPurposes {
		for _, ag := range byPurpose[id] {
			if ag.Hits > 0 {
				summary = append(summary, ui.AiaSummaryItem{Name: ag.Name, Count: fmtNum(ag.Hits)})
			}
		}
	}
	var sum *ui.AiaSummary
	if len(summary) > 0 {
		sum = &ui.AiaSummary{Label: "Social, SEO tools, archives", Items: summary}
	}

	var other []ui.AiaOtherPurpose
	for _, r := range form.Rows {
		switch agents.Purpose(r.Purpose) {
		case agents.PurposeSocial, agents.PurposeSEO, agents.PurposeArchive:
			other = append(other, ui.AiaOtherPurpose{Purpose: r.Purpose, Name: r.Label, Description: purposeDescriptions[r.Purpose], Policy: policies[r.Purpose]})
		}
	}

	return &crawlersPage{
		Verify: a.verifyBanner(ctx, q, rg),
		Lede: strconv.Itoa(fetched) + " of " + strconv.Itoa(known) + " known AI agents and " + strconv.Itoa(len(searchRows)) + " " + plural(len(searchRows), "search engine", "search engines") + " fetched your site " + rangeWords(rg) +
			". Allow or block a whole purpose at the top of each group.",
		KPIs: []ui.Stat{
			{Label: "AI fetches", Value: fmtNum(total)},
			{Label: "Search engine crawls", Value: fmtNum(searchHits)},
			{Label: "Verified", Value: pct(ver, spo, unv), Note: "of AI and search engine fetches"},
		},
		Hero: ui.AiaChart{ID: "w-cr-hero", Title: "All crawling by purpose", Description: "Stacked by what the crawler reads pages for.",
			Src: a.dataURL("overview.json", rg, cmp) + "&group=purpose", Chart: "fetches_all", Hero: true, Compare: cmp,
			Override: `{"` + searchSeriesName + `":{"n":6}}`,
			Table:    ov.fetchAllM.table("Crawler fetches per day by purpose", "Day"),
			Toggle: &ui.Segmented{Mode: "buttons", Label: "Crawlers shown", Items: []ui.SegmentedItem{
				{Label: "AI only", Value: "fetches", Attrs: map[string]string{"data-chart-toggle": "fetches"}},
				{Label: "AI + search", Value: "fetches_all", Pressed: true, Attrs: map[string]string{"data-chart-toggle": "fetches_all"}},
			}},
			AltChart: "fetches", AltTable: ov.fetchM.table("AI fetches per day by purpose", "Day")},
		Agents:    ui.AiaAgents{Caption: "Crawlers by purpose", Groups: groups, Other: other, Summary: sum},
		Owner:     owner,
		Return:    sectionHref(a.adminPath+"/analytics/crawlers", q),
		NoFetches: total == 0 && searchHits == 0,
	}, nil
}
