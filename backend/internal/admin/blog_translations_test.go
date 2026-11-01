package admin

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// blogTranslationsRig is a post editor on a site with the given locales and a
// fake group store and copier.
func blogTranslationsRig(t *testing.T, tags ...string) (*blogRig, *fakeGroups) {
	t.Helper()
	r := newBlogRig(t)
	g := newFakeGroups()
	r.a.deps.Content.Groups = g
	r.a.deps.Content.Copier = newFakeCopier()
	site := r.a.deps.Site.(*fakeSite)
	for _, tag := range tags {
		site.cfg.Locales = append(site.cfg.Locales, siteconfig.LocaleEntry{Tag: tag, Segment: strings.ToLower(tag)})
	}
	return r, g
}

func TestBlogEditorTranslationsTab(t *testing.T) {
	t.Parallel()
	r, g := blogTranslationsRig(t, "en", "pt-BR", "de-DE")
	g.setGroup(
		entity.Member{ID: fid(79), Group: fid(900), Locale: "en", Slug: "go", Name: "Go", Status: "published", Original: true, CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0},
		entity.Member{ID: r.ent.ID, Group: fid(900), Locale: "pt-BR", Slug: "go-servers", Name: "Go servers", Status: "draft", CreatedAt: t0, UpdatedAt: t0, ContentChangedAt: t0})
	r.ent.Locale = "pt-BR"
	body := r.get(t, "owner-token", r.path(), "")
	has(t, body, `id="be-tab-translations"`, `id="be-tabpanel-translations"`, "You are editing the pt-BR copy of the en original.",
		"Compare copies", "Link an existing page", `data-hm-open="create-copy-de-DE"`, `id="create-copy-de-DE"`)
	panel := body[strings.Index(body, `id="be-tabpanel-translations"`):]
	panel = panel[:strings.Index(panel, "</aside>")]
	lacks(t, panel, "<form", "name=")
	// The narrow aside keeps language, status and action: no Address column, and
	// the panel sits in a card like the other tabs.
	has(t, panel, `id="be-translations"`, "Language", "Status")
	lacks(t, panel, ">Address<")
	// The language column is not the title column (240px at least), or the table
	// would scroll sideways in the 319px aside and hide Create.
	has(t, panel, "hm-col-plain")
	lacks(t, panel, "hm-col-title")
	// The Create dialog holds a form, so it sits after the editor form closes.
	createButton(t, body, "create-copy-de-DE")
	if form, dlg := strings.Index(body, `id="hm-be"`), strings.Index(body, `id="create-copy-de-DE"`); dlg < strings.Index(body[form:], "</form>")+form {
		t.Error("the Create copy dialog is inside the editor form")
	}
}

func TestBlogEditorSingleLocaleHasNoTranslations(t *testing.T) {
	t.Parallel()
	r, _ := blogTranslationsRig(t)
	body := r.get(t, "owner-token", r.path(), "blogedit") // golden: byte-identical to before
	lacks(t, body, "be-tab-translations", "be-tabpanel-translations", "create-copy", "Translations")
}

func TestBlogEditorSharedEntryHasNoTranslations(t *testing.T) {
	t.Parallel()
	r, _ := blogTranslationsRig(t, "en", "pt-BR")
	r.ent.Locale = entity.LocaleShared
	lacks(t, r.get(t, "owner-token", r.path(), ""), "be-tab-translations")
}

func TestBlogEditorGroupsNilHasNoTranslations(t *testing.T) {
	t.Parallel()
	r, _ := blogTranslationsRig(t, "en", "pt-BR")
	r.a.deps.Content.Groups = nil
	lacks(t, r.get(t, "owner-token", r.path(), ""), "be-tab-translations")
}

func TestBlogEditorShowsAPanelLoadError(t *testing.T) {
	t.Parallel()
	r, g := blogTranslationsRig(t, "en", "pt-BR")
	g.err = errors.New("db down")
	body := r.get(t, "owner-token", r.path(), "")
	has(t, body, `id="be-tab-translations"`, "The translations could not be loaded.", "hm-callout--danger")
	has(t, body, "Retry", `href="`+r.path()+`"`)
	lacks(t, body, "db down", "Reload the page")
}
