package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/redirect"
)

const redirectsPerPage = 25

func (a *Admin) mountRedirects(authed *httpx.Group) {
	authed.HandleFunc("GET /settings/redirects", a.handleRedirects)
	authed.HandleFunc("GET /settings/redirects/404", a.handleNotFoundLog)
	authed.HandleFunc("POST /settings/redirects/404/{id}/ignore", a.handle404Ignore)
	authed.HandleFunc("GET /settings/redirects/404/{id}/delete", a.handle404DeleteForm)
	authed.HandleFunc("POST /settings/redirects/404/{id}/delete", a.handle404Delete)
	authed.HandleFunc("GET /settings/redirects/explain", a.handleRedirectExplain)
	authed.HandleFunc("GET /settings/redirects/new", a.handleRedirectNew)
	authed.HandleFunc("POST /settings/redirects", a.handleRedirectCreate)
	authed.HandleFunc("POST /settings/redirects/bulk", a.handleRedirectsBulk)
	authed.HandleFunc("GET /settings/redirects/{id}/edit", a.handleRedirectEdit)
	authed.HandleFunc("POST /settings/redirects/{id}/edit", a.handleRedirectSave)
	authed.HandleFunc("POST /settings/redirects/{id}/toggle", a.handleRedirectToggle)
	authed.HandleFunc("GET /settings/redirects/{id}/delete", a.handleRedirectDeleteForm)
	authed.HandleFunc("POST /settings/redirects/{id}/delete", a.handleRedirectDelete)
}

func (a *Admin) redirectStore(w http.ResponseWriter, r *http.Request) (RedirectStore, bool) {
	if a.deps.Growth.Redirects == nil {
		a.unavailable(w, r, "Redirects")
		return nil, false
	}
	return a.deps.Growth.Redirects, true
}

func (a *Admin) redirectTabs(active string) []ui.Tab {
	tabs := []ui.Tab{
		{Label: "Redirect rules", Href: a.adminPath + "/settings/redirects"},
		{Label: "404 log", Href: a.adminPath + "/settings/redirects/404"},
		{Label: "Explain a URL", Href: a.adminPath + "/settings/redirects/explain"},
	}
	for i := range tabs {
		tabs[i].Active = tabs[i].Href == active
	}
	return tabs
}

func pageParam(r *http.Request) int {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if p < 1 {
		p = 1
	}
	return p
}

type redirectsPage struct {
	Tabs    []ui.Tab
	Table   ui.Table
	Actions []ui.Button
	Codes   []ui.Option
	Host    string     // site host shown before the "Test a URL" path
	Top     ui.BarList // 404 log only: the busiest open addresses
}

// notFoundTop is how many open addresses the 404 log's top list shows.
const notFoundTop = 8

func (a *Admin) handleRedirects(w http.ResponseWriter, r *http.Request) {
	rs, ok := a.redirectStore(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := pageParam(r)
	list, total, err := rs.ListRedirects(r.Context(), redirect.RedirectListParams{Search: q, Page: page, PerPage: redirectsPerPage})
	if err != nil {
		a.logger().Error("list redirects failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The redirects could not be loaded.")
		return
	}
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	t := ui.Table{
		ID: "redirects", Caption: "Redirect rules", BaseURL: a.adminPath + "/settings/redirects", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "source", Label: "From"}, {Key: "target", Label: "To"}, {Key: "code", Label: "Type"}, {Key: "hits", Label: "Hits", Align: "right"}, {Key: "state", Label: "State"}},
		Query:   q, SearchLabel: "Search redirects",
		Bulk:    []ui.BulkAction{{Label: "Delete", Action: a.adminPath + "/settings/redirects/bulk", Value: "delete", Danger: true}, {Label: "Turn off", Action: a.adminPath + "/settings/redirects/bulk", Value: "off"}, {Label: "Turn on", Action: a.adminPath + "/settings/redirects/bulk", Value: "on"}},
		BulkURL: a.adminPath + "/settings/redirects/bulk",
		Page:    ui.Pagination{Page: page, PerPage: redirectsPerPage, Total: total, BaseURL: a.adminPath + "/settings/redirects", Query: query},
		Empty:   ui.Empty{Icon: "corner-up-right", Title: "No redirects", Body: "A redirect sends visitors from an old address to a new one. Create one, or start from the 404 log.", Actions: []ui.Button{{Label: "New redirect", Href: a.adminPath + "/settings/redirects/new", Variant: "primary"}}},
	}
	t.CreatedID = createdID(r)
	maxHits := 0
	for _, rd := range list {
		maxHits = max(maxHits, rd.HitCount)
	}
	for _, rd := range list {
		id := rd.ID.String()
		state := &ui.Badge{Label: "On", Kind: "success"}
		toggle := "Turn off"
		if !rd.IsActive {
			state, toggle = &ui.Badge{Label: "Off", Kind: "neutral"}, "Turn on"
		}
		source := ui.Cell{Text: rd.SourcePath, Link: a.adminPath + "/settings/redirects/" + id + "/edit"}
		if redirect.IsPatternSource(rd.SourcePath) {
			source.Text += " (wildcard)"
		}
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: "redirect from " + rd.SourcePath, Cells: []ui.Cell{
			source, {Text: rd.TargetURL}, {Badge: codeBadge(rd.StatusCode)}, hitsCell(rd.HitCount, maxHits), {Badge: state},
		}, Actions: []ui.Action{
			{Label: "Edit", Href: a.adminPath + "/settings/redirects/" + id + "/edit"},
			{Label: toggle, Post: a.adminPath + "/settings/redirects/" + id + "/toggle"},
			{Label: "Delete", Href: a.adminPath + "/settings/redirects/" + id + "/delete", Post: a.adminPath + "/settings/redirects/" + id + "/delete", Danger: true, ConfirmLabel: "Delete redirect",
				Confirm: "Visitors to " + rd.SourcePath + " are no longer sent anywhere."},
		}})
	}
	if page == 1 {
		t.Rows = pinFirst(t.Rows, t.CreatedID)
	}
	v := a.NewView(w, r, "Redirects")
	host := ""
	if a.deps.BaseURL != nil {
		if u, err := url.Parse(a.deps.BaseURL(r)); err == nil {
			host = u.Host
		}
	}
	v.Data = redirectsPage{Host: host, Tabs: a.redirectTabs(a.adminPath + "/settings/redirects"), Table: t, Codes: codeOptions(""), Actions: []ui.Button{{Label: "New redirect", Href: a.adminPath + "/settings/redirects/new", Variant: "primary", Icon: "plus", Attrs: map[string]string{"data-hm-open": "new-redirect"}}}}
	a.Render(w, r, http.StatusOK, "redirects/list", v)
}

// codeBadge names a redirect type. Permanent ones are info, temporary ones
// neutral, so colour does not suggest one is better.
func codeBadge(c int) *ui.Badge {
	if c == 301 || c == 308 {
		return &ui.Badge{Label: strconv.Itoa(c) + " Permanent", Kind: "info"}
	}
	return &ui.Badge{Label: strconv.Itoa(c) + " Temporary", Kind: "neutral"}
}

// hitsCell shows the hit count with a bar relative to the busiest redirect on
// the page; a redirect nobody used gets the number only.
func hitsCell(n, maxN int) ui.Cell {
	c := ui.Cell{Text: strconv.Itoa(n)}
	if n > 0 && maxN > 0 {
		c.Bar = &ui.Bar{Label: strconv.Itoa(n) + " " + plural(n, "hit", "hits"), Pct: float64(n) / float64(maxN) * 100}
	}
	return c
}

// ---- 404 log ----

func (a *Admin) handleNotFoundLog(w http.ResponseWriter, r *http.Request) {
	rs, ok := a.redirectStore(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := pageParam(r)
	list, total, err := rs.List404Entries(r.Context(), redirect.NotFoundListParams{Search: q, Page: page, PerPage: redirectsPerPage})
	if err != nil {
		a.logger().Error("list 404 log failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The 404 log could not be loaded.")
		return
	}
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	t := ui.Table{
		ID: "notfound", Caption: "Addresses that returned 404", BaseURL: a.adminPath + "/settings/redirects/404", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "path", Label: "Address"}, {Key: "hits", Label: "Hits", Align: "right"}, {Key: "last", Label: "Last seen"}, {Key: "referrer", Label: "Came from"}, {Key: "state", Label: "State"}},
		Query:   q, SearchLabel: "Search the 404 log",
		Page:  ui.Pagination{Page: page, PerPage: redirectsPerPage, Total: total, BaseURL: a.adminPath + "/settings/redirects/404", Query: query},
		Empty: ui.Empty{Icon: "check", Title: "No missing pages logged", Body: "Addresses visitors asked for that did not exist show up here."},
	}
	for _, e := range list {
		id := e.ID.String()
		state := &ui.Badge{Label: "Open", Kind: "warning"}
		switch {
		case e.RedirectID != nil:
			state = &ui.Badge{Label: "Has a redirect", Kind: "success"}
		case e.Ignored:
			state = &ui.Badge{Label: "Ignored", Kind: "neutral"}
		}
		ref := ""
		if e.LastReferrer != nil {
			ref = truncate(*e.LastReferrer, 60)
		}
		var acts []ui.Action
		if e.RedirectID == nil {
			acts = append(acts, ui.Action{Label: "Create redirect", Href: a.adminPath + "/settings/redirects/new?" + url.Values{"source": {e.Path}, "log": {id}}.Encode()})
		}
		acts = append(acts, ui.Action{Label: "Explain", Href: a.adminPath + "/settings/redirects/explain?" + url.Values{"path": {e.Path}}.Encode()})
		if !e.Ignored && e.RedirectID == nil {
			acts = append(acts, ui.Action{Label: "Ignore", Post: a.adminPath + "/settings/redirects/404/" + id + "/ignore"})
		}
		acts = append(acts, ui.Action{Label: "Delete", Href: a.adminPath + "/settings/redirects/404/" + id + "/delete", Post: a.adminPath + "/settings/redirects/404/" + id + "/delete", Danger: true, ConfirmLabel: "Delete entry",
			Confirm: "The entry for " + e.Path + " is deleted from the log. It returns if the address is requested again."})
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: e.Path, Cells: []ui.Cell{
			{Text: e.Path}, {Text: strconv.Itoa(e.HitCount)}, {Text: fmtDateTime(e.LastSeenAt)}, {Text: ref}, {Badge: state},
		}, Actions: acts})
	}
	d := redirectsPage{Tabs: a.redirectTabs(a.adminPath + "/settings/redirects/404"), Table: t}
	// Page 1 is ordered by hits, so its open entries are the busiest; past it,
	// or while searching, a "top" list would be wrong.
	if page == 1 && q == "" {
		d.Top = ui.BarList{Label: "Most requested missing pages. Select an address to create a redirect for it", NameHeading: "Address (select to create a redirect)", ValueHeading: "Hits"}
		for _, e := range list {
			if e.RedirectID != nil || e.Ignored || len(d.Top.Items) == notFoundTop {
				continue
			}
			d.Top.Items = append(d.Top.Items, ui.BarItem{Label: e.Path, N: float64(e.HitCount), Value: fmt.Sprintf("%d %s", e.HitCount, plural(e.HitCount, "hit", "hits")),
				Href: a.adminPath + "/settings/redirects/new?" + url.Values{"source": {e.Path}, "log": {e.ID.String()}}.Encode()})
		}
	}
	v := a.NewView(w, r, "404 log")
	v.Data = d
	a.Render(w, r, http.StatusOK, "redirects/notfound", v)
}

func (a *Admin) entryID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Not found", "That entry does not exist.", a.adminPath+"/settings/redirects/404", "Back to the 404 log")
		return uuid.Nil, false
	}
	return id, true
}

func (a *Admin) handle404Ignore(w http.ResponseWriter, r *http.Request) {
	rs, ok := a.redirectStore(w, r)
	if !ok {
		return
	}
	id, ok := a.entryID(w, r)
	if !ok {
		return
	}
	if err := rs.Ignore404Entry(r.Context(), id); err != nil && !errors.Is(err, redirect.ErrNotFoundEntryNotFound) {
		a.logger().Error("ignore 404 failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The entry could not be ignored.")
		return
	}
	a.SetFlash(w, r, "success", "Entry ignored.")
	Redirect(w, r, a.adminPath+"/settings/redirects/404")
}

func (a *Admin) handle404DeleteForm(w http.ResponseWriter, r *http.Request) {
	id, ok := a.entryID(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{Title: "Delete this entry from the 404 log?", Message: "It returns if the address is requested again.",
		Post: a.adminPath + "/settings/redirects/404/" + id.String() + "/delete", Label: "Delete entry", Back: a.adminPath + "/settings/redirects/404", Danger: true})
}

func (a *Admin) handle404Delete(w http.ResponseWriter, r *http.Request) {
	rs, ok := a.redirectStore(w, r)
	if !ok {
		return
	}
	id, ok := a.entryID(w, r)
	if !ok {
		return
	}
	if err := rs.Delete404Entry(r.Context(), id); err != nil && !errors.Is(err, redirect.ErrNotFoundEntryNotFound) {
		a.logger().Error("delete 404 failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The entry could not be deleted.")
		return
	}
	a.SetFlash(w, r, "success", "Entry deleted.")
	Redirect(w, r, a.adminPath+"/settings/redirects/404")
}

// ---- explain ----

type explainStage struct {
	Provider, Result, Target, Rule, Variant string
	Matched                                 bool
}

type explainData struct {
	Tabs                      []ui.Tab
	Path                      string
	Asked                     bool
	Err                       string
	Normalised                string
	Verdict                   string // one sentence: what happens to the URL
	Kind                      string // redirect | page | notfound
	VerdictKind, VerdictTitle string // callout kind and title for Kind
	Stages                    []explainStage
	PageName                  string
	PageSchema                string
	PagePath                  string
	PageID                    string
	CreateURL                 string
}

func providerLabel(name string) string {
	switch name {
	case "db_rules", "db", "DBRules":
		return "Your redirect rules"
	case "wp_patterns", "wp", "WPPatterns":
		return "Built-in WordPress addresses"
	case "bare_slug_to_blog", "BareSlugToBlog":
		return "Bare post address to blog"
	}
	return name
}

func (a *Admin) handleRedirectExplain(w http.ResponseWriter, r *http.Request) {
	d := explainData{Tabs: a.redirectTabs(a.adminPath + "/settings/redirects/explain"), Path: strings.TrimSpace(r.URL.Query().Get("path"))}
	status := http.StatusOK
	if d.Path != "" {
		d.Asked = true
		switch {
		case a.deps.Growth.Explain == nil:
			d.Err = "Explaining addresses is not available on this server."
		case !strings.HasPrefix(d.Path, "/") || strings.ContainsAny(d.Path, "?# \t\r\n") || utf8.RuneCountInString(d.Path) > 2000:
			d.Err = "Enter an address path starting with /, without a query string or fragment, for example /old-page."
			status = http.StatusUnprocessableEntity
		default:
			ex, err := a.deps.Growth.Explain.ExplainURL(r.Context(), d.Path)
			if err != nil {
				a.logger().Error("explain url failed", "err", err)
				d.Err = "The address could not be checked."
				status = http.StatusInternalServerError
				break
			}
			a.fillExplanation(&d, ex)
		}
	}
	v := a.NewView(w, r, "Explain a URL")
	v.Data = d
	a.Render(w, r, status, "redirects/explain", v)
}

func (a *Admin) fillExplanation(d *explainData, ex URLExplanation) {
	defer func() {
		switch d.Kind {
		case "redirect":
			d.VerdictKind, d.VerdictTitle = "info", "Redirected"
		case "page":
			d.VerdictKind, d.VerdictTitle = "success", "Page found"
		default:
			d.VerdictKind, d.VerdictTitle = "warning", "Nothing answers this address"
		}
	}()
	if ans := ex.Redirects; ans != nil {
		d.Normalised = ans.Normalised
		for _, st := range ans.Stages {
			res := "No match"
			if st.Matched {
				res = "Matched: redirects to " + st.Target + " (" + strconv.Itoa(st.Status) + ")"
			}
			d.Stages = append(d.Stages, explainStage{Provider: providerLabel(st.Provider), Result: res, Target: st.Target, Rule: st.Rule, Variant: st.Variant, Matched: st.Matched})
		}
		if ans.Found() {
			d.Kind = "redirect"
			d.Verdict = d.Path + " is redirected to " + ans.Redirect.Target + " with status " + strconv.Itoa(ans.Redirect.Status) + ", by " + providerLabel(ans.MatchedBy) + "."
			return
		}
	}
	if p := ex.Page; p.Kind != "" {
		d.Kind, d.PageName, d.PageSchema, d.PagePath, d.PageID = "page", p.Name, p.Schema, p.Path, p.EntityID
		switch p.Kind {
		case "home":
			d.Verdict = d.Path + " shows the home page."
		case "archive":
			d.Verdict = d.Path + " shows the archive " + p.Name + "."
		default:
			d.Verdict = d.Path + " shows the page " + p.Name + "."
		}
		return
	}
	d.Kind = "notfound"
	d.Verdict = "Nothing answers " + d.Path + ": no redirect matches and no page or archive lives there, so visitors get a 404. (Category and tag archives, author pages, search and feeds are not checked here.)"
	d.CreateURL = a.adminPath + "/settings/redirects/new?" + url.Values{"source": {d.Path}}.Encode()
}

// ---- create, edit ----

type redirectForm struct {
	ID, Source, Target, Code, Log string
	Active                        bool
	New                           bool
	Errors                        map[string]string
	Summary                       []ui.FieldError
	Codes                         []ui.Option
}

func codeOptions(selected string) []ui.Option {
	if selected == "" {
		selected = "301"
	}
	return []ui.Option{
		{Value: "301", Label: "301: moved permanently", Selected: selected == "301"},
		{Value: "302", Label: "302: moved temporarily", Selected: selected == "302"},
		{Value: "307", Label: "307: temporary, keeps the request method", Selected: selected == "307"},
		{Value: "308", Label: "308: permanent, keeps the request method", Selected: selected == "308"},
	}
}

func (a *Admin) renderRedirectForm(w http.ResponseWriter, r *http.Request, status int, f redirectForm) {
	f.Codes = codeOptions(f.Code)
	f.Summary = nil
	for _, k := range []struct{ id, label string }{{"source", "From"}, {"target", "To"}, {"code", "Type"}} {
		if m := f.Errors[k.id]; m != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	title := "Edit redirect"
	if f.New {
		title = "New redirect"
	}
	v := a.NewView(w, r, title)
	v.Crumbs = a.areaCrumbs("redirects", Crumb{Label: title})
	v.Data = f
	v.Width = "narrow"
	a.Render(w, r, status, "redirects/edit", v)
}

func (a *Admin) handleRedirectNew(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.redirectStore(w, r); !ok {
		return
	}
	q := r.URL.Query()
	log := ""
	if id, err := uuid.Parse(q.Get("log")); err == nil {
		log = id.String()
	}
	a.renderRedirectForm(w, r, http.StatusOK, redirectForm{New: true, Source: strings.TrimSpace(q.Get("source")), Code: "301", Active: true, Log: log})
}

// validateRedirect checks what the form can check; the service adds the
// wildcard rules.
func validateRedirect(f *redirectForm, host string) {
	f.Errors = map[string]string{}
	switch {
	case f.Source == "":
		f.Errors["source"] = "Enter the address to redirect from"
	case !strings.HasPrefix(f.Source, "/") || strings.ContainsAny(f.Source, "?# \t\r\n\\") || utf8.RuneCountInString(f.Source) > 2000:
		f.Errors["source"] = "Start with / and leave out spaces, ? and #. Example: /old-page or /blog/**"
	}
	switch {
	case f.Target == "":
		f.Errors["target"] = "Enter where visitors should go"
	case !redirect.SafeLocation(f.Target, true) || utf8.RuneCountInString(f.Target) > 2000:
		f.Errors["target"] = "Enter a path starting with / or a full address starting with https://"
	}
	if f.Errors["target"] == "" && f.Source != "" && redirectsToItself(f.Source, f.Target, host) {
		f.Errors["target"] = "A redirect cannot point to itself. Choose a different address."
	}
	if c, err := strconv.Atoi(f.Code); err != nil || !redirect.ValidStatusCode(c) {
		f.Errors["code"] = "Choose a redirect type"
	}
}

// redirectsToItself reports whether the target lands on the source after the
// resolver's normalising: a trailing slash, doubled slashes, or the site's own
// host in front make no difference.
func redirectsToItself(source, target, host string) bool {
	if u, err := url.Parse(target); err == nil && u.IsAbs() && host != "" && strings.EqualFold(u.Host, host) {
		target = u.Path
	}
	// A query or fragment does not change which source matches.
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		target = target[:i]
	}
	return strings.HasPrefix(target, "/") && redirect.Normalise(source) == redirect.Normalise(target)
}

// siteHost is the host this request is served on, for comparing absolute targets.
func (a *Admin) siteHost(r *http.Request) string {
	if a.deps.BaseURL != nil {
		if u, err := url.Parse(a.deps.BaseURL(r)); err == nil {
			return u.Host
		}
	}
	return ""
}

// redirectErr maps a service error to a field message; ok is false for an
// error that is not the editor's to fix.
func redirectErr(err error) (field, msg string, ok bool) {
	switch {
	case errors.Is(err, redirect.ErrInvalidPattern):
		return "source", "Wildcards must be whole segments: * for one segment, ** as the last segment to match the rest. Example: /blog/*/old", true
	case errors.Is(err, redirect.ErrUnsafePattern):
		return "source", "This wildcard rule is unsafe: start with a fixed segment (not * or **) and do not send visitors to an address the rule matches itself", true
	case errors.Is(err, redirect.ErrExternalTarget):
		return "target", "The public demo only redirects to a path on this site, such as /new-page.", true
	case errors.Is(err, redirect.ErrDuplicateSourcePath):
		return "source", "A redirect from this address already exists", true
	case errors.Is(err, redirect.ErrInvalidStatusCode):
		return "code", "Choose a redirect type", true
	}
	return "", "", false
}

func (a *Admin) handleRedirectCreate(w http.ResponseWriter, r *http.Request) {
	rs, ok := a.redirectStore(w, r)
	if !ok {
		return
	}
	f := redirectForm{New: true, Source: strings.TrimSpace(r.PostFormValue("source")), Target: strings.TrimSpace(r.PostFormValue("target")), Code: r.PostFormValue("code"), Active: true}
	if id, err := uuid.Parse(r.PostFormValue("log")); err == nil {
		f.Log = id.String()
	}
	validateRedirect(&f, a.siteHost(r))
	if len(f.Errors) > 0 {
		a.renderRedirectForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	code, _ := strconv.Atoi(f.Code)
	in := &redirect.CreateRedirectInput{SourcePath: f.Source, TargetURL: f.Target, StatusCode: code}
	if id, err := uuid.Parse(f.Log); err == nil {
		in.NotFoundLogID = &id
	}
	created, err := rs.CreateRedirect(r.Context(), in)
	if err != nil {
		if field, msg, ok := redirectErr(err); ok {
			f.Errors[field] = msg
			a.renderRedirectForm(w, r, http.StatusUnprocessableEntity, f)
			return
		}
		a.logger().Error("create redirect failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The redirect could not be created.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Redirect created.")
	Redirect(w, r, createdRedirect(a.adminPath+"/settings/redirects", created.ID.String()))
}

func (a *Admin) loadRedirect(w http.ResponseWriter, r *http.Request) (*redirect.Redirect, bool) {
	rs, ok := a.redirectStore(w, r)
	if !ok {
		return nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil {
		var rd *redirect.Redirect
		if rd, err = rs.GetRedirect(r.Context(), id); err == nil {
			return rd, true
		}
		if !errors.Is(err, redirect.ErrRedirectNotFound) {
			a.logger().Error("load redirect failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The redirect could not be loaded.")
			return nil, false
		}
	}
	a.ErrorBack(w, r, http.StatusNotFound, "Redirect not found", "That redirect does not exist.", a.adminPath+"/settings/redirects", "Back to redirects")
	return nil, false
}

func (a *Admin) handleRedirectEdit(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.loadRedirect(w, r)
	if !ok {
		return
	}
	a.renderRedirectForm(w, r, http.StatusOK, redirectForm{ID: rd.ID.String(), Source: rd.SourcePath, Target: rd.TargetURL, Code: strconv.Itoa(rd.StatusCode), Active: rd.IsActive})
}

func (a *Admin) handleRedirectSave(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.loadRedirect(w, r)
	if !ok {
		return
	}
	f := redirectForm{ID: rd.ID.String(), Source: strings.TrimSpace(r.PostFormValue("source")), Target: strings.TrimSpace(r.PostFormValue("target")), Code: r.PostFormValue("code"), Active: r.PostFormValue("active") != ""}
	validateRedirect(&f, a.siteHost(r))
	if len(f.Errors) > 0 {
		a.renderRedirectForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	code, _ := strconv.Atoi(f.Code)
	if _, err := a.deps.Growth.Redirects.UpdateRedirect(r.Context(), rd.ID, &redirect.UpdateRedirectInput{SourcePath: &f.Source, TargetURL: &f.Target, StatusCode: &code, IsActive: &f.Active}); err != nil {
		if field, msg, ok := redirectErr(err); ok {
			f.Errors[field] = msg
			a.renderRedirectForm(w, r, http.StatusUnprocessableEntity, f)
			return
		}
		a.logger().Error("save redirect failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The redirect could not be saved.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Redirect saved.")
	http.Redirect(w, r, a.adminPath+"/settings/redirects", http.StatusSeeOther)
}

func (a *Admin) handleRedirectToggle(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.loadRedirect(w, r)
	if !ok {
		return
	}
	on := !rd.IsActive
	if _, err := a.deps.Growth.Redirects.UpdateRedirect(r.Context(), rd.ID, &redirect.UpdateRedirectInput{IsActive: &on}); err != nil {
		a.logger().Error("toggle redirect failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The redirect could not be changed.")
		return
	}
	a.invalidate(r.Context())
	msg := "Redirect turned off."
	if on {
		msg = "Redirect turned on."
	}
	a.SetFlash(w, r, "success", msg)
	Redirect(w, r, a.adminPath+"/settings/redirects")
}

func (a *Admin) handleRedirectDeleteForm(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.loadRedirect(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{Title: "Delete the redirect from " + rd.SourcePath + "?", Message: "Visitors to " + rd.SourcePath + " are no longer sent anywhere.",
		Post: a.adminPath + "/settings/redirects/" + rd.ID.String() + "/delete", Label: "Delete redirect", Back: a.adminPath + "/settings/redirects", Danger: true})
}

func (a *Admin) handleRedirectDelete(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.loadRedirect(w, r)
	if !ok {
		return
	}
	if err := a.deps.Growth.Redirects.DeleteRedirect(r.Context(), rd.ID); err != nil && !errors.Is(err, redirect.ErrRedirectNotFound) {
		a.logger().Error("delete redirect failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The redirect could not be deleted.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Redirect deleted.")
	Redirect(w, r, a.adminPath+"/settings/redirects")
}

// handleRedirectsBulk deletes, turns on or turns off the ticked rules. Delete
// shows a confirmation page first.
func (a *Admin) handleRedirectsBulk(w http.ResponseWriter, r *http.Request) {
	rs, ok := a.redirectStore(w, r)
	if !ok {
		return
	}
	action := r.PostFormValue("action")
	var ids []uuid.UUID
	for _, s := range r.PostForm["id"] {
		if id, err := uuid.Parse(s); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		a.SetFlash(w, r, "error", "Tick at least one redirect first.")
		http.Redirect(w, r, a.adminPath+"/settings/redirects", http.StatusSeeOther)
		return
	}
	if len(ids) > 200 {
		ids = ids[:200]
	}
	switch action {
	case "delete":
		if r.PostFormValue("confirm") == "" {
			var hidden []string
			for _, id := range ids {
				hidden = append(hidden, id.String())
			}
			v := a.NewView(w, r, "Delete redirects")
			v.Data = bulkDelete{len(ids), a.adminPath + "/settings/redirects/bulk", a.adminPath + "/settings/redirects", hidden, "redirect", "delete"}
			v.Width = "narrow"
			a.Render(w, r, http.StatusOK, "bulkdelete", v)
			return
		}
	case "on", "off":
	default:
		a.Error(w, r, http.StatusBadRequest, "Bad request", "That is not a bulk action.")
		return
	}
	done := 0
	for _, id := range ids {
		var err error
		switch action {
		case "delete":
			err = rs.DeleteRedirect(r.Context(), id)
		default:
			on := action == "on"
			_, err = rs.UpdateRedirect(r.Context(), id, &redirect.UpdateRedirectInput{IsActive: &on})
		}
		if err != nil {
			a.logger().Error("bulk redirect action failed", "action", action, "err", err)
			continue
		}
		done++
	}
	a.invalidate(r.Context())
	verb := map[string]string{"delete": "deleted", "on": "turned on", "off": "turned off"}[action]
	a.SetFlash(w, r, "success", strconv.Itoa(done)+" "+plural(done, "redirect", "redirects")+" "+verb+".")
	http.Redirect(w, r, a.adminPath+"/settings/redirects", http.StatusSeeOther)
}
