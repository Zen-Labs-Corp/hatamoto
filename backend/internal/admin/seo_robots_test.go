package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/seo"
)

func goodRobotsForm() url.Values {
	return url.Values{
		"max_snippet": {"50"}, "max_image_preview": {"large"}, "noarchive": {"true"}, "nosnippet": {"false"},
		"extra_lines": {"User-agent: Foo\r\nDisallow: /private\r\n\r\n"},
	}
}

func TestSEORobotsSave(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Home = "%sitename%"
	s.svc.set.Verification.Google = "keep"
	rec := s.do("POST", "/admin/settings/seo/robots", "owner-token", goodRobotsForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/robots" {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	got := s.svc.set.Robots
	if got.MaxSnippet == nil || *got.MaxSnippet != 50 || got.MaxImagePreview != "large" || !got.NoArchive || got.NoSnippet {
		t.Fatalf("robots = %+v", got)
	}
	if len(got.ExtraLines) != 2 || got.ExtraLines[0] != "User-agent: Foo" || got.ExtraLines[1] != "Disallow: /private" {
		t.Errorf("lines = %q", got.ExtraLines)
	}
	if s.svc.set.Home != "%sitename%" || s.svc.set.Verification.Google != "keep" {
		t.Errorf("another tab's keys were clobbered: %+v", s.svc.set)
	}
	if s.flushed != 1 {
		t.Errorf("page cache invalidated %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Fatalf("audit = %+v", s.events)
	}
}

func TestSEORobotsClearsValues(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	n := 5
	s.svc.set.Robots = seo.Robots{MaxSnippet: &n, MaxImagePreview: "none", NoSnippet: true, ExtraLines: []string{"Disallow: /x"}}
	form := url.Values{"max_snippet": {""}, "max_image_preview": {""}, "nosnippet": {"false"}, "extra_lines": {""}}
	if rec := s.do("POST", "/admin/settings/seo/robots", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if !s.svc.set.Robots.IsZero() {
		t.Errorf("robots = %+v, want none", s.svc.set.Robots)
	}
}

func TestSEORobotsRejectsBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		key, val, want string
	}{
		"max-snippet text":  {"max_snippet", "lots", "Use -1, 0 or a positive number."},
		"max-snippet low":   {"max_snippet", "-5", "Use -1, 0 or a positive number."},
		"image preview":     {"max_image_preview", "huge", "Choose one of the listed sizes."},
		"unknown directive": {"extra_lines", "Foo: bar", "Line 1"},
		"admin address":     {"extra_lines", "Disallow: /admin/x", "admin address"},
	} {
		s := newSEORig(t, newFakeSEO())
		form := goodRobotsForm()
		form.Set(tc.key, tc.val)
		rec := s.do("POST", "/admin/settings/seo/robots", "owner-token", form, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d, want 422 with %q", name, rec.Code, tc.want)
		}
		if s.svc.merges != 0 || s.flushed != 0 {
			t.Errorf("%s: a rejected save reached the store", name)
		}
	}
}

func TestSEORobotsOwnerOnly(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	for _, m := range []string{"GET", "POST"} {
		if rec := s.do(m, "/admin/settings/seo/robots", "editor-token", goodRobotsForm(), nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
	if s.svc.merges != 0 {
		t.Error("editor save reached the service")
	}
}

func TestSEOTitlesSaveKeepsRobots(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	if rec := s.do("POST", "/admin/settings/seo/robots", "owner-token", goodRobotsForm(), nil); rec.Code != 303 {
		t.Fatalf("robots save = %d", rec.Code)
	}
	if rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", goodTitlesForm(), nil); rec.Code != 303 {
		t.Fatalf("titles save = %d", rec.Code)
	}
	if rec := s.do("POST", "/admin/settings/seo/social", "owner-token", goodSocialForm(), nil); rec.Code != 303 {
		t.Fatalf("social save = %d", rec.Code)
	}
	got := s.svc.set.Robots
	if got.MaxImagePreview != "large" || len(got.ExtraLines) != 2 {
		t.Errorf("a Titles or Social save changed robots: %+v", got)
	}
}

func TestSEORobotsPreview(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Robots.ExtraLines = []string{"Disallow: /stored"}
	var seen []string
	s.a.deps.RobotsPreview = func(_ *http.Request, rs seo.Robots) string {
		seen = rs.ExtraLines
		return "User-agent: *\nDisallow: /<b>x</b>\n"
	}
	body := s.get(t, "/admin/settings/seo/robots")
	if !strings.Contains(body, "Preview of /robots.txt") || !strings.Contains(body, "Disallow: /&lt;b&gt;x&lt;/b&gt;") || strings.Contains(body, "<b>x</b>") {
		t.Errorf("preview missing or not escaped:\n%s", body)
	}
	if len(seen) != 1 || seen[0] != "Disallow: /stored" {
		t.Errorf("preview saw %q, want the stored lines", seen)
	}
	// A rejected save previews what was submitted.
	form := goodRobotsForm()
	form.Set("max_snippet", "x")
	if rec := s.do("POST", "/admin/settings/seo/robots", "owner-token", form, nil); rec.Code != 422 {
		t.Fatalf("save = %d", rec.Code)
	}
	if len(seen) != 2 || seen[1] != "Disallow: /private" {
		t.Errorf("preview saw %q after a rejected save, want the submitted lines", seen)
	}

	s.a.deps.RobotsPreview = nil
	if body := s.get(t, "/admin/settings/seo/robots"); strings.Contains(body, "Preview of /robots.txt") {
		t.Error("the preview card shows without a preview func")
	}
}

func TestSEORobotsAIPolicyForm(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	body := s.get(t, "/admin/settings/seo/robots")
	if strings.Contains(body, `action="/admin/analytics/crawlers/robots"`) || !strings.Contains(body, "AI crawler policy is not available.") {
		t.Error("without the analytics module the policy form must give way to a notice")
	}

	s.a.deps.Growth.Agents = &fakeAgentStats{denied: []agents.Purpose{agents.PurposeTraining}}
	body = s.get(t, "/admin/settings/seo/robots")
	for _, want := range []string{
		`action="/admin/analytics/crawlers/robots"`, `name="return" value="/admin/settings/seo/robots"`,
		`name="purpose.training"`, `name="purpose.archive"`, `data-aia-saved="deny"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The policy form sits beside the robots form, never inside it.
	robots := strings.Index(body, `action="/admin/settings/seo/robots"`)
	policy := strings.Index(body, `action="/admin/analytics/crawlers/robots"`)
	if end := strings.Index(body[robots:], "</form>"); robots < 0 || end < 0 || policy < robots+end {
		t.Error("the policy form is nested in the robots form")
	}
}
