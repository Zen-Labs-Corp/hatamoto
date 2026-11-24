package admin

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/kit"
)

// The a11y tests below cover this page (TestEveryPageTemplateIsExercised).
func init() {
	areaPages["kitsreview"] = []string{"settings/kits-review"}
}

const kitsReviewPath = "/admin/settings/kits/community"

func TestKitReviewRendersThePlan(t *testing.T) {
	t.Parallel()

	p := newKitsRig(t, kitsFixture())
	rec := p.do("GET", kitsReviewPath, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{
		"Community site", "v1.0.0", "signed: first-party", "This kit will",
		"community-plug", "Handle with care", "Pseudonymous visitor keys",
		"JavaScript on every public page", `name="permission" value="site.scripts"`,
		"community-look", "Also make it the active theme.",
		"Stories", "/item/&lt;slug&gt;", "Latest, Newest, Submit", "Header", "latest | newest",
		"Use Latest as the homepage.",
		comment.EnableWarning, comment.EnableConfirmWarning, "Turn comments on.",
		"Already on this site (kept as is): menu &#39;Footer&#39;.",
		">Cancel<", ">Apply kit<", `action="/admin/settings/kits/community"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Index(body, `name="_csrf"`) > strings.Index(body, `role="switch"`) {
		t.Error("hm/csrf is not the first field")
	}
	for _, id := range []string{"kit-comments-on", "kit-homepage"} {
		i := strings.Index(body, `id="`+id+`"`)
		tag := body[strings.LastIndex(body[:i], "<input"):]
		if tag = tag[:strings.Index(tag, ">")]; strings.Contains(tag, " checked") {
			t.Errorf("%s is ticked on an existing site: %s", id, tag)
		}
	}
	if strings.Contains(body, `type="checkbox"`) && !strings.Contains(body, `role="switch"`) {
		t.Error("checkbox without switch role")
	}
	apply := body[strings.Index(body, ">Apply kit<")-200 : strings.Index(body, ">Apply kit<")]
	if strings.Contains(apply, "disabled") {
		t.Errorf("Apply is disabled in HTML: %s", apply)
	}
}

func TestKitReviewFreshSiteTicksThemeAndHomepage(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	f.plan.Fresh = true
	p := newKitsRig(t, f)
	body := p.do("GET", kitsReviewPath, "owner-token", nil, nil).Body.String()
	for id, want := range map[string]bool{"kit-theme-0": true, "kit-homepage": true, "kit-comments-on": false} {
		i := strings.Index(body, `id="`+id+`"`)
		if i < 0 {
			t.Fatalf("no %s", id)
		}
		tag := body[strings.LastIndex(body[:i], "<input"):]
		tag = tag[:strings.Index(tag, ">")]
		if got := strings.Contains(tag, " checked"); got != want {
			t.Errorf("%s checked = %v, want %v", id, got, want)
		}
	}
}

func TestKitReviewAuthz(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	p := newKitsRig(t, f)
	if rec := p.do("GET", kitsReviewPath, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if rec := p.do("POST", kitsReviewPath, "editor-token", url.Values{"comments": {"1"}}, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d, want 403", rec.Code)
	}
	if len(f.applies) != 0 {
		t.Errorf("Apply reached by a refused request: %+v", f.applies)
	}
}

func TestKitApplyCommentsTickedWithoutAddress(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	p := newKitsRig(t, f)
	svc := &fakeComments{}
	p.a.deps.Comments, p.a.deps.BaseURL = svc, func(*http.Request) string { return "" }
	rec := p.do("POST", kitsReviewPath, "owner-token", url.Values{"comments": {"1"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), html.EscapeString(csetNoAddressMsg)) {
		t.Fatalf("POST = %d, want 422 with the no-address message", rec.Code)
	}
	if len(f.applies) != 0 || svc.settings.Enabled || len(p.events) != 0 {
		t.Errorf("nothing may be applied: applies=%d enabled=%v events=%v", len(f.applies), svc.settings.Enabled, p.events)
	}
	if !strings.Contains(rec.Body.String(), ">Apply kit<") {
		t.Error("the review is not shown again")
	}
}

func TestKitApplyWithoutCommentsTick(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	f.result = &kit.Result{Created: map[string]int{"plugins": 1, "pages": 4}}
	p := newKitsRig(t, f)
	svc := &fakeComments{}
	p.a.deps.Comments, p.a.deps.BaseURL = svc, func(*http.Request) string { return "https://example.com" }
	rec := p.do("POST", kitsReviewPath, "owner-token", url.Values{"homepage": {"1"}, "permission": {"site.scripts"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/kits" {
		t.Fatalf("POST = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.applies) != 1 {
		t.Fatalf("Apply called %d times", len(f.applies))
	}
	a := f.applies[0]
	if a.Name != "community" || a.A.Comments || !a.A.Homepage || a.A.ActivateTheme || len(a.A.Extra) != 1 || a.A.Extra[0] != "site.scripts" {
		t.Errorf("accepts = %+v", a)
	}
	if svc.settings.Enabled {
		t.Error("comments went on without the tick")
	}
	if len(p.events) != 1 || p.events[0].Action != "kit.apply" || p.events[0].Target != "kit:community" {
		t.Errorf("audit = %+v", p.events)
	}
}

func TestKitApplyCommentsTickedWithAddress(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	p := newKitsRig(t, f)
	svc := &fakeComments{}
	p.a.deps.Comments, p.a.deps.BaseURL = svc, func(*http.Request) string { return "https://example.com" }
	rec := p.do("POST", kitsReviewPath, "owner-token", url.Values{"comments": {"1"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("POST = %d", rec.Code)
	}
	if !svc.settings.Enabled {
		t.Error("comments not turned on")
	}
	var actions []string
	for _, e := range p.events {
		actions = append(actions, e.Action)
	}
	if strings.Join(actions, ",") != "comments.enable,kit.apply" {
		t.Errorf("audit actions = %v", actions)
	}
}

func TestKitApplyRefusalsAre422(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	f.plan.Refusals = []string{"plugin community-plug is not listed in the registry", "this kit needs SDK ^9"}
	p := newKitsRig(t, f)
	for _, m := range []string{"GET", "POST"} {
		rec := p.do(m, kitsReviewPath, "owner-token", url.Values{"comments": {"1"}}, nil)
		body := rec.Body.String()
		if rec.Code != 422 {
			t.Fatalf("%s = %d, want 422", m, rec.Code)
		}
		for _, want := range []string{"This kit cannot be applied", "plugin community-plug is not listed in the registry", "this kit needs SDK ^9"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", m, want)
			}
		}
		if strings.Contains(body, ">Apply kit<") {
			t.Errorf("%s offers Apply for a refused kit", m)
		}
	}
	if len(f.applies) != 0 {
		t.Error("a refused kit was applied")
	}
}

func TestKitSummary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		res  *kit.Result
		want string
	}{
		{&kit.Result{Created: map[string]int{}}, "The kit was already applied. Nothing was added."},
		{&kit.Result{Created: map[string]int{"plugins": 1, "pages": 4, "menus": 1}}, "Kit applied: added 1 plugin, 4 pages, 1 menu."},
		{&kit.Result{Created: map[string]int{"schemas": 2}, Messages: []string{"Comments left off."}}, "Kit applied: added 2 content types. Comments left off."},
	}
	for _, c := range cases {
		if got := kitSummary(c.res); got != c.want {
			t.Errorf("kitSummary = %q, want %q", got, c.want)
		}
	}
}
