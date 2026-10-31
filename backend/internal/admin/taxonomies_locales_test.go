package admin

import (
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/taxonomy"
)

// unbalancedTags lists the container tags of body whose end tags do not pair
// with their start tags, in document order. The HTML parser repairs a stray
// end tag silently, so only the token stream shows it.
func unbalancedTags(body string) []string {
	watch := map[string]bool{"section": true, "form": true, "ul": true, "li": true, "footer": true, "table": true, "div": true}
	var stack, bad []string
	z := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF {
				bad = append(bad, z.Err().Error())
			}
			return append(bad, stack...)
		}
		name, _ := z.TagName()
		tag := string(name)
		if !watch[tag] {
			continue
		}
		switch tt {
		case html.StartTagToken:
			stack = append(stack, tag)
		case html.EndTagToken:
			if n := len(stack); n == 0 || stack[n-1] != tag {
				bad = append(bad, "stray </"+tag+">")
			} else {
				stack = stack[:n-1]
			}
		}
	}
}

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

func TestTermLocalesBlockIsBalanced(t *testing.T) {
	t.Parallel()
	c, tg, id := termLocalesRig(t, "en", "pt-BR", "de-DE")
	tid := c.terms.terms[0].ID
	tg.locales[tid] = "en"
	tg.members[tid] = []taxonomy.TermMember{{ID: tid, Locale: "en", Name: "Guides"}, {ID: uuid.New(), Locale: "pt-BR", Name: "Guias"}}
	tg.candidates = []taxonomy.TermMember{{ID: uuid.New(), Locale: "de-DE", Name: "Anleitungen"}}
	body := c.get(t, "owner-token", "/admin/settings/taxonomies/terms/"+id, "")
	if bad := unbalancedTags(body); len(bad) > 0 {
		t.Errorf("term page tags do not pair: %v", bad)
	}
	i := strings.Index(body, `aria-labelledby="term-locales-h"`)
	j := strings.Index(body[i:], "</section>")
	if i < 0 || j < 0 || !strings.Contains(body[i:i+j], "Guias") || !strings.Contains(body[i:i+j], "Create") {
		t.Errorf("the Languages section must hold the copies list")
	}
}

func TestTermUnlinkChecksMembership(t *testing.T) {
	t.Parallel()
	c, tg, id := termLocalesRig(t, "en", "pt-BR")
	tid := c.terms.terms[0].ID
	member, stranger := uuid.New(), uuid.New()
	tg.members[tid] = []taxonomy.TermMember{{ID: tid, Locale: "en"}, {ID: member, Locale: "pt-BR"}}
	path := "/admin/settings/taxonomies/terms/" + id + "/unlink"

	rec := c.post(path, url.Values{"term": {stranger.String()}})
	if rec.Code != 303 || len(tg.unlinked) != 0 {
		t.Fatalf("stranger: %d, unlinked %v", rec.Code, tg.unlinked)
	}
	has(t, c.next(rec, "/admin/settings/taxonomies/terms/"+id), "That term is not linked to this one.")

	if rec := c.post(path, url.Values{"term": {member.String()}}); rec.Code != 303 || len(tg.unlinked) != 1 || tg.unlinked[0] != member {
		t.Errorf("member: %d, unlinked %v", rec.Code, tg.unlinked)
	}

	tg.err = taxonomy.ErrTermNotFound
	rec = c.post(path, url.Values{"term": {id}})
	if rec.Code != 303 {
		t.Fatalf("missing term: %d, want a flash redirect", rec.Code)
	}
}
