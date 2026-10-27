package admin

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

var (
	topbarTrail = regexp.MustCompile(`(?s)<nav class="hm-topbar__crumbs" aria-label="Breadcrumb"><ol>(.*?)</ol></nav>`)
	trailItem   = regexp.MustCompile(`(?s)<li[^>]*>(?:<a href="[^"]*">)?(.*?)(?:</a>)?</li>`)
)

// trailLabels returns the labels of the top bar breadcrumb trail.
func trailLabels(t *testing.T, body string) []string {
	t.Helper()
	m := topbarTrail.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no top bar trail")
	}
	var out []string
	for _, li := range trailItem.FindAllStringSubmatch(m[1], -1) {
		out = append(out, li[1])
	}
	return out
}

// Entity sub-pages and the entity type editor keep their intermediate levels:
// the handlers set the trail, the top bar renders it.
func TestSubPageTrailsKeepIntermediateLevels(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	c.post("/admin/entities/"+id, url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"Second"}})
	cases := []struct{ path, want string }{
		{"/admin/entities/" + id, "Home > Entities > Alpha service"},
		{"/admin/entities/" + id + "/versions", "Home > Entities > Alpha service > Versions"},
		{"/admin/entities/" + id + "/versions/compare?from=1&to=0", "Home > Entities > Alpha service > Versions > Compare"},
		{"/admin/entities/" + id + "/versions/1", "Home > Entities > Alpha service > Versions > v1"},
		{"/admin/entities/new", "Home > Entities > New"},
		{"/admin/settings/types/" + fid(1).String(), "Home > Settings > Entity models > Entity types > Service"},
	}
	for _, tc := range cases {
		body := c.get(t, "owner-token", tc.path, "")
		if got := strings.Join(trailLabels(t, body), " > "); got != tc.want {
			t.Errorf("%s: trail %q, want %q", tc.path, got, tc.want)
		}
		if n := strings.Count(body, `aria-label="Breadcrumb"`); n != 1 {
			t.Errorf("%s: %d Breadcrumb navs, want 1", tc.path, n)
		}
	}
}

// Phones keep a trail: the top bar must never be hidden at the phone breakpoint, because
// it is the only place a page shows where it sits (and a way back to the parent).
func TestTopbarSurvivesPhones(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if regexp.MustCompile(`\.hm-topbar\s*\{[^}]*display:\s*none`).MatchString(css) {
		t.Error("base.css hides .hm-topbar: phones would have no breadcrumb trail")
	}
	if !strings.Contains(css, ".hm-topbar__crumbs li:nth-last-child(2)") {
		t.Error("base.css has no compact phone trail (back link to the parent crumb)")
	}
}
