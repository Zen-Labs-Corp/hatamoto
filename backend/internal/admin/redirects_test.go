package admin

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/redirect"
)

// fakeRedirects applies the same rules as redirect.Service (status code,
// wildcard validation, duplicate source) over memory.
type fakeRedirects struct {
	rules  map[uuid.UUID]*redirect.Redirect
	log    map[uuid.UUID]*redirect.NotFoundEntry
	linked map[uuid.UUID]uuid.UUID
}

func newFakeRedirects() *fakeRedirects {
	return &fakeRedirects{rules: map[uuid.UUID]*redirect.Redirect{}, log: map[uuid.UUID]*redirect.NotFoundEntry{}, linked: map[uuid.UUID]uuid.UUID{}}
}

func (f *fakeRedirects) add(src, target string, code int) *redirect.Redirect {
	rd := &redirect.Redirect{ID: uuid.New(), SourcePath: src, TargetURL: target, StatusCode: code, IsActive: true, CreatedAt: time.Now()}
	f.rules[rd.ID] = rd
	return rd
}

func (f *fakeRedirects) addLog(path string, hits int) *redirect.NotFoundEntry {
	e := &redirect.NotFoundEntry{ID: uuid.New(), Path: path, HitCount: hits, FirstSeenAt: time.Now(), LastSeenAt: time.Now()}
	f.log[e.ID] = e
	return e
}

func (f *fakeRedirects) ListRedirects(_ context.Context, p redirect.RedirectListParams) ([]*redirect.Redirect, int, error) {
	var out []*redirect.Redirect
	for _, rd := range f.rules {
		if p.Search == "" || strings.Contains(rd.SourcePath+" "+rd.TargetURL, p.Search) {
			out = append(out, rd)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourcePath < out[j].SourcePath })
	return out, len(out), nil
}
func (f *fakeRedirects) GetRedirect(_ context.Context, id uuid.UUID) (*redirect.Redirect, error) {
	if rd, ok := f.rules[id]; ok {
		c := *rd
		return &c, nil
	}
	return nil, redirect.ErrRedirectNotFound
}
func (f *fakeRedirects) CreateRedirect(_ context.Context, in *redirect.CreateRedirectInput) (*redirect.Redirect, error) {
	if !redirect.ValidStatusCode(in.StatusCode) {
		return nil, redirect.ErrInvalidStatusCode
	}
	if err := redirect.ValidatePatternRule(in.SourcePath, in.TargetURL); err != nil {
		return nil, err
	}
	for _, rd := range f.rules {
		if rd.SourcePath == in.SourcePath {
			return nil, redirect.ErrDuplicateSourcePath
		}
	}
	rd := f.add(in.SourcePath, in.TargetURL, in.StatusCode)
	if in.NotFoundLogID != nil {
		f.linked[*in.NotFoundLogID] = rd.ID
		if e, ok := f.log[*in.NotFoundLogID]; ok {
			e.RedirectID = &rd.ID
		}
	}
	return rd, nil
}
func (f *fakeRedirects) UpdateRedirect(_ context.Context, id uuid.UUID, in *redirect.UpdateRedirectInput) (*redirect.Redirect, error) {
	rd, ok := f.rules[id]
	if !ok {
		return nil, redirect.ErrRedirectNotFound
	}
	src, tgt := rd.SourcePath, rd.TargetURL
	if in.SourcePath != nil {
		src = *in.SourcePath
	}
	if in.TargetURL != nil {
		tgt = *in.TargetURL
	}
	if err := redirect.ValidatePatternRule(src, tgt); err != nil {
		return nil, err
	}
	rd.SourcePath, rd.TargetURL = src, tgt
	if in.StatusCode != nil {
		rd.StatusCode = *in.StatusCode
	}
	if in.IsActive != nil {
		rd.IsActive = *in.IsActive
	}
	return rd, nil
}
func (f *fakeRedirects) DeleteRedirect(_ context.Context, id uuid.UUID) error {
	if _, ok := f.rules[id]; !ok {
		return redirect.ErrRedirectNotFound
	}
	delete(f.rules, id)
	return nil
}

// List404Entries orders by hits (then path) like the store and honours PerPage
// (default 50), so tests see what production would.
func (f *fakeRedirects) List404Entries(_ context.Context, p redirect.NotFoundListParams) ([]*redirect.NotFoundEntry, int, error) {
	out := f.sorted()
	total := len(out)
	if p.PerPage <= 0 {
		p.PerPage = 50
	}
	if p.Page <= 0 {
		p.Page = 1
	}
	lo := min((p.Page-1)*p.PerPage, total)
	return out[lo:min(lo+p.PerPage, total)], total, nil
}

// ListRecent404Entries mirrors the store: open entries seen since, by hits, at most limit.
func (f *fakeRedirects) ListRecent404Entries(_ context.Context, since time.Time, limit int) ([]*redirect.NotFoundEntry, error) {
	var out []*redirect.NotFoundEntry
	for _, e := range f.sorted() {
		if !e.Ignored && e.RedirectID == nil && !e.LastSeenAt.Before(since) {
			out = append(out, e)
		}
	}
	return out[:min(limit, len(out))], nil
}

func (f *fakeRedirects) sorted() []*redirect.NotFoundEntry {
	var out []*redirect.NotFoundEntry
	for _, e := range f.log {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HitCount != out[j].HitCount {
			return out[i].HitCount > out[j].HitCount
		}
		return out[i].Path < out[j].Path
	})
	return out
}
func (f *fakeRedirects) Ignore404Entry(_ context.Context, id uuid.UUID) error {
	if e, ok := f.log[id]; ok {
		e.Ignored = true
		return nil
	}
	return redirect.ErrNotFoundEntryNotFound
}
func (f *fakeRedirects) Delete404Entry(_ context.Context, id uuid.UUID) error {
	if _, ok := f.log[id]; !ok {
		return redirect.ErrNotFoundEntryNotFound
	}
	delete(f.log, id)
	return nil
}

type fakeExplainer struct{ answers map[string]URLExplanation }

func (f fakeExplainer) ExplainURL(_ context.Context, path string) (URLExplanation, error) {
	if a, ok := f.answers[path]; ok {
		return a, nil
	}
	return URLExplanation{Redirects: &redirect.Answer{Path: path, Normalised: path, Stages: []redirect.Stage{{Provider: "db_rules", Priority: 100}}}}, nil
}

func newRedirectRig(t *testing.T) (*rig, *fakeRedirects) {
	t.Helper()
	fr := newFakeRedirects()
	ex := fakeExplainer{answers: map[string]URLExplanation{
		"/old": {Redirects: &redirect.Answer{Path: "/old", Normalised: "/old", MatchedBy: "db_rules",
			Redirect: &redirect.Match{Target: "/new", Status: 301, Rule: "exact"},
			Stages:   []redirect.Stage{{Provider: "db_rules", Priority: 100, Matched: true, Target: "/new", Status: 301, Rule: "exact", Variant: "exact"}}}},
		"/about": {Redirects: &redirect.Answer{Path: "/about", Normalised: "/about", Stages: []redirect.Stage{{Provider: "db_rules", Priority: 100}, {Provider: "wp_patterns", Priority: 200}}},
			Page: PageAnswer{Kind: "entity", Name: "About us", Schema: "page", Path: "/about"}},
	}}
	g := newRig(t, func(d *Deps) { d.Growth.Redirects, d.Growth.Explain = fr, ex })
	return g, fr
}

func TestRedirectListSearchAndA11y(t *testing.T) {
	t.Parallel()

	g, fr := newRedirectRig(t)
	fr.add("/old", "/new", 301).HitCount = 4
	fr.add("/blog/**", "/articles/$1", 302)
	body := g.do("GET", "/admin/settings/redirects", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"/old", "/blog/** (wildcard)", "301 Permanent", "302 Temporary", "Redirect tools", "301 and 308 are permanent"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(body, `class="hm-cellbar"`); n != 1 {
		t.Errorf("%d hit bars, want 1 (none for the unused redirect)", n)
	}
	if strings.Index(body, "hm-redirect-test") > strings.Index(body, "<table") {
		t.Error("the test box belongs above the table")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if body := g.do("GET", "/admin/settings/redirects?q=blog", "owner-token", nil, nil).Body.String(); strings.Contains(body, ">/old<") {
		t.Error("search should narrow the list")
	}
	// /admin/settings/redirects is the real list now, for every signed-in role.
	for _, tok := range []string{"owner-token", "editor-token"} {
		if rec := g.do("GET", "/admin/settings/redirects", tok, nil, nil); rec.Code != 200 {
			t.Errorf("%s: settings redirects list %d", tok, rec.Code)
		}
	}
	if rec := g.do("POST", "/admin/redirects/404/5/ignore", "owner-token", nil, nil); rec.Code != 308 || rec.Header().Get("Location") != "/admin/settings/redirects/404/5/ignore" {
		t.Errorf("old 404 ignore URL: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRedirectCreateEditToggleDelete(t *testing.T) {
	t.Parallel()

	g, fr := newRedirectRig(t)
	newPage := g.do("GET", "/admin/settings/redirects/new?source=/gone", "owner-token", nil, nil).Body.String()
	if !strings.Contains(newPage, `value="/gone"`) {
		t.Error("source should be prefilled")
	}
	if issues := a11y.Check(newPage, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("new a11y: %v", issues)
	}
	// the v1-09 wildcard validation is enforced through the service
	for name, f := range map[string]url.Values{
		"mid-segment wildcard": {"source": {"/a/b*c"}, "target": {"/x"}, "code": {"301"}},
		"catch-all":            {"source": {"/**"}, "target": {"/x"}, "code": {"301"}},
		"self-matching target": {"source": {"/blog/*"}, "target": {"/blog/$1"}, "code": {"301"}},
		"bad code":             {"source": {"/a"}, "target": {"/b"}, "code": {"200"}},
		"no leading slash":     {"source": {"a"}, "target": {"/b"}, "code": {"301"}},
		"open redirect target": {"source": {"/a"}, "target": {"//evil.example"}, "code": {"301"}},
		"javascript target":    {"source": {"/a"}, "target": {"javascript:alert(1)"}, "code": {"301"}},
	} {
		rec := g.do("POST", "/admin/settings/redirects", "owner-token", f, nil)
		if rec.Code != 422 || len(fr.rules) != 0 {
			t.Errorf("%s: want 422 and nothing stored, got %d (%d rules)", name, rec.Code, len(fr.rules))
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", name, issues)
		}
	}
	rec := g.do("POST", "/admin/settings/redirects", "owner-token", url.Values{"source": {"/legacy/**"}, "target": {"/archive/$1"}, "code": {"308"}}, nil)
	if rec.Code != 303 || len(fr.rules) != 1 {
		t.Fatalf("create wildcard: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/redirects", "owner-token", url.Values{"source": {"/legacy/**"}, "target": {"/x/$1"}, "code": {"301"}}, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("duplicate: %d", rec.Code)
	}
	var id uuid.UUID
	for k := range fr.rules {
		id = k
	}
	edit := g.do("GET", "/admin/settings/redirects/"+id.String()+"/edit", "owner-token", nil, nil)
	if edit.Code != 200 || !strings.Contains(edit.Body.String(), `value="/archive/$1"`) {
		t.Fatalf("edit: %d", edit.Code)
	}
	if issues := a11y.Check(edit.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("edit a11y: %v", issues)
	}
	g.do("POST", "/admin/settings/redirects/"+id.String()+"/edit", "owner-token", url.Values{"source": {"/legacy/**"}, "target": {"/library/$1"}, "code": {"301"}}, nil)
	if got := fr.rules[id]; got.TargetURL != "/library/$1" || got.StatusCode != 301 || got.IsActive {
		t.Errorf("edit: %+v (unticked 'on' turns it off)", got)
	}
	g.do("POST", "/admin/settings/redirects/"+id.String()+"/toggle", "owner-token", url.Values{}, nil)
	if !fr.rules[id].IsActive {
		t.Error("toggle should turn it on")
	}
	if g.do("GET", "/admin/settings/redirects/"+id.String()+"/delete", "owner-token", nil, nil).Code != 200 || len(fr.rules) != 1 {
		t.Fatal("confirmation page must not delete")
	}
	if rec := g.do("POST", "/admin/settings/redirects/"+id.String()+"/delete", "owner-token", url.Values{}, nil); rec.Code != 303 || len(fr.rules) != 0 {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestRedirectBulk(t *testing.T) {
	t.Parallel()

	g, fr := newRedirectRig(t)
	a, b, c := fr.add("/a", "/x", 301), fr.add("/b", "/y", 301), fr.add("/c", "/z", 301)
	rec := g.do("POST", "/admin/settings/redirects/bulk", "owner-token", url.Values{"action": {"off"}, "id": {a.ID.String(), b.ID.String()}}, nil)
	if rec.Code != 303 || fr.rules[a.ID].IsActive || fr.rules[b.ID].IsActive || !fr.rules[c.ID].IsActive {
		t.Fatalf("turn off: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/settings/redirects/bulk", "owner-token", url.Values{"action": {"delete"}, "id": {a.ID.String(), b.ID.String()}}, nil)
	if rec.Code != 200 || len(fr.rules) != 3 || !strings.Contains(rec.Body.String(), "Delete 2 redirects?") {
		t.Fatalf("delete must ask first: %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `hm-pageheader`) || !strings.Contains(body, "hm-callout--danger") {
		t.Errorf("confirm page needs the page header and a danger callout: %.400s", body)
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("confirm a11y: %v", issues)
	}
	rec = g.do("POST", "/admin/settings/redirects/bulk", "owner-token", url.Values{"action": {"delete"}, "confirm": {"1"}, "id": {a.ID.String(), b.ID.String()}}, nil)
	if rec.Code != 303 || len(fr.rules) != 1 {
		t.Fatalf("confirmed delete: %d (%d left)", rec.Code, len(fr.rules))
	}
}

func Test404LogToRedirect(t *testing.T) {
	t.Parallel()

	g, fr := newRedirectRig(t)
	e := fr.addLog("/missing", 7)
	fr.addLog("/other", 1)
	body := g.do("GET", "/admin/settings/redirects/404", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "/missing") || !strings.Contains(body, "Create redirect") || !strings.Contains(body, "source=%2Fmissing") {
		t.Fatalf("404 log: %s", body)
	}
	top := ""
	if i := strings.Index(body, `class="hm-barlist"`); i >= 0 {
		top = body[i:]
		if j := strings.Index(top, "</ul>"); j >= 0 {
			top = top[:j]
		}
	}
	if !strings.Contains(top, `href="/admin/settings/redirects/new?log=`) || !strings.Contains(top, ">/missing</a>") {
		t.Errorf("the most-requested list must link each address to the redirect form, named by its path: %q", top)
	}
	for _, u := range []string{"?page=2", "?q=x"} {
		if strings.Contains(g.do("GET", "/admin/settings/redirects/404"+u, "owner-token", nil, nil).Body.String(), "hm-barlist") {
			t.Errorf("404 log %s must not show the top list", u)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	form := g.do("GET", "/admin/settings/redirects/new?source=%2Fmissing&log="+e.ID.String(), "owner-token", nil, nil).Body.String()
	if !strings.Contains(form, `name="log" value="`+e.ID.String()+`"`) {
		t.Fatal("the log id must ride along")
	}
	rec := g.do("POST", "/admin/settings/redirects", "owner-token", url.Values{"source": {"/missing"}, "target": {"/found"}, "code": {"301"}, "log": {e.ID.String()}}, nil)
	if rec.Code != 303 || fr.linked[e.ID] == uuid.Nil {
		t.Fatalf("redirect from 404: %d linked=%v", rec.Code, fr.linked)
	}
	if body := g.do("GET", "/admin/settings/redirects/404", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Has a redirect") {
		t.Error("the entry should now say it has a redirect")
	}
	other := fr.addLog("/ignore-me", 1)
	g.do("POST", "/admin/settings/redirects/404/"+other.ID.String()+"/ignore", "owner-token", url.Values{}, nil)
	if !fr.log[other.ID].Ignored {
		t.Error("ignore")
	}
	if g.do("GET", "/admin/settings/redirects/404/"+other.ID.String()+"/delete", "owner-token", nil, nil).Code != 200 || len(fr.log) != 3 {
		t.Fatal("confirmation page must not delete")
	}
	g.do("POST", "/admin/settings/redirects/404/"+other.ID.String()+"/delete", "owner-token", url.Values{}, nil)
	if len(fr.log) != 2 {
		t.Error("remove")
	}
}

func TestRedirectExplain(t *testing.T) {
	t.Parallel()

	g, _ := newRedirectRig(t)
	empty := g.do("GET", "/admin/settings/redirects/explain", "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(empty, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("empty a11y: %v", issues)
	}
	for path, want := range map[string][]string{
		"/old":     {"hm-callout--info", "Redirected", "redirected to /new with status 301", "Your redirect rules", "Matched"},
		"/about":   {"hm-callout--success", "Page found", "shows the page About us", "No match", "Built-in WordPress addresses"},
		"/nothing": {"hm-callout--warning", "Nothing answers this address", "visitors get a 404", "Create a redirect for it", "source=%2Fnothing"},
	} {
		rec := g.do("GET", "/admin/settings/redirects/explain?path="+url.QueryEscape(path), "owner-token", nil, nil)
		body := rec.Body.String()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", path, w)
			}
		}
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", path, issues)
		}
	}
	if rec := g.do("GET", "/admin/settings/redirects/explain?path=nope", "owner-token", nil, nil); rec.Code != 422 {
		t.Errorf("relative path: %d", rec.Code)
	}
}

func TestRedirectCreateGoesToList(t *testing.T) {
	t.Parallel()

	g, fr := newRedirectRig(t)
	fr.add("/old", "/new", 301)
	rec := g.do("POST", "/admin/settings/redirects", "owner-token", url.Values{"source": {"/fresh"}, "target": {"/found"}, "code": {"301"}}, nil)
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, "/admin/settings/redirects?created=") {
		t.Fatalf("create: %d Location=%q", rec.Code, loc)
	}
	body := g.do("GET", loc, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "hm-row--new") {
		t.Error("the new row is not marked")
	}
	if strings.Index(body, "<table") < strings.Index(body, "Test a URL") {
		t.Error("the Test a URL card must come before the table")
	}
	for _, want := range []string{`hx-select="#explain-result"`, `id="test-answer"`, `action="/admin/settings/redirects/explain"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Test a URL form lacks %s", want)
		}
	}
	if on, off, del := strings.Index(body, "Turn on"), strings.Index(body, "Turn off"), strings.LastIndex(body, "Delete"); !(on < off && off < del) {
		t.Errorf("bulk order must be Turn on, Turn off, Delete (%d %d %d)", on, off, del)
	}
	for _, want := range []string{`data-hm-open="new-redirect"`, `id="new-redirect"`, `form="new-redirect-form"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "hm-tabs--segmented") {
		t.Error("redirect tools tabs must not be segmented")
	}
	if page := g.do("GET", "/admin/settings/redirects/new?source=%2Fx", "owner-token", nil, nil).Body.String(); !strings.Contains(page, `value="/x"`) {
		t.Error("the /new page must still prefill")
	}
}

func TestRedirectRefusesSelfTarget(t *testing.T) {
	t.Parallel()

	g, fr := newRedirectRig(t)
	rec := g.do("POST", "/admin/settings/redirects", "owner-token", url.Values{"source": {"/same"}, "target": {"/same"}, "code": {"301"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "A redirect cannot point to itself") || len(fr.rules) != 0 {
		t.Errorf("self redirect: %d rules=%d", rec.Code, len(fr.rules))
	}
}

func TestRedirectsToItself(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		source, target string
		want           bool
	}{
		{"/same", "/same", true},
		{"/same", "/same/", true},
		{"/same/", "/same", true},
		{"/same", "//same", true},
		{"/same", "https://example.test/same/", true},
		{"/same", "https://other.test/same", false},
		{"/same", "/same?x=1", true},
		{"/same", "/same#top", true},
		{"/same", "https://example.test/same?x=1", true},
		{"/same", "/other?x=/same", false},
		{"/same", "/other", false},
		{"/blog/**", "/blog/$1", false},
	} {
		if got := redirectsToItself(c.source, c.target, "example.test"); got != c.want {
			t.Errorf("redirectsToItself(%q, %q) = %v, want %v", c.source, c.target, got, c.want)
		}
	}
}
