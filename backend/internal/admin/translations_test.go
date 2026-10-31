package admin

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
)

// groupedRig is a four-locale site (it is hidden) with Alpha (en, original),
// Beta (fr, published, edited before Alpha) and Gamma (es, draft, edited
// after Alpha), all in one group; it has no copy.
func groupedRig(t *testing.T) (*contentRig, *fakeGroups) {
	t.Helper()
	c, g := translationsRig(t, "en", "fr", "es", "it")
	site := c.a.deps.Site.(*fakeSite)
	site.cfg.Locales[3].Hidden = true
	site.cfg.XDefaultLocale = "en"
	grp := fid(900)
	en := entity.Member{ID: fid(11), Group: grp, Locale: "en", Slug: "alpha", Name: "Alpha service", Status: "published", Original: true, CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0.Add(2 * time.Hour)}
	fr := entity.Member{ID: fid(12), Group: grp, Locale: "fr", Slug: "beta", Name: "Beta service", Status: "published", CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0.Add(time.Hour)}
	es := entity.Member{ID: fid(13), Group: grp, Locale: "es", Slug: "gamma", Name: "Gamma service", Status: "draft", CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0.Add(3 * time.Hour)}
	g.setGroup(en, fr, es)
	c.ents.ents[fid(12)].Status = entity.StatusPublished
	return c, g
}

func TestTranslationsPageRows(t *testing.T) {
	t.Parallel()
	c, _ := groupedRig(t)
	body := c.get(t, "owner-token", translationsTab, "")
	has(t, body, "Translations of Alpha service", "Translations 3/4",
		"Original", "Out of date", "no copy, hidden", "(hidden)", "Search title", "Updated", "(older than original)",
		"Search engines see", "hreflang en", "hreflang fr", "hreflang x-default", "/services/beta")
	// The draft copy is listed in the grid and rows but never offered to search engines.
	if strings.Contains(body, "hreflang es") {
		t.Error("a draft copy is listed under search engines")
	}
	if strings.Contains(body, "hreflang it") {
		t.Error("a hidden locale is listed under search engines")
	}
	if strings.Count(body, "Out of date") != 1 {
		t.Errorf("Out of date shown %d times, want once (fr only)", strings.Count(body, "Out of date"))
	}
	lacks(t, body, "Follows", "Own ", "Shared by every locale")
}

func TestTranslationsPageUngroupedShowsItself(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t, "en", "fr")
	body := c.get(t, "owner-token", translationsTab, "")
	has(t, body, "no copy", "Translations 1/2")
	lacks(t, body, ">Original<")
}

func TestTranslationsPageSingleLocaleEmptyState(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t)
	body := c.get(t, "owner-token", translationsTab, "")
	has(t, body, "This site has one language", "Add a locale", "/admin/settings/locales")
	lacks(t, body, "Search engines see", "Translations 1")
}

func TestTranslationsPageSharedEntry(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t, "en", "fr")
	c.ents.ents[fid(11)].Locale = entity.LocaleShared
	body := c.get(t, "owner-token", translationsTab, "")
	has(t, body, "Shared by every locale")
	lacks(t, body, "Search engines see")
}

func TestTranslationsPageNoCreateButton(t *testing.T) {
	t.Parallel()
	c, _ := groupedRig(t)
	body := c.get(t, "owner-token", translationsTab, "")
	lacks(t, body, "Create", "/translations/create")
}
