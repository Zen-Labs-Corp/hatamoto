package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/permtext"
)

const standingRel = "automatic"

// standingWaitingShown is how many waiting runs the tab lists.
const standingWaitingShown = 5

// mountStandingPages registers the Automatic updates tab of a plugin. Saving
// the rule lets the plugin publish without review, so that route alone asks
// for the password again; a POST is never replayed after that page.
func (a *Admin) mountStandingPages(owner *httpx.Group) {
	owner.HandleFunc("GET /plugins/{name}/"+standingRel, a.handlePluginStanding)
	// Turning off only narrows what the plugin may do: no sign in again.
	owner.HandleFunc("POST /plugins/{name}/"+standingRel+"/off", a.handleStandingOff)
	owner.Group("", a.RequireSudo).HandleFunc("POST /plugins/{name}/"+standingRel, a.handleStandingApprove)
}

// waitingRun is one run held back for review, as the tab words it.
type waitingRun struct {
	Title, Body, Review string
}

type standingData struct {
	pluginHead
	// Sample is the layer 1 template call the first card shows.
	Sample string
	Rule   StandingRule
	Lines  []promptStatement
	Action string // the sign in again and allow POST
	Off    string // the turn off POST
	Cancel string
	// Approval is set while the rule is allowed.
	Approval   *StandingApproval
	ApprovedOn string
	Waiting    []waitingRun
	// WaitingMore counts waiting runs past the ones listed.
	WaitingMore int
	// Paused is set while an ended approval has not been given again.
	Paused *pausedNotice
	// ShowRule is false while the paused notice stands alone: the rule and
	// the waiting runs come back once the owner chose to review it.
	ShowRule bool
	// Summary is the daily summary table; SummaryTitle is "" when no run
	// published itself in the last day.
	Summary      ui.Table
	SummaryTitle string
	// Created lists the entries runs published on their own; nil when none.
	Created *ui.Table
}

// pausedNotice is the callout shown after an approval ended.
type pausedNotice struct {
	Title, Body, Allow, Keep string
	// Waiting says how many runs are held for review, with a link to them.
	Waiting, WaitingHref string
}

// pausedTitle says what ended the approval, in words for the owner.
func pausedTitle(p PluginInfo, reason string) string {
	switch reason {
	case "schema changed":
		return "A content type " + p.Name + " updates was changed. Its automatic updates are paused."
	case "turned off":
		return "Automatic updates for " + p.Name + " are turned off."
	case "plugin uninstalled":
		return p.Name + " was installed again. Its automatic updates are paused."
	}
	return p.Name + " was updated to " + p.Version + ". Its automatic updates are paused."
}

// standingPlugin loads the plugin and its rule for the tab and its POST; it
// answers 404 for a plugin that declares no standing approval.
func (a *Admin) standingPlugin(w http.ResponseWriter, r *http.Request) (PluginInfo, StandingRule, bool) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return PluginInfo{}, StandingRule{}, false
	}
	if a.deps.Growth.Standing == nil {
		a.unavailable(w, r, "Automatic updates")
		return PluginInfo{}, StandingRule{}, false
	}
	p, found := a.findPlugin(pa, r.PathValue("name"))
	var rule StandingRule
	if found && p.Standing {
		rule, found = a.deps.Growth.Standing.Rule(p.Name)
	} else {
		found = false
	}
	if !found {
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed, or it declares no automatic updates.", a.adminPath+"/plugins", "Back to plugins")
		return PluginInfo{}, StandingRule{}, false
	}
	return p, rule, true
}

// handlePluginStanding is the Automatic updates tab: the no approval way to
// show a value, the narrow rule the owner may allow, and the runs that
// stepped outside it.
func (a *Admin) handlePluginStanding(w http.ResponseWriter, r *http.Request) {
	p, rule, ok := a.standingPlugin(w, r)
	if !ok {
		return
	}
	sa := a.deps.Growth.Standing
	active, err := sa.Active(r.Context(), p.Name, rule.Run)
	if err != nil {
		a.serverError(w, r, "The automatic update rule could not be loaded", err)
		return
	}
	waiting, err := sa.Waiting(r.Context(), p.Name)
	if err != nil {
		a.serverError(w, r, "The waiting runs could not be loaded", err)
		return
	}
	paused, err := sa.Paused(r.Context(), p.Name, rule.Run)
	if err != nil {
		a.serverError(w, r, "The automatic update rule could not be loaded", err)
		return
	}
	digest, err := sa.Digest(r.Context(), pluginsNow().Add(-24*time.Hour))
	if err != nil {
		a.serverError(w, r, "The daily summary could not be loaded", err)
		return
	}
	d := standingData{pluginHead: a.newPluginHead(p, standingRel, true), Rule: rule, Lines: ruleLines(rule),
		Sample: "{{ plugin_" + strings.ReplaceAll(p.Name, "-", "_") + "_<function> \"key\" }}",
		Action: a.pluginURL(p.Name, standingRel), Off: a.pluginURL(p.Name, standingRel+"/off"), Cancel: a.pluginURL(p.Name, "settings"), Approval: active}
	if paused != nil && active == nil {
		d.State.Badge = ui.Badge{Label: "Updated", Kind: "info"}
		body := "Approvals end whenever the plugin or a content type it updates changes, so a new version can\u2019t inherit one. Until you allow it again, its automatic runs do not start."
		if paused.Reason == "turned off" {
			d.State.Badge = ui.Badge{Label: "Off", Kind: "info"}
			body = "You turned them off. Until you allow them again, its automatic runs do not start."
		}
		d.Paused = &pausedNotice{Title: pausedTitle(p, paused.Reason), Body: body, Allow: d.Action + "?review=1#opt2", Keep: d.Cancel}
		if len(waiting) > 0 {
			d.Paused.Waiting = countNoun(len(waiting), "run") + " waiting for your review."
			d.Paused.WaitingHref = a.pluginURL(p.Name, "runs")
			if len(waiting) == 1 {
				d.Paused.WaitingHref = a.changesetURL(p.Name, waiting[0].ChangesetID, "")
			}
		}
	}
	d.ShowRule = d.Paused == nil || r.URL.Query().Get("review") != ""
	if d.ShowRule {
		d.Paused = nil
	}
	d.SummaryTitle, d.Summary, d.Created = a.standingSummary(r.Context(), digest)
	if active != nil {
		d.ApprovedOn = fmtDate(active.At.In(a.siteLocation(r.Context())))
	}
	for i, wr := range waiting {
		if i == standingWaitingShown {
			d.WaitingMore = len(waiting) - i
			break
		}
		d.Waiting = append(d.Waiting, waitingRun{
			Title:  fmt.Sprintf("%s run #%d is waiting for you.", runTitle(p, wr.Run), wr.ChangesetID),
			Body:   countNoun(wr.Items, "change") + " staged. Some may be outside what you allowed, so nothing was published.",
			Review: a.changesetURL(p.Name, wr.ChangesetID, ""),
		})
	}
	v := a.NewView(w, r, p.Name+" automatic updates")
	v.Width = "narrow"
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: p.Name, Href: a.pluginURL(p.Name, "settings")}, {Label: "Automatic updates"}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/automatic", v)
}

// standingSummary groups the last day's automatic runs by plugin.
func (a *Admin) standingSummary(ctx context.Context, rows []StandingDigestRow) (string, ui.Table, *ui.Table) {
	if len(rows) == 0 {
		return "", ui.Table{}, nil
	}
	type sum struct{ runs, changes, created int }
	var order []string
	by := map[string]*sum{}
	for _, d := range rows {
		if by[d.Plugin] == nil {
			by[d.Plugin] = &sum{}
			order = append(order, d.Plugin)
		}
		by[d.Plugin].runs++
		by[d.Plugin].changes += d.Changes
		by[d.Plugin].created += d.Created
	}
	title := "Daily summary, " + fmtDate(pluginsNow().In(a.siteLocation(ctx)))
	t := ui.Table{
		ID: "summary", Caption: title,
		Columns: []ui.Col{{Key: "plugin", Label: "Plugin", Role: "title"}, {Key: "runs", Label: "Automatic runs", Role: "num"}, {Key: "changes", Label: "Changes", Role: "num"}},
	}
	for _, name := range order {
		s := by[name]
		t.Rows = append(t.Rows, ui.Row{ID: name, Label: name, Cells: []ui.Cell{{Text: name}, {Text: thousands(s.runs)}, {Text: thousands(s.changes)}},
			Actions: []ui.Action{{Label: "See runs", Href: a.pluginURL(name, "runs"), Button: true, AriaLabel: "See runs of " + name}}})
	}
	var created *ui.Table
	for _, name := range order {
		n := by[name].created
		if n == 0 {
			continue
		}
		if created == nil {
			created = &ui.Table{ID: "summary-created", Caption: "New entries published automatically",
				Columns: []ui.Col{{Key: "plugin", Label: "Plugin", Role: "title"}, {Key: "created", Label: "New entries", Role: "num"}}}
		}
		created.Rows = append(created.Rows, ui.Row{ID: "created-" + name, Label: name, Cells: []ui.Cell{{Text: name}, {Text: thousands(n)}},
			Actions: []ui.Action{{Label: "See runs", Href: a.pluginURL(name, "runs"), Button: true, AriaLabel: "See runs of " + name}}})
	}
	return title, t, created
}

// handleStandingApprove records the owner's approval. The form carries only
// the run's name: the rule is read from the installed plugin, never from the
// request, so a form cannot widen it.
func (a *Admin) handleStandingApprove(w http.ResponseWriter, r *http.Request) {
	p, rule, ok := a.standingPlugin(w, r)
	if !ok {
		return
	}
	back := a.pluginURL(p.Name, standingRel)
	if r.PostFormValue("run") != rule.Run {
		a.ErrorBack(w, r, http.StatusBadRequest, "Nothing to allow", "That run does not declare automatic updates.", back, "Back to automatic updates")
		return
	}
	by := ""
	if u := userFrom(r.Context()); u != nil {
		by = u.Email
	}
	err := a.deps.Growth.Standing.Approve(r.Context(), p.Name, rule.Run, by, r.PostFormValue("version"), r.PostFormValue("rule"))
	if errors.Is(err, ErrStandingChanged) {
		a.SetFlash(w, r, "warning", "The plugin changed; review the rule again.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if err != nil {
		a.serverError(w, r, "The automatic updates could not be allowed", err)
		return
	}
	a.SetFlash(w, r, "success", "Automatic updates are allowed for "+p.Name+".")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handleStandingOff ends the plugin's approval. It narrows access, so it asks
// for no password.
func (a *Admin) handleStandingOff(w http.ResponseWriter, r *http.Request) {
	p, _, ok := a.standingPlugin(w, r)
	if !ok {
		return
	}
	if err := a.deps.Growth.Standing.TurnOff(r.Context(), p.Name); err != nil {
		a.serverError(w, r, "The automatic updates could not be turned off", err)
		return
	}
	a.SetFlash(w, r, "success", "Automatic updates are off for "+p.Name+".")
	http.Redirect(w, r, a.pluginURL(p.Name, standingRel), http.StatusSeeOther)
}

// ruleLines words the rule as the permission list: what it allows, then what
// it never does.
func ruleLines(rule StandingRule) []promptStatement {
	line := func(k permtext.Kind, title, sub string) promptStatement {
		return promptStatement{Statement: permtext.Statement{Kind: k, Title: title, Sub: sub}, Icon: statementIcon(k)}
	}
	never := "Never text, links, page addresses or pages you wrote"
	lines := []promptStatement{
		line(permtext.KindOK, "May change "+joinWords(rule.Fields), "Only numbers, yes or no values, dates and fixed choices in those fields."),
		line(permtext.KindOK, "Only on "+joinWords(rule.Schemas)+" it created", "Content you wrote, or that another plugin made, is never touched."),
		line(permtext.KindOK, "Up to "+countNoun(rule.MaxPerHour, "change")+" an hour", "A run that would go past this waits for your review."),
	}
	if len(rule.CreateSchemas) > 0 {
		lines = append(lines, line(permtext.KindReview, "May publish new "+joinWords(rule.CreateSchemas)+" entries on its own, up to "+thousands(rule.CreateMaxPerHour)+" an hour", "A run that would go past this waits for your review."))
		never = "Never deletes, redirects, menus, media, other sources\u2019 content or other content types"
	}
	return append(lines, line(permtext.KindNo, never, ""))
}

// joinWords joins names for a sentence: "a", "a and b", "a, b and c".
func joinWords(in []string) string {
	switch len(in) {
	case 0:
		return ""
	case 1:
		return in[0]
	}
	return strings.Join(in[:len(in)-1], ", ") + " and " + in[len(in)-1]
}
