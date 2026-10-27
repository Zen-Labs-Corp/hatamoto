package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func walkHTML(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTML(c, f)
	}
}

func attrOf(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// The plugin directory section's golden has a labelled search input, a polite
// live count region, ordered headings and safe external links.
func TestPluginDirectoryGoldenAccessible(t *testing.T) {
	checkDirectoryGoldenAccessible(t, "plugin_directory")
}

// The theme directory golden meets the same bar (and its screenshots are
// decorative, so every img carries an empty alt).
func TestThemeDirectoryGoldenAccessible(t *testing.T) {
	checkDirectoryGoldenAccessible(t, "theme_directory")
}

func checkDirectoryGoldenAccessible(t *testing.T, golden string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "golden", golden+".html"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var searches, counts, links int
	lastHeading := 0
	walkHTML(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		switch n.Data {
		case "input":
			if _, ok := attrOf(n, "data-hm-plugin-search"); ok {
				searches++
				if v, _ := attrOf(n, "aria-label"); strings.TrimSpace(v) == "" {
					t.Error("search input has no accessible name")
				}
				if typ, _ := attrOf(n, "type"); typ != "search" {
					t.Errorf("search input type %q", typ)
				}
				if _, ok := attrOf(n, "hidden"); !ok {
					t.Error("search input must be hidden until JS enables it")
				}
			}
		case "p":
			if _, ok := attrOf(n, "data-hm-plugin-count"); ok {
				counts++
				if v, _ := attrOf(n, "aria-live"); v != "polite" {
					t.Errorf("count region aria-live = %q", v)
				}
			}
		case "h2", "h3":
			level := int(n.Data[1] - '0')
			if lastHeading != 0 && level > lastHeading+1 {
				t.Errorf("heading level jumps from h%d to %s", lastHeading, n.Data)
			}
			lastHeading = level
		case "img":
			if alt, ok := attrOf(n, "alt"); !ok || alt != "" {
				t.Errorf("decorative screenshot needs alt=\"\", got %q", alt)
			}
			if src, _ := attrOf(n, "src"); !strings.HasPrefix(src, "https://") {
				t.Errorf("non-https screenshot %q", src)
			}
		case "a":
			href, _ := attrOf(n, "href")
			if strings.HasPrefix(strings.ToLower(href), "javascript:") {
				t.Errorf("javascript: link %q", href)
			}
			if strings.HasPrefix(href, "http") {
				links++
				if rel, _ := attrOf(n, "rel"); !strings.Contains(rel, "noopener") {
					t.Errorf("external link %q lacks rel=noopener", href)
				}
				if !strings.HasPrefix(href, "https://") {
					t.Errorf("non-https link %q", href)
				}
			}
		}
	})
	if searches != 1 || counts != 1 {
		t.Errorf("search inputs %d, count regions %d, want 1 each", searches, counts)
	}
	if links == 0 {
		t.Error("no external links found")
	}
	if lastHeading == 0 {
		t.Error("no headings")
	}
}
