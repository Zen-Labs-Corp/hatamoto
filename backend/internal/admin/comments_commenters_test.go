package admin

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

// cmtrSaveFake records the entries SaveBlocklist received, which the shared
// fake does not keep.
type cmtrSaveFake struct {
	*fakeComments
	mu      sync.Mutex
	kind    string
	entries []string
	saved   bool
}

func (f *cmtrSaveFake) SaveBlocklist(ctx context.Context, kind string, entries []string, actor string) error {
	f.mu.Lock()
	f.kind, f.entries, f.saved = kind, entries, true
	f.mu.Unlock()
	return f.fakeComments.SaveBlocklist(ctx, kind, entries, actor)
}

func cmtrBlocklistsFake() *fakeComments {
	return &fakeComments{
		supports: map[string]bool{"blocklists": true},
		blocklists: []Blocklist{
			{Kind: "email_domain", Entries: []string{"spam.example"}, Count: 1},
			{Kind: "link_domain", Count: 0},
			{Kind: "body_hash", Entries: []string{"deadbeefdeadbeef"}, Count: 12},
			{Kind: "ip_hash", Entries: []string{"cafebabecafebabe"}, Count: 1},
			{Kind: "net_hash", Count: 0},
		},
	}
}

func TestCommentBlocklistsPage(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cmtrBlocklistsFake())
	for _, token := range []string{"owner-token", "editor-token"} {
		rec := p.do("GET", "/admin/settings/comments/blocklists", token, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s GET = %d", token, rec.Code)
		}
		body := rec.Body.String()
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("a11y: %v", issues)
		}
		for _, want := range []string{"Email domains", "spam.example", "12 entries", "1 entry", "Clear"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: body lacks %q", token, want)
			}
		}
		for _, bad := range []string{"deadbeef", "cafebabe"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: body shows a hash %q", token, bad)
			}
		}
	}
}

func TestCommentBlocklistsSave(t *testing.T) {
	t.Parallel()

	svc := &cmtrSaveFake{fakeComments: cmtrBlocklistsFake()}
	p := newCommentsRig(t, nil)
	p.a.deps.Comments = svc
	form := url.Values{"kind": {"email_domain"}, "entries": {" @Spam.Example \nspam.example\r\nhttps://Other.test/path\n\n"}}
	rec := p.do("POST", "/admin/settings/comments/blocklists", "editor-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/comments/blocklists" {
		t.Fatalf("POST = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.kind != "email_domain" || strings.Join(svc.entries, ",") != "spam.example,other.test" {
		t.Errorf("saved %q %q", svc.kind, svc.entries)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.blocklist" || p.events[0].Target != "email_domain" || p.events[0].Detail["count"] != 2 {
		t.Errorf("audit = %+v", p.events)
	}

	rec = p.do("POST", "/admin/settings/comments/blocklists", "owner-token", url.Values{"kind": {"ip_hash"}, "clear": {"1"}}, nil)
	if rec.Code != 303 || svc.kind != "ip_hash" || svc.entries != nil {
		t.Errorf("clear = %d kind %q entries %q", rec.Code, svc.kind, svc.entries)
	}
}

func TestCommentBlocklistsInvalid(t *testing.T) {
	t.Parallel()

	svc := &cmtrSaveFake{fakeComments: cmtrBlocklistsFake()}
	p := newCommentsRig(t, nil)
	p.a.deps.Comments = svc
	form := url.Values{"kind": {"link_domain"}, "entries": {"ok.example\nnot a domain\nnodot"}}
	rec := p.do("POST", "/admin/settings/comments/blocklists", "owner-token", form, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Line 2 is not a domain") || !strings.Contains(body, "Line 3 is not a domain") {
		t.Fatalf("POST = %d, body lacks line errors", rec.Code)
	}
	if !strings.Contains(body, "not a domain\nnodot") {
		t.Error("submitted text is not shown again")
	}
	if svc.saved || len(p.events) != 0 {
		t.Errorf("a refused save reached the service: saved=%v events=%d", svc.saved, len(p.events))
	}
}

// Learned entries the shape check refuses (IDN, single label) must not block
// saving the card; a new line of that shape still does.
func TestCommentBlocklistsKeepLearnedEntries(t *testing.T) {
	t.Parallel()

	fake := cmtrBlocklistsFake()
	fake.blocklists[1] = Blocklist{Kind: "link_domain", Entries: []string{"пример.рф", "localhost"}, Count: 2}
	svc := &cmtrSaveFake{fakeComments: fake}
	p := newCommentsRig(t, nil)
	p.a.deps.Comments = svc
	rec := p.do("POST", "/admin/settings/comments/blocklists", "owner-token", url.Values{"kind": {"link_domain"}, "entries": {"пример.рф\nlocalhost\nnew.example"}}, nil)
	if rec.Code != 303 || !svc.saved || len(svc.entries) != 3 {
		t.Fatalf("POST = %d saved=%v entries=%v, want the learned lines kept", rec.Code, svc.saved, svc.entries)
	}
	rec = p.do("POST", "/admin/settings/comments/blocklists", "owner-token", url.Values{"kind": {"link_domain"}, "entries": {"пример.рф\nбыл.рф"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Line 2 is not a domain") {
		t.Errorf("a new IDN line = %d, want 422 on line 2", rec.Code)
	}
}

func TestCommentBlocklistsUnsupported(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	rec := p.do("GET", "/admin/settings/comments/blocklists", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Blocklists are not available yet.") {
		t.Fatalf("GET = %d", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	rec = p.do("POST", "/admin/settings/comments/blocklists", "owner-token", url.Values{"kind": {"email_domain"}, "entries": {"a.example"}}, nil)
	if rec.Code != 303 || len(svc.calls) != 0 || len(p.events) != 0 {
		t.Errorf("POST = %d calls=%v events=%d", rec.Code, svc.calls, len(p.events))
	}
}

func TestCommentBlocklistsBadKind(t *testing.T) {
	t.Parallel()

	svc := cmtrBlocklistsFake()
	p := newCommentsRig(t, svc)
	for _, form := range []url.Values{
		{"kind": {"nope"}, "entries": {"a.example"}},
		{"kind": {"ip_hash"}, "entries": {"a.example"}}, // learned kinds can only be cleared
		{"entries": {"a.example"}},
	} {
		if rec := p.do("POST", "/admin/settings/comments/blocklists", "owner-token", form, nil); rec.Code != 400 {
			t.Errorf("POST %v = %d, want 400", form, rec.Code)
		}
	}
	if len(svc.calls) != 0 || len(p.events) != 0 {
		t.Errorf("a refused save reached the service: %v", svc.calls)
	}
}

// The a11y tests below cover these pages (TestEveryPageTemplateIsExercised).
func init() {
	areaPages["commentscommenters"] = []string{"settings/comments-blocklists", "settings/comments-commenters", "settings/comments-erase", "settings/comments-commenter-confirm"}
}

const cmtrTestID = "5d1c7d3e-0a53-4d52-8d0e-6a3c6f6b8a11"

func cmtrCommentersFake() *fakeComments {
	return &fakeComments{commenters: []CommenterRow{
		{ID: cmtrTestID, Name: "Alice", Trust: "new", ApprovedCount: 2, LastSeen: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)},
		{ID: "6e2d8e4f-1b64-4e63-9e1f-7b4d7a7c9b22", Trust: "blocked"},
	}}
}

func TestCommentersPage(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cmtrCommentersFake())
	for _, token := range []string{"owner-token", "editor-token"} {
		rec := p.do("GET", "/admin/settings/comments/commenters", token, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s GET = %d", token, rec.Code)
		}
		body := rec.Body.String()
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("a11y: %v", issues)
		}
		table := body[strings.Index(body, "<table"):strings.Index(body, "</table>")]
		for _, want := range []string{"Alice", "Unnamed", "2 Nov 2026", "Vouch", "Block", "Sign out all devices", "commenter=" + cmtrTestID} {
			if !strings.Contains(table, want) {
				t.Errorf("%s: table lacks %q", token, want)
			}
		}
		for _, bad := range []string{"@", "http", "Website"} {
			if strings.Contains(table, bad) {
				t.Errorf("%s: table contains %q", token, bad)
			}
		}
	}
}

func TestCommentersHasMore(t *testing.T) {
	t.Parallel()

	svc := cmtrCommentersFake()
	svc.hasMore = true
	p := newCommentsRig(t, svc)
	body := p.do("GET", "/admin/settings/comments/commenters?q=al", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `rel="next"`) || !strings.Contains(body, "page=2") || strings.Contains(body, `rel="prev"`) {
		t.Errorf("page 1 pager wrong")
	}
	body = p.do("GET", "/admin/settings/comments/commenters?page=2", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `rel="prev"`) {
		t.Errorf("page 2 lacks the previous link")
	}
}

func TestCommenterVouch(t *testing.T) {
	t.Parallel()

	cmtrActionTest(t, "vouch", "editor-token", "vouch")
}

func TestCommenterBlock(t *testing.T) {
	t.Parallel()

	cmtrActionTest(t, "block", "owner-token", "block")
}

func TestCommenterSignout(t *testing.T) {
	t.Parallel()

	cmtrActionTest(t, "signout", "editor-token", "signout")
}

// cmtrActionTest posts one commenter action and checks the redirect, the one
// recorded service call and the audit event.
func cmtrActionTest(t *testing.T, action, token, call string) {
	svc := cmtrCommentersFake()
	p := newCommentsRig(t, svc)
	path := "/admin/settings/comments/commenters/" + cmtrTestID + "/" + action
	rec := p.do("POST", path, token, url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/comments/commenters" {
		t.Fatalf("POST = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.calls) != 1 || svc.calls[0] != call || len(svc.lastIDs) != 1 || svc.lastIDs[0] != cmtrTestID {
		t.Errorf("calls = %v ids = %v, want exactly %s", svc.calls, svc.lastIDs, call)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.commenter."+action || p.events[0].Target != cmtrTestID {
		t.Errorf("audit = %+v", p.events)
	}
	// The back link stays inside the commenters area.
	rec = p.do("POST", path+"?back=%2Fadmin%2Fsettings%2Fcomments%2Fcommenters%3Fq%3Dal", token, url.Values{}, nil)
	if got := rec.Header().Get("Location"); got != "/admin/settings/comments/commenters?q=al" {
		t.Errorf("back = %q", got)
	}
	rec = p.do("POST", path+"?back=https%3A%2F%2Felsewhere.test", token, url.Values{}, nil)
	if got := rec.Header().Get("Location"); got != "/admin/settings/comments/commenters" {
		t.Errorf("foreign back = %q", got)
	}
}

func TestCommenterSignoutNotFound(t *testing.T) {
	t.Parallel()

	svc := cmtrCommentersFake()
	svc.err = ErrCommentNotFound
	p := newCommentsRig(t, svc)
	rec := p.do("POST", "/admin/settings/comments/commenters/"+cmtrTestID+"/signout", "owner-token", url.Values{}, nil)
	if rec.Code != 404 || len(p.events) != 0 {
		t.Errorf("POST = %d events=%d", rec.Code, len(p.events))
	}
}

func TestCommenterBadAction(t *testing.T) {
	t.Parallel()

	svc := cmtrCommentersFake()
	p := newCommentsRig(t, svc)
	for _, path := range []string{"/" + cmtrTestID + "/promote", "/not-a-uuid/vouch"} {
		if rec := p.do("POST", "/admin/settings/comments/commenters"+path, "owner-token", url.Values{}, nil); rec.Code != 404 {
			t.Errorf("POST %s = %d, want 404", path, rec.Code)
		}
	}
	if len(svc.calls) != 0 || len(p.events) != 0 {
		t.Errorf("a refused action reached the service: %v", svc.calls)
	}
}

func TestCommenterActionNoCSRF(t *testing.T) {
	t.Parallel()

	svc := cmtrCommentersFake()
	p := newCommentsRig(t, svc)
	rec := p.do("POST", "/admin/settings/comments/commenters/"+cmtrTestID+"/block", "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""})
	if rec.Code != 403 || len(svc.calls) != 0 {
		t.Errorf("POST = %d calls=%v", rec.Code, svc.calls)
	}
}

func cmtrPrivacyFake() *fakeComments {
	svc := cmtrCommentersFake()
	svc.supports = map[string]bool{"privacy": true}
	return svc
}

func TestCommenterExport(t *testing.T) {
	t.Parallel()

	svc := cmtrPrivacyFake()
	p := newCommentsRig(t, svc)
	path := "/admin/settings/comments/commenters/" + cmtrTestID + "/export"
	rec := p.do("GET", path, "editor-token", nil, nil)
	if rec.Code != 403 || len(svc.calls) != 0 {
		t.Fatalf("editor GET = %d calls=%v", rec.Code, svc.calls)
	}
	rec = p.do("GET", path, "owner-token", nil, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), p.a.sudoPath()) || len(svc.calls) != 0 {
		t.Fatalf("no sudo: %d %s calls=%v", rec.Code, rec.Header().Get("Location"), svc.calls)
	}
	grantSudo(p.rig)
	rec = p.do("GET", path, "owner-token", nil, nil)
	h := rec.Header()
	if rec.Code != 200 || h.Get("Content-Type") != "application/json" || h.Get("Cache-Control") != "no-store" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Disposition") != `attachment; filename="commenter-`+cmtrTestID+`.json"` {
		t.Errorf("GET = %d headers %v", rec.Code, h)
	}
	if len(svc.calls) != 1 || svc.calls[0] != "export" || len(p.events) != 1 || p.events[0].Action != "comments.commenter.export" {
		t.Errorf("calls=%v audit=%+v", svc.calls, p.events)
	}

	// Unsupported: the feature flag or the service itself refuses.
	off := cmtrCommentersFake()
	p2 := newCommentsRig(t, off)
	grantSudo(p2.rig)
	if rec := p2.do("GET", path, "owner-token", nil, nil); rec.Code != 404 || len(off.calls) != 0 {
		t.Errorf("unsupported GET = %d", rec.Code)
	}
	svc.err = ErrCommentsUnsupported
	if rec := p.do("GET", path, "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("service-unsupported GET = %d", rec.Code)
	}
}

func TestCommenterErase(t *testing.T) {
	t.Parallel()

	svc := cmtrPrivacyFake()
	p := newCommentsRig(t, svc)
	path := "/admin/settings/comments/commenters/" + cmtrTestID + "/erase"
	rec := p.do("POST", path, "owner-token", url.Values{"confirm_phrase": {"erase"}}, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), p.a.sudoPath()) || len(svc.calls) != 0 {
		t.Fatalf("no sudo: %d %s calls=%v", rec.Code, rec.Header().Get("Location"), svc.calls)
	}
	grantSudo(p.rig)
	rec = p.do("POST", path, "owner-token", url.Values{"confirm_phrase": {"nope"}}, nil)
	if rec.Code != 422 || len(svc.calls) != 0 || len(p.events) != 0 {
		t.Fatalf("wrong phrase = %d calls=%v events=%d", rec.Code, svc.calls, len(p.events))
	}
	rec = p.do("POST", path, "owner-token", url.Values{"confirm_phrase": {"erase"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/comments/commenters" {
		t.Fatalf("POST = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.calls) != 1 || svc.calls[0] != "erase" || svc.lastIDs[0] != cmtrTestID {
		t.Errorf("calls = %v", svc.calls)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.commenter.erase" || p.events[0].Detail["deleted"] != 1 || p.events[0].Detail["anonymised"] != 0 || len(p.events[0].Detail) != 2 {
		t.Errorf("audit = %+v", p.events)
	}
}

func TestCommenterPrivacyHidden(t *testing.T) {
	t.Parallel()

	for name, rig := range map[string]struct {
		svc   *fakeComments
		token string
	}{"unsupported": {cmtrCommentersFake(), "owner-token"}, "editor": {cmtrPrivacyFake(), "editor-token"}} {
		p := newCommentsRig(t, rig.svc)
		body := p.do("GET", "/admin/settings/comments/commenters", rig.token, nil, nil).Body.String()
		if strings.Contains(body, cmtrTestID+"/export") || strings.Contains(body, cmtrTestID+"/erase") {
			t.Errorf("%s: the list offers export or erase", name)
		}
	}
	p := newCommentsRig(t, cmtrPrivacyFake())
	body := p.do("GET", "/admin/settings/comments/commenters", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "/"+cmtrTestID+"/export") || !strings.Contains(body, "/"+cmtrTestID+"/erase") {
		t.Error("an owner with privacy support does not see export and erase")
	}
}

func TestCommenterErasePage(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cmtrPrivacyFake())
	grantSudo(p.rig)
	rec := p.do("GET", "/admin/settings/comments/commenters/"+cmtrTestID+"/erase", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Comments nobody replied to are deleted.") {
		t.Fatalf("GET = %d", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	rec = p.do("POST", "/admin/settings/comments/commenters/"+cmtrTestID+"/erase", "owner-token", url.Values{"confirm_phrase": {"x"}}, nil)
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); rec.Code != 422 || len(issues) > 0 {
		t.Errorf("422 page = %d a11y %v", rec.Code, issues)
	}
}

func TestCommenterConfirmPages(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cmtrCommentersFake())
	base := "/admin/settings/comments/commenters/" + cmtrTestID
	for _, action := range []string{"block", "signout"} {
		rec := p.do("GET", base+"/"+action+"?back=%2Fadmin%2Fsettings%2Fcomments%2Fcommenters%3Fq%3Dal", "editor-token", nil, nil)
		body := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(body, `action="`+base+"/"+action+`"`) || !strings.Contains(body, `value="/admin/settings/comments/commenters?q=al"`) {
			t.Errorf("GET %s = %d, want a form posting to the action", action, rec.Code)
		}
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", action, issues)
		}
	}
	for _, path := range []string{base + "/vouch", base + "/nope", "/admin/settings/comments/commenters/not-a-uuid/block"} {
		if rec := p.do("GET", path, "editor-token", nil, nil); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}
