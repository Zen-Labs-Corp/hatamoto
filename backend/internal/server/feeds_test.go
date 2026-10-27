package server

import (
	"bytes"
	"encoding/xml"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
)

var updateFeedGoldens = flag.Bool("update-feeds", false, "rewrite testdata/feeds goldens")

func feedFixture() (feedMeta, []feedItem) {
	meta := feedMeta{
		Title: "Acme Blog", Description: "News from Acme", Language: "en",
		Home: "https://acme.example/", RSSURL: "https://acme.example/feed.xml", AtomURL: "https://acme.example/atom.xml",
	}
	items := []feedItem{
		{
			Title: "Second & best", ID: "urn:uuid:00000000-0000-0000-0000-000000000002", URL: "https://acme.example/blog/second",
			Summary: "The <b>second</b> post.", Author: "Ada",
			ContentHTML: `<p>Hello <a href="https://acme.example/blog/first">first</a> & ]]> done.</p>`,
			Published:   time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC), Updated: time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC),
		},
		{
			Title: "First", ID: "urn:uuid:00000000-0000-0000-0000-000000000001", URL: "https://acme.example/blog/first",
			ContentHTML: "<p>One</p>",
			Published:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Updated: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	return meta, items
}

func checkFeedGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "feeds", name)
	if *updateFeedGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden %s mismatch (run with -update)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestFeedGoldens(t *testing.T) {
	meta, items := feedFixture()
	checkFeedGolden(t, "feed.rss.xml", buildRSS(meta, items))
	checkFeedGolden(t, "feed.atom.xml", buildAtom(meta, items))
	checkFeedGolden(t, "feed-empty.rss.xml", buildRSS(meta, nil))
	checkFeedGolden(t, "feed-empty.atom.xml", buildAtom(meta, nil))
}

func TestFeedItemsLocateAndAbsolutize(t *testing.T) {
	t.Parallel()
	pub := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mk := func(slug, status, attrs string) *entity.Entity {
		return &entity.Entity{ID: uuid.New(), Name: slug, Slug: slug, Status: entity.EntityStatus(status), Attributes: []byte(attrs), PublishedAt: &pub}
	}
	posts := []*entity.Entity{
		mk("a", "published", `{"title":"Alpha","content":"<p>See <a data-entity-id=\"t1\" href=\"/blog/old\">b</a> <img src=\"/media/x.png\"></p>","author":"Ada"}`),
		mk("draft", "draft", `{}`),
		mk("nourl", "published", `{}`),
	}
	locate := func(e *entity.Entity) string {
		if e.Slug == "nourl" {
			return ""
		}
		return "/blog/" + e.Slug
	}
	got := feedItems(posts, "https://x.io", locate, map[string]string{"t1": "/blog/new"}, nil)
	if len(got) != 1 {
		t.Fatalf("items %+v", got)
	}
	it := got[0]
	if it.Title != "Alpha" || it.URL != "https://x.io/blog/a" || it.Author != "Ada" || it.Summary != "See b" {
		t.Errorf("item %+v", it)
	}
	for _, want := range []string{`href="https://x.io/blog/new"`, `src="https://x.io/media/x.png"`} {
		if !strings.Contains(it.ContentHTML, want) {
			t.Errorf("missing %s in %s", want, it.ContentHTML)
		}
	}
}

func TestAbsolutizeHTMLLeavesOthersAlone(t *testing.T) {
	t.Parallel()
	in := `<p><a href="//cdn.x/y">p</a> <a href="https://z">q</a> <a href="#f">r</a> &amp; text</p>`
	if got := absolutizeHTML(in, "https://x.io"); got != in {
		t.Errorf("changed: %s", got)
	}
}

func TestFeedItemsSkipNoIndexAndStripControlChars(t *testing.T) {
	t.Parallel()
	pub := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mk := func(slug, attrs, meta string) *entity.Entity {
		return &entity.Entity{ID: uuid.New(), Name: slug, Slug: slug, Status: entity.StatusPublished, Attributes: []byte(attrs), Metadata: []byte(meta), PublishedAt: &pub}
	}
	posts := []*entity.Entity{
		mk("ok", `{"content":"<p>a\u000bb\u0008c</p>","author_name":"Bo"}`, `{}`),
		mk("meta", `{}`, `{"no_index":true}`),
		mk("attr", `{"no_index":true}`, `{}`),
		mk("robots", `{"seo":{"robots":"noindex,follow"}}`, `{}`),
	}
	got := feedItems(posts, "https://x.io", func(e *entity.Entity) string { return "/blog/" + e.Slug }, nil, nil)
	if len(got) != 1 || got[0].Author != "Bo" || !strings.HasPrefix(got[0].ID, "urn:uuid:") {
		t.Fatalf("items %+v", got)
	}
	meta, _ := feedFixture()
	for name, body := range map[string][]byte{"rss": buildRSS(meta, got), "atom": buildAtom(meta, got)} {
		d := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := d.Token(); err != nil {
				if err != io.EOF {
					t.Fatalf("%s invalid XML: %v", name, err)
				}
				break
			}
		}
	}
}

func TestFeedItemsUsePersonAndOrganisationBylines(t *testing.T) {
	t.Parallel()
	pub := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mk := func(slug, attrs string) *entity.Entity {
		return &entity.Entity{ID: uuid.New(), Name: slug, Slug: slug, Status: entity.StatusPublished, Attributes: []byte(attrs), PublishedAt: &pub}
	}
	byPerson, byOrg, plain := mk("p", `{"author":"Old Name"}`), mk("o", `{"author":"ignored"}`), mk("t", `{"author":"Plain Text"}`)
	personID := uuid.New()
	b := &bylines{
		credits: map[uuid.UUID]person.Credit{byPerson.ID: {PersonID: &personID}, byOrg.ID: {Org: true}},
		people:  map[uuid.UUID]*person.Person{personID: {ID: personID, Name: "Ada Lovelace"}},
		org:     "Acme Org",
	}
	got := feedItems([]*entity.Entity{byPerson, byOrg, plain}, "https://x.io", func(e *entity.Entity) string { return "/blog/" + e.Slug }, nil,
		b.creditedNames([]uuid.UUID{byPerson.ID, byOrg.ID, plain.ID}))
	want := []string{"Ada Lovelace", "Acme Org", "Plain Text"}
	for i, it := range got {
		if it.Author != want[i] {
			t.Errorf("item %d author = %q, want %q", i, it.Author, want[i])
		}
	}
}
