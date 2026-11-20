package admin

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"

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
	if rec.Code != 404 || len(svc.calls) != 0 || len(p.events) != 0 {
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
