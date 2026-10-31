package admin

import (
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

func builderTranslationsRig(t *testing.T, tags ...string) *editorRig {
	t.Helper()
	r := newEditorRig(t)
	r.a.deps.Content.Groups = newFakeGroups()
	r.a.deps.Content.Copier = newFakeCopier()
	site := r.a.deps.Site.(*fakeSite)
	for _, tag := range tags {
		site.cfg.Locales = append(site.cfg.Locales, siteconfig.LocaleEntry{Tag: tag, Segment: strings.ToLower(tag)})
	}
	return r
}

func TestBuilderTranslationsDrawer(t *testing.T) {
	t.Parallel()
	r := builderTranslationsRig(t, "en", "pt-BR")
	page := builderPage(t, r, builderOnlyDoc)
	has(t, page, "data-hm-bs-translations", "hm-bs-translations-drawer", `"translations":"/admin/entities/`)
	rec := r.do("GET", r.path()[:strings.LastIndex(r.path(), "/")]+"/builder/translations", "owner-token", nil, htmxHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	has(t, body, `id="hm-bs-translations"`, "Compare copies", `data-hm-open="create-copy-pt-BR"`)
	createButton(t, body, "create-copy-pt-BR")
	lacks(t, body, "<html", "hm-sidebar")
}

func TestBuilderTranslationsSingleLocale(t *testing.T) {
	t.Parallel()
	r := builderTranslationsRig(t)
	lacks(t, builderPage(t, r, builderOnlyDoc), "data-hm-bs-translations", "hm-bs-translations-drawer")
	url := r.path()[:strings.LastIndex(r.path(), "/")] + "/builder/translations"
	if rec := r.do("GET", url, "owner-token", nil, htmxHdr); rec.Code != http.StatusNotFound {
		t.Errorf("single-locale: %d", rec.Code)
	}
	r.ent.Locale = entity.LocaleShared
}
