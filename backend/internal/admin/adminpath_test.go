package admin

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"

	"github.com/google/uuid"
)

func TestNormalizeAdminPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", "/admin", true},
		{"/admin", "/admin", true},
		{"/x-test", "/x-test", true},
		{"/a", "/a", true},
		{"/" + strings.Repeat("a", 64), "/" + strings.Repeat("a", 64), true},
		{"/" + strings.Repeat("a", 65), "", false},
		{"x-test", "", false},
		{"/x-test/", "", false},
		{"/", "", false},
		{"//evil.example", "", false},
		{"/\\evil", "", false},
		{"/a b", "", false},
		{"/{x}", "", false},
		{"/A", "", false},
		{"/a/b", "", false},
		{"/-x", "", false},
		{"/x-", "", false},
		{"/a?b", "", false},
		{"/a#b", "", false},
		{"/\u00fc", "", false},
	} {
		got, err := normalizeAdminPath(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("normalizeAdminPath(%q) = %q, %v; want %q ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestAdminURL(t *testing.T) {
	t.Parallel()

	a := &Admin{adminPath: "/x-test"}
	for _, tc := range []struct {
		parts []string
		want  string
	}{
		{nil, "/x-test"},
		{[]string{"settings", "forms"}, "/x-test/settings/forms"},
		{[]string{"/entities/", "42", "edit"}, "/x-test/entities/42/edit"},
		{[]string{"entities", "?status=draft"}, "/x-test/entities?status=draft"},
		{[]string{"analytics", "#w-cr-list"}, "/x-test/analytics#w-cr-list"},
	} {
		if got := a.url(tc.parts...); got != tc.want {
			t.Errorf("url(%q) = %q, want %q", tc.parts, got, tc.want)
		}
	}
}

func TestNewRejectsBadAdminPath(t *testing.T) {
	t.Parallel()

	for _, p := range []string{"x-test", "/x-test/", "/", "//evil.example", "/{x}"} {
		if a, err := New(Deps{AdminPath: p}); err == nil {
			t.Errorf("AdminPath %q accepted (%v)", p, a.AdminPath())
		}
	}
	if a, err := New(Deps{}); err != nil || a.AdminPath() != "/admin" {
		t.Errorf("default: %v %v", a, err)
	}
}

func TestNewDemoNeedsDefaultAdminPath(t *testing.T) {
	t.Parallel()

	if a, err := New(Deps{AdminPath: "/x-test", Demo: config.DemoConfig{Enabled: true}}); err == nil {
		t.Errorf("demo with /x-test accepted (%v)", a.AdminPath())
	}
	for _, p := range []string{"", "/admin"} {
		if _, err := New(Deps{AdminPath: p, Demo: config.DemoConfig{Enabled: true}}); err != nil {
			t.Errorf("demo with AdminPath %q: %v", p, err)
		}
	}
	if _, err := New(Deps{AdminPath: "/x-test"}); err != nil {
		t.Errorf("non-demo /x-test: %v", err)
	}
}

// attrRx finds URL-bearing attributes (href, src, action, formaction, hx-*,
// data-*, content) and their values.
var adminSegment = regexp.MustCompile(`(^|[^\w.-])/admin([^\w-]|$)`)

var attrRx = regexp.MustCompile(`\s(?:href|src|action|formaction|content|hx-[a-z-]+|data-[a-z-]+)="([^"]*)"`)

// pathRig is a rig mounted at /x-test with every dependency the sampled pages
// and flows need.
type pathRig struct {
	*rig
	ents *fakeEntities
	mail *fakeMailer
	toks *fakeTokens
}

func newPathRig(t *testing.T) *pathRig {
	t.Helper()
	p := &pathRig{ents: newFakeEntities()}
	svc := p.ents.addSchema(1, "Service", "service", serviceSchema, `{"url_pattern":"/services/{path}"}`, false)
	page := p.ents.addSchema(2, "Page", "page", `{"type":"object","properties":{"body":{"type":"string","format":"richtext"}}}`, `{}`, true)
	p.ents.addEntity(11, svc, "Alpha service", "alpha", entity.StatusPublished, `{"summary":"The first"}`, t0)
	p.ents.addEntity(14, page, "About us", "about", entity.StatusPublished, `{"body":"<p>Hi</p>"}`, t0)
	p.rig = newRig(t, func(d *Deps) {
		d.AdminPath = "/x-test"
		d.Growth.Forms, d.Growth.FormMail = newFakeForms(), &fakeFormMail{on: true}
		d.Growth.Plugins = &fakePlugins{settings: map[string]map[string]any{}}
		d.Growth.Packages = &fakePackages{enabled: map[string]bool{}, settings: map[string]any{},
			themes: []ThemeInfo{{Name: "default", Builtin: true, Active: true}, {Name: "dusk", Version: "0.3.0", Source: "upload"}}}
		d.Content = ContentDeps{Entities: p.ents, Terms: newFakeTerms(), Media: newFakeMedia(), Authors: &fakeAuthors{org: "Acme Site"},
			PublicPath: func(_ context.Context, e *entity.Entity) string { return "/services/" + e.Slug }}
	})
	owner := p.owner
	p.toks = &fakeTokens{resets: map[string]*user.User{}, invites: map[string]*auth.Invite{}, user: owner}
	p.mail = &fakeMailer{configured: true, sent: make(chan email.ResetMailData, 4)}
	p.a.deps.Auth = &fakeAuth{users: map[string]*user.User{owner.Email: owner}, fails: map[string]int{}}
	p.a.deps.Tokens, p.a.deps.Finder, p.a.deps.Mailer = p.toks, fakeFinder{owner}, p.mail
	p.a.deps.Manager = &fakeManager{users: map[uuid.UUID]*user.User{p.owner.ID: p.owner, p.editor.ID: p.editor}}
	p.a.deps.Passwords = &fakePasswords{}
	return p
}

// TestAdminPathPrefix proves the prefix is the only admin address: with
// AdminPath "/x-test" every page, redirect, link, asset URL, cookie and email
// link uses it and nothing is mounted under /admin.
func TestAdminPathPrefix(t *testing.T) {
	t.Parallel()

	g := newPathRig(t)
	if g.a.AdminPath() != "/x-test" {
		t.Fatalf("AdminPath() = %q", g.a.AdminPath())
	}

	t.Run("unauthenticated redirect", func(t *testing.T) {
		rec := g.do("GET", "/x-test/settings/forms", "", nil, nil)
		loc := rec.Header().Get("Location")
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/x-test/login?next=") {
			t.Fatalf("%d Location %q", rec.Code, loc)
		}
		if next, _ := url.Parse(loc); next.Query().Get("next") != "/x-test/settings/forms" {
			t.Errorf("next = %q", next.Query().Get("next"))
		}
		rec = g.do("GET", "/x-test/settings/forms", "", nil, map[string]string{"HX-Request": "true"})
		if hx := rec.Header().Get("HX-Redirect"); !strings.HasPrefix(hx, "/x-test/login") {
			t.Errorf("HX-Redirect %q (%d)", hx, rec.Code)
		}
	})

	t.Run("admin is not mounted", func(t *testing.T) {
		for _, p := range []string{"/admin/settings/forms", "/admin/login", "/admin"} {
			if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", p, rec.Code)
			}
		}
	})

	var sample string // one full page, for the asset checks
	rules := 0        // pages that carried speculation rules
	t.Run("pages", func(t *testing.T) {
		for _, p := range []string{
			"", "/entities", "/entities/" + fid(14).String() + "/edit",
			"/settings/general", "/settings/forms", "/theme/themes", "/settings/users", "/settings/media",
		} {
			rec := g.do("GET", "/x-test"+p, "owner-token", nil, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("GET /x-test%s = %d %s", p, rec.Code, rec.Header().Get("Location"))
				continue
			}
			body := rec.Body.String()
			if p == "/entities" {
				sample = body
			}
			if !strings.Contains(body, `<meta name="admin-base" content="/x-test">`) {
				t.Errorf("GET /x-test%s: admin-base meta missing", p)
			}
			// The whole body, not only URL attributes: inline JSON, the speculation
			// rules, single-quoted hx-vals and style urls must not name /admin either.
			// Nothing legitimate remains, so there is no allowlist.
			if m := adminSegment.FindStringIndex(body); m != nil {
				lo, hi := max(m[0]-60, 0), min(m[1]+60, len(body))
				t.Errorf("GET /x-test%s: body still names /admin: ...%s...", p, body[lo:hi])
			}
			if strings.Contains(body, `type="speculationrules"`) { // the editor layout has none
				rules++
				if !strings.Contains(body, `"href_matches":"/x-test/*"`) {
					t.Errorf("GET /x-test%s: speculation rules not under /x-test", p)
				}
			}
			links := 0
			for _, m := range attrRx.FindAllStringSubmatch(body, -1) {
				if adminSegment.MatchString(m[1]) {
					t.Errorf("GET /x-test%s: attribute still names /admin: %s", p, m[0])
				}
				if strings.HasPrefix(m[1], "/x-test/") {
					links++
				}
			}
			if links == 0 || !strings.Contains(body, `href="/x-test/`) {
				t.Errorf("GET /x-test%s: no /x-test links", p)
			}
		}
		if sample == "" {
			t.Fatal("no sample page")
		}
		if rules == 0 {
			t.Error("no sampled page carried speculation rules")
		}
		for _, want := range []string{`action="/x-test/`, `hx-`} {
			if !strings.Contains(sample+g.do("GET", "/x-test/settings/forms", "owner-token", nil, nil).Body.String(), want) {
				t.Errorf("no %s attribute seen", want)
			}
		}
	})

	t.Run("assets", func(t *testing.T) {
		if sample == "" {
			t.Skip("no sample page")
		}
		css := regexp.MustCompile(`<link rel="stylesheet" href="([^"]+)"`).FindStringSubmatch(sample)
		preload := regexp.MustCompile(`<link rel="modulepreload" href="([^"]+)"`).FindStringSubmatch(sample)
		module := regexp.MustCompile(`<script type="module"[^>]* src="([^"]+)"`).FindStringSubmatch(sample)
		imap := regexp.MustCompile(`(?s)<script type="importmap"[^>]*>(.*?)</script>`).FindStringSubmatch(sample)
		if css == nil || preload == nil || module == nil || imap == nil {
			t.Fatalf("missing asset tags: css=%v preload=%v module=%v importmap=%v", css != nil, preload != nil, module != nil, imap != nil)
		}
		for name, u := range map[string]string{"css": css[1], "modulepreload": preload[1], "module script": module[1]} {
			if !strings.HasPrefix(u, "/x-test/_/") {
				t.Errorf("%s URL %q", name, u)
			}
		}
		urls := regexp.MustCompile(`"(/[^"]+)"`).FindAllStringSubmatch(imap[1], -1)
		if len(urls) == 0 {
			t.Error("importmap has no URLs")
		}
		for _, m := range urls {
			if !strings.HasPrefix(m[1], "/x-test/_/") {
				t.Errorf("importmap URL %q", m[1])
			}
		}
		if rec := g.do("GET", css[1], "", nil, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d", css[1], rec.Code)
		}
	})

	t.Run("redirecting POST and flash cookie", func(t *testing.T) {
		rec := g.do("POST", "/x-test/entities/"+fid(11).String()+"/delete", "owner-token", url.Values{}, nil)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/x-test/") {
			t.Fatalf("%d Location %q", rec.Code, rec.Header().Get("Location"))
		}
		var flash *http.Cookie
		for _, ck := range rec.Result().Cookies() {
			if ck.Name == "hm_flash" {
				flash = ck
			}
		}
		if flash == nil || flash.Path != "/x-test" {
			t.Fatalf("flash cookie %+v", flash)
		}
	})

	t.Run("email links", func(t *testing.T) {
		rec := g.do("POST", "/x-test/forgot", "", url.Values{"email": {g.owner.Email}}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("forgot %d", rec.Code)
		}
		if d := <-g.mail.sent; !strings.HasPrefix(d.URL, "https://acme.example/x-test/reset?token=") {
			t.Errorf("reset mail URL %q", d.URL)
		}
		rec = g.do("POST", "/x-test/settings/users/"+g.editor.ID.String()+"/reset", "owner-token", url.Values{}, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://acme.example/x-test/reset?token=") {
			t.Errorf("owner reset link: %d", rec.Code)
		}
		rec = g.do("POST", "/x-test/settings/users/invite", "owner-token", url.Values{"email": {"new@example.com"}, "role": {"editor"}}, nil)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("invite %d", rec.Code)
		}
		body := g.do("GET", rec.Header().Get("Location"), "", nil, map[string]string{
			"Cookie": "hatamoto_session=owner-token; " + flashPair(rec),
		}).Body.String()
		if !strings.Contains(body, "https://acme.example/x-test/accept?token=") {
			t.Errorf("invite link missing:\n%s", body)
		}
	})
}

// TestAdminBaseMetaDefault checks the default prefix is published to JS.
func TestAdminBaseMetaDefault(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	rec := r.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/settings/general = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<meta name="admin-base" content="/admin">`) {
		t.Error("admin-base meta missing or wrong under the default prefix")
	}
}

// flashPair renders the hm_flash cookie rec set as a Cookie header pair.
func flashPair(rec interface{ Result() *http.Response }) string {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "hm_flash" {
			return ck.Name + "=" + ck.Value
		}
	}
	return ""
}

// TestAdminPathUpdateBanner proves the owner banner links under the instance
// prefix whatever Href the service cached, and leaves the cached banner alone.
func TestAdminPathUpdateBanner(t *testing.T) {
	t.Parallel()

	g := newPathRig(t)
	f := baseUpdates()
	f.banner = bannerOf("rolled_back", "Update rolled back")
	g.a.deps.Updates = f

	body := g.do("GET", "/x-test/settings/general", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `href="/x-test/settings/updates"`) {
		t.Errorf("banner link is not under /x-test:\n%s", body)
	}
	if strings.Contains(body, `href="/admin/settings/updates"`) {
		t.Error("banner still links to /admin")
	}
	if f.banner.Href != "/admin/settings/updates" {
		t.Errorf("cached banner was mutated: %q", f.banner.Href)
	}
}
