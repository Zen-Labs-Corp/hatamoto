package admin

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/siteconfig"
)

func TestLocaleStartWithCopies(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}}
	site.cfg.HomepageEntityID = fid(11)
	cp := newFakeCopier()
	g.a.deps.Content.Copier = cp
	g.a.deps.Content.Groups = newFakeGroups()
	g.a.deps.Content.Entities = newFakeEntities()
	g.a.deps.Content.Entities.(*fakeEntities).ents[fid(11)] = &entity.Entity{ID: fid(11), Locale: "en", Name: "Home"}
	g.a.deps.Content.Entities.(*fakeEntities).ents[fid(12)] = &entity.Entity{ID: fid(12), Locale: "en", Name: "About"}
	g.a.deps.Content.Entities.(*fakeEntities).ents[fid(13)] = &entity.Entity{ID: fid(13), Locale: "en", Name: "Team"}
	ms := newFakeMenus()
	a12, a13 := fid(12), fid(13)
	ms.add("Main", menu.LocationHeader,
		&menu.MenuItem{Label: "About", EntityID: &a12, Children: []*menu.MenuItem{{Label: "Team", EntityID: &a13}, {Label: "Ext"}}},
		&menu.MenuItem{Label: "Home again", EntityID: &a12})
	g.a.deps.Growth.Menus = ms
	cp.errFor[fid(13)] = entity.ErrLocaleTaken
	rec := g.do("POST", "/admin/settings/locales/add", "owner-token", url.Values{"tag": {"it-IT"}, "start": {"copies"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("add: %d", rec.Code)
	}
	if len(cp.calls) != 3 {
		t.Fatalf("calls = %d, want 3 (home, about, team once each)", len(cp.calls))
	}
	for _, call := range cp.calls {
		if call.Opts.From != entity.CopyFromThis {
			t.Errorf("From = %q, want %q", call.Opts.From, entity.CopyFromThis)
		}
	}
	has(t, g.do("GET", "/admin/settings/locales/new", "owner-token", nil, nil).Body.String(), `name="start"`, "Start with")
}

func TestLocaleStartHiddenWithoutEntries(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	g.a.deps.Content.Copier = newFakeCopier()
	g.a.deps.Content.Entities = newFakeEntities()
	lacks(t, g.do("GET", "/admin/settings/locales/new", "owner-token", nil, nil).Body.String(), `name="start"`)
}

// Asking for copies always gets an answer, in a warning tone when none were made.
func TestLocaleStartWithCopiesNothingCopied(t *testing.T) {
	t.Parallel()
	for name, taken := range map[string]bool{"all taken": true, "no sources": false} {
		g, site := localeFixture(t)
		site.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}}
		cp := newFakeCopier()
		g.a.deps.Content.Copier = cp
		g.a.deps.Content.Groups = newFakeGroups()
		ents := newFakeEntities()
		g.a.deps.Content.Entities = ents
		ents.ents[fid(12)] = &entity.Entity{ID: fid(12), Locale: "en", Name: "Other"} // on the site, not a start source
		want := "Nothing copied: the home page and the header menu have no pages to copy."
		if taken {
			site.cfg.HomepageEntityID = fid(11)
			ents.ents[fid(11)] = &entity.Entity{ID: fid(11), Locale: "en", Name: "Home"}
			cp.errFor[fid(11)] = entity.ErrLocaleTaken
			want = "Nothing copied: the page already has a it-IT copy."
		}
		rec := g.do("POST", "/admin/settings/locales/add", "owner-token", url.Values{"tag": {"it-IT"}, "start": {"copies"}}, nil)
		if rec.Code != 303 {
			t.Fatalf("%s: add: %d", name, rec.Code)
		}
		if kind, msg := startFlash(t, rec); kind != "warning" || msg != "Added Italian (Italy). "+want {
			t.Errorf("%s: flash = %s %q", name, kind, msg)
		}
	}
}

// startFlash reads the flash a response set, without following the redirect.
func startFlash(t *testing.T, rec *httptest.ResponseRecorder) (kind, msg string) {
	t.Helper()
	for _, ck := range rec.Result().Cookies() {
		if ck.Name != flashCookie {
			continue
		}
		raw, _, _ := strings.Cut(ck.Value, ".")
		b, err := base64.RawURLEncoding.DecodeString(raw)
		var f Flash
		if err != nil || json.Unmarshal(b, &f) != nil {
			t.Fatalf("bad flash cookie %q", ck.Value)
		}
		return f.Kind, f.Message
	}
	t.Fatal("no flash set")
	return "", ""
}
