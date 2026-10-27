package main

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// LintContent runs the converter over every page and checks that each
// relative .md link and each #anchor points at something that exists, so the
// offline lint rejects what a publish would reject. Anchors are checked
// against the heading ids the site layout generates.
func LintContent(pages []Page, m Manifest) []error {
	type link struct {
		from      Page
		key, frag string
	}
	var links []link
	render, err := newRenderer(pages, m, func(from Page, key, frag string) {
		if frag != "" {
			links = append(links, link{from, key, frag})
		}
	})
	if err != nil {
		return []error{err}
	}

	var errs []error
	ids := map[string]map[string]bool{} // file key -> heading ids
	for _, p := range pages {
		out, err := render(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Path, err))
			continue
		}
		set := headingIDs(out)
		ids[fileKey(p)] = set
		for _, frag := range sameAnchorRe.FindAllStringSubmatch(out, -1) {
			if id := html.UnescapeString(frag[1]); id != "" && !set[id] {
				errs = append(errs, fmt.Errorf("%s: link #%s: no heading with that id in this page", p.Path, id))
			}
		}
	}
	for _, l := range links {
		set, ok := ids[l.key]
		if ok && !set[l.frag] {
			errs = append(errs, fmt.Errorf("%s: link %s#%s: no heading with that id in the target page", l.from.Path, l.key, l.frag))
		}
	}
	return errs
}

var (
	renderedHeadingRe = regexp.MustCompile(`(?s)<h([23])>(.*?)</h[23]>`)
	tagRe             = regexp.MustCompile(`<[^>]*>`)
	sameAnchorRe      = regexp.MustCompile(`<a href="#([^"]*)"`)
)

// reservedIDs are ids the layouts own; headings never take them (see
// docReservedIDs in backend/internal/render/doctoc.go).
var reservedIDs = map[string]bool{"main-content": true, "toc": true, "top": true}

// headingIDs returns the ids the layout gives the h2 and h3 headings of the
// converted body: the slug of the heading text, numbered from -2 on a clash.
// It mirrors docBody in backend/internal/render/doctoc.go; the converter
// never emits ids itself, so no existing id needs to be honoured.
func headingIDs(body string) map[string]bool {
	used := map[string]bool{}
	for id := range reservedIDs {
		used[id] = true
	}
	got := map[string]bool{}
	for _, h := range renderedHeadingRe.FindAllStringSubmatch(body, -1) {
		text := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(h[2], ""))), " ")
		base := slugifyHeading(text)
		id := base
		for n := 2; used[id]; n++ {
			id = base + "-" + strconv.Itoa(n)
		}
		used[id] = true
		got[id] = true
	}
	return got
}

// slugifyHeading lowercases ASCII letters and digits and joins runs of
// anything else with single hyphens. An empty result is "section". Same
// rules as the function of that name in backend/internal/render/doctoc.go.
func slugifyHeading(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}
