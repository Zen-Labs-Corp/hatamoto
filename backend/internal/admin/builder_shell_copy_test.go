package admin

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The builder shell speaks to page authors: no internal terms in the words it shows. The scan reads the visible
// copy of the files UX-1 owns (string literals in JS that read as a sentence or a label, and the text, labels and
// titles in builder.html) and flags the whole words below. Class lists, selectors, ids, module specifiers, data-*
// and aria-* names, and comments are not copy. An em dash is never part of copy.
var copyTerms = regexp.MustCompile(`(?i)\b(node|nodes|kind|kinds|ref|refs|slot|slots|preset|presets|StyleSpec|synced|entity|entities|token|tokens)\b`)

var shellCopyJS = []string{
	"addtab", "banners", "clipops", "coachmarks", "colournames", "commandk", "history", "historylabel", "keys", "kindnames", "navigator",
	"palette", "panes", "patterns", "phone", "recent", "reuse", "reusecore", "reuseops", "savedialog", "screen", "search", "sitestyles", "startpanel",
}

// inspectorCopyJS and canvasCopyJS are the inspector and canvas files of builder-ux-canvas-inspector, scanned the same way.
// "block", "class" and "element" are the words the inspector uses; "preset" stays an internal word (the stored key).
var inspectorCopyJS = []string{
	"boxmodel", "bp", "columnsops", "controls", "controlsui", "ctxmenu", "design", "dom", "elementsettings", "fields", "inlinefield", "inlinehtml",
	"inspcontent", "inspector", "inspheader", "items", "keymap", "noderestore", "picker", "presetpanel", "presetsrc", "restoreops", "showon", "style",
}

var canvasCopyJS = []string{"chrome", "fmtbar", "host", "layout", "points", "protocol"}

// copyPhrases are product names that carry a term on purpose; they are removed before the scan.
// "Content slot" is the layout part's own name for where the page body goes (the Add tab item, the layout parts page and the save messages use it).
var copyPhrases = []string{"Content slot", "X-CSRF-Token"}

// copyStrings returns the string literals of JS source that read as visible copy, with their line numbers.
func copyStrings(src string) (out []copyHit) {
	line, i := 1, 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return out
			}
			line += strings.Count(src[i:i+2+end], "\n")
			i += end + 4
		case c == '"' || c == '\'' || c == '`':
			startLine := line
			i++
			var b strings.Builder
			depth := 0
			for i < len(src) {
				d := src[i]
				if d == '\\' && i+1 < len(src) {
					b.WriteByte(src[i+1])
					i += 2
					continue
				}
				if c == '`' && strings.HasPrefix(src[i:], "${") {
					depth++
					b.WriteString(" ")
					i += 2
					continue
				}
				if depth > 0 {
					if d == '{' {
						depth++
					} else if d == '}' {
						depth--
					} else if d == '\n' {
						line++
					}
					i++
					continue
				}
				if d == c {
					break
				}
				if d == '\n' {
					line++
					if c != '`' {
						break // an unterminated quote: a regex or an apostrophe in a comment-free line; move on
					}
				}
				b.WriteByte(d)
				i++
			}
			i++
			if s := b.String(); isCopy(s) {
				out = append(out, copyHit{Line: startLine, Text: s})
			}
		default:
			i++
		}
	}
	return out
}

type copyHit struct {
	Line int
	Text string
}

var (
	wordLike  = regexp.MustCompile(`[A-Za-z]{2,}`)
	plainWord = regexp.MustCompile(`^[A-Za-z][A-Za-z' ,.:;!?…()/-]*$`)
)

// isCopy: a literal that has letters, no selector or class-list marks, and reads as words with a space or a capital.
func isCopy(s string) bool {
	s = strings.TrimSpace(s)
	if !wordLike.MatchString(s) || strings.HasPrefix(s, "@hm/") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "http") {
		return false
	}
	if strings.ContainsAny(s, "[]{}=#<>_$") || strings.Contains(s, "hm-") || strings.Contains(s, "data-") || strings.Contains(s, "::") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, ":") {
		return false
	}
	if !plainWord.MatchString(s) {
		return strings.Contains(s, " ") && !strings.ContainsAny(s, "-")
	}
	return strings.Contains(s, " ") || s[0] >= 'A' && s[0] <= 'Z'
}

var (
	htmlText  = regexp.MustCompile(`>([^<>{}]*[A-Za-z][^<>{}]*)<`)
	htmlAttr  = regexp.MustCompile(`\b(?:aria-label|title|placeholder|alt|data-hm-confirm|data-hm-confirm-label)="([^"]+)"`)
	htmlLabel = regexp.MustCompile(`"(?:Label|Title|Text|AriaLabel)" "([^"]+)"`)
)

func htmlCopy(src string) (out []copyHit) {
	for _, re := range []*regexp.Regexp{htmlText, htmlAttr, htmlLabel} {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			out = append(out, copyHit{Line: 1 + strings.Count(src[:m[2]], "\n"), Text: strings.TrimSpace(src[m[2]:m[3]])})
		}
	}
	return out
}

func flagCopy(file string, hits []copyHit, report func(string)) {
	for _, h := range hits {
		if strings.Contains(h.Text, "—") {
			report(file + ":" + strconv.Itoa(h.Line) + `: em dash in copy: "` + h.Text + `"`)
		}
		text := h.Text
		for _, p := range copyPhrases {
			text = strings.ReplaceAll(text, p, "")
		}
		if m := copyTerms.FindString(text); m != "" {
			report(file + ":" + strconv.Itoa(h.Line) + `: internal term "` + m + `" in copy: "` + h.Text + `"`)
		}
	}
}

func TestBuilderShellCopyHasNoInternalTerms(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "..", "web", "admin")
	report := func(s string) { t.Error(s) }
	scanned := 0
	scan := func(sub string, names []string) {
		for _, name := range names {
			b, err := os.ReadFile(filepath.Join(dir, "js", sub, name+".js"))
			if err != nil {
				t.Fatal(err)
			}
			hits := copyStrings(string(b))
			scanned += len(hits)
			flagCopy("js/"+sub+"/"+name+".js", hits, report)
		}
	}
	scan("builder", shellCopyJS)
	scan("builder", inspectorCopyJS)
	scan("canvas", canvasCopyJS)
	b, err := os.ReadFile(filepath.Join(dir, "pages", "entities", "builder.html"))
	if err != nil {
		t.Fatal(err)
	}
	hits := htmlCopy(string(b))
	scanned += len(hits)
	flagCopy("pages/entities/builder.html", hits, report)
	if scanned < 400 { // a scanner that stops seeing strings would pass everything
		t.Errorf("only %d copy strings found: the scan is broken", scanned)
	}
	t.Logf("%d copy strings scanned", scanned)
}

// The scan itself: it finds a term in a sentence and an em dash, and leaves selectors, class lists, specifiers and comments alone.
func TestBuilderShellCopyScanner(t *testing.T) {
	t.Parallel()

	var got []string
	report := func(s string) { got = append(got, s) }
	src := "import { a } from \"@hm/builder/node\";\n// the slot comment\nconst x = h(\"div\", { class: \"hm-bs-node data-kind\" }, \"[data-hm-bs-slot]\");\nwarn(\"Pick a node first\");\nwarn(`Removed ${n} — done`);\nconst k = \"kind\";\n"
	flagCopy("x.js", copyStrings(src), report)
	joined := strings.Join(got, "\n")
	for _, want := range []string{`internal term "node"`, "em dash"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	for _, bad := range []string{"@hm/", "hm-bs-node", "[data-hm-bs-slot]", "the slot comment", `"kind"`} {
		if strings.Contains(joined, bad) {
			t.Errorf("flagged %q: %s", bad, joined)
		}
	}
	got = nil
	flagCopy("b.html", htmlCopy(`<h2>Add a slot</h2><button aria-label="Close preset" class="hm-x">Go</button>{{template "hm/button" (dict "Label" "Synced block")}}`), report)
	if len(got) != 3 {
		t.Errorf("html scan: %v", got)
	}
}
