package render

import (
	"context"
	"strings"
	"testing"
	"time"
)

func hostileFilter(tags ...HeadTag) HeadFilter {
	return func(_ context.Context, c HeadContext) HeadContext {
		c.Tags = append(c.Tags, tags...)
		return c
	}
}

func TestHeadExtHostilePayloadEscaped(t *testing.T) {
	t.Parallel()
	f := hostileFilter(
		HeadTag{Kind: "meta", Attrs: map[string]string{"name": "x", "content": `"><script>alert(1)</script>`}},
		HeadTag{Kind: "meta", Attrs: map[string]string{"name": "y", "content": "ok", "onload": "alert(1)"}},
		HeadTag{Kind: "link", Attrs: map[string]string{"rel": "stylesheet", "href": "javascript:alert(1)"}},
		HeadTag{Kind: "link", Attrs: map[string]string{"rel": "preconnect", "href": "https://fonts.example.com", "crossorigin": "anonymous"}},
		HeadTag{Kind: "script-src", Attrs: map[string]string{"src": "http://insecure.example.com/a.js"}},
		HeadTag{Kind: "script-src", Attrs: map[string]string{"src": "https://cdn.example.com/a.js", "onerror": "x()"}},
		HeadTag{Kind: "jsonld", JSONLD: []byte(`{"@type":"Thing","name":"</script><script>alert(2)</script>"}`)},
		HeadTag{Kind: "script", Attrs: map[string]string{"src": "https://x/y.js"}},
		HeadTag{Kind: "meta", Attrs: map[string]string{"content": "no name or property"}},
	)
	h := buildHeadView(context.Background(), &PageView{BaseURL: "https://x.io", Path: "/"}, f)
	out := renderHead(t, h)

	if strings.Contains(out, "<script>alert") || strings.Contains(out, "</script><script") {
		t.Fatalf("hostile payload not neutralised:\n%s", out)
	}
	if !strings.Contains(out, `content="&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`) {
		t.Errorf("content attr not escaped:\n%s", out)
	}
	for _, bad := range []string{"onload", "onerror", "javascript:", "insecure.example.com", `content="no name`} {
		if strings.Contains(out, bad) {
			t.Errorf("%q must be dropped:\n%s", bad, out)
		}
	}
	for _, good := range []string{
		`<meta name="y" content="ok">`,
		`<link rel="preconnect" href="https://fonts.example.com" crossorigin="anonymous">`,
		`<script src="https://cdn.example.com/a.js"></script>`,
	} {
		if !strings.Contains(out, good) {
			t.Errorf("missing %q:\n%s", good, out)
		}
	}
	if strings.Count(out, `application/ld+json`) != 2 { // site graph + plugin jsonld
		t.Errorf("expected plugin jsonld script:\n%s", out)
	}
}

func TestHeadExtFailOpen(t *testing.T) {
	old := headFilterTimeout
	headFilterTimeout = 50 * time.Millisecond
	defer func() { headFilterTimeout = old }()

	v := &PageView{BaseURL: "https://x.io", Path: "/"}
	tests := []struct {
		name string
		f    HeadFilter
	}{
		{"nil", nil},
		{"panic", func(context.Context, HeadContext) HeadContext { panic("boom") }},
		{"slow", func(ctx context.Context, c HeadContext) HeadContext {
			time.Sleep(500 * time.Millisecond)
			c.Tags = []HeadTag{{Kind: "meta", Attrs: map[string]string{"name": "late", "content": "x"}}}
			return c
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := buildHeadView(context.Background(), v, tt.f)
			if len(h.Ext) != 0 {
				t.Fatalf("failed filter must contribute nothing: %v", h.Ext)
			}
			if h.Canonical != "https://x.io/" {
				t.Fatalf("seo head must survive: %q", h.Canonical)
			}
		})
	}
}

func TestHeadExtFilterSeesContext(t *testing.T) {
	t.Parallel()
	var got HeadContext
	f := func(_ context.Context, c HeadContext) HeadContext { got = c; return c }
	buildHeadView(context.Background(), &PageView{Path: "/a", Site: SiteView{Language: "en"}, SchemaSlug: "page", EntityID: "e1"}, f)
	if got.Path != "/a" || got.Language != "en" || got.SchemaSlug != "page" || got.EntityID != "e1" {
		t.Fatalf("%+v", got)
	}
}

func TestHeadExtSanitizeTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tag  HeadTag
		ok   bool
	}{
		{"meta ok", HeadTag{Kind: "meta", Attrs: map[string]string{"NAME": "a", "Content": "b"}}, true},
		{"link relative ok", HeadTag{Kind: "link", Attrs: map[string]string{"rel": "icon", "href": "/f.ico"}}, true},
		{"link data href", HeadTag{Kind: "link", Attrs: map[string]string{"rel": "icon", "href": "data:text/html,x"}}, false},
		{"link proto-relative", HeadTag{Kind: "link", Attrs: map[string]string{"rel": "icon", "href": "//evil.com/x"}}, false},
		{"link no rel", HeadTag{Kind: "link", Attrs: map[string]string{"href": "/x"}}, false},
		{"script relative", HeadTag{Kind: "script-src", Attrs: map[string]string{"src": "/a.js"}}, true},
		{"script bad crossorigin kept off", HeadTag{Kind: "script-src", Attrs: map[string]string{"src": "/a.js", "crossorigin": "x"}}, true},
		{"unknown kind", HeadTag{Kind: "style", Attrs: map[string]string{"content": "a{}"}}, false},
		{"script inline", HeadTag{Kind: "script-src", Attrs: map[string]string{"text": "alert(1)"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			et, ok := sanitizeTag(tt.tag)
			if ok != tt.ok {
				t.Fatalf("ok=%v want %v (%+v)", ok, tt.ok, et)
			}
			if tt.name == "script bad crossorigin kept off" && et.Crossorigin != "" {
				t.Fatalf("crossorigin should be dropped: %q", et.Crossorigin)
			}
		})
	}
}

func replaceGraphFilter(c HeadContext) HeadContext {
	c.ReplaceSiteGraph = true
	c.Tags = append(c.Tags, HeadTag{Kind: "jsonld", JSONLD: []byte(`{"@context":"https://schema.org","@type":"Organization","name":"Plugin Org"}`)})
	return c
}

func TestHeadExtReplaceSiteGraph(t *testing.T) {
	t.Parallel()
	v := &PageView{Site: SiteView{Name: "S"}, BaseURL: "https://x.io", Path: "/"}
	count := func(h headView, sub string) (n int) {
		for _, s := range h.Scripts {
			if strings.Contains(string(s), sub) {
				n++
			}
		}
		return n
	}

	// No flag: the generic graph stays, plugin tags are additive.
	h := buildHeadView(context.Background(), v, hostileFilter(HeadTag{Kind: "jsonld", JSONLD: []byte(`{"@type":"Thing"}`)}))
	if count(h, `"@graph"`) != 1 || count(h, `"Thing"`) != 1 {
		t.Fatalf("default: want generic graph + plugin doc, got %v", h.Scripts)
	}

	// Flag: generic graph replaced by the plugin's own.
	h = buildHeadView(context.Background(), v, func(_ context.Context, c HeadContext) HeadContext { return replaceGraphFilter(c) })
	if count(h, `"@graph"`) != 0 || count(h, "Plugin Org") != 1 {
		t.Fatalf("replace: want only plugin graph, got %v", h.Scripts)
	}
}

func TestHeadExtReplaceSiteGraphFailOpen(t *testing.T) {
	old := headFilterTimeout
	headFilterTimeout = 20 * time.Millisecond
	defer func() { headFilterTimeout = old }()
	v := &PageView{BaseURL: "https://x.io", Path: "/"}
	for name, f := range map[string]HeadFilter{
		"panic": func(context.Context, HeadContext) HeadContext { panic("boom") },
		"slow": func(ctx context.Context, c HeadContext) HeadContext {
			time.Sleep(200 * time.Millisecond)
			return replaceGraphFilter(c)
		},
	} {
		h := buildHeadView(context.Background(), v, f)
		found := false
		for _, s := range h.Scripts {
			found = found || strings.Contains(string(s), `"@graph"`)
		}
		if !found {
			t.Errorf("%s: generic graph must be kept", name)
		}
	}
}

// A plugin that sets ReplaceSiteGraph but supplies no valid jsonld tag must not
// leave the page without a site graph.
func TestHeadExtReplaceSiteGraphWithoutValidTagKeepsGeneric(t *testing.T) {
	t.Parallel()
	v := &PageView{Site: SiteView{Name: "S"}, BaseURL: "https://x.io", Path: "/"}
	for name, tags := range map[string][]HeadTag{
		"no tags":      nil,
		"invalid json": {{Kind: "jsonld", JSONLD: []byte(`{not json`)}},
	} {
		h := buildHeadView(context.Background(), v, func(_ context.Context, c HeadContext) HeadContext {
			c.ReplaceSiteGraph = true
			c.Tags = append(c.Tags, tags...)
			return c
		})
		found := false
		for _, s := range h.Scripts {
			found = found || strings.Contains(string(s), `"@graph"`)
		}
		if !found {
			t.Errorf("%s: generic graph must be kept", name)
		}
	}
}
