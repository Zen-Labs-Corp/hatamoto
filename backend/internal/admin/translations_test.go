package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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

// With no x-default set the default-locale copy is x-default, whatever the
// order of the locale tags, as the renderer emits it.
func TestTranslationsHreflangFollowsRenderer(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "it", "fr")
	c.a.deps.Site.(*fakeSite).cfg.Language = "fr"
	grp := fid(900)
	it := entity.Member{ID: fid(11), Group: grp, Locale: "it", Slug: "alpha", Name: "Alpha", Status: "published", Original: true, CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0}
	fr := entity.Member{ID: fid(12), Group: grp, Locale: "fr", Slug: "beta", Name: "Beta", Status: "published", CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0}
	g.setGroup(it, fr)
	c.ents.ents[fid(12)].Status = entity.StatusPublished
	body := c.get(t, "owner-token", translationsTab, "")
	i, j, k := strings.Index(body, "hreflang it"), strings.Index(body, "hreflang fr"), strings.Index(body, "hreflang x-default")
	if i < 0 || j < i || k < j {
		t.Fatalf("hreflang order: it %d, fr %d, x-default %d; want settings order, x-default last", i, j, k)
	}
	if rest := body[k:]; !strings.Contains(rest[:strings.Index(rest, "</tr>")], "/services/beta") {
		t.Errorf("x-default is not the default-locale copy (beta)")
	}
}

// The original falls back to the default-locale copy even when the stored
// language differs in case from the locale tag.
func TestTranslationsOriginalUsesNormalizedDefault(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr")
	c.a.deps.Site.(*fakeSite).cfg.Language = "FR"
	grp := fid(900)
	en := entity.Member{ID: fid(11), Group: grp, Locale: "en", Slug: "alpha", Name: "Alpha", Status: "published", CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0.Add(time.Hour)}
	fr := entity.Member{ID: fid(12), Group: grp, Locale: "fr", Slug: "beta", Name: "Beta", Status: "published", CreatedAt: t0.Add(time.Hour), UpdatedAt: t0, ContentChangedAt: t0.Add(3 * time.Hour)}
	g.setGroup(en, fr)
	body := c.get(t, "owner-token", translationsTab, "")
	// fr is the original (default locale), so en is the stale copy; with the
	// oldest copy as original nothing would be out of date.
	if strings.Count(body, "Out of date") != 1 {
		t.Errorf("Out of date shown %d times, want once (en)", strings.Count(body, "Out of date"))
	}
}

// A member that cannot be loaded does not show the viewed page's address.
func TestTranslationsUnloadableMemberShowsNoAddress(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr")
	grp := fid(900)
	en := entity.Member{ID: fid(11), Group: grp, Locale: "en", Slug: "alpha", Name: "Alpha", Status: "published", Original: true, CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0}
	fr := entity.Member{ID: fid(99), Group: grp, Locale: "fr", Slug: "gone", Name: "Gone", Status: "published", CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0}
	g.setGroup(en, fr)
	body := c.get(t, "owner-token", translationsTab, "")
	// en shows its address in its copy row, grid cell, hreflang row and
	// x-default row, each as text and as title: 8. The unloadable copy adds none.
	if n := strings.Count(body, "/services/alpha"); n != 8 {
		t.Errorf("viewed page's address shown %d times, want 8 (none for the unloadable copy)", n)
	}
}

// translationsGridBody is the field grid's markup: from its table to the one after.
func translationsGridBody(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `id="translations-grid"`)
	j := strings.Index(body, `id="translations-hreflang"`)
	if i < 0 || j < i {
		t.Fatalf("field grid not found (grid %d, hreflang %d)", i, j)
	}
	return body[i:j]
}

// The field grid has one Edit link per locale column, to that copy's Details.
func TestTranslationsGridHasEditLinkPerCopy(t *testing.T) {
	t.Parallel()
	c, _ := groupedRig(t)
	grid := translationsGridBody(t, c.get(t, "owner-token", translationsTab, ""))
	if n := strings.Count(grid, ">Edit</a>"); n != 3 {
		t.Errorf("grid has %d Edit links, want 3 (one per copy)", n)
	}
	for _, id := range []uuid.UUID{fid(11), fid(12), fid(13)} {
		if !strings.Contains(grid, "/entities/"+id.String()+`"`) {
			t.Errorf("grid lacks an Edit link to copy %s", id)
		}
	}
}

// The grid columns follow the locale order of the settings (and the copies
// table, not the alphabet (the members arrive alphabetically).
func TestTranslationsGridColumnsFollowSettingsOrder(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr", "es")
	grp := fid(900)
	g.setGroup(
		entity.Member{ID: fid(11), Group: grp, Locale: "en", Slug: "alpha", Name: "Alpha", Status: "published", Original: true, CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0},
		entity.Member{ID: fid(13), Group: grp, Locale: "es", Slug: "gamma", Name: "Gamma", Status: "draft", CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0},
		entity.Member{ID: fid(12), Group: grp, Locale: "fr", Slug: "beta", Name: "Beta", Status: "published", CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0},
	)
	grid := translationsGridBody(t, c.get(t, "owner-token", translationsTab, ""))
	en, fr, es := strings.Index(grid, ">en<"), strings.Index(grid, ">fr<"), strings.Index(grid, ">es<")
	if en < 0 || fr < en || es < fr {
		t.Errorf("grid column order en %d, fr %d, es %d; want en, fr, es", en, fr, es)
	}
}

// A cell with no value shows the admin's dash, never "None": the address of a
// locale with no copy and an empty search title.
func TestTranslationsEmptyCellsShowDash(t *testing.T) {
	t.Parallel()
	c, _ := groupedRig(t)
	body := c.get(t, "owner-token", translationsTab, "")
	lacks(t, body, "None")
	has(t, body, "—")
}

// The copies table, the field grid and the search-engine list each have a
// visible heading, so they stay labelled where the stacked mobile tables drop
// their column headers.
func TestTranslationsSectionsHaveHeadings(t *testing.T) {
	t.Parallel()
	c, _ := groupedRig(t)
	body := c.get(t, "owner-token", translationsTab, "")
	has(t, body, `<h2 class="hm-card__title">Languages</h2>`, `<h2 class="hm-card__title">Fields by language</h2>`, `<h2 class="hm-card__title">Search engines see</h2>`)
}
