package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodFM = "---\ntitle: T\ndescription: D.\n---\n\n## Body\n\ntext\n"

// site writes files (relative path to content) under a temp root.
func site(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const manifest = `{"root":"docs","sections":[{"slug":"a","title":"A","description":"d","pages":["one",{"slug":"two","title":"Two"}]}]}`

func baseFiles() map[string]string {
	return map[string]string{
		"manifest.json": manifest,
		"index.md":      goodFM,
		"a/index.md":    goodFM,
		"a/one.md":      goodFM,
		"a/two.md":      goodFM,
	}
}

func lintAll(t *testing.T, files map[string]string) []string {
	t.Helper()
	pages, m, err := LoadSite(site(t, files))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range Lint(pages, m) {
		out = append(out, e.Error())
	}
	return out
}

func wantOne(t *testing.T, errs []string, sub string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return
		}
	}
	t.Fatalf("no error containing %q in %q", sub, errs)
}

func TestLoadSiteClean(t *testing.T) {
	t.Parallel()
	root := site(t, baseFiles())
	pages, m, err := LoadSite(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 4 || len(m.Sections) != 1 || len(m.Sections[0].Pages) != 2 {
		t.Fatalf("pages=%d sections=%+v", len(pages), m.Sections)
	}
	if errs := Lint(pages, m); len(errs) != 0 {
		t.Fatalf("unexpected lint errors: %v", errs)
	}
	p := pages[0]
	if p.Title != "T" || p.NavTitle != "T" || p.Description != "D." || p.Hash == "" || p.BodyLine != 5 {
		t.Fatalf("bad page: %+v", p)
	}
	if !strings.HasPrefix(p.Body, "\n## Body") {
		t.Fatalf("body = %q", p.Body)
	}
}

func TestLintDash(t *testing.T) {
	t.Parallel()
	for _, d := range []string{"\u2014", "\u2013"} {
		f := baseFiles()
		f["a/one.md"] = goodFM + "bad " + d + " dash\n"
		errs := lintAll(t, f)
		wantOne(t, errs, "one.md:9: em or en dash")
	}
}

func TestLintBannedWord(t *testing.T) {
	t.Parallel()
	f := baseFiles()
	f["a/one.md"] = goodFM + "A Ne" + "xus here\n"
	wantOne(t, lintAll(t, f), "banned word")
}

func TestLintJargonAndH1(t *testing.T) {
	t.Parallel()
	f := baseFiles()
	f["a/one.md"] = goodFM + "see v1-12\n```sh\n# comment in code is fine\n```\n# Title\n"
	errs := lintAll(t, f)
	wantOne(t, errs, "internal jargon")
	wantOne(t, errs, "h1 in body")
	for _, e := range errs {
		if strings.Contains(e, ":11:") {
			t.Fatalf("h1 inside a fence flagged: %s", e)
		}
	}
}

func TestLintMissingFile(t *testing.T) {
	t.Parallel()
	f := baseFiles()
	delete(f, "a/two.md")
	wantOne(t, lintAll(t, f), "a/two.md: listed in the manifest but the file is missing")
}

func TestLintExtraFile(t *testing.T) {
	t.Parallel()
	f := baseFiles()
	f["a/stray.md"] = goodFM
	wantOne(t, lintAll(t, f), "stray.md: file is not listed in manifest.json")
}

func TestLintFrontMatter(t *testing.T) {
	t.Parallel()
	f := baseFiles()
	f["a/one.md"] = "---\ntitle: T\ncolour: red\n---\n"
	errs := lintAll(t, f)
	wantOne(t, errs, "unknown front matter key")
	wantOne(t, errs, "description is required")
	f["a/one.md"] = "## no front matter\n"
	wantOne(t, lintAll(t, f), "title is required")
}

func TestLintManifestSlugs(t *testing.T) {
	t.Parallel()
	f := baseFiles()
	f["manifest.json"] = `{"root":"docs","sections":[{"slug":"a","pages":["Bad_Slug","index","one","one"]}]}`
	errs := lintAll(t, f)
	wantOne(t, errs, "must match")
	wantOne(t, errs, "reserved")
	wantOne(t, errs, "duplicate page a/one")
}

func TestLoadSiteNoManifest(t *testing.T) {
	t.Parallel()
	if _, _, err := LoadSite(t.TempDir()); err == nil {
		t.Fatal("want error")
	}
}

func TestLintSectionNamedLandingSlug(t *testing.T) {
	t.Parallel()
	f := baseFiles()
	f["manifest.json"] = `{"root":"docs","sections":[{"slug":"docs","pages":[]}]}`
	f["docs/index.md"] = "---\ntitle: T\ndescription: D\n---\n"
	wantOne(t, lintAll(t, f), "reserved for the landing page")
}

func TestLintPageNamedLandingSlug(t *testing.T) {
	t.Parallel()
	f := baseFiles()
	f["manifest.json"] = `{"root":"docs","sections":[{"slug":"a","pages":["docs"]}]}`
	wantOne(t, lintAll(t, f), "page slug \"docs\" is reserved for the landing page")
}

const groupedManifest = `{"root":"docs","sections":[{"slug":"a","title":"A","description":"d","guides":["one","two"],"reference":["ref"]}],"moved":{"a/old":"a/one"}}`

func guideFM(next string) string {
	return "---\ntitle: T\ndescription: D.\nnext: " + next + "\n---\n\n## Body\n\ntext\n"
}

func groupedFiles() map[string]string {
	return map[string]string{
		"manifest.json": groupedManifest,
		"index.md":      goodFM,
		"a/index.md":    goodFM,
		"a/one.md":      guideFM("two.md, ref.md"),
		"a/two.md":      guideFM("one.md, ref.md"),
		"a/ref.md":      goodFM,
		"STYLE.md":      "not a page",
		"a/README.md":   "not a page",
	}
}

func TestGroupedManifestClean(t *testing.T) {
	t.Parallel()
	pages, m, err := LoadSite(site(t, groupedFiles()))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 5 {
		t.Fatalf("pages=%d", len(pages))
	}
	if errs := Lint(pages, m); len(errs) != 0 {
		t.Fatalf("unexpected lint errors: %v", errs)
	}
	for _, p := range pages {
		if p.Slug == "one" && (p.Group != GroupGuides || len(p.Next) != 2 || p.Next[1] != "ref.md") {
			t.Fatalf("bad guide: %+v", p)
		}
		if p.Slug == "ref" && p.Group != GroupReference {
			t.Fatalf("bad reference: %+v", p)
		}
	}
}

func TestLintGroupsNextAndMoved(t *testing.T) {
	t.Parallel()
	with := func(f map[string]string, k, v string) map[string]string { f[k] = v; return f }
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"guide without next", with(groupedFiles(), "a/one.md", goodFM), "a guide needs 2 to 3"},
		{"too many next", with(groupedFiles(), "a/one.md", guideFM("a.md, b.md, c.md, d.md")), "a guide needs 2 to 3"},
		{"bad next format", with(groupedFiles(), "a/one.md", guideFM("two.md, ref")), "relative .md path"},
		{"next on reference", with(groupedFiles(), "a/ref.md", guideFM("one.md, two.md")), "only allowed on guides"},
		{"next on index", with(groupedFiles(), "a/index.md", guideFM("one.md, two.md")), "only allowed on guides"},
		{"next steps heading", with(groupedFiles(), "a/two.md", guideFM("one.md, ref.md")+"\n## Next steps\n"), "Next steps heading"},
		{"duplicate across groups", with(groupedFiles(), "manifest.json", strings.Replace(groupedManifest, `"reference":["ref"]`, `"reference":["one"]`, 1)), "duplicate page a/one"},
		{"moved target missing", with(groupedFiles(), "manifest.json", strings.Replace(groupedManifest, `"a/old":"a/one"`, `"a/old":"a/nope"`, 1)), `moved target "a/nope"`},
		{"moved key is a page", with(groupedFiles(), "manifest.json", strings.Replace(groupedManifest, `"a/old":"a/one"`, `"a/two":"a/one"`, 1)), `moved key "a/two"`},
		{"moved bad format", with(groupedFiles(), "manifest.json", strings.Replace(groupedManifest, `"a/old":"a/one"`, `"/docs/old":"a/one"`, 1)), "must be section/slug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantOne(t, lintAll(t, tt.files), tt.want)
		})
	}
}
