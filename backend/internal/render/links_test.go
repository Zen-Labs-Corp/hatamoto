package render

import (
	"html/template"
	"reflect"
	"strings"
	"testing"
)

func TestEntityLinkIDs(t *testing.T) {
	t.Parallel()
	in := `<p><a data-entity-id="a" href="/x">x</a> <a href="/plain">p</a> <a data-entity-id="b" href="/y">y</a> <a data-entity-id="a" href="/x2">again</a></p>`
	if got, want := EntityLinkIDs(in), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids %v, want %v", got, want)
	}
	if got := EntityLinkIDs("<p>none</p>"); got != nil {
		t.Fatalf("ids %v", got)
	}
}

func TestRewriteEntityLinks(t *testing.T) {
	t.Parallel()
	hrefs := map[string]string{"a": "/blog/new-slug", "b": "//evil.example/x", "c": "https://evil.example"}
	tests := []struct{ name, in, want string }{
		{"stale href replaced",
			`<p>See <a data-entity-id="a" href="/blog/old" class="k">this</a>.</p>`,
			`<p>See <a data-entity-id="a" href="/blog/new-slug" class="k">this</a>.</p>`},
		{"missing href added",
			`<a data-entity-id="a">t</a>`, `<a data-entity-id="a" href="/blog/new-slug">t</a>`},
		{"unknown id untouched",
			`<a data-entity-id="zzz" href="/keep">t</a>`, `<a data-entity-id="zzz" href="/keep">t</a>`},
		{"non-entity links untouched",
			`<a href="/blog/old">t</a>`, `<a href="/blog/old">t</a>`},
		{"protocol-relative and absolute targets refused",
			`<a data-entity-id="b" href="/b">t</a><a data-entity-id="c" href="/c">u</a>`,
			`<a data-entity-id="b" href="/b">t</a><a data-entity-id="c" href="/c">u</a>`},
		{"text and other markup byte-identical",
			`<h2>A &amp; B</h2><!-- c --><p>x<br/>y</p>`, `<h2>A &amp; B</h2><!-- c --><p>x<br/>y</p>`},
		{"href value is escaped",
			`<a data-entity-id="q" href="/o">t</a>`, `<a data-entity-id="q" href="/a&#34;b">t</a>`},
	}
	hrefs["q"] = `/a"b`
	for _, tt := range tests {
		if got := RewriteEntityLinks(tt.in, hrefs); got != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", tt.name, got, tt.want)
		}
	}
	if got := RewriteEntityLinks(`<a data-entity-id="a" href="/o">t</a>`, nil); !strings.Contains(got, "/o") {
		t.Errorf("nil map must be a no-op: %s", got)
	}
}

func TestRewriteLinksInNestedValues(t *testing.T) {
	t.Parallel()
	v := map[string]any{
		"plain": `<a data-entity-id="a" href="/old">not html-typed</a>`,
		"body":  template.HTML(`<a data-entity-id="a" href="/old">x</a>`),
		"items": []any{map[string]any{"d": template.HTML(`<a data-entity-id="a" href="/old">y</a>`)}},
	}
	got := rewriteLinksIn(v, map[string]string{"a": "/new"}).(map[string]any)
	if got["plain"] != v["plain"] || !strings.Contains(string(got["body"].(template.HTML)), `href="/new"`) {
		t.Fatalf("body/plain: %v", got)
	}
	d := got["items"].([]any)[0].(map[string]any)["d"].(template.HTML)
	if !strings.Contains(string(d), `href="/new"`) {
		t.Fatalf("nested: %s", d)
	}
}

func TestRenderRewritesEntityLinksInSectionsAndBody(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.EntityID = "ent-1"
	v.Sections = []Section{{ID: "s1", Type: "content", Content: map[string]any{
		"body": `<p><a data-entity-id="t1" href="/old-slug">go</a></p>`,
	}}}
	v.Attrs = map[string]any{"content": `<p><a data-entity-id="t1" href="/old-slug">body</a></p>`}
	v.EntityLinks = map[string]string{"t1": "/blog/fresh"}
	out := renderToString(t, e, v)
	if strings.Contains(out, "/old-slug") {
		t.Errorf("stale href survived:\n%s", out)
	}
	if !strings.Contains(out, `href="/blog/fresh"`) {
		t.Errorf("fresh href missing:\n%s", out)
	}
}
