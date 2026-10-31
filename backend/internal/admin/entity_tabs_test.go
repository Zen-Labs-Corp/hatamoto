package admin

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// translationsRig is a content rig whose site has the given locale tags and
// whose content deps carry a fake group store.
func translationsRig(t *testing.T, tags ...string) (*contentRig, *fakeGroups) {
	t.Helper()
	c := newContentRig(t)
	g := newFakeGroups()
	c.a.deps.Content.Groups = g
	site := c.a.deps.Site.(*fakeSite)
	for _, tag := range tags {
		site.cfg.Locales = append(site.cfg.Locales, siteconfig.LocaleEntry{Tag: tag, Segment: tag})
	}
	return c, g
}

const translationsTab = `/admin/entities/00000000-0000-0000-0000-000000000011/translations`

func TestEntityTabsSingleLocaleHasTwo(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t)
	body := c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, body, ">Versions<")
	lacks(t, body, "Translations", translationsTab)
}

func TestEntityTabsMultiLocaleHasTranslationsCount(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr", "es")
	body := c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, body, "Translations 1/3", translationsTab)
	g.setGroup(entity.Member{ID: fid(11), Group: fid(900), Locale: "en"}, entity.Member{ID: fid(12), Group: fid(900), Locale: "fr"})
	body = c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, body, "Translations 2/3")
	// The tab sits between Details and Versions and shows on the Versions page too.
	body = c.get(t, "owner-token", "/admin/entities/"+fid(11).String()+"/versions", "")
	has(t, body, "Translations 2/3")
	i, j := strings.Index(body, ">Details<"), strings.Index(body, "Translations 2/3")
	if i < 0 || j < i || !strings.Contains(body[j:], ">Versions<") {
		t.Errorf("tab order: details=%d translations=%d", i, j)
	}
	// A failed member lookup keeps the tab without a count.
	g.err = errors.New("db down")
	body = c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, body, translationsTab)
	lacks(t, body, "Translations 2/3", "Translations 1/3")
}

func TestEntityTabsGroupsNilHasTwo(t *testing.T) {
	t.Parallel()
	c, _ := translationsRig(t, "en", "fr")
	c.a.deps.Content.Groups = nil
	body := c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	lacks(t, body, "Translations")
}
