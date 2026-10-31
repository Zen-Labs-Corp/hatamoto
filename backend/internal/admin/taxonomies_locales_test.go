package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func termLocalesRig(t *testing.T, tags ...string) (*contentRig, *fakeTermGroups, string) {
	t.Helper()
	c := newContentRig(t)
	c.a.deps.Content.Groups = newFakeGroups()
	tg := newFakeTermGroups()
	c.a.deps.Content.TermGroups = tg
	site := c.a.deps.Site.(*fakeSite)
	for _, tag := range tags {
		site.cfg.Locales = append(site.cfg.Locales, siteconfig.LocaleEntry{Tag: tag, Segment: strings.ToLower(tag)})
	}
	var id string
	id = c.terms.terms[0].ID.String()
	return c, tg, id
}

func TestTermLocalesBlock(t *testing.T) {
	t.Parallel()
	c, tg, id := termLocalesRig(t, "en", "pt-BR")
	path := "/admin/settings/taxonomies/terms/" + id
	has(t, c.get(t, "owner-token", path, ""), "Save locale", "Set a locale first")
	rec := c.post(path+"/locale", url.Values{"locale": {"en"}})
	if rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	body := c.get(t, "owner-token", path, "")
	has(t, body, "(this term)", `data-hm-open="create-term-copy-pt-BR"`)
	rec = c.post(path+"/copy", url.Values{"locale": {"pt-BR"}, "name": {"Tag"}, "slug": {"tag-pt"}})
	if rec.Code != 303 || len(tg.copies) != 1 {
		t.Errorf("copy: %d %v", rec.Code, tg.copies)
	}
}

func TestTermLocalesOffSingleLocale(t *testing.T) {
	t.Parallel()
	c, _, id := termLocalesRig(t)
	lacks(t, c.get(t, "owner-token", "/admin/settings/taxonomies/terms/"+id, ""), "Save locale")
	if rec := c.post("/admin/settings/taxonomies/terms/"+id+"/locale", url.Values{"locale": {"en"}}); rec.Code != 404 {
		t.Errorf("%d", rec.Code)
	}
}
