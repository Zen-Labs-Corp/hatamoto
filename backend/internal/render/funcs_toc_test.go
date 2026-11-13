package render

import (
	"html/template"
	"reflect"
	"testing"
)

func TestTOC(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		in    template.HTML
		html  string
		items []TOCEntry
	}{
		{"empty", "", "", nil},
		{"blank", "  \n", "", nil},
		{"h2 ids", "<h2>Hello World</h2><p>x</p>", `<h2 id="hello-world">Hello World</h2><p>x</p>`,
			[]TOCEntry{{"hello-world", "Hello World"}}},
		{"dedup", "<h2>A</h2><h2>A</h2><h2>A</h2>", `<h2 id="a">A</h2><h2 id="a-2">A</h2><h2 id="a-3">A</h2>`,
			[]TOCEntry{{"a", "A"}, {"a-2", "A"}, {"a-3", "A"}}},
		{"existing id kept", `<h2 id="mine">A</h2><h2>Mine</h2>`, `<h2 id="mine">A</h2><h2 id="mine-2">Mine</h2>`,
			[]TOCEntry{{"mine", "A"}, {"mine-2", "Mine"}}},
		{"reserved id", "<h2>Toc</h2>", `<h2 id="toc-2">Toc</h2>`, []TOCEntry{{"toc-2", "Toc"}}},
		{"h3 untouched", "<h3>Sub</h3><h2>Top</h2>", `<h3>Sub</h3><h2 id="top-2">Top</h2>`, []TOCEntry{{"top-2", "Top"}}},
		{"pre and code ignored", "<pre><h2>x</h2></pre><pre><code><h2>y</h2></code></pre>", "<pre><h2>x</h2></pre><pre><code><h2>y</h2></code></pre>", nil},
	}
	for _, tc := range tests {
		got := tocFn(tc.in)
		if h, _ := got["html"].(template.HTML); string(h) != tc.html {
			t.Errorf("%s: html = %q, want %q", tc.name, h, tc.html)
		}
		items, ok := got["items"].([]TOCEntry)
		if !ok || !reflect.DeepEqual(items, tc.items) {
			t.Errorf("%s: items = %#v, want %#v", tc.name, got["items"], tc.items)
		}
	}
}
