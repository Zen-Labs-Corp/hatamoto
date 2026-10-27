package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// realSite loads the real docs/site, so these tests fail when a page that
// used to exist disappears without a redirect.
func realSite(t *testing.T) ([]Page, Manifest) {
	t.Helper()
	pages, m, err := LoadSite("../../../docs/site")
	if err != nil {
		t.Fatal(err)
	}
	if errs := Lint(pages, m); len(errs) > 0 {
		t.Fatal(errs)
	}
	return pages, m
}

// pageURL is the site path a page is published at.
func pageURL(root string, p Page) string {
	if p.Section == "" {
		return "/" + strings.Trim(root, "/")
	}
	return sitePath(root, Key(p))
}

// TestOldURLsStillServed checks that every address of the previous docs layout
// (testdata/old-urls.txt, taken from the manifest on main before the layout
// change) is still produced by the new manifest or redirected by a moved entry.
func TestOldURLsStillServed(t *testing.T) {
	t.Parallel()
	pages, m := realSite(t)

	live := map[string]bool{}
	for _, p := range pages {
		live[pageURL(m.Root, p)] = true
	}
	redirected := map[string]string{}
	for _, r := range RedirectsFromMoved(m) {
		redirected[r.Source] = r.Target
	}

	f, err := os.Open("testdata/old-urls.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		url := strings.TrimSpace(sc.Text())
		if url == "" {
			continue
		}
		n++
		if live[url] {
			continue
		}
		if target, ok := redirected[url]; ok {
			if !live[target] {
				t.Errorf("%s is redirected to %s, which is not a page", url, target)
			}
			continue
		}
		t.Errorf("old URL %s is neither a page of the new manifest nor a moved entry", url)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 82 {
		t.Fatalf("old-urls.txt lists %d URLs, want 82 (landing, 9 sections, 72 pages)", n)
	}
}

// TestRealSiteEndToEnd publishes the real docs/site, plus one moved entry, to
// the fake server: the dry run writes nothing, --apply writes pages, the
// grouped menu and the redirect, and a second --apply writes nothing at all.
func TestRealSiteEndToEnd(t *testing.T) {
	t.Parallel()
	pages, m := realSite(t)
	m.Moved = map[string]string{"themes/retired-page": "themes/layout"}
	if errs := Lint(pages, m); len(errs) > 0 {
		t.Fatal(errs)
	}
	s := newStack(t)
	c, mc := s.clients()
	ctx := context.Background()

	var out bytes.Buffer
	if err := publish(ctx, c, mc, fakeSchema, false, pages, m, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if len(s.writes()) != 0 || len(s.items) != 0 || len(s.redirects) != 0 {
		t.Fatalf("dry run wrote: %v, %d menu items, %d redirects", s.writes(), len(s.items), len(s.redirects))
	}
	for _, e := range s.events {
		if e != "MCP menus_get" && e != "MCP schemas_get" && e != "MCP redirects_list" {
			t.Errorf("dry run event %q", e)
		}
	}
	// Counts come from docs/site/manifest.json (as loaded into m), not from
	// numbers pinned here: the landing page, one index page per section and
	// every page the sections list.
	wantPages, wantGuides, wantReference := 1+len(m.Sections), 0, 0
	for _, sec := range m.Sections {
		wantPages += len(sec.All())
		if len(sec.Guides) > 0 {
			wantGuides++
		}
		if len(sec.Reference) > 0 {
			wantReference++
		}
	}
	if len(pages) != wantPages {
		t.Fatalf("the site loads %d pages, the manifest lists %d", len(pages), wantPages)
	}
	for _, want := range []string{fmt.Sprintf("plan: %d create", wantPages), "redirect create /docs/themes/retired-page -> /docs/themes/layout", "dry run: nothing written"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := publish(ctx, c, mc, fakeSchema, true, pages, m, &out); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	if len(s.ents) != len(pages) {
		t.Errorf("%d entities, want %d", len(s.ents), len(pages))
	}
	for _, e := range s.ents {
		if e.Status != "published" {
			t.Errorf("%s not published", e.Slug)
		}
	}
	labels := map[string]int{}
	for _, it := range s.items {
		if it.EntityID == "" {
			labels[it.Label]++
		}
	}
	if labels["Guides"] != wantGuides || labels["Reference"] != wantReference {
		t.Errorf("group labels in the menu: %v", labels)
	}
	if len(s.redirects) != 1 || s.redirects[0].SourcePath != "/docs/themes/retired-page" || s.redirects[0].TargetURL != "/docs/themes/layout" || s.redirects[0].StatusCode != 301 {
		t.Errorf("redirects: %+v", s.redirects)
	}

	s.events = nil
	nItems, nRedirects := len(s.items), len(s.redirects)
	out.Reset()
	if err := publish(ctx, c, mc, fakeSchema, true, pages, m, &out); err != nil {
		t.Fatalf("second apply: %v\n%s", err, out.String())
	}
	for _, e := range s.events {
		if strings.HasPrefix(e, "REST") || e == "MCP menus_update" || e == "MCP redirects_create" {
			t.Errorf("write on the second apply: %s", e)
		}
	}
	if len(s.items) != nItems || len(s.redirects) != nRedirects {
		t.Errorf("second apply changed the menu or the redirects")
	}
	if !strings.Contains(out.String(), "redirect exists /docs/themes/retired-page -> /docs/themes/layout") {
		t.Errorf("second apply output:\n%s", out.String())
	}
}
