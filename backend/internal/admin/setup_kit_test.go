package admin

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/kit"
)

// The a11y tests below cover these pages (TestEveryPageTemplateIsExercised).
func init() {
	areaPages["setupkit"] = []string{"setup/kit", "setup/kit-review"}
}

func newSetupKitRig(t *testing.T, kits *fakeKits) *setupRig {
	t.Helper()
	g := newSetupRig(t)
	g.count.n = 1 // the owner exists: the later steps are for the signed-in owner
	g.a.deps.Kits = kits
	return g
}

func contentRadios(body string) int { return strings.Count(body, `name="content"`) }

func TestSetupContentShowsKitCardWhenRegistryListsKits(t *testing.T) {
	t.Parallel()

	g := newSetupKitRig(t, kitsFixture())
	body := g.do("GET", "/admin/setup/content", "owner-token", nil, nil).Body.String()
	if n := contentRadios(body); n != 4 {
		t.Errorf("%d cards, want 4", n)
	}
	for _, want := range []string{"Start from a kit", ">Continue<", "Step 6 of 7"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, ">Finish setup<") {
		t.Error("the step still says Finish setup")
	}
}

func TestSetupContentHidesKitCardOffline(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	f.available, f.listErr = nil, errors.New("offline")
	for name, kits := range map[string]*fakeKits{"error": f, "empty": {}} {
		g := newSetupKitRig(t, kits)
		body := g.do("GET", "/admin/setup/content", "owner-token", nil, nil).Body.String()
		if n := contentRadios(body); n != 3 || !strings.Contains(body, ">Finish setup<") || strings.Contains(body, "Start from a kit") {
			t.Errorf("%s: %d cards, want 3 with Finish setup", name, n)
		}
		rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"kit"}}, nil)
		if rec.Code != 422 || g.site.cfg.SetupCompletedAt != nil {
			t.Errorf("%s: kit chosen while hidden = %d (setup completed: %v)", name, rec.Code, g.site.cfg.SetupCompletedAt != nil)
		}
	}
}

func TestSetupContentKitChoiceKeepsSetupOpen(t *testing.T) {
	t.Parallel()

	g := newSetupKitRig(t, kitsFixture())
	rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"kit"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/kit" {
		t.Fatalf("POST = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if g.site.cfg.SetupCompletedAt != nil || g.starter.calls != 0 {
		t.Error("choosing a kit completed setup or installed starter content")
	}
	rec = g.do("GET", "/admin/setup/kit", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /setup/kit = %d: setup must still be open", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{"Community site 1.0.0", "signed: first-party", "Journal 0.3.0", ">Continue<", "Step 6 of 7"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	rec = g.do("POST", "/admin/setup/kit", "owner-token", url.Values{"kit": {"community"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/kit/community" {
		t.Errorf("pick = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := g.do("POST", "/admin/setup/kit", "owner-token", url.Values{}, nil); rec.Code != 422 {
		t.Errorf("no pick = %d, want 422", rec.Code)
	}
}

func TestSetupKitReviewFreshSiteTicksOn(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	f.plan.Fresh = true
	g := newSetupKitRig(t, f)
	rec := g.do("GET", "/admin/setup/kit/community", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for id, want := range map[string]bool{"kit-theme-0": true, "kit-homepage": true, "kit-comments-on": false} {
		i := strings.Index(body, `id="`+id+`"`)
		if i < 0 {
			t.Fatalf("no %s", id)
		}
		tag := body[strings.LastIndex(body[:i], "<input"):]
		if got := strings.Contains(tag[:strings.Index(tag, ">")], " checked"); got != want {
			t.Errorf("%s checked = %v, want %v", id, got, want)
		}
	}
	for _, want := range []string{"Step 6 of 7", ">Apply kit<", ">Cancel<", `href="/admin/setup/content"`, `action="/admin/setup/kit/community"`} {
		if !strings.Contains(body, want) {
			t.Errorf("review lacks %q", want)
		}
	}
}

func TestSetupKitApplyFinishesSetup(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	f.result = &kit.Result{Created: map[string]int{"plugins": 1}}
	g := newSetupKitRig(t, f)
	rec := g.do("POST", "/admin/setup/kit/community", "owner-token", url.Values{"homepage": {"1"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/done" {
		t.Fatalf("POST = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.applies) != 1 || !f.applies[0].A.Homepage {
		t.Errorf("applies = %+v", f.applies)
	}
	if g.site.cfg.SetupCompletedAt == nil {
		t.Error("setup was not completed after the kit")
	}
	if g.starter.named != 1 {
		t.Errorf("the seed home page was named %d times, want 1", g.starter.named)
	}
	if rec := g.do("GET", "/admin/setup/kit/community", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("the kit flow after completion = %d, want 404", rec.Code)
	}
}

func TestSetupKitFailedApplyLeavesSetupOpen(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	f.applyErr = errors.New("boom")
	g := newSetupKitRig(t, f)
	rec := g.do("POST", "/admin/setup/kit/community", "owner-token", nil, nil)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "safe to apply again") {
		t.Errorf("POST = %d", rec.Code)
	}
	if g.site.cfg.SetupCompletedAt != nil {
		t.Error("a failed apply completed setup")
	}
	if rec := g.do("GET", "/admin/setup/kit/community", "owner-token", nil, nil); rec.Code != 200 {
		t.Errorf("the review after a failure = %d, want 200", rec.Code)
	}
}

func TestSetupKitRoutesRefuseEditor(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	g := newSetupKitRig(t, f)
	for _, rt := range []struct{ method, path string }{{"GET", "/admin/setup/kit"}, {"POST", "/admin/setup/kit"}, {"GET", "/admin/setup/kit/community"}, {"POST", "/admin/setup/kit/community"}} {
		if rec := g.do(rt.method, rt.path, "editor-token", url.Values{"kit": {"community"}}, nil); rec.Code != 403 {
			t.Errorf("editor %s %s = %d, want 403", rt.method, rt.path, rec.Code)
		}
	}
	if len(f.applies) != 0 {
		t.Error("an editor applied a kit")
	}
}
