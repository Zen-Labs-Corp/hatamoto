package admin

import (
	"context"
	"encoding/json"
	"errors"
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
	Get(ctx context.Context, id int64) (changeset.Header, []changeset.StoredItem, error)
}

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
func (a *Admin) loadChangeset(w http.ResponseWriter, r *http.Request) (changeset.Header, []changeset.StoredItem, bool) {
	if a.deps.Growth.Changesets == nil {
		a.unavailable(w, r, "Changesets")
		return changeset.Header{}, nil, false
	}
	back, label := a.adminPath+"/plugins", "Back to plugins"
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		a.ErrorBack(w, r, http.StatusNotFound, "Changeset not found", "That changeset does not exist.", back, label)
		return changeset.Header{}, nil, false
	}
	h, items, err := a.deps.Growth.Changesets.Get(r.Context(), id)
	switch {
	case errors.Is(err, changeset.ErrNotFound), err == nil && h.Plugin != r.PathValue("name"):
		a.ErrorBack(w, r, http.StatusNotFound, "Changeset not found", "That changeset does not exist.", back, label)
		return h, nil, false
	case err != nil:
		a.serverError(w, r, "The changeset could not be loaded", err)
		return h, nil, false
	}
	return h, items, true
}

// previewable is what the preview can show for an item: an entity that is
// added or changed, or a redirect.
func previewable(it changeset.StoredItem) bool {
	return it.Op == changeset.OpUpsert && (it.Kind == changeset.KindEntity || it.Kind == changeset.KindRedirect)
}

// stagedNow reports whether the changeset can still be previewed.
func stagedNow(h changeset.Header) bool {
	return h.State == changeset.StateStaged && (h.ExpiresAt.IsZero() || h.ExpiresAt.After(time.Now()))
}

// itemLabel names an item for people: its title, else its address, else the
// key the plugin staged it under.
func itemLabel(it changeset.StoredItem) string {
	var p struct {
		Name, Title, Slug, SourcePath string
	}
	_ = json.Unmarshal(it.Payload, &p)
	for _, s := range []string{p.Name, p.Title, p.SourcePath, p.Slug, it.SourceKey} {
		if s != "" {
			return s
		}
	}
	return "Item " + strconv.Itoa(it.Seq)
}

func itemChange(it changeset.StoredItem) string {
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
}

// handleChangeset is the plain changeset page: what it holds, and the way to
// its preview. It stands in until the review screen replaces it.
func (a *Admin) handleChangeset(w http.ResponseWriter, r *http.Request) {
	h, items, ok := a.loadChangeset(w, r)
	if !ok {
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
	h, items, ok := a.loadChangeset(w, r)
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
	var seqs []int
	for _, it := range items {
		if previewable(it) {
			seqs = append(seqs, it.Seq)
		}
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
