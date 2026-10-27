package admin

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// bigCreated asserts a create answered 303 to an editor URL (prefix, then an
// ID, then suffix) and set the flash cookie.
func bigCreated(t *testing.T, rec *httptest.ResponseRecorder, prefix, suffix string) {
	t.Helper()
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, prefix) || !strings.HasSuffix(loc, suffix) || len(loc) <= len(prefix)+len(suffix) {
		t.Fatalf("want 303 to %s<id>%s, got %d %q", prefix, suffix, rec.Code, loc)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == flashCookie && c.Value != "" {
			return
		}
	}
	t.Fatal("create set no flash cookie")
}

func TestBigCreateDraft(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	rec := r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"post"}}, nil)
	bigCreated(t, rec, "/admin/", "/edit")
}

func TestBigCreateEntity(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rec := c.post("/admin/entities", url.Values{"type": {"service"}, "name": {"Plumbing"}, "attr.summary": {"Pipes"}})
	bigCreated(t, rec, "/admin/entities/", "")
}

func TestBigCreateForm(t *testing.T) {
	t.Parallel()

	g, _, _ := newFormRig(t)
	rec := g.do("POST", "/admin/settings/forms", "owner-token", url.Values{"name": {"Newsletter"}}, nil)
	bigCreated(t, rec, "/admin/settings/forms/", "/edit")
}

func TestBigCreateEntityType(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rec := c.post("/admin/settings/types", url.Values{"name": {"Testimonial"}, "slug": {"testimonial"}, "field": {"r0"}, "f.r0.name": {"quote"}, "f.r0.kind": {"text"}})
	bigCreated(t, rec, "/admin/settings/types/", "")
}

func TestBigCreateLinkingRule(t *testing.T) {
	t.Parallel()

	g, _, target, _ := newLinkRig(t)
	rec := g.do("POST", "/admin/settings/linking/rules", "owner-token", url.Values{"phrase": {"web design"}, "target": {target.String()}, "priority": {"80"}, "maxlinks": {"2"}, "active": {"1"}}, nil)
	bigCreated(t, rec, "/admin/settings/linking/rules/", "")
}
