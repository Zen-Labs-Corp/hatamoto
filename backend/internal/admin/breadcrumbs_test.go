package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// crumbsHeader carries the rendered view's trail on test responses.
const (
	crumbsHeader     = "X-Test-Crumbs"
	crumbHrefsHeader = "X-Test-Crumb-Hrefs"
)

func init() {
	viewSeen = func(w http.ResponseWriter, v *View) {
		labels, hrefs := make([]string, len(v.Crumbs)), make([]string, len(v.Crumbs))
		for i, c := range v.Crumbs {
			labels[i], hrefs[i] = c.Label, c.Href
		}
		w.Header().Set(crumbsHeader, strings.Join(labels, "\x1f"))
		w.Header().Set(crumbHrefsHeader, strings.Join(hrefs, "\x1f"))
	}
}

// trailLabels returns the crumb labels the handler gave the view. The shell
// draws no top bar, so this data is the page's trail contract.
func trailLabels(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if strings.Contains(rec.Body.String(), "hm-topbar") {
		t.Error("the shell still renders a top bar")
	}
	h := rec.Header().Get(crumbsHeader)
	if h == "" {
		t.Fatal("page rendered without crumbs")
	}
	return strings.Split(h, "\x1f")
}

// Entity sub-pages and the entity type editor keep their intermediate levels:
// the handlers set the trail; the shell no longer renders a bar for it.
func TestSubPageTrailsKeepIntermediateLevels(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	c.post("/admin/entities/"+id, url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"Second"}})
	cases := []struct{ path, want string }{
		{"/admin/entities/" + id, "Entities > Alpha service"},
		{"/admin/entities/" + id + "/versions", "Entities > Alpha service > Versions"},
		{"/admin/entities/" + id + "/versions/compare?from=1&to=0", "Entities > Alpha service > Versions > Compare"},
		{"/admin/entities/" + id + "/versions/1", "Entities > Alpha service > Versions > v1"},
		{"/admin/entities/new", "Entities > New"},
		{"/admin/settings/types/" + fid(1).String(), "Settings > Entity models > Entity types > Service"},
	}
	for _, tc := range cases {
		rec := c.do("GET", tc.path, "owner-token", nil, nil)
		if got := strings.Join(trailLabels(t, rec), " > "); got != tc.want {
			t.Errorf("%s: trail %q, want %q", tc.path, got, tc.want)
		}
	}
}
