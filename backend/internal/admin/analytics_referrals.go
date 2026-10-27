package admin

import (
	"context"
	"net/url"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// referralsPage is the body of the Referrals section.
type referralsPage struct {
	Hero        ui.AiaChart
	ByAssistant ui.BarList
	Share       ui.AiaChart
}

func (a *Admin) buildReferrals(ctx context.Context, _ url.Values, rg agents.Range, cmp bool) (any, error) {
	refs, err := a.referralsData(ctx, rg)
	if err != nil {
		return nil, err
	}
	unit := per(rg)
	daily := "Daily"
	if unit == "week" {
		daily = "Weekly"
	}
	src := a.dataURL("referrals.json", rg, cmp)
	// The share chart's table shows both sides of the ratio; they are not added up.
	tbl := matrix{Days: refs.refM.Days, Cols: []string{"AI-referred visits", "Pageviews"},
		Vals: map[string][]int64{"AI-referred visits": dailySums(refs.refM), "Pageviews": refs.pvM.Vals["Pageviews"]}}

	p := &referralsPage{
		Hero: ui.AiaChart{ID: "w-rf-hero", Title: "AI-referred visits by assistant",
			Description: daily + " visits, stacked by assistant. The five largest are named, the rest are grouped as Other.",
			Src:         src, Chart: "referrals", Hero: true, Compare: cmp,
			Table: refs.refs},
		ByAssistant: ui.BarList{Label: "AI-referred visits by assistant", Quiet: true, Empty: "No data for this period."},
		Share: ui.AiaChart{ID: "w-rf-share", Title: "AI share of traffic", Description: "AI-referred visits as a share of all pageviews.",
			Src: src, Chart: "referrals", Ratio: "referrals,pageviews", RatioLabel: "AI share of traffic", Override: `{"AI share of traffic":{"n":3}}`, Compare: cmp,
			Table: withoutTotal(tbl.table("AI-referred visits and pageviews per day", "Day"))},
	}
	for _, as := range refs.Assistants {
		p.ByAssistant.Items = append(p.ByAssistant.Items, ui.BarItem{Label: assistantName(as.Assistant), Value: fmtNum(as.Visits),
			Share: shareText(ratio(as.Visits, refs.Visits)), N: float64(as.Visits)})
	}
	return p, nil
}
