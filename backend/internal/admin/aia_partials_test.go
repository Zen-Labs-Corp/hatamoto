package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestAiaPartials(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/aia-attention", "base", []ui.AiaAttention{
		{Sev: 2, Count: "78", Text: "spoofed fetches claiming to be an AI agent", Rate: "0.1% of fetches", Href: "/admin/analytics/health#w-h-ver", Action: "Review spoofed"},
		{Sev: 0, Count: "0", Text: "agents ignoring robots.txt fetched pages while blocked", Note: "Counts only agents on a blocked purpose.", Href: "/admin/analytics/crawlers#w-cr-list", Action: "See these agents"},
	})
	if !strings.Contains(s, `class="hm-statcard hm-statcard--attention"`) || !strings.Contains(s, `<p class="hm-statcard__value">78</p>`) {
		t.Errorf("hm/aia-attention is a stat card with the count as its value:\n%s", s)
	}
	if strings.Count(s, `href=`) != 1 || !strings.Contains(s, "Act now") || !strings.Contains(s, "Nothing to do") {
		t.Errorf("hm/aia-attention: a link only for sev>0, a badge word for each:\n%s", s)
	}

	s = checkPartial(t, a, "hm/aia-chart", "base", ui.AiaChart{
		ID: "w-ct-first", Title: "First crawls per day", Description: "Pages fetched by an AI agent for the first time.",
		Src: "/admin/analytics/data/first-crawls.json?from=2026-01-01&to=2026-01-03", Chart: "per_day", Cols: true, Compare: true,
		Table: ui.DataTable{Caption: "First crawls per day", Columns: []string{"Date", "First crawls"}, Flat: true,
			Rows: []ui.DataRow{{Label: "1 Jan", Cells: []string{"2"}}, {Label: "2 Jan", Cells: []string{"0"}}}},
	})
	for _, want := range []string{`id="w-ct-first"`, `data-hm-aia-chart="time"`, `data-chart="per_day"`, ` data-cols`, `role="group" aria-label="Show or hide series" data-aia-legend-list`, `<template data-aia-legend>`, `<template data-aia-empty>`, `<template data-aia-error>`, `data-aia-retry`, `data-aia-sd`, `data-aia-data`, `1 Jan`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/aia-chart missing %q", want)
		}
	}
	if strings.Contains(s, "style=") {
		t.Error("hm/aia-chart: no style attributes (CSP)")
	}

	s = checkPartial(t, a, "hm/aia-agents", "base", ui.AiaAgents{Caption: "AI agents by purpose", Groups: []ui.AiaAgentGroup{
		{Purpose: "Training", Description: "Read pages to train AI models.", Summary: "27,997 fetches, 50% of all AI fetches.",
			Policy: &ui.AiaPolicy{Purpose: "training", Legend: "Robots policy for all training agents", Deny: true, Owner: true},
			Rows: []ui.AiaAgentRow{
				{Name: "GPTBot", Href: "/admin/analytics/agents/gptbot", Operator: "OpenAI", Fetches: "6,921", Share: 100, Verification: ui.Badge{Label: "13 spoofed", Kind: "danger"}, Blocked: true, Purpose: "training", LastSeen: "23 Oct"},
				{Name: "ChatGPT-User", Href: "/admin/analytics/agents/chatgpt-user", Operator: "OpenAI", Fetches: "6,148", Share: 89, Verification: ui.Badge{Label: "All verified", Kind: "success"}, Ignores: true, Purpose: "user_fetch", Fetched: true, LastSeen: "23 Oct"},
			},
			PolicyOnly: []ui.AiaPolicyOnly{{Name: "Google-Extended", Href: "/admin/analytics/agents/google-extended", Operator: "Google"}}},
		{Purpose: "Other AI", Description: "Other AI crawling that fits no group above."},
	}, Other: []ui.AiaOtherPurpose{{Purpose: "search", Name: "Search", Description: "Search engines such as Google and Bing.", Policy: &ui.AiaPolicy{Purpose: "search", Legend: "Robots policy for search crawlers", Owner: true}}}})
	for _, want := range []string{`scope="rowgroup"`, `<h4 class="hm-table__group-title">Training</h4>`, `data-label="Fetches"`, `width="89"`, `Blocked by policy`, `data-aia-blocked="training"`, `data-aia-blocked="user_fetch" hidden`, `data-aia-ignorer="user_fetch"`, `Ignores robots.txt`, `Policy only.`, `name="purpose.training" value="deny" checked`, `name="purpose.search" value="allow" checked`, `Sets the policy for the whole purpose.`, `No agents fetched under this purpose`, `Other crawlers`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/aia-agents missing %q", want)
		}
	}
	if strings.Contains(s, "style=") {
		t.Error("hm/aia-agents: no style attributes (CSP)")
	}

	s = checkPartial(t, a, "hm/aia-policy", "owner", ui.AiaPolicy{Purpose: "seo", Legend: "Robots policy for seo crawlers", Deny: true, Owner: true})
	for _, want := range []string{`<legend class="hm-visually-hidden">Robots policy for seo crawlers</legend>`, `data-aia-saved="deny"`, `name="purpose.seo" value="allow"><`, `name="purpose.seo" value="deny" checked`, `data-aia-unsaved hidden`, `Unsaved change`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/aia-policy owner missing %q:\n%s", want, s)
		}
	}
	s = checkPartial(t, a, "hm/aia-policy", "readonly", ui.AiaPolicy{Purpose: "seo", Legend: "Robots policy for seo crawlers", Deny: true})
	if strings.Contains(s, "<input") || !strings.Contains(s, "Blocked") {
		t.Errorf("hm/aia-policy readonly: a badge, no controls:\n%s", s)
	}
}

func TestAiaPolicyForm(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	start := func(f ui.AiaPolicyForm) string {
		out, err := a.RenderPartial("hm/aia-policy-start", f)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	end := func(f ui.AiaPolicyForm) string {
		out, err := a.RenderPartial("hm/aia-policy-end", f)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	owner := ui.AiaPolicyForm{CSRF: "tok", Return: "/admin/analytics/health?from=2026-01-01", Owner: true}
	s, e := start(owner), end(owner)
	for _, want := range []string{`<form class="hm-aia-policyform" method="post" action="/admin/analytics/crawlers/robots"`, `name="_csrf" value="tok"`, `name="return" value="/admin/analytics/health?from=2026-01-01"`} {
		if !strings.Contains(s, want) {
			t.Errorf("hm/aia-policy-start missing %q:\n%s", want, s)
		}
	}
	for _, want := range []string{`class="hm-savebar" data-aia-pending`, `role="status" data-aia-live`, `type="submit"`, `data-aia-discard`, "</form>"} {
		if !strings.Contains(e, want) {
			t.Errorf("hm/aia-policy-end missing %q:\n%s", want, e)
		}
	}
	if strings.Index(e, "data-aia-live") > strings.Index(e, "data-aia-pending") || strings.Contains(e, `data-aia-count aria-live`) {
		t.Error("the live region must come before, not inside, the bar that starts hidden")
	}
	if strings.Contains(e, `data-aia-pending hidden`) {
		t.Error("the bar must be visible without JavaScript so Save works")
	}
	ro := ui.AiaPolicyForm{}
	if s, e := start(ro), end(ro); strings.Contains(s+e, "<form") || strings.Contains(e, "Save") || strings.Contains(e, "savebar") {
		t.Errorf("a non-owner gets no form and no save bar:\n%s\n%s", s, e)
	}
}
