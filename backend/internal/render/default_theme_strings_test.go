package render

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// hardcodedAllowlist lists the visible runs the default theme may keep as
// literals, keyed "<path>: <text>", each with the reason it is not a string.
var hardcodedAllowlist = map[string]string{
	"partials/header.html: 旗本":         "decorative logo mark, hidden from assistive tech",
	"sections/contact_form.html: Yes":  "machine value submitted by the consent checkbox",
	"sections/contact_form.html: true": "machine value submitted by the checkbox",
}

var (
	guardActionRe      = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	guardBlockRe       = regexp.MustCompile(`(?s)<!--.*?-->|<script\b.*?</script>|<style\b.*?</style>`)
	guardTagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	guardVisibleAttrRe = regexp.MustCompile(`\b(?:aria-label|placeholder|title|alt|value)="([^"]*)"`)
	guardEntityRe      = regexp.MustCompile(`&#?[A-Za-z0-9]+;`)
	guardWordRe        = regexp.MustCompile(`\p{L}{2,}`)
	guardUsedKeyRe     = regexp.MustCompile(`\bt\s+[.$][\w.]*\s+"([a-z][a-z0-9_]*)"`)
)

// mask blanks every match of re with spaces, keeping newlines so offsets and
// line numbers still point into the original source.
func mask(s string, re *regexp.Regexp) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, m)
	})
}

// TestDefaultThemeHasNoHardcodedStrings keeps the default theme's words in
// theme.json: no visible English text node or aria-label, placeholder, title,
// alt or value literal in the templates (outside the allowlist), and the keys
// the templates use are exactly the keys theme.json declares.
func TestDefaultThemeHasNoHardcodedStrings(t *testing.T) {
	fsys := DefaultFS()
	th, err := LoadTheme(Chain{fsys})
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	scanned := 0
	for _, dir := range []string{"layouts", "partials", "sections"} {
		files, err := fs.Glob(fsys, dir+"/*.html")
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no templates (%v)", dir, err)
		}
		for _, p := range files {
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				t.Fatal(err)
			}
			scanned++
			src := string(b)
			for _, m := range guardUsedKeyRe.FindAllStringSubmatch(src, -1) {
				used[m[1]] = true
			}
			text := mask(mask(mask(src, guardActionRe), guardBlockRe), guardActionRe)
			line := func(off int) int { return 1 + strings.Count(text[:off], "\n") }
			report := func(off int, what, run string) {
				run = strings.TrimSpace(run)
				if hardcodedAllowlist[p+": "+run] != "" {
					return
				}
				t.Errorf("%s:%d: hardcoded %s %q: move it to theme.json strings and use {{t . \"key\"}}", p, line(off), what, run)
			}
			prev := 0
			for _, loc := range guardTagRe.FindAllStringIndex(text, -1) {
				chunk := guardEntityRe.ReplaceAllString(text[prev:loc[0]], " ")
				for _, w := range guardWordRe.FindAllStringIndex(chunk, -1) {
					report(prev+w[0], "text", strings.TrimSpace(chunk))
					break
				}
				tag := text[loc[0]:loc[1]]
				for _, m := range guardVisibleAttrRe.FindAllStringSubmatchIndex(tag, -1) {
					if v := tag[m[2]:m[3]]; guardWordRe.MatchString(v) {
						report(loc[0]+m[2], "attribute", v)
					}
				}
				prev = loc[1]
			}
		}
	}
	if scanned < 20 {
		t.Fatalf("scanned %d templates, expected the whole theme", scanned)
	}
	for k := range used {
		if _, ok := th.Strings[k]; !ok {
			t.Errorf("templates use t key %q that theme.json does not declare", k)
		}
	}
	var unused []string
	for k := range th.Strings {
		if !used[k] {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("theme.json declares keys no template uses: %v", unused)
	}
}
