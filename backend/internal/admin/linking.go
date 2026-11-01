package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	linkingv2 "hatamoto.dev/core/internal/linking/v2"
)

const (
	rulesPerPage     = 25
	maxBulkImport    = 500
	maxPhraseLen     = 200
	defaultPriority  = 50
	bulkImportSource = "bulk_import"
)

var sourceLabels = map[string]string{"manual": "Manual", "bulk_import": "Bulk import", "auto_city": "Auto (city)", "auto_partial": "Auto (partial)", "auto_sibling": "Auto (sibling)", "auto_tag": "Auto (tag)"}

func sourceLabel(s string) string {
	if l, ok := sourceLabels[s]; ok {
		return l
	}
	return s
}

func (a *Admin) mountLinking(authed *httpx.Group) {
	authed.HandleFunc("GET /settings/linking", a.handleRules)
	authed.HandleFunc("GET /settings/linking/rules/new", a.handleRuleNew)
	authed.HandleFunc("POST /settings/linking/rules", a.handleRuleSave)
	authed.HandleFunc("POST /settings/linking/rules/bulk", a.handleRulesBulk)
	authed.HandleFunc("GET /settings/linking/rules/delete-all", a.handleRulesDeleteAllForm)
	authed.HandleFunc("POST /settings/linking/rules/delete-all", a.handleRulesDeleteAll)
	authed.HandleFunc("POST /settings/linking/rules/delete-source", a.handleRulesDeleteSource)
	authed.HandleFunc("GET /settings/linking/rules/{id}", a.handleRuleEdit)
	authed.HandleFunc("POST /settings/linking/rules/{id}", a.handleRuleSave)
	authed.HandleFunc("POST /settings/linking/rules/{id}/toggle", a.handleRuleToggle)
	authed.HandleFunc("GET /settings/linking/rules/{id}/delete", a.handleRuleDeleteForm)
	authed.HandleFunc("POST /settings/linking/rules/{id}/delete", a.handleRuleDelete)
	authed.HandleFunc("GET /settings/linking/import", a.handleImportForm)
	authed.HandleFunc("POST /settings/linking/import", a.handleImport)
	authed.HandleFunc("GET /settings/linking/suggestions", a.handleSuggestions)
	authed.HandleFunc("POST /settings/linking/suggestions/insert", a.handleSuggestionInsert)
	authed.HandleFunc("GET /settings/linking/equity", a.handleEquity)
	authed.HandleFunc("GET /settings/linking/equity/{id}", a.handleInbound)
}

func (a *Admin) linkStore(w http.ResponseWriter, r *http.Request) (LinkingStore, bool) {
	if a.deps.Growth.Linking == nil {
		a.unavailable(w, r, "Internal linking")
		return nil, false
	}
	return a.deps.Growth.Linking, true
}

func (a *Admin) linkTabs(active string) []ui.Tab {
	tabs := []ui.Tab{
		{Label: "Rules", Href: a.adminPath + "/settings/linking"},
		{Label: "Suggestions", Href: a.adminPath + "/settings/linking/suggestions"},
		{Label: "Link equity", Href: a.adminPath + "/settings/linking/equity"},
		{Label: "Bulk import", Href: a.adminPath + "/settings/linking/import"},
	}
	for i := range tabs {
		tabs[i].Active = tabs[i].Href == active
	}
	return tabs
}

// ---- rules list ----

type rulesPage struct {
	Tabs     []ui.Tab
	Table    ui.Table
	Actions  []ui.Button
	Menu     []ui.Action
	Sources  []ui.Option
	Total    int
	CSRFHint string
}

var ruleSortCols = map[string]bool{"priority": true, "created_at": true, "match_value": true, "link_count": true}

func (a *Admin) handleRules(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	qv := r.URL.Query()
	q, source, active := strings.TrimSpace(qv.Get("q")), qv.Get("source"), qv.Get("active")
	sortKey, dir := qv.Get("sort"), qv.Get("dir")
	if !ruleSortCols[sortKey] {
		sortKey, dir = "created_at", "desc"
	}
	if dir != "asc" {
		dir = "desc"
	}
	page := pageParam(r)
	f := &linkingv2.RuleFilter{Query: q, Source: source, Page: page, PerPage: rulesPerPage, Sort: sortKey, Order: dir}
	switch active {
	case "on":
		t := true
		f.IsActive = &t
	case "off":
		t := false
		f.IsActive = &t
	}
	res, err := ls.ListRules(r.Context(), f)
	if err != nil {
		a.logger().Error("list link rules failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The rules could not be loaded.")
		return
	}
	query := url.Values{}
	for k, v := range map[string]string{"q": q, "source": source, "active": active} {
		if v != "" {
			query.Set(k, v)
		}
	}
	if sortKey != "created_at" || dir != "desc" {
		query.Set("sort", sortKey)
		query.Set("dir", dir)
	}
	srcOpts := []ui.Option{{Value: "", Label: "All sources", Selected: source == ""}}
	srcNames := make([]string, 0, len(sourceLabels))
	for k := range sourceLabels {
		srcNames = append(srcNames, k)
	}
	sort.Strings(srcNames)
	for _, k := range srcNames {
		srcOpts = append(srcOpts, ui.Option{Value: k, Label: sourceLabels[k], Selected: k == source})
	}
	t := ui.Table{
		ID: "rules", Caption: "Linking rules", BaseURL: a.adminPath + "/settings/linking", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "match_value", Label: "Phrase", Sortable: true}, {Key: "target", Label: "Links to"}, {Key: "priority", Label: "Priority", Sortable: true, Align: "right"}, {Key: "link_count", Label: "Links to target", Sortable: true, Align: "right"}, {Key: "source", Label: "Source"}, {Key: "state", Label: "State"}},
		SortKey: sortKey, SortDir: dir, Query: q, SearchLabel: "Search phrases and targets",
		Filters: []ui.Filter{
			{Name: "source", Label: "Source", Options: srcOpts},
			{Name: "active", Label: "State", Options: []ui.Option{{Value: "", Label: "On and off", Selected: active == ""}, {Value: "on", Label: "On", Selected: active == "on"}, {Value: "off", Label: "Off", Selected: active == "off"}}},
		},
		Bulk:    []ui.BulkAction{{Label: "Delete", Action: a.adminPath + "/settings/linking/rules/bulk", Value: "delete", Danger: true}},
		BulkURL: a.adminPath + "/settings/linking/rules/bulk",
		Page:    ui.Pagination{Page: page, PerPage: rulesPerPage, Total: res.Total, BaseURL: a.adminPath + "/settings/linking", Query: query},
		Empty:   ui.Empty{Icon: "link", Title: "No linking rules", Body: "A rule names a phrase and the page it should link to. Suggestions then offer the link wherever the phrase appears.", Actions: []ui.Button{{Label: "New rule", Href: a.adminPath + "/settings/linking/rules/new", Variant: "primary"}}},
	}
	for _, rl := range res.Rules {
		id := rl.ID.String()
		state, toggle := &ui.Badge{Label: "On", Kind: "success"}, "Turn off"
		if !rl.IsActive {
			state, toggle = &ui.Badge{Label: "Off", Kind: "muted"}, "Turn on"
		}
		phrase := rl.MatchValue
		if rl.AnchorText != nil && *rl.AnchorText != "" && *rl.AnchorText != rl.MatchValue {
			phrase += " (anchor: " + *rl.AnchorText + ")"
		}
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: "rule " + rl.MatchValue, Cells: []ui.Cell{
			{Text: phrase, Link: a.adminPath + "/settings/linking/rules/" + id}, {Text: rl.TargetEntityName}, {Text: strconv.Itoa(rl.Priority), Bar: &ui.Bar{Label: "Priority " + strconv.Itoa(rl.Priority) + " of 100", Pct: float64(rl.Priority), Kind: "ink"}}, {Text: strconv.Itoa(rl.LinkCount)}, {Text: sourceLabel(rl.Source)}, {Badge: state},
		}, Actions: []ui.Action{
			{Label: "Edit", Href: a.adminPath + "/settings/linking/rules/" + id},
			{Label: toggle, Post: a.adminPath + "/settings/linking/rules/" + id + "/toggle"},
			{Label: "Delete", Href: a.adminPath + "/settings/linking/rules/" + id + "/delete", Post: a.adminPath + "/settings/linking/rules/" + id + "/delete", Danger: true, ConfirmLabel: "Delete rule",
				Confirm: "The rule for " + rl.MatchValue + " is deleted. Links already in your content stay."},
		}})
	}
	v := a.NewView(w, r, "Internal linking")
	v.Data = rulesPage{Tabs: a.linkTabs(a.adminPath + "/settings/linking"), Table: t, Total: res.Total, Actions: []ui.Button{{Label: "New rule", Href: a.adminPath + "/settings/linking/rules/new", Variant: "primary", Icon: "plus"}}, Menu: []ui.Action{{Label: "Delete all rules", Href: a.adminPath + "/settings/linking/rules/delete-all", Danger: true}}, Sources: srcOpts[1:]}
	a.Render(w, r, http.StatusOK, "linking/rules", v)
}

// ---- rule form ----

type ruleForm struct {
	ID, Phrase, Anchor, Priority, MaxLinks string
	Target, TargetLabel, TargetQ           string
	Active, New                            bool
	Errors                                 map[string]string
	Summary                                []ui.FieldError
	Preview                                *linkingv2.RulePreviewResult
	PreviewRows                            []previewRow
	PreviewStats                           []ui.Stat
}

// previewRow is one page of a rule preview.
type previewRow struct {
	Name, Slug, Schema, Reason string
	Badge                      *ui.Badge
}

const alreadyLinkedLabel = "Already links to the target"

// linkStatusBadge is the one mapping from a suggestion or preview status to its
// badge, shared by both pages. A page that already links to the target counts
// as already linked whether the status or the skip reason says so.
func linkStatusBadge(status, reason string) *ui.Badge {
	switch {
	case status == "insertable":
		return &ui.Badge{Label: "Can be linked", Kind: "success"}
	case status == "already_linked" || reason == "already_linked":
		return &ui.Badge{Label: alreadyLinkedLabel, Kind: "info"}
	}
	return &ui.Badge{Label: "Skipped", Kind: "neutral"}
}

func (a *Admin) renderRuleForm(w http.ResponseWriter, r *http.Request, status int, f ruleForm) {
	f.Summary = nil
	for _, k := range []struct{ id, label string }{{"phrase", "Phrase"}, {"anchor", "Anchor text"}, {"target", "Links to"}, {"priority", "Priority"}, {"maxlinks", "Links per page"}} {
		if m := f.Errors[k.id]; m != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	title := "Edit linking rule"
	if f.New {
		title = "New linking rule"
	}
	f.PreviewRows, f.PreviewStats = nil, nil
	if f.Preview != nil {
		f.PreviewStats = []ui.Stat{
			{Label: plural(f.Preview.MatchCount, "Page that mentions it", "Pages that mention it"), Value: fmtNum(int64(f.Preview.MatchCount))},
			{Label: "Can be linked", Value: fmtNum(int64(f.Preview.InsertableCount))},
			{Label: "Already link to the target", Value: fmtNum(int64(f.Preview.LinkedCount))},
		}
		for _, m := range f.Preview.Matches {
			row := previewRow{Name: m.EntityName, Slug: m.EntitySlug, Schema: m.SchemaSlug, Badge: linkStatusBadge(m.Status, m.SkipReason)}
			if m.Status != "insertable" && row.Badge.Label != alreadyLinkedLabel && m.SkipReason != "" {
				row.Reason = skipReason(m.SkipReason)
			}
			f.PreviewRows = append(f.PreviewRows, row)
		}
	}
	v := a.NewView(w, r, title)
	v.Crumbs = a.areaCrumbs("linking", Crumb{Label: title})
	v.Data = f
	v.Width = "narrow"
	a.Render(w, r, status, "linking/rule", v)
}

func (a *Admin) handleRuleNew(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.linkStore(w, r); !ok {
		return
	}
	a.renderRuleForm(w, r, http.StatusOK, ruleForm{New: true, Priority: "50", MaxLinks: "1", Active: true})
}

func (a *Admin) loadRule(w http.ResponseWriter, r *http.Request) (*linkingv2.LinkRule, bool) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil {
		var rl *linkingv2.LinkRule
		if rl, err = ls.GetRule(r.Context(), id); err == nil {
			return rl, true
		}
		if !errors.Is(err, linkingv2.ErrRuleNotFound) {
			a.logger().Error("load link rule failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The rule could not be loaded.")
			return nil, false
		}
	}
	a.ErrorBack(w, r, http.StatusNotFound, "Rule not found", "That linking rule does not exist.", a.adminPath+"/settings/linking", "Back to the rules")
	return nil, false
}

func (a *Admin) handleRuleEdit(w http.ResponseWriter, r *http.Request) {
	rl, ok := a.loadRule(w, r)
	if !ok {
		return
	}
	f := ruleForm{ID: rl.ID.String(), Phrase: rl.MatchValue, Priority: strconv.Itoa(rl.Priority), MaxLinks: strconv.Itoa(rl.MaxLinksPerSource), Active: rl.IsActive,
		Target: rl.TargetEntityID.String(), TargetLabel: rl.TargetEntityName}
	if rl.AnchorText != nil {
		f.Anchor = *rl.AnchorText
	}
	a.renderRuleForm(w, r, http.StatusOK, f)
}

// handleRuleSave creates (no {id}) or updates a rule. The "Preview" button
// posts the same form with _op=preview and shows which pages the phrase would
// link in, without saving.
func (a *Admin) handleRuleSave(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	var cur *linkingv2.LinkRule
	f := ruleForm{New: true}
	if r.PathValue("id") != "" {
		if cur, ok = a.loadRule(w, r); !ok {
			return
		}
		f.New, f.ID = false, cur.ID.String()
	}
	t := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	f.Phrase, f.Anchor, f.Priority, f.MaxLinks, f.Active = t("phrase"), t("anchor"), t("priority"), t("maxlinks"), r.PostFormValue("active") != ""
	f.Target, f.TargetLabel, f.TargetQ = t("target"), t("target_prev"), t("target_q")
	f.Errors = map[string]string{}
	id, label := a.resolvePick(r.Context(), "entity", f.Target, f.TargetLabel, f.TargetQ)
	f.Target, f.TargetLabel = id, label
	tid, terr := uuid.Parse(id)
	switch {
	case f.Phrase == "":
		f.Errors["phrase"] = "Enter the phrase to link"
	case utf8.RuneCountInString(f.Phrase) > maxPhraseLen:
		f.Errors["phrase"] = "Use at most 200 characters"
	}
	if utf8.RuneCountInString(f.Anchor) > maxPhraseLen {
		f.Errors["anchor"] = "Use at most 200 characters"
	}
	if terr != nil {
		f.Errors["target"] = "Choose the page the phrase links to"
	}
	prio, perr := strconv.Atoi(f.Priority)
	if perr != nil || prio < 1 || prio > 100 {
		f.Errors["priority"] = "Enter a number from 1 to 100"
	}
	maxl, merr := strconv.Atoi(f.MaxLinks)
	if merr != nil || maxl < 1 || maxl > 20 {
		f.Errors["maxlinks"] = "Enter a number from 1 to 20"
	}
	if r.PostFormValue("_op") == "preview" {
		if f.Phrase == "" || terr != nil {
			f.Errors["phrase"] = orDefault(f.Errors["phrase"], "Enter a phrase and choose a page to preview")
			a.renderRuleForm(w, r, http.StatusUnprocessableEntity, f)
			return
		}
		phrase := f.Phrase
		if f.Anchor != "" {
			phrase = f.Anchor
		}
		res, err := ls.PreviewRule(r.Context(), phrase, tid)
		if err != nil {
			a.logger().Error("preview link rule failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The preview could not be computed.")
			return
		}
		f.Errors = nil
		f.Preview = &res
		a.renderRuleForm(w, r, http.StatusOK, f)
		return
	}
	if len(f.Errors) > 0 {
		a.renderRuleForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	anchor := f.Anchor
	var err error
	var created *linkingv2.LinkRule
	if f.New {
		in := &linkingv2.CreateRuleInput{TargetEntityID: tid, RuleType: linkingv2.RuleTypeKeyword, MatchValue: f.Phrase, Priority: prio, MaxLinksPerSource: maxl, IsActive: f.Active}
		if anchor != "" {
			in.AnchorText = &anchor
		}
		created, err = ls.CreateRule(r.Context(), in)
	} else {
		_, err = ls.UpdateRule(r.Context(), cur.ID, &linkingv2.UpdateRuleInput{TargetEntityID: &tid, MatchValue: &f.Phrase, Priority: &prio, MaxLinksPerSource: &maxl, IsActive: &f.Active, AnchorText: &anchor})
	}
	switch {
	case errors.Is(err, linkingv2.ErrDuplicateRule):
		f.Errors["phrase"] = "A rule with this phrase already exists for this page"
		a.renderRuleForm(w, r, http.StatusUnprocessableEntity, f)
		return
	case errors.Is(err, linkingv2.ErrMatchValueRequired), errors.Is(err, linkingv2.ErrTargetEntityRequired):
		f.Errors["phrase"] = "Enter a phrase and choose a page"
		a.renderRuleForm(w, r, http.StatusUnprocessableEntity, f)
		return
	case err != nil:
		a.logger().Error("save link rule failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The rule could not be saved.")
		return
	}
	if created != nil {
		a.SetFlash(w, r, "success", "Linking rule created.")
		http.Redirect(w, r, a.adminPath+"/settings/linking/rules/"+created.ID.String(), http.StatusSeeOther)
		return
	}
	a.SetFlash(w, r, "success", "Linking rule saved.")
	http.Redirect(w, r, a.adminPath+"/settings/linking", http.StatusSeeOther)
}

func (a *Admin) handleRuleToggle(w http.ResponseWriter, r *http.Request) {
	rl, ok := a.loadRule(w, r)
	if !ok {
		return
	}
	on := !rl.IsActive
	if _, err := a.deps.Growth.Linking.UpdateRule(r.Context(), rl.ID, &linkingv2.UpdateRuleInput{IsActive: &on}); err != nil {
		a.logger().Error("toggle link rule failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The rule could not be changed.")
		return
	}
	msg := "Rule turned off."
	if on {
		msg = "Rule turned on."
	}
	a.SetFlash(w, r, "success", msg)
	Redirect(w, r, a.adminPath+"/settings/linking")
}

func (a *Admin) handleRuleDeleteForm(w http.ResponseWriter, r *http.Request) {
	rl, ok := a.loadRule(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{Title: "Delete the rule for " + rl.MatchValue + "?", Message: "Links already in your content stay.",
		Post: a.adminPath + "/settings/linking/rules/" + rl.ID.String() + "/delete", Label: "Delete rule", Back: a.adminPath + "/settings/linking", Danger: true})
}

func (a *Admin) handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	rl, ok := a.loadRule(w, r)
	if !ok {
		return
	}
	if _, err := a.deps.Growth.Linking.DeleteRules(r.Context(), []uuid.UUID{rl.ID}); err != nil {
		a.logger().Error("delete link rule failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The rule could not be deleted.")
		return
	}
	a.SetFlash(w, r, "success", "Rule deleted.")
	Redirect(w, r, a.adminPath+"/settings/linking")
}

func (a *Admin) handleRulesBulk(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	var ids []uuid.UUID
	for _, s := range r.PostForm["id"] {
		if id, err := uuid.Parse(s); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		a.SetFlash(w, r, "error", "Tick at least one rule first.")
		http.Redirect(w, r, a.adminPath+"/settings/linking", http.StatusSeeOther)
		return
	}
	if r.PostFormValue("action") != "delete" {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "That is not a bulk action.")
		return
	}
	if len(ids) > 200 {
		ids = ids[:200]
	}
	if r.PostFormValue("confirm") == "" {
		hidden := make([]string, len(ids))
		for i, id := range ids {
			hidden[i] = id.String()
		}
		v := a.NewView(w, r, "Delete rules")
		v.Data = bulkDelete{len(ids), a.adminPath + "/settings/linking/rules/bulk", a.adminPath + "/settings/linking", hidden, "rule", "delete"}
		v.Width = "narrow"
		a.Render(w, r, http.StatusOK, "bulkdelete", v)
		return
	}
	n, err := ls.DeleteRules(r.Context(), ids)
	if err != nil {
		a.logger().Error("bulk delete link rules failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The rules could not be deleted.")
		return
	}
	a.SetFlash(w, r, "success", strconv.Itoa(n)+" "+plural(n, "rule", "rules")+" deleted.")
	http.Redirect(w, r, a.adminPath+"/settings/linking", http.StatusSeeOther)
}

func (a *Admin) handleRulesDeleteAllForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.linkStore(w, r); !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{Title: "Delete every linking rule?", Message: "All rules are deleted, whatever their source. Links already in your content stay. This cannot be undone.",
		Post: a.adminPath + "/settings/linking/rules/delete-all", Label: "Delete all rules", Back: a.adminPath + "/settings/linking", Danger: true})
}

func (a *Admin) handleRulesDeleteAll(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	n, err := ls.DeleteAllRules(r.Context())
	if err != nil {
		a.logger().Error("delete all link rules failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The rules could not be deleted.")
		return
	}
	a.SetFlash(w, r, "success", strconv.Itoa(n)+" "+plural(n, "rule", "rules")+" deleted.")
	http.Redirect(w, r, a.adminPath+"/settings/linking", http.StatusSeeOther)
}

// handleRulesDeleteSource clears one cohort (for example the rules the old
// generators made). The first post shows the confirmation.
func (a *Admin) handleRulesDeleteSource(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	src := r.PostFormValue("source")
	if _, known := sourceLabels[src]; !known {
		a.SetFlash(w, r, "error", "Choose which source to delete.")
		http.Redirect(w, r, a.adminPath+"/settings/linking", http.StatusSeeOther)
		return
	}
	if r.PostFormValue("confirm") == "" {
		a.renderConfirm(w, r, confirmData{Title: "Delete every rule from the source " + sourceLabel(src) + "?", Message: "Links already in your content stay. This cannot be undone.",
			Post: a.adminPath + "/settings/linking/rules/delete-source", Label: "Delete these rules", Back: a.adminPath + "/settings/linking", Danger: true,
			Hidden: []hiddenField{{"source", src}, {"confirm", "1"}}})
		return
	}
	n, err := ls.DeleteRulesBySource(r.Context(), src)
	if err != nil {
		a.logger().Error("delete link rules by source failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The rules could not be deleted.")
		return
	}
	a.SetFlash(w, r, "success", strconv.Itoa(n)+" "+plural(n, "rule", "rules")+" deleted.")
	http.Redirect(w, r, a.adminPath+"/settings/linking", http.StatusSeeOther)
}

// ---- bulk import ----

type importForm struct {
	Tabs    []ui.Tab
	Text    string
	Err     string
	Result  string
	Skipped []string
}

func (a *Admin) renderImport(w http.ResponseWriter, r *http.Request, status int, f importForm) {
	f.Tabs = a.linkTabs(a.adminPath + "/settings/linking/import")
	v := a.NewView(w, r, "Bulk import linking rules")
	v.Data = f
	a.Render(w, r, status, "linking/import", v)
}

func (a *Admin) handleImportForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.linkStore(w, r); !ok {
		return
	}
	a.renderImport(w, r, http.StatusOK, importForm{})
}

// handleImport reads "phrase, entity-slug, priority" lines (blank lines and
// lines starting with # are skipped), resolves each slug to a page and creates
// the rules one by one, so one bad line never blocks the rest.
func (a *Admin) handleImport(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	f := importForm{Text: r.PostFormValue("rules")}
	type row struct {
		phrase, slug string
		prio         int
		line         int
	}
	var rows []row
	for i, line := range strings.Split(strings.ReplaceAll(f.Text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			f.Skipped = append(f.Skipped, "Line "+strconv.Itoa(i+1)+": expected phrase, page slug[, priority]")
			continue
		}
		rw := row{phrase: strings.TrimSpace(parts[0]), slug: strings.TrimSpace(parts[1]), prio: defaultPriority, line: i + 1}
		if len(parts) > 2 {
			if p, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil && p >= 1 && p <= 100 {
				rw.prio = p
			}
		}
		if rw.phrase == "" || rw.slug == "" || utf8.RuneCountInString(rw.phrase) > maxPhraseLen {
			f.Skipped = append(f.Skipped, "Line "+strconv.Itoa(i+1)+": the phrase or page is empty or too long")
			continue
		}
		rows = append(rows, rw)
	}
	switch {
	case len(rows) == 0 && len(f.Skipped) == 0:
		f.Err = "Enter at least one line."
	case len(rows) > maxBulkImport:
		f.Err = "Import at most 500 rules at a time."
	}
	if f.Err != "" {
		a.renderImport(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	created, dup := 0, 0
	for _, rw := range rows {
		id := a.entityBySlug(r.Context(), rw.slug)
		if id == uuid.Nil {
			f.Skipped = append(f.Skipped, "Line "+strconv.Itoa(rw.line)+": no page with the slug "+rw.slug)
			continue
		}
		_, err := ls.CreateRule(r.Context(), &linkingv2.CreateRuleInput{TargetEntityID: id, RuleType: linkingv2.RuleTypeKeyword, MatchValue: rw.phrase, Priority: rw.prio, MaxLinksPerSource: 1, IsActive: true, Source: bulkImportSource})
		switch {
		case err == nil:
			created++
		case errors.Is(err, linkingv2.ErrDuplicateRule):
			dup++
		default:
			a.logger().Error("bulk import rule failed", "err", err)
			f.Skipped = append(f.Skipped, "Line "+strconv.Itoa(rw.line)+": the rule could not be created")
		}
	}
	f.Result = strconv.Itoa(created) + " " + plural(created, "rule", "rules") + " created"
	if dup > 0 {
		f.Result += ", " + strconv.Itoa(dup) + " already existed"
	}
	f.Result += "."
	f.Text = ""
	if len(f.Skipped) > 0 {
		f.Text = r.PostFormValue("rules")
	}
	a.renderImport(w, r, http.StatusOK, f)
}

// entityBySlug finds a page by its exact slug through the entity picker.
func (a *Admin) entityBySlug(ctx context.Context, slug string) uuid.UUID {
	if a.deps.Growth.Pickers == nil {
		return uuid.Nil
	}
	opts, err := a.deps.Growth.Pickers.SearchEntities(ctx, slug, 10)
	if err != nil {
		return uuid.Nil
	}
	for _, o := range opts {
		if o.Slug == slug {
			if id, err := uuid.Parse(o.Value); err == nil {
				return id
			}
		}
	}
	return uuid.Nil
}

// ---- suggestions ----

type suggestionsPage struct {
	Tabs   []ui.Tab
	Entity string
	Label  string
	Query  string
	Asked  bool
	Err    string
	Rows   []suggestionRow
	Counts struct{ Insertable, Linked, Skipped int }
	Mix    ui.CategoryBar
}

type suggestionRow struct {
	Anchor, Target, URL, Reason string
	TargetID                    string
	Insertable                  bool
	Badge                       *ui.Badge
}

var skipReasons = map[string]string{
	"no_match": "The phrase does not appear in this content", "in_heading": "The phrase only appears in headings", "already_linked": "This content already links to the page",
	"in_code": "The phrase only appears in code", "in_link": "The phrase is already inside another link",
}

func skipReason(s string) string {
	if l, ok := skipReasons[s]; ok {
		return l
	}
	return strings.ReplaceAll(s, "_", " ")
}

func (a *Admin) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	d := suggestionsPage{Tabs: a.linkTabs(a.adminPath + "/settings/linking/suggestions")}
	q := r.URL.Query()
	d.Entity, d.Label, d.Query = strings.TrimSpace(q.Get("entity")), strings.TrimSpace(q.Get("entity_prev")), strings.TrimSpace(q.Get("entity_q"))
	status := http.StatusOK
	if d.Entity != "" || d.Query != "" {
		d.Asked = true
		id, label := a.resolvePick(r.Context(), "entity", d.Entity, d.Label, d.Query)
		d.Entity, d.Label = id, label
		eid, err := uuid.Parse(id)
		if err == nil && d.Label == "" {
			// Opened with ?entity=<id> only: show the page's name in the picker and heading.
			d.Label = ls.EntityName(r.Context(), eid)
		}
		if err != nil {
			d.Err = "Choose the page to find link suggestions for."
			status = http.StatusUnprocessableEntity
		} else {
			sugg, err := ls.Suggest(r.Context(), eid)
			switch {
			case errors.Is(err, linkingv2.ErrSourceNotFound):
				d.Err = "That page does not exist."
				status = http.StatusNotFound
			case err != nil:
				a.logger().Error("link suggestions failed", "err", err)
				d.Err = "Suggestions could not be computed. Try again in a moment."
				status = http.StatusInternalServerError
			default:
				for _, s := range sugg {
					row := suggestionRow{Anchor: s.AnchorPhrase, Target: s.TargetName, URL: s.URL, TargetID: s.TargetEntityID.String(), Insertable: s.Status == "insertable"}
					row.Badge = linkStatusBadge(s.Status, s.SkipReason)
					switch {
					case row.Insertable:
						d.Counts.Insertable++
					case row.Badge.Label == alreadyLinkedLabel:
						d.Counts.Linked++
					default:
						row.Reason = skipReason(s.SkipReason)
						d.Counts.Skipped++
					}
					d.Rows = append(d.Rows, row)
				}
			}
		}
	}
	d.Mix = ui.CategoryBar{Label: "Suggestions by result", Segments: []ui.CategorySegment{
		{Label: "Can be linked", Value: strconv.Itoa(d.Counts.Insertable), Kind: "success", N: float64(d.Counts.Insertable)},
		{Label: "Already linked", Value: strconv.Itoa(d.Counts.Linked), Kind: "info", N: float64(d.Counts.Linked)},
		{Label: "Skipped", Value: strconv.Itoa(d.Counts.Skipped), Kind: "neutral", N: float64(d.Counts.Skipped)},
	}}
	v := a.NewView(w, r, "Link suggestions")
	v.Data = d
	a.Render(w, r, status, "linking/suggestions", v)
}

// handleSuggestionInsert links one suggested phrase in the page's saved content.
func (a *Admin) handleSuggestionInsert(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	src, e1 := uuid.Parse(r.PostFormValue("source"))
	tgt, e2 := uuid.Parse(r.PostFormValue("target"))
	anchor := strings.TrimSpace(r.PostFormValue("anchor"))
	back := a.adminPath + "/settings/linking/suggestions?" + url.Values{"entity": {src.String()}, "entity_prev": {r.PostFormValue("label")}}.Encode()
	if e1 != nil || e2 != nil || anchor == "" {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "That suggestion is incomplete.")
		return
	}
	out, err := ls.Insert(r.Context(), src, tgt, anchor)
	switch {
	case errors.Is(err, linkingv2.ErrSourceNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Page not found", "The page you were linking from no longer exists.", a.adminPath+"/settings/linking/suggestions", "Back to suggestions")
		return
	case err != nil:
		a.logger().Error("insert link failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The link could not be inserted.")
		return
	}
	if out.Inserted {
		// Insert rewrote the page's saved content: rendered pages are stale.
		a.invalidate(r.Context())
		a.SetFlash(w, r, "success", "Link inserted. It is part of the page's saved content now.")
	} else {
		a.SetFlash(w, r, "error", "No link was inserted: "+strings.ToLower(skipReason(out.SkipReason))+".")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ---- equity and inbound links ----

type equityPage struct {
	Tabs  []ui.Tab
	Stats []ui.Stat
	Top   ui.BarList
	Tiers ui.CategoryBar
	Table ui.Table
}

// equityTop is how many pages the most-linked list shows.
const equityTop = 8

// equityTierKinds colours the tier mix like tierBadge colours the tier.
var equityTierKinds = []struct{ key, label, kind string }{
	{"primary", "Primary", "info"}, {"secondary", "Secondary", "success"}, {"cluster", "Cluster", "warning"}, {"none", "No tier", "neutral"},
}

func tierBadge(t string) *ui.Badge {
	switch t {
	case "primary":
		return &ui.Badge{Label: "Primary", Kind: "info"}
	case "secondary":
		return &ui.Badge{Label: "Secondary", Kind: "success"}
	case "cluster":
		return &ui.Badge{Label: "Cluster", Kind: "warning"}
	}
	return &ui.Badge{Label: "None", Kind: "neutral"}
}

// inboundBar sizes a "Links in" cell against the busiest page; none at zero.
func inboundBar(n, maxN int) *ui.Bar {
	if n <= 0 || maxN <= 0 {
		return nil
	}
	return &ui.Bar{Label: strconv.Itoa(n) + " " + plural(n, "link", "links") + " in", Pct: float64(n) / float64(maxN) * 100}
}

func (a *Admin) handleEquity(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	rep, err := ls.Equity(r.Context())
	if err != nil {
		a.logger().Error("link equity failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The link equity report could not be computed.")
		return
	}
	tier, schema, q := r.URL.Query().Get("tier"), r.URL.Query().Get("schema"), strings.TrimSpace(r.URL.Query().Get("q"))
	page := pageParam(r)
	// Tier counts and schema choices come from the unfiltered report, so they
	// stay put while a filter is active.
	tierCount := map[string]int{}
	schemas := map[string]bool{}
	var rows []linkingv2.EquityTargetRow
	for _, t := range rep.Targets {
		key := t.Tier
		if key == "" {
			key = "none"
		}
		tierCount[key]++
		schemas[t.TargetSchema] = true
		if (tier == "" || key == tier) && (schema == "" || t.TargetSchema == schema) &&
			(q == "" || strings.Contains(strings.ToLower(t.TargetName+" "+t.TargetSlug), strings.ToLower(q))) {
			rows = append(rows, t)
		}
	}
	schemaOpts := []ui.Option{{Value: "", Label: "All types", Selected: schema == ""}}
	names := make([]string, 0, len(schemas))
	for s := range schemas {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		schemaOpts = append(schemaOpts, ui.Option{Value: s, Label: s, Selected: s == schema})
	}
	top := ui.BarList{Label: "Most linked pages", NameHeading: "Page", ValueHeading: "Links in", Empty: "No page has links pointing at it yet."}
	ranked := append([]linkingv2.EquityTargetRow(nil), rep.Targets...)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].InboundTotal > ranked[j].InboundTotal })
	for _, t := range ranked[:min(equityTop, len(ranked))] {
		if t.InboundTotal == 0 {
			break
		}
		top.Items = append(top.Items, ui.BarItem{Label: t.TargetName, Href: a.adminPath + "/settings/linking/equity/" + t.TargetEntityID.String(), Value: fmtNum(int64(t.InboundTotal)), N: float64(t.InboundTotal)})
	}
	tiers := ui.CategoryBar{Label: "Pages by link equity tier"}
	for _, k := range equityTierKinds {
		if len(rep.Targets) == 0 {
			break
		}
		tiers.Segments = append(tiers.Segments, ui.CategorySegment{Label: k.label, Value: fmtNum(int64(tierCount[k.key])), Kind: k.kind, N: float64(tierCount[k.key])})
	}
	maxIn := 0
	for _, t := range rep.Targets {
		maxIn = max(maxIn, t.InboundTotal)
	}
	total := len(rows)
	lo, hi := min((page-1)*rulesPerPage, total), min(page*rulesPerPage, total)
	query := url.Values{}
	for k, v := range map[string]string{"tier": tier, "schema": schema, "q": q} {
		if v != "" {
			query.Set(k, v)
		}
	}
	tierOpt := func(value, label string) ui.Option {
		if value != "" {
			label += " (" + strconv.Itoa(tierCount[value]) + ")"
		}
		return ui.Option{Value: value, Label: label, Selected: tier == value}
	}
	t := ui.Table{
		ID: "equity", Caption: "Pages and the links pointing at them", BaseURL: a.adminPath + "/settings/linking/equity", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "target", Label: "Page"}, {Key: "type", Label: "Type"}, {Key: "tier", Label: "Tier"}, {Key: "inbound", Label: "Links in", Align: "right"}, {Key: "from", Label: "Coming from"}},
		Query:   q, SearchLabel: "Search pages",
		Filters: []ui.Filter{
			{Name: "tier", Label: "Tier", Options: []ui.Option{tierOpt("", "All tiers"), tierOpt("primary", "Primary"), tierOpt("secondary", "Secondary"), tierOpt("cluster", "Cluster"), tierOpt("none", "No tier")}},
			{Name: "schema", Label: "Page type", Options: schemaOpts},
		},
		Page:  ui.Pagination{Page: page, PerPage: rulesPerPage, Total: total, BaseURL: a.adminPath + "/settings/linking/equity", Query: query},
		Empty: ui.Empty{Icon: "link", Title: "No internal links yet", Body: "Links between your pages appear here after pages are saved."},
	}
	for _, row := range rows[lo:hi] {
		var parts []string
		keys := make([]string, 0, len(row.InboundBySourceSchema))
		for k := range row.InboundBySourceSchema {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, k+" "+strconv.Itoa(row.InboundBySourceSchema[k]))
		}
		id := row.TargetEntityID.String()
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: row.TargetName, Cells: []ui.Cell{
			{Text: row.TargetName + " (/" + row.TargetSlug + ")", Link: a.adminPath + "/settings/linking/equity/" + id}, {Text: row.TargetSchema}, {Badge: tierBadge(row.Tier)}, {Text: strconv.Itoa(row.InboundTotal), Bar: inboundBar(row.InboundTotal, maxIn)}, {Text: strings.Join(parts, ", ")},
		}, Actions: []ui.Action{{Label: "Inbound links", Href: a.adminPath + "/settings/linking/equity/" + id}}})
	}
	v := a.NewView(w, r, "Link equity")
	v.Data = equityPage{
		Tabs: a.linkTabs(a.adminPath + "/settings/linking/equity"), Table: t, Top: top, Tiers: tiers,
		Stats: []ui.Stat{{Label: "Internal links", Value: fmtNum(int64(rep.LinksTotal))}, {Label: "Pages with links", Value: fmtNum(int64(rep.UniqueSources))}},
	}
	a.Render(w, r, http.StatusOK, "linking/equity", v)
}

type inboundPage struct {
	Target  string
	Table   ui.DataTable
	Total   int
	Actions []ui.Button
}

func (a *Admin) handleInbound(w http.ResponseWriter, r *http.Request) {
	ls, ok := a.linkStore(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Not found", "That page does not exist.", a.adminPath+"/settings/linking/equity", "Back to link equity")
		return
	}
	rows, err := ls.Inbound(r.Context(), id)
	if err != nil {
		a.logger().Error("inbound links failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The inbound links could not be loaded.")
		return
	}
	name := ls.EntityName(r.Context(), id)
	if name == "" && len(rows) == 0 {
		a.ErrorBack(w, r, http.StatusNotFound, "Not found", "That page does not exist.", a.adminPath+"/settings/linking/equity", "Back to link equity")
		return
	}
	d := inboundPage{Target: name, Total: len(rows), Actions: []ui.Button{{Label: "Back to link equity", Href: a.adminPath + "/settings/linking/equity"}}, Table: ui.DataTable{Caption: "Links pointing at " + name, Columns: []string{"From page", "Type", "Link text", "Link address"}, Empty: "No page links to this one yet."}}
	for _, row := range rows {
		d.Table.Rows = append(d.Table.Rows, ui.DataRow{Label: row.SourceName + " (/" + row.SourceSlug + ")", Cells: []string{row.SourceSchema, row.AnchorText, row.Href}})
	}
	v := a.NewView(w, r, "Links to "+name)
	v.Crumbs = a.areaCrumbs("linking", Crumb{Label: "Link equity", Href: a.adminPath + "/settings/linking/equity"}, Crumb{Label: name})
	v.Data = d
	a.Render(w, r, http.StatusOK, "linking/inbound", v)
}
