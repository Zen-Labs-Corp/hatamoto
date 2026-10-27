package main

import (
	"strings"
	"testing"
)

func contentSite(a, b string) ([]Page, Manifest) {
	m := Manifest{Root: "docs", Sections: []SectionSpec{{Slug: "s", Pages: []PageRef{{Slug: "a"}, {Slug: "b"}}}}}
	pages := []Page{
		{Slug: IndexSlug, Path: "index.md", Body: "Home.\n"},
		{Section: "s", Slug: IndexSlug, Path: "s/index.md", Body: "Index.\n"},
		{Section: "s", Slug: "a", Path: "s/a.md", Body: a},
		{Section: "s", Slug: "b", Path: "s/b.md", Body: b},
	}
	return pages, m
}

func TestLintContentAcceptsGoodLinksAndAnchors(t *testing.T) {
	t.Parallel()
	pages, m := contentSite(
		"## Intro\n\nSee [b](b.md#hello-world-2) and [up](#intro).\n",
		"## Hello, World!\n\n## `Hello` world\n",
	)
	if errs := LintContent(pages, m); len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestLintContentRejects(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, a, b, want string }{
		{"missing page", "[x](gone.md)\n", "", `no page in the manifest for "gone.md"`},
		{"missing anchor", "[x](b.md#nope)\n", "## Real\n", "s/b.md#nope"},
		{"anchor of the first duplicate heading is not numbered", "[x](b.md#real-2)\n", "## Real\n", "s/b.md#real-2"},
		{"same page anchor", "## One\n\n[x](#two)\n", "", "#two"},
		{"reserved id is skipped", "[x](b.md#toc)\n", "## TOC\n", "s/b.md#toc"},
		{"raw html", "<kbd>x</kbd>\n", "", "s/a.md"},
	}
	for _, c := range cases {
		pages, m := contentSite(c.a, c.b)
		errs := LintContent(pages, m)
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), c.want) {
			t.Errorf("%s: got %v, want one error containing %q", c.name, errs, c.want)
		}
	}
}

func TestHeadingIDsMatchLayoutRules(t *testing.T) {
	t.Parallel()
	got := headingIDs("<h2>Zed</h2><h3>A &amp; B</h3><h2>Zed</h2><h2><code>Zed</code></h2><h3>!!!</h3><h3>!!!</h3><h4>Skip</h4><h2>TOC</h2>")
	for _, id := range []string{"zed", "a-b", "zed-2", "zed-3", "section", "section-2", "toc-2"} {
		if !got[id] {
			t.Errorf("missing id %q in %v", id, got)
		}
	}
	if got["skip"] || got["toc"] {
		t.Errorf("h4 and reserved ids must not appear: %v", got)
	}
}

func nextSite(next ...string) ([]Page, Manifest) {
	pages, m := contentSite("## Intro\n", "## Other\n")
	pages[2].Next = next
	pages[3].NavTitle, pages[3].Title = "Tom & Jerry", "Long title"
	pages[1].Title = "Section"
	return pages, m
}

func TestNextStepsHTMLShape(t *testing.T) {
	t.Parallel()
	pages, m := nextSite("b.md", "index.md")
	render, err := renderer(pages, m)
	if err != nil {
		t.Fatal(err)
	}
	out, err := render(pages[2])
	if err != nil {
		t.Fatal(err)
	}
	want := `<h2>Next steps</h2><ul class="hm-next-steps"><li><a href="/docs/s/b">Tom &amp; Jerry</a></li><li><a href="/docs/s">Section</a></li></ul>`
	if !strings.HasSuffix(out, want) || !strings.Contains(out, "Intro") {
		t.Fatalf("got %q, want suffix %q", out, want)
	}
	plain, err := render(pages[3])
	if err != nil || strings.Contains(plain, "Next steps") {
		t.Fatalf("a page without next must not get the block: %q, %v", plain, err)
	}
}

func TestNextStepsUnknownTargetIsLintError(t *testing.T) {
	t.Parallel()
	pages, m := nextSite("b.md", "gone.md")
	errs := LintContent(pages, m)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `no page in the manifest for "gone.md"`) {
		t.Fatalf("got %v", errs)
	}
}

func TestNextStepsHeadingHasAnID(t *testing.T) {
	t.Parallel()
	pages, m := nextSite("b.md", "index.md")
	pages[2].Body = "## Intro\n\nJump to [the end](#next-steps).\n"
	if errs := LintContent(pages, m); len(errs) != 0 {
		t.Fatalf("the rendered heading must be an anchor target: %v", errs)
	}
}

func TestLintContentAcceptsBlocksInListItems(t *testing.T) {
	t.Parallel()
	pages, m := contentSite("1. Run:\n\n   ```bash\n   make x\n   ```\n2. See:\n\n   | A |\n   |---|\n   | 1 |\n", "## B\n")
	if errs := LintContent(pages, m); len(errs) != 0 {
		t.Fatal(errs)
	}
}
