package admin

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// badgeException names one golden badge that may render without a dot. Badges
// with an icon are exempt by rule (the icon carries the tone), as are neutral Plain and Mono tags (no status), not by name.
type badgeException struct{ File, Label, Reason string }

// None yet; extend only with a reason a dot is wrong for that badge.
var badgeExceptions = []badgeException{}

var badgeTags = regexp.MustCompile(`<[^>]*>`)

// badgeOpenTag matches an opening tag that has a class attribute, in any attribute order.
var badgeOpenTag = regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9]*)\b[^>]*?\sclass=(?:"([^"]*)"|'([^']*)'|([^\s>"']+))[^>]*>`)

// badgeSpans returns the markup of every element whose class list holds the token
// hm-badge (not hm-badge__dot or hm-badge--kind), including nested elements of the same name.
func badgeSpans(html string) []string {
	var out []string
	for _, m := range badgeOpenTag.FindAllStringSubmatchIndex(html, -1) {
		classes := ""
		for _, g := range []int{4, 6, 8} {
			if m[g] >= 0 {
				classes = html[m[g]:m[g+1]]
			}
		}
		isBadge := false
		for _, c := range strings.Fields(classes) {
			isBadge = isBadge || c == "hm-badge"
		}
		if !isBadge {
			continue
		}
		name := html[m[2]:m[3]]
		rest, depth, end := html[m[0]:], 0, 0
		for j := 0; j < len(rest) && end == 0; {
			switch {
			case strings.HasPrefix(rest[j:], "<"+name) && !isNameByte(rest, j+1+len(name)):
				depth++
				j += 1 + len(name)
			case strings.HasPrefix(rest[j:], "</"+name+">"):
				depth--
				j += len(name) + 3
				if depth == 0 {
					end = j
				}
			default:
				j++
			}
		}
		if end == 0 {
			end = len(rest)
		}
		out = append(out, rest[:end])
	}
	return out
}

// isNameByte reports whether s[i] continues a tag name (so <spanx is not <span).
func isNameByte(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	c := s[i]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func TestBadgeDotEverywhere(t *testing.T) {
	t.Parallel()

	var files []string
	for _, dir := range []string{"pages", "partials"} {
		m, err := filepath.Glob(filepath.Join("testdata", "golden", dir, "*.html"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	total := 0
	used := make([]bool, len(badgeExceptions))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel := filepath.ToSlash(strings.TrimPrefix(f, filepath.Join("testdata", "golden")+string(filepath.Separator)))
		for _, span := range badgeSpans(string(b)) {
			total++
			// Neutral Plain and Mono badges are labels and tags, not statuses: no tone to back up with a dot.
			// A coloured one is a status and keeps its dot.
			neutralTag := strings.Contains(span, "hm-badge--neutral") && (strings.Contains(span, "hm-badge--plain") || strings.Contains(span, "hm-badge--mono"))
			if strings.Contains(span, "hm-badge__dot") || strings.Contains(span, "hm-badge__icon") || neutralTag {
				continue
			}
			label := strings.TrimSpace(badgeTags.ReplaceAllString(span, ""))
			excused := false
			for i, e := range badgeExceptions {
				if e.File == rel && e.Label == label {
					used[i], excused = true, true
				}
			}
			if !excused {
				t.Errorf("%s: badge %q has no dot", rel, label)
			}
		}
	}
	if total < 150 {
		t.Errorf("found %d badges in the goldens, want more than 150 (broken glob?)", total)
	}
	for i, e := range badgeExceptions {
		if e.Reason == "" {
			t.Errorf("badge exception %+v has no reason", e)
		}
		if !used[i] {
			t.Errorf("badge exception %+v matches nothing; remove it", e)
		}
	}
}

// The negative assertions on badges must be able to fail: they see through the dot.
func TestBadgeAssertionRegexpsSeeThroughDot(t *testing.T) {
	t.Parallel()

	dot := `<span class="hm-badge__dot" aria-hidden="true"></span>`
	for name, tc := range map[string]struct {
		re   *regexp.Regexp
		html string
		want bool
	}{
		"zero count with dot":    {zeroCountBadge, `<span class="hm-badge hm-badge--neutral">` + dot + `0</span>`, true},
		"zero count without dot": {zeroCountBadge, `<span class="hm-badge hm-badge--neutral">0</span>`, true},
		"other count":            {zeroCountBadge, `<span class="hm-badge hm-badge--neutral">` + dot + `2</span>`, false},
		"empty with dot":         {emptyBadge, `<span class="hm-badge hm-badge--info">` + dot + `</span>`, true},
		"empty without dot":      {emptyBadge, `<span class="hm-badge"></span>`, true},
		"labelled":               {emptyBadge, `<span class="hm-badge hm-badge--info">` + dot + `Seen</span>`, false},
	} {
		if got := tc.re.MatchString(tc.html); got != tc.want {
			t.Errorf("%s: match = %v, want %v", name, got, tc.want)
		}
	}
}

// badgeSpans finds a badge whatever the attribute order, quoting or element, and never
// mistakes an hm-badge__ element or an hm-badge--kind modifier alone for one.
func TestBadgeSpansDetection(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		html string
		want int
	}{
		"class first":          {`<span class="hm-badge hm-badge--info">A</span>`, 1},
		"attribute before":     {`<span data-x hidden class="hm-badge hm-badge--warning">A</span>`, 1},
		"extra attribute":      {`<span class="hm-badge" title="t">A</span>`, 1},
		"bare class":           {`<span class="hm-badge">A</span>`, 1},
		"other element":        {`<div class="row hm-badge">A</div>`, 1},
		"single quotes":        {`<span class='hm-badge'>A</span>`, 1},
		"nested span":          {`<span class="hm-badge hm-badge--info">A<span class="hm-visually-hidden">b</span></span>`, 1},
		"dot is not a badge":   {`<span class="hm-badge__dot" aria-hidden="true"></span>`, 0},
		"modifier alone":       {`<span class="hm-badge--info">A</span>`, 0},
		"two badges":           {`<span class="hm-badge">A</span><span id="x" class="hm-badge">B</span>`, 2},
		"badge inside a badge": {`<span class="hm-badge"><span class="hm-badge__dot"></span>A</span>`, 1},
	} {
		if got := len(badgeSpans(tc.html)); got != tc.want {
			t.Errorf("%s: found %d badges, want %d", name, got, tc.want)
		}
	}
	spans := badgeSpans(`<span data-x class="hm-badge">A<span class="v">b</span></span>tail`)
	if len(spans) != 1 || spans[0] != `<span data-x class="hm-badge">A<span class="v">b</span></span>` {
		t.Errorf("span extent wrong: %q", spans)
	}
}
