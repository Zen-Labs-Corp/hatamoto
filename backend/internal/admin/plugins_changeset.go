package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/user"
)

// ChangesetReader reads a staged changeset. The changeset engine implements it.
type ChangesetReader interface {
	Header(ctx context.Context, id int64) (changeset.Header, error)
	// ItemPage reads item summaries (no payloads) for one page of the list.
	ItemPage(ctx context.Context, id int64, limit, offset int) ([]changeset.ItemSummary, error)
	// PreviewSeqs lists the seq of every item the preview can open.
	PreviewSeqs(ctx context.Context, id int64) ([]int, error)
}

// changesetItemsPer is the page size of the changeset page's item list.
const changesetItemsPer = 50

// ChangesetPreviewer renders one item of a staged changeset as the site would
// show it once published: a complete HTML document, from the changeset's own
// data, written nowhere. It answers changeset.ErrNotFound, ErrNotStaged or
// ErrExpired when there is nothing to show.
type ChangesetPreviewer interface {
	PreviewChangeset(ctx context.Context, id int64, seq int) ([]byte, error)
}

// SessionLiveness tells whether an admin session is still signed in, and as
// whom, without a cookie and without touching it. The preview host has no
// cookie, so it asks this instead; a session store without it refuses
// every preview-host request.
type SessionLiveness interface {
	LiveAdminUser(ctx context.Context, id uuid.UUID) (uuid.UUID, bool)
}

func (a *Admin) mountChangesets(authed *httpx.Group, public *httpx.Group) {
	owner := authed.Group("", a.RequireRole("owner"))
	owner.HandleFunc("GET /plugins/{name}/changesets/{id}", a.handleChangeset)
	owner.HandleFunc("GET /plugins/{name}/changesets/{id}/preview", a.handleChangesetPreview)
	// The adminguard verifies the one-time frame ticket in the URL (session,
	// owner, changeset and item bound) before this handler runs.
	public.HandleFunc("GET /changesets/{id}/frame/{seq}/{ticket}", a.handleChangesetFrame)
}

// changesetURL is the admin page of a changeset; page is "" or "preview".
func (a *Admin) changesetURL(plugin string, id int64, page string) string {
	u := a.pluginURL(plugin, "changesets/"+strconv.FormatInt(id, 10))
	if page != "" {
		u += "/" + page
	}
	return u
}

// loadChangeset reads the changeset named by the path for the plugin named by
// the path; any other plugin's changeset is "not found".
func (a *Admin) loadChangeset(w http.ResponseWriter, r *http.Request) (changeset.Header, bool) {
	if a.deps.Growth.Changesets == nil {
		a.unavailable(w, r, "Changesets")
		return changeset.Header{}, false
	}
	back, label := a.adminPath+"/plugins", "Back to plugins"
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		a.ErrorBack(w, r, http.StatusNotFound, "Changeset not found", "That changeset does not exist.", back, label)
		return changeset.Header{}, false
	}
	h, err := a.deps.Growth.Changesets.Header(r.Context(), id)
	switch {
	case errors.Is(err, changeset.ErrNotFound), err == nil && h.Plugin != r.PathValue("name"):
		a.ErrorBack(w, r, http.StatusNotFound, "Changeset not found", "That changeset does not exist.", back, label)
		return h, false
	case err != nil:
		a.serverError(w, r, "The changeset could not be loaded", err)
		return h, false
	}
	return h, true
}

// previewable is what the preview can show for an item: an entity that is
// added or changed, or a redirect. changeset.Engine.PreviewSeqs applies the
// same rule in SQL.
func previewable(it changeset.ItemSummary) bool {
	return it.Op == changeset.OpUpsert && (it.Kind == changeset.KindEntity || it.Kind == changeset.KindRedirect)
}

// stagedNow reports whether the changeset can still be previewed.
func stagedNow(h changeset.Header) bool {
	return h.State == changeset.StateStaged && (h.ExpiresAt.IsZero() || h.ExpiresAt.After(time.Now()))
}

// itemLabel names an item for people: its title, else its address, else the
// key the plugin staged it under.
func itemLabel(it changeset.ItemSummary) string {
	for _, s := range []string{it.Label, it.SourceKey} {
		if s != "" {
			return s
		}
	}
	return "Item " + strconv.Itoa(it.Seq)
}

func itemChange(it changeset.ItemSummary) string {
	switch {
	case it.Op == changeset.OpDelete:
		return "Delete"
	case it.TargetID != "":
		return "Change"
	}
	return "Add or change"
}

func stateBadge(state string) ui.Badge {
	switch state {
	case changeset.StateStaged:
		return ui.Badge{Label: "Not live", Kind: "warning"}
	case changeset.StatePublished:
		return ui.Badge{Label: "Published", Kind: "success"}
	case changeset.StateReverted:
		return ui.Badge{Label: "Undone", Kind: "muted"}
	case changeset.StateExpired:
		return ui.Badge{Label: "Expired", Kind: "muted"}
	}
	return ui.Badge{Label: "Discarded", Kind: "muted"}
}

type changesetPage struct {
	ID      int64
	Badge   ui.Badge
	Intro   string
	Preview string // "" when the changeset can no longer be previewed
	Table   ui.Table
	// Review is set when a reviewer is wired: the review screen replaces the plain list.
	Review *reviewPage
}

// handleChangeset is the changeset page. With a reviewer it is the review
// screen; without one it is the plain list of what the changeset holds and the
// way to its preview.
func (a *Admin) handleChangeset(w http.ResponseWriter, r *http.Request) {
	h, ok := a.loadChangeset(w, r)
	if !ok {
		return
	}
	if a.deps.Growth.Reviewer != nil {
		a.renderReview(w, r, h)
		return
	}
	page := min(pageParam(r), max(1, (h.ItemCount+changesetItemsPer-1)/changesetItemsPer))
	items, err := a.deps.Growth.Changesets.ItemPage(r.Context(), h.ID, changesetItemsPer, (page-1)*changesetItemsPer)
	if err != nil {
		a.serverError(w, r, "The changeset could not be loaded", err)
		return
	}
	d := changesetPage{ID: h.ID, Badge: stateBadge(h.State), Intro: "Staged by " + h.Plugin + "."}
	if h.State == changeset.StateStaged && !h.ExpiresAt.IsZero() {
		d.Intro += " Expires " + fmtDateTime(h.ExpiresAt.In(a.siteLocation(r.Context()))) + "."
	}
	if stagedNow(h) {
		d.Preview = a.changesetURL(h.Plugin, h.ID, "preview")
	}
	d.Table = ui.Table{
		ID: "changes", Caption: "Changes in this changeset",
		Columns: []ui.Col{{Key: "name", Label: "Name", Role: "title"}, {Key: "kind", Label: "Kind"}, {Key: "change", Label: "Change"}},
		Empty:   ui.Empty{Icon: "file-text", Title: "Nothing staged.", Body: "This changeset holds no changes."},
		BaseURL: a.changesetURL(h.Plugin, h.ID, ""),
		Page:    ui.Pagination{Page: page, PerPage: changesetItemsPer, Total: h.ItemCount, BaseURL: a.changesetURL(h.Plugin, h.ID, "")},
	}
	for _, it := range items {
		row := ui.Row{ID: strconv.Itoa(it.Seq), Label: itemLabel(it), Cells: []ui.Cell{
			{Text: itemLabel(it), Clip: true}, {Text: strings.ToUpper(string(it.Kind)[:1]) + string(it.Kind)[1:]}, {Text: itemChange(it)},
		}}
		if d.Preview != "" && previewable(it) {
			row.Actions = []ui.Action{{Label: "Preview", Href: d.Preview + "?item=" + strconv.Itoa(it.Seq), Button: true}}
		}
		d.Table.Rows = append(d.Table.Rows, row)
	}
	v := a.NewView(w, r, "Changeset #"+strconv.FormatInt(h.ID, 10))
	v.Width = "narrow"
	v.Crumbs = a.changesetCrumbs(h, Crumb{Label: "Changeset #" + strconv.FormatInt(h.ID, 10)})
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/changeset", v)
}

func (a *Admin) changesetCrumbs(h changeset.Header, tail ...Crumb) []Crumb {
	c := []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: h.Plugin, Href: a.pluginURL(h.Plugin, "settings")}}
	if len(tail) > 1 {
		tail[0].Href = a.changesetURL(h.Plugin, h.ID, "")
	}
	return append(c, tail...)
}

type changesetPreviewPage struct {
	ID         int64
	Badge      ui.Badge
	FrameTitle string
	Src        string // the iframe src, with its one-time ticket
	Shown      string // the same address without the ticket
	Prev, Next string
	Back       string
	Empty      bool
}

// handleChangesetPreview is screen "Preview the site": the sealed frame of one
// change of a staged changeset and the way to the others. Each view mints a
// fresh one-time ticket, so a reload of this page re-opens the frame.
func (a *Admin) handleChangesetPreview(w http.ResponseWriter, r *http.Request) {
	h, ok := a.loadChangeset(w, r)
	if !ok {
		return
	}
	back := a.changesetURL(h.Plugin, h.ID, "")
	if !stagedNow(h) {
		a.ErrorBack(w, r, http.StatusConflict, "Preview not available", "Only a changeset that is waiting for a decision can be previewed.", back, "Back to review")
		return
	}
	if a.deps.Growth.ChangesetPreview == nil {
		a.unavailable(w, r, "Changeset preview")
		return
	}
	seqs, err := a.deps.Growth.Changesets.PreviewSeqs(r.Context(), h.ID)
	if err != nil {
		a.serverError(w, r, "The changeset could not be loaded", err)
		return
	}
	title := "Preview changeset #" + strconv.FormatInt(h.ID, 10)
	d := changesetPreviewPage{ID: h.ID, Badge: stateBadge(h.State), Back: back, Empty: len(seqs) == 0,
		FrameTitle: "Isolated preview of changeset #" + strconv.FormatInt(h.ID, 10)}
	at := 0
	if q := r.URL.Query().Get("item"); q != "" {
		seq, err := strconv.Atoi(q)
		at = indexOf(seqs, seq)
		if err != nil || at < 0 {
			a.ErrorBack(w, r, http.StatusNotFound, "Change not found", "That change is not in this changeset, or it cannot be previewed.", back, "Back to review")
			return
		}
	}
	if !d.Empty {
		seq := seqs[at]
		origin := a.requestOrigin(r)
		tok, ok := a.mintChangesetPreview(r, h.ID, seq, origin)
		if !ok {
			a.refuse(w, r)
			return
		}
		path := "/changesets/" + strconv.FormatInt(h.ID, 10) + "/frame/" + strconv.Itoa(seq)
		base := a.adminPath
		if a.deps.PreviewHost != "" {
			base = "https://" + a.deps.PreviewHost
		}
		d.Src, d.Shown = base+path+"/"+url.PathEscape(tok), base+path
		page := a.changesetURL(h.Plugin, h.ID, "preview") + "?item="
		if at > 0 {
			d.Prev = page + strconv.Itoa(seqs[at-1])
		}
		if at < len(seqs)-1 {
			d.Next = page + strconv.Itoa(seqs[at+1])
		}
	}
	v := a.NewView(w, r, title)
	v.Width = "narrow"
	v.Crumbs = a.changesetCrumbs(h, Crumb{Label: "Changeset #" + strconv.FormatInt(h.ID, 10)}, Crumb{Label: "Preview"})
	v.Data = d
	// This page alone may frame the preview host.
	w.Header().Set("Content-Security-Policy", cspHeader(Nonce(r.Context()), false, a.deps.PreviewHost))
	a.Render(w, r, http.StatusOK, "plugins/changeset-preview", v)
}

func indexOf(s []int, v int) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// handleChangesetFrame serves the frame on the admin's own origin. Only the
// guard's ticket check (it marks the request frameable) lets a request in.
func (a *Admin) handleChangesetFrame(w http.ResponseWriter, r *http.Request) {
	ref, ok := parseChangesetFrame(r.PathValue("id") + "/frame/" + r.PathValue("seq") + "/" + r.PathValue("ticket"))
	if !ok || !frameable(r.Context()) {
		a.refuse(w, r)
		return
	}
	a.serveChangesetFrame(w, r, ref.id, ref.seq, a.requestOrigin(r))
}

// PreviewHostHandler serves the changeset frame on the preview host, and
// nothing else: no cookie is read or set, so the ticket is the whole
// credential. It is one-time, 60 seconds, bound to the owner session that
// minted it, and that session must still be signed in as an owner.
func (a *Admin) PreviewHostHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header
		cs, ok := a.changesetHostRoute(r)
		if !ok || a.deps.PreviewHost == "" || h.Get("Sec-Fetch-Dest") != "iframe" || h.Get("Sec-Fetch-Mode") != "navigate" {
			a.refuse(w, r)
			return
		}
		sess, origin, ok := a.tickets.consumeBound(ticketChangesetPreview, cs.ticket, changesetTarget(cs.id, cs.seq))
		live, _ := a.deps.Sessions.(SessionLiveness)
		if !ok || live == nil || a.deps.Users == nil || !validOrigin(string(origin)) {
			a.refuse(w, r)
			return
		}
		uid, ok := live.LiveAdminUser(r.Context(), sess)
		if !ok {
			a.refuse(w, r)
			return
		}
		if u, err := a.deps.Users.GetByID(r.Context(), uid); err != nil || u.Role != user.RoleOwner {
			a.refuse(w, r)
			return
		}
		a.serveChangesetFrame(w, r, cs.id, cs.seq, string(origin))
	})
}

// changesetHostRoute parses /changesets/{id}/frame/{seq}/{ticket}.
func (a *Admin) changesetHostRoute(r *http.Request) (changesetRef, bool) {
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/changesets/")
	if r.Method != http.MethodGet || !ok {
		return changesetRef{}, false
	}
	return parseChangesetFrame(rest)
}

// validOrigin accepts scheme://host[:port] and nothing else, so what goes into
// a frame-ancestors directive cannot carry a second source.
func validOrigin(o string) bool {
	u, err := url.Parse(o)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" &&
		u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil && !strings.ContainsAny(o, " ;,'\"")
}

// frameSiteOrigin is the scheme://host of the configured site address, or ""
// when none is set or it is not a plain origin.
func (a *Admin) frameSiteOrigin(r *http.Request) string {
	if a.deps.BaseURL == nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(a.deps.BaseURL(r)))
	if err != nil || u.Host == "" {
		return ""
	}
	if o := u.Scheme + "://" + u.Host; validOrigin(o) {
		return o
	}
	return ""
}

// rootRelAttr matches an href, src, poster or srcset value that starts with a
// single slash, so it is relative to the site root.
var rootRelAttr = regexp.MustCompile(`((?:href|src|poster|srcset)=")/([^/"])`)

// absolutizeAssets points root-relative URLs at the public site, so a frame
// served from the preview host (or any other address) still loads the theme.
func absolutizeAssets(body []byte, site string) []byte {
	return rootRelAttr.ReplaceAll(body, []byte("${1}"+site+"/${2}"))
}

// serveChangesetFrame writes the rendered document under the sealed header
// set: it runs no script, takes no form, may only be framed by the admin's
// origin, and is never stored or indexed. It sets no cookie.
func (a *Admin) serveChangesetFrame(w http.ResponseWriter, r *http.Request, id int64, seq int, origin string) {
	p := a.deps.Growth.ChangesetPreview
	if p == nil {
		a.refuse(w, r)
		return
	}
	body, err := p.PreviewChangeset(r.Context(), id, seq)
	switch {
	case errors.Is(err, changeset.ErrNotFound), errors.Is(err, changeset.ErrNotStaged), errors.Is(err, changeset.ErrExpired):
		a.refuse(w, r)
		return
	case err != nil:
		a.logger().Error("changeset preview failed", "changeset", id, "err", err)
		http.Error(w, "The preview could not be built.", http.StatusInternalServerError)
		return
	}
	hd := w.Header()
	hd.Del("Set-Cookie")
	hd.Del("X-Frame-Options") // frame-ancestors below is the rule
	hd.Del("Content-Security-Policy-Report-Only")
	// The theme's stylesheet, fonts and images come from the public site: its
	// origin when one is configured (the only way on a preview host, which
	// serves nothing else), the frame's own origin otherwise. Scripts stay off.
	site := a.frameSiteOrigin(r)
	src := "'self'"
	if site != "" {
		src = site
		body = absolutizeAssets(body, site)
	}
	hd.Set("Content-Security-Policy", "sandbox; default-src 'none'; script-src 'none'; form-action 'none'; base-uri 'none'; "+
		"style-src 'unsafe-inline' "+src+"; font-src "+src+"; img-src "+src+" data: https:; frame-ancestors "+origin)
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Robots-Tag", "noindex")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

// reviewReasons say, in the owner's words, why a staged item needs a decision.
var reviewReasons = map[string]string{
	string(changeset.ReasonLinkHost):    "Links to a site this one does not link to yet",
	string(changeset.ReasonLinkQuery):   "A link keeps its address but changes its query",
	string(changeset.ReasonEmbed):       "Embeds content from outside",
	string(changeset.ReasonHiddenChar):  "Hides a character the reader cannot see",
	string(changeset.ReasonRedirect):    "Sends visitors from one address to another",
	string(changeset.ReasonSlug):        "Changes the address or the parent of a page",
	string(changeset.ReasonDelete):      "Removes content from the site",
	string(changeset.ReasonMenuLink):    "A menu links to a site this one does not link to yet",
	string(changeset.ReasonUnparseable): "Its content could not be checked",
}

// reviewPage is the review screen of a staged changeset.
type reviewPage struct {
	ID         int64
	Title      string
	Badge      ui.Badge
	ChipBadges []ui.Badge
	Actions    []ui.Button
	// Closed is the read-only summary of a changeset that is no longer open for
	// review: published, discarded, undone or expired.
	Closed         bool
	ClosedNote     string
	ChangesHref    string
	Expiry         string
	SkippedNote    string
	Suggestions    []reviewSuggestion
	SuggestionsURL string
	Risky          []reviewRisk
	Group          *reviewGroup
	Decided        ui.Badge
	Kinds          ui.Table
	Skipped        ui.Table
	DecideURL      string
	PublishURL     string
	DiscardURL     string
	PublishLabel   string
	CanPublish     bool
	Note           string
	Preview        string
}

type reviewSuggestion struct {
	ID         int64
	Title      string
	Applicable bool
	Decided    bool
	Fields     []SuggestionField
	Seg        ui.Segmented
}

type reviewRisk struct {
	Label, Reason string
	Old, New      string
	Hidden        bool // the marker badge of a hidden character
	Seg           ui.Segmented
}

type reviewGroup struct {
	Label, Sub, Open string
	Seg              ui.Segmented
}

// reviewSegmented is the Keep or Drop choice of one risky item; keep names the
// first choice and drop the second.
func reviewSegmented(label, name, decision, keep, drop string) ui.Segmented {
	items := []ui.SegmentedItem{{Label: keep, Value: "approved", Pressed: decision == "approved"}, {Label: drop, Value: "dropped", Pressed: decision == "dropped"}}
	return ui.Segmented{Mode: "radio", Label: label, Name: name, Items: items}
}

// renderReview shows the review screen, or the read-only summary once the
// changeset is closed.
func (a *Admin) renderReview(w http.ResponseWriter, r *http.Request, h changeset.Header) {
	title := "Changeset #" + strconv.FormatInt(h.ID, 10)
	back := a.adminPath + "/settings/changes"
	page := &reviewPage{ID: h.ID, Title: title, Badge: stateBadge(h.State), ChangesHref: back}
	v := a.NewView(w, r, title)
	v.Width = "narrow"
	v.Crumbs = a.changesetCrumbs(h, Crumb{Label: title})
	if !stagedNow(h) {
		page.Closed = true
		switch {
		case h.State == changeset.StateStaged:
			page.Badge = stateBadge(changeset.StateExpired)
			page.ClosedNote = "Nobody decided in time, so these changes expired and were deleted. Nothing reached the site."
		case h.State == changeset.StatePublished:
			page.ClosedNote = "These changes are live. You can undo the whole run from Settings, Changes."
		case h.State == changeset.StateReverted:
			page.ClosedNote = "These changes were published and then undone."
		default:
			page.ClosedNote = "These changes were thrown away. Nothing reached the site."
		}
		v.Data = changesetPage{ID: h.ID, Badge: page.Badge, Review: page}
		a.Render(w, r, http.StatusOK, "plugins/changeset", v)
		return
	}
	rd, err := a.deps.Growth.Reviewer.Review(r.Context(), h.ID)
	switch {
	case errors.Is(err, ErrReviewNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Changeset not found", "That changeset does not exist.", a.adminPath+"/plugins", "Back to plugins")
		return
	case errors.Is(err, ErrReviewNotStaged), errors.Is(err, ErrReviewExpired):
		http.Redirect(w, r, a.changesetURL(h.Plugin, h.ID, ""), http.StatusSeeOther)
		return
	case err != nil:
		a.serverError(w, r, "The changeset could not be loaded", err)
		return
	}
	a.fillReview(page, h, rd)
	page.Title += ": " + rd.RunName
	v.Title = page.Title
	v.Data = changesetPage{ID: h.ID, Badge: page.Badge, Review: page}
	a.Render(w, r, http.StatusOK, "plugins/changeset", v)
}

// fillReview turns the reviewer's data into the screen's rows.
func (a *Admin) fillReview(p *reviewPage, h changeset.Header, rd ReviewData) {
	p.Badge = ui.Badge{Label: "Waiting for you", Kind: "warning"}
	p.DecideURL = a.changesetURL(h.Plugin, h.ID, "decide")
	p.PublishURL = a.changesetURL(h.Plugin, h.ID, "publish")
	p.DiscardURL = a.changesetURL(h.Plugin, h.ID, "discard")
	p.SuggestionsURL = a.changesetURL(h.Plugin, h.ID, "suggestions")
	if rd.Previewable {
		p.Preview = a.changesetURL(h.Plugin, h.ID, "preview")
		p.Actions = []ui.Button{{Label: "Open preview", Href: p.Preview, Icon: "eye"}}
	}
	if !rd.ExpiresAt.IsZero() {
		p.Expiry = fmt.Sprintf("Deleted in %d h if not decided", max(1, int((time.Until(rd.ExpiresAt)+time.Hour-1)/time.Hour)))
	}
	if n := len(rd.Skipped); n > 0 {
		p.SkippedNote = fmt.Sprintf("%d skipped (you edited them)", n)
	}
	undecidedSuggestions := 0
	for _, s := range rd.Suggestions {
		row := reviewSuggestion{ID: s.ID, Title: s.Title, Applicable: s.Applicable, Decided: s.Decision != "", Fields: s.Fields}
		name := "suggestion_" + strconv.FormatInt(s.ID, 10)
		row.Seg = ui.Segmented{Mode: "radio", Label: "Decision for " + s.Title, Name: name, Items: []ui.SegmentedItem{
			{Label: "Approve", Value: "approved", Pressed: s.Decision == "approved", Disabled: !s.Applicable || s.Decision != ""},
			{Label: "Reject", Value: "rejected", Pressed: s.Decision == "rejected", Disabled: s.Decision != ""},
		}}
		if !s.Applicable {
			row.Seg.Items = row.Seg.Items[1:]
		}
		if s.Decision == "" {
			undecidedSuggestions++
		}
		p.Suggestions = append(p.Suggestions, row)
	}
	for _, it := range rd.Risky {
		label := it.Label
		drop := "Drop"
		if it.Reason == string(changeset.ReasonHiddenChar) {
			drop = "Remove it"
		}
		p.Risky = append(p.Risky, reviewRisk{
			Label: label, Reason: reviewReasons[it.Reason], Old: it.Old, New: it.New, Hidden: it.Reason == string(changeset.ReasonHiddenChar),
			Seg: reviewSegmented("Decision for "+label, "item_"+strconv.Itoa(it.Seq), it.Decision, "Keep", drop),
		})
	}
	if g := rd.RedirectGroup; g != nil && g.Count > 0 {
		label := fmt.Sprintf("%s redirects", thousands(g.Count))
		if g.Count == 1 {
			label = "1 redirect"
		}
		p.Group = &reviewGroup{Label: label, Sub: "All to pages in this changeset", Seg: reviewSegmented("Decision for "+label, "group_redirects", g.Decision, "Keep", "Drop")}
		if p.Preview != "" && len(g.Seqs) > 0 {
			p.Group.Open = p.Preview + "?item=" + strconv.Itoa(g.Seqs[0])
		}
	}
	total := rd.High + rd.Critical
	p.Decided = ui.Badge{Label: fmt.Sprintf("%d of %d decided", rd.HighDecided, total), Kind: "warning"}
	if rd.HighDecided >= total {
		p.Decided.Kind = "success"
	}
	p.Kinds = ui.Table{
		ID: "ordinary", Caption: "Ordinary changes",
		Columns: []ui.Col{{Key: "what", Label: "What", Role: "title"}, {Key: "new", Label: "New", Role: "num"}, {Key: "updated", Label: "Updated", Role: "num"}, {Key: "removed", Label: "Removed", Role: "num"}},
		Empty:   ui.Empty{Icon: "file-text", Title: "Nothing else.", Body: "Every change in this run needs a decision."},
	}
	for _, k := range rd.Kinds {
		p.Kinds.Rows = append(p.Kinds.Rows, ui.Row{ID: k.Label, Label: k.Label, Cells: []ui.Cell{{Text: k.Label}, {Text: thousands(k.New)}, {Text: thousands(k.Updated)}, {Text: thousands(k.Removed)}}})
	}
	p.Skipped = ui.Table{
		ID: "skipped", Caption: "Skipped changes",
		Columns: []ui.Col{{Key: "page", Label: "Page", Role: "title"}, {Key: "why", Label: "Why"}},
		Empty:   ui.Empty{Icon: "file-text", Title: "Nothing skipped."},
	}
	for i, s := range rd.Skipped {
		p.Skipped.Rows = append(p.Skipped.Rows, ui.Row{ID: strconv.Itoa(i), Label: s.Label, Cells: []ui.Cell{{Text: s.Label}, {Text: s.Why}}})
	}
	p.CanPublish = rd.CanPublish
	p.PublishLabel = "Publish " + thousands(rd.PublishCount) + " changes"
	if rd.PublishCount == 1 {
		p.PublishLabel = "Publish 1 change"
	}
	undecidedRisky := max(0, total-rd.HighDecided)
	switch {
	case rd.CanPublish:
		p.Note = "Everything is decided. Publishing makes these changes live."
		if rd.DroppedCount > 0 {
			p.Note = fmt.Sprintf("Everything is decided. %d will be dropped.", rd.DroppedCount)
		}
	default:
		p.Note = fmt.Sprintf("%s and %s still need a decision.", countNoun(undecidedRisky, "risky item"), countNoun(undecidedSuggestions, "suggestion"))
	}
	p.ChipBadges = []ui.Badge{{Label: thousands(rd.Items) + " changes", Plain: true}}
	if n := len(rd.Suggestions); n > 0 {
		p.ChipBadges = append(p.ChipBadges, ui.Badge{Label: countNoun(n, "suggestion"), Kind: "warning"})
	}
	if total > 0 {
		p.ChipBadges = append(p.ChipBadges, ui.Badge{Label: fmt.Sprintf("%d risky", total), Kind: "danger"})
	}
}
