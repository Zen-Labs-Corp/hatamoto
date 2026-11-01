package render

import (
	"context"
	"encoding/json"
	"html/template"
	"reflect"
	"strings"
	"testing"
	"time"
)

// graphPage is a page with an Organization, a LocalBusiness and a WebSite node.
func graphPage() *PageView {
	v := businessPage(&BusinessView{ID: "xx", Address: Address{City: "Sample City"}, Lat: "1.5", Lng: "2"})
	v.Path = "/about"
	return v
}

// scriptGraph decodes the @graph of the first site graph script in scripts.
func scriptGraph(t *testing.T, scripts []template.HTML) []map[string]any {
	t.Helper()
	for _, s := range scripts {
		body := strings.TrimSuffix(strings.TrimPrefix(string(s), `<script type="application/ld+json">`), `</script>`)
		var doc struct {
			Graph []map[string]any `json:"@graph"`
		}
		if json.Unmarshal([]byte(body), &doc) == nil && doc.Graph != nil {
			return doc.Graph
		}
	}
	t.Fatalf("no site graph in %v", scripts)
	return nil
}

func byID(nodes []map[string]any, suffix string) map[string]any {
	for _, n := range nodes {
		if id, _ := n["@id"].(string); strings.HasSuffix(id, suffix) {
			return n
		}
	}
	return nil
}

func graphScript(t *testing.T, v *PageView, f SiteGraphFilter) []map[string]any {
	t.Helper()
	return scriptGraph(t, buildHeadViewWith(context.Background(), v, nil, f).Scripts)
}

func TestSiteGraphFilter_AddsKeyAndNode(t *testing.T) {
	var got SiteGraphInput
	f := func(_ context.Context, in SiteGraphInput) SiteGraphInput {
		got = in
		for _, n := range in.Nodes {
			if n["@type"] == "Organization" {
				n["knowsAbout"] = []any{"samples"}
			}
		}
		in.Nodes = append(in.Nodes, map[string]any{"@id": "https://example.test#extra", "@type": "Thing", "name": "Extra"})
		return in
	}
	v := graphPage()
	v.Locale, v.Segment, v.SchemaSlug, v.EntityID = "en-XX", "xx", "page", "e1"
	nodes := graphScript(t, v, f)
	if org := byID(nodes, "#organization"); !reflect.DeepEqual(org["knowsAbout"], []any{"samples"}) {
		t.Errorf("Organization = %v", org)
	}
	if extra := byID(nodes, "#extra"); extra == nil || extra["name"] != "Extra" {
		t.Errorf("extra node missing: %v", nodes)
	}
	if nodes[len(nodes)-1]["@id"] != "https://example.test#extra" {
		t.Errorf("a new node must come last: %v", nodes)
	}
	if got.Path != "/about" || got.Locale != "en-XX" || got.Segment != "xx" || got.SchemaSlug != "page" || got.EntityID != "e1" || len(got.Nodes) != 3 {
		t.Errorf("input = %+v", got)
	}
}

func TestSiteGraphFilter_CoreKeysAreProtected(t *testing.T) {
	f := func(_ context.Context, in SiteGraphInput) SiteGraphInput {
		for _, n := range in.Nodes {
			if n["@type"] == "Organization" {
				n["@id"] = "https://evil.test#org"
				n["name"] = "Hijacked"
				delete(n, "url")
			}
		}
		// Another route to the same attack: a node that repeats a core @id.
		in.Nodes = append(in.Nodes, map[string]any{"@id": "https://example.test#website", "@type": "Other", "name": "X", "inLanguage": "zz", "extra": "yes"})
		return in
	}
	v := graphPage()
	v.Site.Language = "en"
	nodes := graphScript(t, v, f)
	org := byID(nodes, "#organization")
	if org == nil || org["@type"] != "Organization" || org["name"] != "Example Site" || org["url"] != "https://example.test" {
		t.Errorf("Organization changed: %v", org)
	}
	site := byID(nodes, "#website")
	if site["@type"] != "WebSite" || site["name"] != "Example Site" || site["extra"] != "yes" {
		t.Errorf("WebSite = %v", site)
	}
	if site["inLanguage"] == "zz" {
		t.Errorf("an existing key was overwritten: %v", site)
	}
	if len(nodes) != 3 {
		t.Errorf("nodes = %v, want the 3 core nodes", nodes)
	}
}

func TestMergeSiteGraph(t *testing.T) {
	core := []map[string]any{{"@id": "a", "@type": "T", "k": 1}, {"@id": "b", "@type": "U"}}
	plugin := []map[string]any{
		{"@id": "a", "n": 2, "k": 9},
		{"@type": "NoID", "name": "dropped"},
		{"@id": "c", "name": "new"},
		{"@id": "c", "name": "duplicate"},
		{"@id": "", "name": "empty id"},
	}
	got := mergeSiteGraph(core, plugin)
	want := []map[string]any{{"@id": "a", "@type": "T", "k": 1, "n": 2}, {"@id": "b", "@type": "U"}, {"@id": "c", "name": "new"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged = %v, want %v", got, want)
	}
	if _, ok := core[0]["n"]; ok {
		t.Errorf("the core node was modified: %v", core[0])
	}
}

func TestSiteGraphFilter_DoesNotReencodeCoreValues(t *testing.T) {
	v := graphPage()
	v.Site.Org.EmployeesMax = 9007199254740993 // beyond float64's exact integers
	f := func(_ context.Context, in SiteGraphInput) SiteGraphInput {
		in.Nodes = []map[string]any{{"@id": "https://example.test#organization", "slogan": "s"}}
		return in
	}
	body := string(buildHeadViewWith(context.Background(), v, nil, f).Scripts[0])
	if !strings.Contains(body, `"maxValue":9007199254740993`) || !strings.Contains(body, `"slogan":"s"`) {
		t.Errorf("core integer re-encoded or key missing: %s", body)
	}
}

func TestSiteGraphFilter_FailOpenAndDegraded(t *testing.T) {
	old := siteGraphFilterTimeout
	siteGraphFilterTimeout = 50 * time.Millisecond
	defer func() { siteGraphFilterTimeout = old }()

	plain := graphScript(t, graphPage(), nil)
	tests := map[string]SiteGraphFilter{
		"panic": func(context.Context, SiteGraphInput) SiteGraphInput { panic("boom") },
		"slow": func(ctx context.Context, in SiteGraphInput) SiteGraphInput {
			time.Sleep(500 * time.Millisecond)
			in.Nodes[0]["late"] = true
			return in
		},
	}
	for name, f := range tests {
		ctx, scope, release := BindDegradeScope(context.Background())
		got := scriptGraph(t, buildHeadViewWith(ctx, graphPage(), nil, f).Scripts)
		release()
		if !reflect.DeepEqual(got, plain) {
			t.Errorf("%s: graph = %v, want the core graph", name, got)
		}
		if scope.Count() == 0 {
			t.Errorf("%s: the render was not marked degraded", name)
		}
	}
}

func TestSiteGraphFilter_NilFilterAndReplace(t *testing.T) {
	v := graphPage()
	a := buildHeadViewWith(context.Background(), v, nil, nil).Scripts
	b := buildHeadView(context.Background(), v, nil).Scripts
	if !reflect.DeepEqual(a, b) {
		t.Errorf("nil filter changed the output:\n%v\n%v", a, b)
	}

	// A filter that adds nothing leaves the bytes alone.
	same := buildHeadViewWith(context.Background(), v, nil, func(_ context.Context, in SiteGraphInput) SiteGraphInput { return in }).Scripts
	if !reflect.DeepEqual(a, same) {
		t.Errorf("an identity filter changed the output:\n%v\n%v", a, same)
	}

	// ReplaceSiteGraph with a valid jsonld tag skips the hook.
	called := false
	sg := func(_ context.Context, in SiteGraphInput) SiteGraphInput { called = true; return in }
	head := func(_ context.Context, c HeadContext) HeadContext {
		c.ReplaceSiteGraph = true
		c.Tags = append(c.Tags, HeadTag{Kind: "jsonld", JSONLD: []byte(`{"@type":"Thing"}`)})
		return c
	}
	buildHeadViewWith(context.Background(), v, head, sg)
	if called {
		t.Error("the site graph hook ran although the head filter replaced the graph")
	}
	// Without a valid tag the core graph stays, and the hook runs.
	noTag := func(_ context.Context, c HeadContext) HeadContext { c.ReplaceSiteGraph = true; return c }
	buildHeadViewWith(context.Background(), v, noTag, sg)
	if !called {
		t.Error("the core graph stayed, so the hook must run")
	}
}
