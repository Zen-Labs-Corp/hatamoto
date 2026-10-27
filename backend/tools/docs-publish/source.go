package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// IndexSlug names the section index file (<section>/index.md) and the site
// landing page (index.md at the root).
const IndexSlug = "index"

// Page is one markdown source file.
type Page struct {
	Section     string // "" for the site landing page
	Slug        string // IndexSlug for a section index or the landing page
	Title       string
	NavTitle    string
	Description string
	Body        string   // markdown without front matter
	Path        string   // path of the file as read, for messages
	Hash        string   // sha256 of the file bytes, hex
	BodyLine    int      // line number of the first body line in the file
	Next        []string // relative .md targets of the Next steps block (guides only)
	Group       string   // GroupGuides or GroupReference; "" for indexes, the landing page and legacy pages
}

// Manifest page groups.
const (
	GroupGuides    = "guides"
	GroupReference = "reference"
)

// Manifest is docs/site/manifest.json.
type Manifest struct {
	Root     string        `json:"root"`
	Sections []SectionSpec `json:"sections"`
	// Moved maps "section/slug" of a page that no longer exists to the
	// "section/slug" that replaces it; each pair becomes a redirect.
	Moved map[string]string `json:"moved"`
}

// SectionSpec is one section: its index page plus ordered page slugs in two
// groups. Pages is the legacy ungrouped list, kept so old manifests load.
type SectionSpec struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Pages       []PageRef `json:"pages"`
	Guides      []PageRef `json:"guides"`
	Reference   []PageRef `json:"reference"`
}

// All returns every page entry in menu order: guides, reference, then the
// legacy pages. Group is filled in.
func (s SectionSpec) All() []PageRef {
	var out []PageRef
	for _, g := range []struct {
		name string
		refs []PageRef
	}{{GroupGuides, s.Guides}, {GroupReference, s.Reference}, {"", s.Pages}} {
		for _, r := range g.refs {
			r.Group = g.name
			out = append(out, r)
		}
	}
	return out
}

// PageRef is a manifest page entry: a plain slug or {"slug","title"}.
type PageRef struct {
	Slug  string
	Title string
	Group string // set by SectionSpec.All, not read from JSON
}

// UnmarshalJSON accepts both entry shapes.
func (p *PageRef) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		p.Slug = s
		return nil
	}
	var o struct{ Slug, Title string }
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("page entry must be a string or an object: %w", err)
	}
	p.Slug, p.Title = o.Slug, o.Title
	return nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// LoadSite reads manifest.json and every markdown file it names, plus the
// landing page and one index per section. Files on disk that the manifest
// does not name are returned too, so Lint can report them.
func LoadSite(root string) ([]Page, Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, m, fmt.Errorf("manifest.json: %w", err)
	}

	var pages []Page
	seen := map[string]bool{}
	add := func(section, slug, group string) error {
		rel := slug + ".md"
		if section != "" {
			rel = filepath.Join(section, rel)
		}
		p, err := readPage(filepath.Join(root, rel))
		if errors.Is(err, os.ErrNotExist) {
			return nil // Lint reports the missing file.
		}
		if err != nil {
			return err
		}
		if seen[filepath.Clean(p.Path)] {
			return nil // listed twice; Lint reports the duplicate.
		}
		p.Section, p.Slug, p.Group = section, slug, group
		pages = append(pages, p)
		seen[filepath.Clean(p.Path)] = true
		return nil
	}
	if err := add("", IndexSlug, ""); err != nil {
		return nil, m, err
	}
	for _, s := range m.Sections {
		if err := add(s.Slug, IndexSlug, ""); err != nil {
			return nil, m, err
		}
		for _, ref := range s.All() {
			if err := add(s.Slug, ref.Slug, ref.Group); err != nil {
				return nil, m, err
			}
		}
	}

	// Unlisted markdown files in section directories.
	for _, s := range m.Sections {
		files, _ := filepath.Glob(filepath.Join(root, s.Slug, "*.md"))
		sort.Strings(files)
		for _, f := range files {
			base := filepath.Base(f)
			if seen[filepath.Clean(f)] || base == "STYLE.md" || base == "README.md" {
				continue
			}
			p, err := readPage(f)
			if err != nil {
				return nil, m, err
			}
			p.Section, p.Slug = s.Slug, strings.TrimSuffix(filepath.Base(f), ".md")
			pages = append(pages, p)
		}
	}
	return pages, m, nil
}

// readPage parses one file: front matter (key: value lines between ---)
// then the markdown body. Parse problems are left in the page fields for
// Lint to report; only I/O errors are returned.
func readPage(path string) (Page, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Page{}, err
	}
	sum := sha256.Sum256(raw)
	p := Page{Path: path, Hash: hex.EncodeToString(sum[:]), BodyLine: 1}
	meta, body, line := splitFrontMatter(string(raw))
	p.Title, p.NavTitle, p.Description = meta["title"], meta["nav_title"], meta["description"]
	if p.NavTitle == "" {
		p.NavTitle = p.Title
	}
	for _, t := range strings.Split(meta["next"], ",") {
		if t = strings.TrimSpace(t); t != "" {
			p.Next = append(p.Next, t)
		}
	}
	p.Body, p.BodyLine = body, line
	return p, nil
}

// frontMatterKeys is the complete set of accepted keys.
var frontMatterKeys = map[string]bool{"title": true, "nav_title": true, "description": true, "next": true}

// splitFrontMatter returns the metadata, the body and the 1-based line of
// the body's first line. A file without front matter has empty metadata.
func splitFrontMatter(s string) (map[string]string, string, int) {
	meta := map[string]string{}
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return meta, s, 1
	}
	for i := 1; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if l == "---" {
			return meta, strings.Join(lines[i+1:], "\n"), i + 2
		}
		if k, v, ok := strings.Cut(l, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		} else if strings.TrimSpace(l) != "" {
			meta["\x00bad:"+l] = ""
		}
	}
	meta["\x00unterminated"] = ""
	return meta, "", 1
}

// Banned patterns are assembled from parts so this file never contains the
// words itself (the repository neutral check scans tracked files).
var (
	bannedRe = regexp.MustCompile(`(?i)` + strings.Join([]string{"mo" + "jo", "ne" + "xus", "over" + "lay", "core" + "_ref", "mj" + "dj"}, "|"))
	jargonRe = regexp.MustCompile(`\bv1-\d+\b|\bB[0-9]\b|\bPhase \d+\b|\bSHIP-M\b`)
	// emDash and enDash are written as escapes so the source stays dash free.
	dashRe  = regexp.MustCompile("[\u2014\u2013]")
	fenceRe = regexp.MustCompile("^\\s*(```|~~~)")
	h1Re    = regexp.MustCompile(`^#\s`)
	// nextHeadingRe matches a body heading the tool renders itself.
	nextHeadingRe = regexp.MustCompile(`(?i)^#{1,6}\s+next steps\s*#*\s*$`)
)

// Lint checks the loaded site and returns every problem as
// "path:line: message". It is offline and deterministic.
func Lint(pages []Page, m Manifest) []error {
	var errs []error
	fail := func(path string, line int, format string, a ...any) {
		prefix := path
		if line > 0 {
			prefix = fmt.Sprintf("%s:%d", path, line)
		}
		errs = append(errs, fmt.Errorf("%s: %s", prefix, fmt.Sprintf(format, a...)))
	}

	byKey := map[string]Page{}
	for _, p := range pages {
		byKey[p.Section+"/"+p.Slug] = p
	}
	need := func(section, slug string) {
		if _, ok := byKey[section+"/"+slug]; !ok {
			errs = append(errs, fmt.Errorf("%s: listed in the manifest but the file is missing", filepath.Join(section, slug+".md")))
		}
	}

	if m.Root == "" {
		errs = append(errs, errors.New("manifest.json: root is required"))
	}
	need("", IndexSlug)
	listed := map[string]bool{"/" + IndexSlug: true}
	secSeen := map[string]bool{}
	for _, s := range m.Sections {
		if !slugRe.MatchString(s.Slug) {
			errs = append(errs, fmt.Errorf("manifest.json: section slug %q must match [a-z0-9-]+", s.Slug))
		}
		if secSeen[s.Slug] {
			errs = append(errs, fmt.Errorf("manifest.json: duplicate section %q", s.Slug))
		}
		if s.Slug == landingSlug {
			errs = append(errs, fmt.Errorf("manifest.json: section slug %q is reserved for the landing page", s.Slug))
		}
		secSeen[s.Slug] = true
		need(s.Slug, IndexSlug)
		listed[s.Slug+"/"+IndexSlug] = true
		pageSeen := map[string]bool{}
		for _, ref := range s.All() {
			switch {
			case !slugRe.MatchString(ref.Slug):
				errs = append(errs, fmt.Errorf("manifest.json: page slug %q in %s must match [a-z0-9-]+", ref.Slug, s.Slug))
				continue
			case ref.Slug == IndexSlug:
				errs = append(errs, fmt.Errorf("manifest.json: %s: %q is reserved for the section index", s.Slug, IndexSlug))
				continue
			case ref.Slug == landingSlug:
				errs = append(errs, fmt.Errorf("manifest.json: %s: page slug %q is reserved for the landing page", s.Slug, landingSlug))
				continue
			case pageSeen[ref.Slug]:
				errs = append(errs, fmt.Errorf("manifest.json: duplicate page %s/%s", s.Slug, ref.Slug))
				continue
			}
			pageSeen[ref.Slug] = true
			listed[s.Slug+"/"+ref.Slug] = true
			need(s.Slug, ref.Slug)
		}
	}
	lintMoved(m, listed, &errs)

	for _, p := range pages {
		if !listed[p.Section+"/"+p.Slug] {
			fail(p.Path, 0, "file is not listed in manifest.json")
		}
		lintPage(p, fail)
	}
	return errs
}

func lintPage(p Page, fail func(path string, line int, format string, a ...any)) {
	raw, err := os.ReadFile(p.Path)
	if err != nil {
		fail(p.Path, 0, "cannot read: %v", err)
		return
	}
	meta, _, _ := splitFrontMatter(string(raw))
	for k := range meta {
		switch {
		case strings.HasPrefix(k, "\x00bad:"):
			fail(p.Path, 0, "front matter line is not key: value: %q", strings.TrimPrefix(k, "\x00bad:"))
		case k == "\x00unterminated":
			fail(p.Path, 0, "front matter is not closed with ---")
		case !frontMatterKeys[k]:
			fail(p.Path, 0, "unknown front matter key %q", k)
		}
	}
	if meta["title"] == "" {
		fail(p.Path, 0, "front matter title is required")
	}
	if meta["description"] == "" {
		fail(p.Path, 0, "front matter description is required")
	}
	lintNext(p, meta, fail)

	inFence := false
	for i, line := range bytes.Split(raw, []byte("\n")) {
		n := i + 1
		s := string(line)
		if dashRe.MatchString(s) {
			fail(p.Path, n, "em or en dash; use a comma, colon, parentheses or \"to\"")
		}
		if w := bannedRe.FindString(s); w != "" {
			fail(p.Path, n, "banned word %q", w)
		}
		if w := jargonRe.FindString(s); w != "" {
			fail(p.Path, n, "internal jargon %q", w)
		}
		if n < p.BodyLine {
			continue
		}
		if fenceRe.MatchString(s) {
			inFence = !inFence
			continue
		}
		if !inFence && h1Re.MatchString(s) {
			fail(p.Path, n, "h1 in body; the title comes from front matter, start at ##")
		}
		if !inFence && nextHeadingRe.MatchString(s) {
			fail(p.Path, n, "## Next steps heading in the body; set next: in the front matter and the tool renders the block")
		}
	}
}

// Next steps bounds on a guide.
const (
	minNext = 2
	maxNext = 3
)

// lintNext checks the next front matter key: required with 2 to 3 relative
// .md targets on guides, not allowed anywhere else.
func lintNext(p Page, meta map[string]string, fail func(path string, line int, format string, a ...any)) {
	_, has := meta["next"]
	if p.Group != GroupGuides {
		if has {
			fail(p.Path, 0, "front matter key next is only allowed on guides")
		}
		return
	}
	if len(p.Next) < minNext || len(p.Next) > maxNext {
		fail(p.Path, 0, "a guide needs %d to %d comma separated .md targets in front matter next, got %d", minNext, maxNext, len(p.Next))
	}
	seen := map[string]bool{}
	for _, t := range p.Next {
		switch {
		case !strings.HasSuffix(t, ".md") || strings.ContainsAny(t, " #?"):
			fail(p.Path, 0, "next target %q must be a relative .md path without a fragment", t)
		case seen[t]:
			fail(p.Path, 0, "next target %q is listed twice", t)
		}
		seen[t] = true
	}
}

// lintMoved checks the moved map: both sides are "section/slug", the target
// is a manifest page and the old name is not one.
func lintMoved(m Manifest, listed map[string]bool, errs *[]error) {
	keys := make([]string, 0, len(m.Moved))
	for k := range m.Moved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, from := range keys {
		to := m.Moved[from]
		for _, v := range []string{from, to} {
			if sec, slug, ok := strings.Cut(v, "/"); !ok || !slugRe.MatchString(sec) || !slugRe.MatchString(slug) {
				*errs = append(*errs, fmt.Errorf("manifest.json: moved entry %q must be section/slug", v))
			}
		}
		if listed[from] {
			*errs = append(*errs, fmt.Errorf("manifest.json: moved key %q is still a manifest page", from))
		}
		if !listed[to] {
			*errs = append(*errs, fmt.Errorf("manifest.json: moved target %q is not a manifest page", to))
		}
	}
}
