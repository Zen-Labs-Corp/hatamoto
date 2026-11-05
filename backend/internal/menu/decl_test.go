package menu

import (
	"encoding/json/jsontext"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestValidLocation(t *testing.T) {
	for loc, want := range map[string]bool{
		"": true, "header": true, "follow": true, "promo-bar": true, "a_b": true,
		"Header": false, "1a": false, "-a": false, "a b": false, "nowhere!": false,
		strings.Repeat("a", 32): true, strings.Repeat("a", 33): false,
	} {
		if got := ValidLocation(loc); got != want {
			t.Errorf("ValidLocation(%q) = %v, want %v", loc, got, want)
		}
	}
	if !IsCoreLocation("footer") || IsCoreLocation("follow") {
		t.Error("IsCoreLocation")
	}
}

func TestParseDeclValid(t *testing.T) {
	raw := `{"follow":{"title":"Follow us","default":[{"label":"Blog","archive":"post"},{"label":"Mastodon","url":"https://example.org/@x"},{"label":"Contact","url":"/contact"}]},"header":{}}`
	locs, err := ParseDecl(jsontext.Value(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 2 || locs[0].Name != "follow" || locs[1].Name != "header" {
		t.Fatalf("locs = %+v", locs)
	}
	if locs[0].Title != "Follow us" || locs[1].Title != "Header" || len(locs[1].Default) != 0 {
		t.Errorf("titles/defaults: %+v", locs)
	}
	d := locs[0].Default
	if len(d) != 3 || d[0].Archive != "post" || d[1].URL != "https://example.org/@x" || d[2].URL != "/contact" {
		t.Errorf("default = %+v", d)
	}
	for _, empty := range []jsontext.Value{nil, jsontext.Value(""), jsontext.Value("null"), jsontext.Value("{}")} {
		if l, err := ParseDecl(empty); l != nil || err != nil {
			t.Errorf("ParseDecl(%q) = %v, %v", empty, l, err)
		}
	}
}

func TestParseDeclErrors(t *testing.T) {
	manyLocs := map[string]any{}
	for i := 0; i < MaxDeclLocations+1; i++ {
		manyLocs[fmt.Sprintf("loc%d", i)] = map[string]any{}
	}
	manyItems := make([]map[string]string, MaxDeclItems+1)
	for i := range manyItems {
		manyItems[i] = map[string]string{"label": "x", "url": "/x"}
	}
	enc := func(v any) string { b, _ := jsonx.Marshal(v); return string(b) }
	item := func(it string) string { return `{"a":{"default":[{"label":"ok","url":"/ok"},` + it + `]}}` }
	cases := []struct{ name, raw, want string }{
		{"not object", `[]`, "menus"},
		{"bad name", `{"Bad Name":{}}`, "menus"},
		{"digit first", `{"1a":{}}`, "menus"},
		{"too many locations", enc(manyLocs), "at most 12 locations"},
		{"too many items", enc(map[string]any{"a": map[string]any{"default": manyItems}}), "menus.a.default: at most 24"},
		{"unknown location key", `{"a":{"x":1}}`, `menus.a: unknown key "x"`},
		{"long title", `{"a":{"title":"` + strings.Repeat("t", 41) + `"}}`, "menus.a.title"},
		{"empty title", `{"a":{"title":""}}`, "menus.a.title"},
		{"default not array", `{"a":{"default":{}}}`, "menus.a.default"},
		{"js url", item(`{"label":"x","url":"javascript:x"}`), "menus.a.default[1]: url"},
		{"protocol relative", item(`{"label":"x","url":"//evil.example"}`), "menus.a.default[1]: url"},
		{"no host", item(`{"label":"x","url":"https://"}`), "menus.a.default[1]: url"},
		{"long url", item(`{"label":"x","url":"/` + strings.Repeat("a", MaxDeclURL) + `"}`), "menus.a.default[1]: url"},
		{"both", item(`{"label":"x","url":"/x","archive":"post"}`), "either url or archive, not both"},
		{"neither", item(`{"label":"x"}`), "needs either url or archive"},
		{"unknown item key", item(`{"label":"x","url":"/x","icon":"a"}`), "menus.a.default[1]"},
		{"no label", item(`{"url":"/x"}`), "menus.a.default[1].label"},
		{"long label", item(`{"label":"` + strings.Repeat("l", 61) + `","url":"/x"}`), "menus.a.default[1].label"},
		{"bad archive", item(`{"label":"x","archive":"Post!"}`), "menus.a.default[1]: archive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseDecl(jsontext.Value(c.raw))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want containing %q", err, c.want)
			}
		})
	}
}

func TestParseDeclItemKeysCaseInsensitive(t *testing.T) {
	locs, err := ParseDecl(jsontext.Value(`{"a":{"default":[{"Label":"Blog","URL":"/blog"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if d := locs[0].Default; len(d) != 1 || d[0].Label != "Blog" || d[0].URL != "/blog" {
		t.Errorf("default = %+v", d)
	}
}
