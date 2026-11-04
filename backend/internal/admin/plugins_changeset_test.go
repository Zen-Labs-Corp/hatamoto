package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/changeset"
)

type fakeChangesets struct {
	h     changeset.Header
	items []changeset.StoredItem
	pages [][2]int // limit, offset of each ItemPage call
}

func (f *fakeChangesets) Header(_ context.Context, id int64) (changeset.Header, error) {
	if id != f.h.ID {
		return changeset.Header{}, changeset.ErrNotFound
	}
	return f.h, nil
}

func (f *fakeChangesets) ItemPage(_ context.Context, _ int64, limit, offset int) ([]changeset.ItemSummary, error) {
	f.pages = append(f.pages, [2]int{limit, offset})
	var out []changeset.ItemSummary
	for i := offset; i < len(f.items) && i < offset+limit; i++ {
		it := f.items[i]
		var p struct{ Name string }
		_ = json.Unmarshal(it.Payload, &p)
		out = append(out, changeset.ItemSummary{Seq: it.Seq, Kind: it.Kind, Op: it.Op, SourceKey: it.SourceKey, TargetID: it.TargetID, Label: p.Name})
	}
	return out, nil
}

func (f *fakeChangesets) PreviewSeqs(context.Context, int64) ([]int, error) {
	var out []int
	for _, it := range f.items {
		if it.Op == changeset.OpUpsert && (it.Kind == changeset.KindEntity || it.Kind == changeset.KindRedirect) {
			out = append(out, it.Seq)
		}
	}
	return out, nil
}

type fakePreviewer struct {
	err   error
	calls []int
	body  string
}

func (f *fakePreviewer) PreviewChangeset(_ context.Context, _ int64, seq int) ([]byte, error) {
	f.calls = append(f.calls, seq)
	if f.err != nil {
		return nil, f.err
	}
	if f.body != "" {
		return []byte(f.body), nil
	}
	return []byte("<!doctype html><title>Staged title</title>"), nil
}

// liveSessions adds the preview host's session check to the rig's store.
type liveSessions struct {
	*fakeSessions
	user uuid.UUID
	live bool
}

func (l liveSessions) LiveAdminUser(context.Context, uuid.UUID) (uuid.UUID, bool) {
	return l.user, l.live
}

func csItem(seq int, kind changeset.Kind, op, name string) changeset.StoredItem {
	p, _ := json.Marshal(map[string]string{"name": name})
	return changeset.StoredItem{Seq: seq, Item: changeset.Item{Kind: kind, Op: op, SourceKey: "k" + name, Payload: p}}
}

func newChangesetRig(t *testing.T, host string) (*rig, *fakeChangesets, *fakePreviewer) {
	t.Helper()
	cs := &fakeChangesets{
		h: changeset.Header{ID: 42, Plugin: "hello", State: changeset.StateStaged, ExpiresAt: time.Now().Add(time.Hour), ItemCount: 4},
		items: []changeset.StoredItem{
			csItem(1, changeset.KindEntity, changeset.OpUpsert, "Hello world"),
			csItem(2, changeset.KindTerm, changeset.OpUpsert, "News"),
			csItem(3, changeset.KindEntity, changeset.OpUpsert, "About"),
			csItem(4, changeset.KindEntity, changeset.OpUpsert, "Contact"),
		},
	}
	fp := &fakePreviewer{}
	g := newRig(t, func(d *Deps) {
		d.Growth.Changesets, d.Growth.ChangesetPreview, d.PreviewHost = cs, fp, host
	})
	return g, cs, fp
}

var (
	iframeFrame = map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "same-origin"}
	frameSrcRe  = regexp.MustCompile(`<iframe [^>]*src="([^"]+)"`)
)

const csPreviewPath = "/admin/plugins/hello/changesets/42/preview"

func TestChangesetPage(t *testing.T) {
	t.Parallel()

	g, cs, _ := newChangesetRig(t, "")
	rec := g.do("GET", "/admin/plugins/hello/changesets/42", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Changeset #42") || !strings.Contains(body, csPreviewPath+"?item=3") || strings.Contains(body, "?item=2") {
		t.Fatalf("changeset page: %d, previewable items must link to their preview and terms must not", rec.Code)
	}
	for path, want := range map[string]int{
		"/admin/plugins/other/changesets/42": 404, // another plugin's changeset
		"/admin/plugins/hello/changesets/43": 404,
		"/admin/plugins/hello/changesets/x":  404,
	} {
		if rec := g.do("GET", path, "owner-token", nil, nil); rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
	if rec := g.do("GET", "/admin/plugins/hello/changesets/42", "editor-token", nil, nil); rec.Code != 404 && rec.Code != 403 {
		t.Errorf("editor: %d, want the standard refusal", rec.Code)
	}
	cs.h.State = changeset.StatePublished
	rec = g.do("GET", "/admin/plugins/hello/changesets/42", "owner-token", nil, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "/changesets/42/preview") {
		t.Errorf("published changeset offers a preview: %d", rec.Code)
	}
}

func TestChangesetPreviewPage(t *testing.T) {
	t.Parallel()

	g, cs, _ := newChangesetRig(t, "")
	rec := g.do("GET", csPreviewPath, "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("preview page: %d", rec.Code)
	}
	tag := regexp.MustCompile(`<iframe [^>]*>`).FindString(body)
	for _, want := range []string{` sandbox=""`, `referrerpolicy="no-referrer"`, `title="Isolated preview of changeset #42"`, `hm-pluginframe--preview`} {
		if !strings.Contains(tag, want) {
			t.Errorf("iframe %q lacks %s", tag, want)
		}
	}
	if strings.Contains(tag, "allow-") || strings.Contains(body, "data-hm-pluginframe") {
		t.Errorf("frame is not sealed: %s", tag)
	}
	if !regexp.MustCompile(`src="/admin/changesets/42/frame/1/[A-Za-z0-9_-]{32}"`).MatchString(tag) {
		t.Errorf("same-origin ticketed src expected: %s", tag)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src 'self';") || strings.Contains(csp, "https://") {
		t.Errorf("admin CSP frame-src: %s", csp)
	}
	if strings.Contains(body, "Previous change") || !strings.Contains(body, "Next change") || !strings.Contains(body, "Back to review") {
		t.Error("first change: Next and Back only")
	}
	// Terms are skipped: from item 3 the neighbours are 1 and 4.
	body = g.do("GET", csPreviewPath+"?item=3", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, csPreviewPath+"?item=1") || !strings.Contains(body, csPreviewPath+"?item=4") {
		t.Error("previous and next of item 3 must be 1 and 4")
	}
	if strings.Contains(g.do("GET", csPreviewPath+"?item=4", "owner-token", nil, nil).Body.String(), "Next change") {
		t.Error("last change has no Next")
	}
	for _, q := range []string{"?item=2", "?item=9", "?item=x"} {
		if rec := g.do("GET", csPreviewPath+q, "owner-token", nil, nil); rec.Code != 404 {
			t.Errorf("%s: %d, want 404", q, rec.Code)
		}
	}
	if rec := g.do("GET", csPreviewPath, "editor-token", nil, nil); rec.Code == 200 {
		t.Error("an editor opened the preview page")
	}
	cs.h.ExpiresAt = time.Now().Add(-time.Minute)
	if rec := g.do("GET", csPreviewPath, "owner-token", nil, nil); rec.Code != 409 {
		t.Errorf("expired: %d, want 409", rec.Code)
	}
	cs.h.ExpiresAt, cs.h.State = time.Now().Add(time.Hour), changeset.StateDiscarded
	if rec := g.do("GET", csPreviewPath, "owner-token", nil, nil); rec.Code != 409 {
		t.Errorf("discarded: %d, want 409", rec.Code)
	}
}

func TestChangesetFrame(t *testing.T) {
	t.Parallel()

	g, _, fp := newChangesetRig(t, "")
	src := func() string {
		t.Helper()
		m := frameSrcRe.FindStringSubmatch(g.do("GET", csPreviewPath, "owner-token", nil, nil).Body.String())
		if m == nil {
			t.Fatal("no iframe src")
		}
		return m[1]
	}
	srcPath := src()
	rec := g.do("GET", srcPath, "owner-token", nil, iframeFrame)
	h := rec.Header()
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Staged title") {
		t.Fatalf("frame: %d %q", rec.Code, rec.Body.String())
	}
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"sandbox;", "default-src 'none'", "script-src 'none'", "form-action 'none'", "frame-ancestors http://example.com"} {
		if !strings.Contains(csp, want) {
			t.Errorf("frame CSP %q lacks %s", csp, want)
		}
	}
	if strings.Contains(csp, "frame-ancestors 'self'") || strings.Contains(csp, "allow-") {
		t.Errorf("frame CSP: %s", csp)
	}
	if h.Get("Set-Cookie") != "" || !strings.Contains(h.Get("Cache-Control"), "no-store") || h.Get("X-Robots-Tag") != "noindex" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("frame headers: %v", h)
	}
	if got := g.do("GET", srcPath, "owner-token", nil, iframeFrame); got.Code != 404 {
		t.Errorf("second use: %d, want 404", got.Code)
	}
	if len(fp.calls) != 1 {
		t.Errorf("renders = %v, the refused replay must not render", fp.calls)
	}
	if got := g.do("GET", src(), "editor-token", nil, iframeFrame); got.Code != 404 {
		t.Errorf("other session: %d", got.Code)
	}
	if got := g.do("GET", src(), "", nil, iframeFrame); got.Code != 404 {
		t.Errorf("no session: %d", got.Code)
	}
	if got := g.do("GET", strings.Replace(src(), "/frame/1/", "/frame/3/", 1), "owner-token", nil, iframeFrame); got.Code != 404 {
		t.Errorf("other item: %d", got.Code)
	}
	if got := g.do("GET", src(), "owner-token", nil, map[string]string{"Sec-Fetch-Dest": "document"}); got.Code != 404 {
		t.Errorf("as a document: %d", got.Code)
	}
	// An unticketed request is not served even as a top-level navigation.
	if got := g.do("GET", "/admin/changesets/42/frame/1/"+strings.Repeat("A", 32), "owner-token", nil, map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"}); got.Code != 404 {
		t.Errorf("unticketed navigation: %d", got.Code)
	}
	for _, e := range []error{changeset.ErrNotStaged, changeset.ErrExpired, changeset.ErrNotFound} {
		fp.err = e
		if got := g.do("GET", src(), "owner-token", nil, iframeFrame); got.Code != 404 {
			t.Errorf("%v: %d, want 404", e, got.Code)
		}
	}
}

func TestChangesetPreviewHost(t *testing.T) {
	t.Parallel()

	const host = "preview.example.net"
	g, _, fp := newChangesetRig(t, host)
	ls := liveSessions{fakeSessions: g.sessions, user: g.owner.ID, live: true}
	g.a.deps.Sessions = ls
	h := g.a.PreviewHostHandler()

	page := g.do("GET", csPreviewPath, "owner-token", nil, nil)
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src 'self' https://"+host+";") {
		t.Errorf("admin CSP frame-src: %s", csp)
	}
	m := frameSrcRe.FindStringSubmatch(page.Body.String())
	if m == nil || !strings.HasPrefix(m[1], "https://"+host+"/changesets/42/frame/1/") {
		t.Fatalf("host-mode src: %v", m)
	}
	get := func(src string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", src, nil)
		req.Host = host
		if hdr == nil {
			hdr = map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "cross-site"}
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	path := strings.TrimPrefix(m[1], "https://"+host)
	rec := get(path, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Staged title") {
		t.Fatalf("host frame: %d", rec.Code)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors http://example.com") || !strings.Contains(csp, "sandbox;") {
		t.Errorf("host frame CSP: %s", csp)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Error("the preview host set a cookie")
	}
	if get(path, nil).Code != 404 {
		t.Error("replay on the host")
	}
	fresh := func() string {
		return strings.TrimPrefix(frameSrcRe.FindStringSubmatch(g.do("GET", csPreviewPath, "owner-token", nil, nil).Body.String())[1], "https://"+host)
	}
	if get(fresh(), map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"}).Code != 404 {
		t.Error("host served a top-level navigation")
	}
	ls.live = false
	g.a.deps.Sessions = ls
	if get(fresh(), nil).Code != 404 {
		t.Error("host served a signed-out session")
	}
	ls.live, ls.user = true, g.editor.ID
	g.a.deps.Sessions = ls
	if get(fresh(), nil).Code != 404 {
		t.Error("host served a session of an editor")
	}
	g.a.deps.Sessions = g.sessions // no liveness check available: fail closed
	if get(fresh(), nil).Code != 404 {
		t.Error("host served without a session check")
	}
	for _, p := range []string{"/", "/admin", "/changesets/42", "/changesets/42/frame/1", "/changesets/42/frame/1/short", "/robots.txt", "/admin/changesets/42/frame/1/" + strings.Repeat("A", 32)} {
		if rec := get(p, nil); rec.Code != 404 {
			t.Errorf("%s on the host: %d, want 404", p, rec.Code)
		}
	}
	if len(fp.calls) != 1 {
		t.Errorf("renders = %v, only the first frame may render", fp.calls)
	}
	g.a.deps.PreviewHost = ""
	if get(fresh(), nil).Code != 404 {
		t.Error("host handler answered with the host mode off")
	}
}

// TestChangesetPageGoldens pins the markup of the two changeset pages and runs
// the accessibility checker on them. The one-time ticket is masked.
func TestChangesetPageGoldens(t *testing.T) {
	t.Parallel()

	g, cs, _ := newChangesetRig(t, "")
	cs.h.ExpiresAt = time.Date(2099, 1, 2, 3, 4, 0, 0, time.UTC)
	ticket := regexp.MustCompile(`/frame/(\d+)/[A-Za-z0-9_-]{32}`)
	for _, p := range []struct{ name, path string }{
		{"changeset", "/admin/plugins/hello/changesets/42"},
		{"changeset-preview", csPreviewPath + "?item=3"},
	} {
		rec := g.do("GET", p.path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", p.name, rec.Code)
		}
		body := ticket.ReplaceAllString(normalizePage(rec.Body.String()), "/frame/$1/TICKET")
		compareGolden(t, "pages/plugins-"+p.name+".html", body)
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %s", p.name, strings.Join(issues, "; "))
		}
	}
}

// The frame must let the theme style the page (stylesheet, fonts, images from
// the public site) and stay free of script.
func TestChangesetFrameLoadsTheme(t *testing.T) {
	t.Parallel()

	const site = "https://www.example.org"
	for _, host := range []string{"", "preview.example.net"} {
		g, _, fp := newChangesetRig(t, host)
		fp.body = `<!doctype html><head><link rel="stylesheet" href="/theme/assets/a.css"></head><img src="/media/x.png"><a href="//cdn.example/y">y</a>`
		g.a.deps.BaseURL = func(*http.Request) string { return site + "/" }
		ls := liveSessions{fakeSessions: g.sessions, user: g.owner.ID, live: true}
		g.a.deps.Sessions = ls
		m := frameSrcRe.FindStringSubmatch(g.do("GET", csPreviewPath, "owner-token", nil, nil).Body.String())
		if m == nil {
			t.Fatal("no iframe src")
		}
		var rec *httptest.ResponseRecorder
		if host == "" {
			rec = g.do("GET", m[1], "owner-token", nil, iframeFrame)
		} else {
			req := httptest.NewRequest("GET", strings.TrimPrefix(m[1], "https://"+host), nil)
			req.Host = host
			req.Header.Set("Sec-Fetch-Dest", "iframe")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			rec = httptest.NewRecorder()
			g.a.PreviewHostHandler().ServeHTTP(rec, req)
		}
		if rec.Code != 200 {
			t.Fatalf("host %q: %d", host, rec.Code)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"style-src 'unsafe-inline' " + site + ";", "font-src " + site + ";", "img-src " + site + " data:", "script-src 'none'", "form-action 'none'", "sandbox;"} {
			if !strings.Contains(csp, want) {
				t.Errorf("host %q: CSP %q lacks %s", host, csp, want)
			}
		}
		body := rec.Body.String()
		for _, want := range []string{`href="` + site + `/theme/assets/a.css"`, `src="` + site + `/media/x.png"`, `href="//cdn.example/y"`} {
			if !strings.Contains(body, want) {
				t.Errorf("host %q: body lacks %s: %s", host, want, body)
			}
		}
	}
}

// The changeset page reads one page of summaries, never the whole item set.
func TestChangesetPagePaginates(t *testing.T) {
	t.Parallel()

	g, cs, _ := newChangesetRig(t, "")
	cs.items = nil
	for i := 1; i <= 120; i++ {
		cs.items = append(cs.items, csItem(i, changeset.KindEntity, changeset.OpUpsert, fmt.Sprintf("Page %03d", i)))
	}
	cs.h.ItemCount = len(cs.items)
	body := g.do("GET", "/admin/plugins/hello/changesets/42?page=2", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Page 051") || !strings.Contains(body, "Page 100") || strings.Contains(body, "Page 050") || strings.Contains(body, "Page 101") {
		t.Errorf("page 2 does not show exactly items 51 to 100")
	}
	if len(cs.pages) != 1 || cs.pages[0] != [2]int{changesetItemsPer, changesetItemsPer} {
		t.Errorf("ItemPage calls = %v, want one bounded page read", cs.pages)
	}
	if rec := g.do("GET", "/admin/plugins/hello/changesets/42?page=99999999999", "owner-token", nil, nil); rec.Code != 200 {
		t.Errorf("huge page: %d", rec.Code)
	}
}
