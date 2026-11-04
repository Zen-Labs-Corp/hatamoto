package searchreplace

import (
	"encoding/json"
	"strings"
	"testing"
)

const sample = `{
 "title":"Old shop",
 "price":9.90,
 "featured":true,
 "meta_title":"Old shop deals",
 "og_title":"Old shop og",
 "body":"<p>Visit the old shop</p>",
 "page_sections":[{"title":"old shop"}],
 "page_doc":{"v":2,"nodes":[
  {"id":"old","t":"container","s":{"base":{"color":"old"}},"c":[
    {"id":"n2","t":"heading","p":{"text":"old shop","level":2},"b":{"text":"old"}},
    {"id":"n3","t":"text","p":{"html":"<p>an <a href=\"/old\">old</a> one</p>"}}
  ]}
 ]}
}`

func TestWalkCollectsByScope(t *testing.T) {
	o := Options{Find: "old"}
	cases := []struct {
		scopes Scope
		want   []string
	}{
		{ScopeContent, []string{"/body", "/page_doc/nodes/0/c/0/p/text", "/page_doc/nodes/0/c/1/p/html", "/page_doc/nodes/0/c/1/p/html"}},
		{ScopeSEO, []string{"/meta_title", "/og_title"}},
		{ScopeOther, []string{"/title"}},
	}
	for _, c := range cases {
		_, hits, n, err := Walk(json.RawMessage(sample), o, c.scopes, nil)
		if err != nil || n != 0 {
			t.Fatalf("scopes %d: err=%v n=%d", c.scopes, err, n)
		}
		var got []string
		for _, h := range hits {
			got = append(got, h.Path)
			if h.Scope != c.scopes {
				t.Errorf("hit %s scope %d want %d", h.Path, h.Scope, c.scopes)
			}
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("scopes %d: got %v want %v", c.scopes, got, c.want)
		}
	}
}

func TestWalkReplaceKeepsEverythingElse(t *testing.T) {
	o := Options{Find: "old", Replace: "new"}
	out, _, n, err := Walk(json.RawMessage(sample), o, ScopeContent|ScopeSEO|ScopeOther, func(string, int) bool { return true })
	if err != nil || n != 7 {
		t.Fatalf("err=%v n=%d", err, n)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid json: %s", out)
	}
	if !strings.Contains(string(out), `"price":9.90`) {
		t.Errorf("number not preserved: %s", out)
	}
	if !strings.Contains(string(out), `"id":"old"`) || !strings.Contains(string(out), `"color":"old"`) || !strings.Contains(string(out), `"b":{"text":"old"}`) {
		t.Errorf("id, style or binding edited: %s", out)
	}
	if !strings.Contains(string(out), `"page_sections":[{"title":"old shop"}]`) {
		t.Errorf("page_sections edited: %s", out)
	}
	if !strings.Contains(string(out), `<a href=\"/new\">new</a>`) || !strings.Contains(string(out), `the new shop`) {
		t.Errorf("not replaced: %s", out)
	}
}

func TestWalkPickOne(t *testing.T) {
	o := Options{Find: "old", Replace: "new"}
	out, _, n, _ := Walk(json.RawMessage(sample), o, ScopeSEO, func(path string, i int) bool { return path == "/og_title" })
	if n != 1 || !strings.Contains(string(out), `"og_title":"new shop og"`) || !strings.Contains(string(out), `"meta_title":"Old shop deals"`) {
		t.Fatalf("n=%d %s", n, out)
	}
}

func TestWalkNoReplaceKeepsInputBytes(t *testing.T) {
	in := json.RawMessage(`{"title":  "none",   "n": 1.0}`)
	out, hits, n, _ := Walk(in, Options{Find: "zzz", Replace: "x"}, ScopeOther, func(string, int) bool { return true })
	if string(out) != string(in) || len(hits) != 0 || n != 0 {
		t.Fatalf("%s %v %d", out, hits, n)
	}
}

func TestComparePath(t *testing.T) {
	if comparePath("/a/2/p", "/a/10/p") >= 0 || comparePath("/a/b", "/a/b") != 0 || comparePath("/a", "/a/b") >= 0 {
		t.Fatal("order")
	}
}

func TestWalkPlainPropWithLessThan(t *testing.T) {
	attrs := json.RawMessage(`{"meta_description":"Compare old.example plans if a<b","page_doc":{"v":2,"nodes":[{"id":"a","t":"heading","p":{"text":"Kids <12 eat free at Joe's old.example & co"}}]}}`)
	out, _, n, err := Walk(attrs, Options{Find: "old.example", Replace: "new.example"}, ScopeContent|ScopeSEO, func(string, int) bool { return true })
	if err != nil || n != 2 {
		t.Fatalf("err=%v n=%d", err, n)
	}
	for _, want := range []string{`Compare new.example plans if a<b`, `Kids <12 eat free at Joe's new.example & co`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}
