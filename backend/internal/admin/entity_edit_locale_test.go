package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

const alphaURL = "/admin/entities/00000000-0000-0000-0000-000000000011"

// alphaForm is a valid Details save of Alpha service.
func alphaForm(extra url.Values) url.Values {
	f := url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"The first"}, "attr.featured": {"true"}, "_present.attr.featured": {"true"}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func TestDetailsLocaleSelectHiddenSingleLocale(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t)
	c.ents.ents[fid(11)].Locale = "en"
	body := c.get(t, "owner-token", alphaURL, "")
	lacks(t, body, `id="locale"`, "Shared by every locale", "Locale")
	// A posted locale on a single-locale site is ignored.
	if rec := c.post(alphaURL, alphaForm(url.Values{"locale": {"fr"}})); rec.Code != 303 || c.ents.ents[fid(11)].Locale != "en" {
		t.Errorf("single-locale save: %d, locale %q; want 303 and en", rec.Code, c.ents.ents[fid(11)].Locale)
	}
}

func TestDetailsLocaleSelectOptionsNoUnd(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t, "en", "fr", "es")
	c.ents.ents[fid(11)].Locale = "fr"
	body := c.get(t, "owner-token", alphaURL, "")
	has(t, body, `id="locale"`, `name="locale"`, `value="en"`, `value="es"`, `<option value="fr" selected`)
	lacks(t, body, `value="und"`)
}

func TestDetailsSaveRetagsLocale(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t, "en", "fr")
	c.ents.ents[fid(11)].Locale = "en"
	rows := c.record()
	rec := c.post(alphaURL, alphaForm(url.Values{"locale": {"fr"}}))
	if rec.Code != 303 {
		t.Fatalf("save: %d\n%s", rec.Code, rec.Body.String())
	}
	if got := c.ents.ents[fid(11)].Locale; got != "fr" {
		t.Errorf("locale = %q, want fr", got)
	}
	if got := rows(); len(got) != 1 || got[0].Detail["locale"] != "fr" {
		t.Errorf("audit = %+v, want the new locale in the detail", got)
	}
}

func TestDetailsSaveOmittedLocaleLeavesNil(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t, "en", "fr")
	c.ents.ents[fid(11)].Locale = "en"
	rows := c.record()
	if rec := c.post(alphaURL, alphaForm(nil)); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	if got := c.ents.ents[fid(11)].Locale; got != "en" {
		t.Errorf("locale = %q, want en", got)
	}
	if _, ok := rows()[0].Detail["locale"]; ok {
		t.Error("audit detail carries a locale that did not change")
	}
}

func TestDetailsSaveRefusesUndAndUnknownLocale(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t, "en", "fr")
	c.ents.ents[fid(11)].Locale = "en"
	for _, loc := range []string{"und", "xx", "de-AT"} {
		rec := c.post(alphaURL, alphaForm(url.Values{"locale": {loc}}))
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose one of the site languages.") {
			t.Errorf("locale %q: %d", loc, rec.Code)
		}
		if c.ents.ents[fid(11)].Locale != "en" {
			t.Errorf("locale %q was stored", loc)
		}
	}
}

func TestDetailsLocaleClashWithGroupMember(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr", "es")
	c.ents.ents[fid(11)].Locale = "en"
	grp := fid(900)
	g.setGroup(entity.Member{ID: fid(11), Group: grp, Locale: "en"}, entity.Member{ID: fid(12), Group: grp, Locale: "fr"})
	rec := c.post(alphaURL, alphaForm(url.Values{"locale": {"fr"}}))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Another copy of this page already uses that locale. Unlink it first.") {
		t.Fatalf("clash: %d", rec.Code)
	}
	if c.ents.ents[fid(11)].Locale != "en" {
		t.Error("the clashing locale was stored")
	}
	// A free locale is fine.
	if rec := c.post(alphaURL, alphaForm(url.Values{"locale": {"es"}})); rec.Code != 303 || c.ents.ents[fid(11)].Locale != "es" {
		t.Errorf("free locale: %d %q", rec.Code, c.ents.ents[fid(11)].Locale)
	}
}

func TestDetailsSharedEntryShowsNote(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t, "en", "fr")
	c.ents.ents[fid(11)].Locale = entity.LocaleShared
	body := c.get(t, "owner-token", alphaURL, "")
	has(t, body, "Shared by every locale")
	lacks(t, body, `name="locale"`)
	// Nothing is posted for it, so a hostile locale field does not re-tag it.
	if rec := c.post(alphaURL, alphaForm(url.Values{"locale": {"fr"}})); rec.Code != 303 {
		t.Errorf("shared save: %d, want 303", rec.Code)
	}
	if c.ents.ents[fid(11)].Locale != entity.LocaleShared {
		t.Error("a shared entry was re-tagged")
	}
}
