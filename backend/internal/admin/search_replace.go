package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/searchreplace"
)

const (
	searchReplaceRel   = "/settings/search-replace"
	auditSearchReplace = "search_replace.apply"
)

// srQuery is what the form asks, read the same way from the preview query and
// from the apply post.
type srQuery struct {
	Find, Replace              string
	WholeWord, MatchCase       bool
	Content, SEO, Other, Menus bool
}

func (q srQuery) options() searchreplace.Options {
	return searchreplace.Options{Find: q.Find, Replace: q.Replace, WholeWord: q.WholeWord, MatchCase: q.MatchCase}
}

func (q srQuery) scopes() searchreplace.Scope {
	var s searchreplace.Scope
	for scope, on := range map[searchreplace.Scope]bool{
		searchreplace.ScopeContent: q.Content, searchreplace.ScopeSEO: q.SEO,
		searchreplace.ScopeOther: q.Other, searchreplace.ScopeMenus: q.Menus,
	} {
		if on {
			s |= scope
		}
	}
	return s
}

// srQueryFrom reads the form values. The "in" values name the places to look.
func srQueryFrom(v url.Values) srQuery {
	in := map[string]bool{}
	for _, x := range v["in"] {
		in[x] = true
	}
	return srQuery{
		Find: v.Get("find"), Replace: v.Get("replace"),
		WholeWord: v.Get("mode") == "word", MatchCase: v.Get("match_case") != "",
		Content: in["content"], SEO: in["seo"], Other: in["other"], Menus: in["menus"],
	}
}

// values is the query as a form: the hidden fields of the apply form and the
// address of the Back link.
func (q srQuery) values() url.Values {
	v := url.Values{"find": {q.Find}, "replace": {q.Replace}, "mode": {"plain"}}
	if q.WholeWord {
		v.Set("mode", "word")
	}
	if q.MatchCase {
		v.Set("match_case", "1")
	}
	for _, place := range []struct {
		name string
		on   bool
	}{{"content", q.Content}, {"seo", q.SEO}, {"other", q.Other}, {"menus", q.Menus}} {
		if place.on {
			v.Add("in", place.name)
		}
	}
	return v
}

type srHidden struct{ Name, Value string }

type srMatch struct {
	ID, Token, Label, Context string
	Checked                   bool
}

type srGroup struct {
	Title   string
	Count   int // all matches in the item
	Note    string
	Matches []srMatch
}

type srPreview struct {
	Headline string
	CapNote  string
	Groups   []srGroup
	Hidden   []srHidden
	Button   string
	Back     string
	Menus    bool // a menu match is listed
	Empty    bool
}

// srPlace is one "Look in" checkbox.
type srPlace struct {
	Value, Label string
	Checked      bool
}

type searchReplaceData struct {
	Q        srQuery
	Modes    []ui.Option
	Places   []srPlace
	Summary  []ui.FieldError
	Errs     map[string]string
	Preview  *srPreview
	Action   string
	BackHref string
}

func (a *Admin) mountSearchReplace(owner *httpx.Group) {
	owner.HandleFunc("GET "+searchReplaceRel, a.handleSearchReplace)
	owner.HandleFunc("POST "+searchReplaceRel, a.handleSearchReplaceApply)
}

// searchReplaceService is the tool over the services the admin already holds.
func (a *Admin) searchReplaceService() *searchreplace.Service {
	if a.deps.Content.Entities == nil {
		return nil
	}
	s := &searchreplace.Service{Ents: a.deps.Content.Entities}
	if a.deps.Growth.Menus != nil {
		s.Menus = a.deps.Growth.Menus
	}
	if a.deps.Content.Ownership != nil {
		s.Owners = a.deps.Content.Ownership
	}
	return s
}

// srValidate returns the problems with a query: no search text, or nowhere to
// look.
func srValidate(q srQuery) []ui.FieldError {
	var errs []ui.FieldError
	if strings.TrimSpace(q.Find) == "" {
		errs = append(errs, ui.FieldError{ID: "sr-find", Label: "Find", Message: "Enter the text or address to find."})
	}
	if q.scopes() == 0 {
		errs = append(errs, ui.FieldError{ID: "sr-in-content", Label: "Look in", Message: "Choose at least one place to look."})
	}
	return errs
}

func (a *Admin) renderSearchReplace(w http.ResponseWriter, r *http.Request, status int, q srQuery, errs []ui.FieldError, p *srPreview) {
	d := searchReplaceData{Q: q, Summary: errs, Errs: map[string]string{}, Preview: p, Action: a.adminPath + searchReplaceRel}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	d.Modes = []ui.Option{
		{Value: "plain", Label: "Plain text: anywhere, even inside a longer word", Selected: !q.WholeWord},
		{Value: "word", Label: "Whole word: not part of a longer word", Selected: q.WholeWord},
	}
	d.Places = []srPlace{
		{"content", "Content: text and page builder text", q.Content},
		{"seo", "SEO fields: titles, descriptions and social text", q.SEO},
		{"other", "Other fields: excerpts and custom fields", q.Other},
		{"menus", "Menus: item labels and links", q.Menus},
	}
	v := a.NewView(w, r, "Search and replace")
	v.Section, v.Tab = "tools", "search-replace"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/search-replace", v)
}

func (a *Admin) handleSearchReplace(w http.ResponseWriter, r *http.Request) {
	svc := a.searchReplaceService()
	if svc == nil {
		a.unavailable(w, r, "Search and replace")
		return
	}
	query := r.URL.Query()
	if !query.Has("find") { // the empty form
		a.renderSearchReplace(w, r, http.StatusOK, srQuery{MatchCase: true, Content: true, SEO: true, Other: true, Menus: true}, nil, nil)
		return
	}
	q := srQueryFrom(query)
	if errs := srValidate(q); len(errs) > 0 {
		a.renderSearchReplace(w, r, http.StatusUnprocessableEntity, q, errs, nil)
		return
	}
	pv, err := svc.Preview(r.Context(), q.options(), q.scopes())
	if err != nil {
		a.logger().Error("search replace preview", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The search could not be completed.")
		return
	}
	a.renderSearchReplace(w, r, http.StatusOK, q, nil, a.srPreviewOf(q, pv))
}

// srPreviewOf turns the service's dry run into the page's model. Matches of
// plugin-owned items start unticked.
func (a *Admin) srPreviewOf(q srQuery, pv *searchreplace.Preview) *srPreview {
	p := &srPreview{Empty: pv.Total == 0, Back: a.adminPath + searchReplaceRel + "?" + q.values().Encode()}
	for k, vals := range q.values() {
		for _, v := range vals {
			p.Hidden = append(p.Hidden, srHidden{k, v})
		}
	}
	slices.SortFunc(p.Hidden, func(x, y srHidden) int {
		if c := strings.Compare(x.Name, y.Name); c != 0 {
			return c
		}
		return strings.Compare(x.Value, y.Value)
	})
	items := pv.TotalItems
	switch {
	case pv.Total == 0:
		p.Headline = "No matches found. Nothing was changed."
	case pv.Capped:
		p.Headline = fmt.Sprintf("More than %s matches in at least %s. Nothing has been changed yet.", thousands(searchreplace.CountCap), srCount(items, "item", "items"))
	default:
		p.Headline = fmt.Sprintf("%s in %s. Nothing has been changed yet.", srCount(pv.Total, "match", "matches"), srCount(items, "item", "items"))
	}
	if pv.Listed < pv.Total || pv.Capped {
		p.CapNote = fmt.Sprintf("Showing the first %d matches. Refine your search to see the rest.", pv.Listed)
	}
	ticked, tickedItems, n := 0, 0, 0
	for _, g := range pv.Groups {
		sg := srGroup{Count: g.Count}
		if g.IsMenu {
			sg.Title = "Menu: " + g.Name
			sg.Note = "Menus have no version history and cannot be restored."
			p.Menus = true
		} else {
			sg.Title = g.Name
			if g.Locale != "" {
				sg.Title += " (" + g.Locale + ")"
			}
			if g.PluginOwned {
				sg.Note = "Replacing takes ownership from the plugin " + g.Plugin + "."
			}
		}
		ticks := false
		for _, m := range g.Matches {
			n++
			sm := srMatch{ID: "sr-m-" + strconv.Itoa(n), Token: m.Token, Label: m.Label, Context: m.Context, Checked: !g.PluginOwned}
			if sm.Checked {
				ticked++
				ticks = true
			}
			sg.Matches = append(sg.Matches, sm)
		}
		if ticks {
			tickedItems++
		}
		p.Groups = append(p.Groups, sg)
	}
	p.Button = fmt.Sprintf("Replace %s in %s", srCount(ticked, "selected match", "selected matches"), srCount(tickedItems, "item", "items"))
	return p
}

func (a *Admin) handleSearchReplaceApply(w http.ResponseWriter, r *http.Request) {
	svc := a.searchReplaceService()
	if svc == nil {
		a.unavailable(w, r, "Search and replace")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	q := srQueryFrom(r.PostForm)
	if errs := srValidate(q); len(errs) > 0 {
		a.renderSearchReplace(w, r, http.StatusUnprocessableEntity, q, errs, nil)
		return
	}
	sel := r.PostForm["sel"]
	if len(sel) == 0 {
		a.SetFlash(w, r, "warning", "No matches were selected, so nothing was changed.")
		http.Redirect(w, r, a.adminPath+searchReplaceRel+"?"+q.values().Encode(), http.StatusSeeOther)
		return
	}
	var actor *uuid.UUID
	if u := userFrom(r.Context()); u != nil {
		if id, err := uuid.Parse(u.ID); err == nil {
			actor = &id
		}
	}
	rep, err := svc.Apply(r.Context(), actor, q.options(), q.scopes(), sel)
	if rep == nil {
		a.logger().Warn("search replace apply", "err", err)
		a.SetFlash(w, r, "error", "Nothing was changed: "+fullStop(err.Error()))
		http.Redirect(w, r, a.adminPath+searchReplaceRel, http.StatusSeeOther)
		return
	}
	if err != nil {
		a.logger().Warn("search replace apply stopped", "err", err)
	}
	for _, f := range rep.Failed {
		a.logger().Warn("search replace: item not changed", "item", f.Name, "reason", f.Reason)
	}
	a.audit(r, auditSearchReplace, "search-replace", map[string]any{
		"find": truncateRunes(q.Find, 100), "replace": truncateRunes(q.Replace, 100),
		"matches": rep.Replaced, "items": rep.Items, "menu_items": rep.MenuItems,
		"skipped": rep.Skipped, "failed": len(rep.Failed),
		"whole_word": q.WholeWord, "match_case": q.MatchCase,
	})
	kind, msg := srOutcome(rep)
	a.SetFlash(w, r, kind, msg)
	http.Redirect(w, r, a.adminPath+searchReplaceRel, http.StatusSeeOther)
}

// srOutcome is the flash after an apply.
func srOutcome(rep *searchreplace.Report) (kind, msg string) {
	kind = "success"
	switch {
	case rep.Replaced == 0:
		kind, msg = "warning", "Nothing was replaced."
	case rep.MenuItems > 0 && rep.Items > 0:
		msg = fmt.Sprintf("Replaced %s in %s and %s. Each item has a new version you can restore from its Versions tab; menus cannot be restored.", srCount(rep.Replaced, "match", "matches"), srCount(rep.Items, "item", "items"), srCount(rep.MenuItems, "menu item", "menu items"))
	case rep.MenuItems > 0:
		msg = fmt.Sprintf("Replaced %s in %s. Menus have no version history.", srCount(rep.Replaced, "match", "matches"), srCount(rep.MenuItems, "menu item", "menu items"))
	default:
		msg = fmt.Sprintf("Replaced %s in %s. Each item has a new version you can restore from its Versions tab.", srCount(rep.Replaced, "match", "matches"), srCount(rep.Items, "item", "items"))
	}
	if rep.Skipped > 0 {
		kind = "warning"
		msg += fmt.Sprintf(" %s skipped because %s changed since the preview.", srCount(rep.Skipped, "item", "items"), plural(rep.Skipped, "it has", "they have"))
	}
	if n := len(rep.Failed); n > 0 {
		kind = "warning"
		msg += fmt.Sprintf(" %s could not be changed.", srCount(n, "item", "items"))
	}
	return kind, msg
}

// srCount is "1 item" or "3 items".
func srCount(n int, one, many string) string { return fmt.Sprintf("%d %s", n, plural(n, one, many)) }

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
