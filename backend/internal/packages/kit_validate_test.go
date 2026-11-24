package packages

import (
	"fmt"
	"strings"
	"testing"
)

// kitWith builds a kit.json whose body (after the required keys) is extra.
func kitWith(extra string) string {
	return `{"name":"community","version":"1.0.0","kit":1,"sdk":"^0.27"` + extra + `}`
}

func repeatJSON(n int, f func(i int) string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = f(i)
	}
	return strings.Join(parts, ",")
}

func plugins(n int) string {
	return `"plugins":[` + repeatJSON(n, func(i int) string { return fmt.Sprintf(`{"name":"plug-%d","range":"^1"}`, i) }) + `]`
}

func themes(n int) string {
	return `"themes":[` + repeatJSON(n, func(i int) string { return fmt.Sprintf(`{"name":"thm-%d","range":"^1"}`, i) }) + `]`
}

func schemas(n int) string {
	return `"schemas":[` + repeatJSON(n, func(i int) string {
		return fmt.Sprintf(`{"slug":"type-%d","name":"T","schema":{"type":"object"}}`, i)
	}) + `]`
}

func pages(n int) string {
	return `"pages":[` + repeatJSON(n, func(i int) string { return fmt.Sprintf(`{"slug":"page-%d","name":"P"}`, i) }) + `]`
}

func menuItems(n int) string {
	return `"menus":[{"location":"header","name":"H","items":[` + repeatJSON(n, func(i int) string {
		return fmt.Sprintf(`{"label":"L%d","url":"/p%d"}`, i, i)
	}) + `]}]`
}

func storySchema(settings string) string {
	return `"schemas":[{"slug":"story","name":"Stories","schema":{"type":"object"},"settings":` + settings + `}]`
}

func pluginGrant(g string) string {
	return `"plugins":[{"name":"example-board","range":"^1","grant":` + g + `}]`
}

func TestKitValidate(t *testing.T) {
	bigBlock := `{"type":"rich-text","props":{"html":"` + strings.Repeat("a", MaxKitPageBytes) + `"}}`
	smallBlock := `{"type":"rich-text","props":{"html":"` + strings.Repeat("a", MaxKitPageBytes-200) + `"}}`
	cases := []struct {
		name string
		json string
		ok   bool
	}{
		{"minimal", kitWith(``), true},
		{"16 schemas", kitWith(`,` + schemas(16)), true},
		{"17 schemas", kitWith(`,` + schemas(17)), false},
		{"32 pages", kitWith(`,` + pages(32)), true},
		{"33 pages", kitWith(`,` + pages(33)), false},
		{"24 menu items", kitWith(`,` + menuItems(24)), true},
		{"25 menu items", kitWith(`,` + menuItems(25)), false},
		{"8 plugins and 8 themes", kitWith(`,` + plugins(8) + `,` + themes(8)), true},
		{"9 plugins", kitWith(`,` + plugins(9)), false},
		{"9 themes", kitWith(`,` + themes(9)), false},
		{"duplicate plugin", kitWith(`,"plugins":[{"name":"aa","range":"^1"},{"name":"aa","range":"^1"}]`), false},
		{"40 char schema slug", kitWith(`,"schemas":[{"slug":"` + strings.Repeat("a", 40) + `","name":"T","schema":{}}]`), true},
		{"41 char schema slug", kitWith(`,"schemas":[{"slug":"` + strings.Repeat("a", 41) + `","name":"T","schema":{}}]`), false},
		{"41 char page slug", kitWith(`,"pages":[{"slug":"` + strings.Repeat("a", 41) + `","name":"T"}]`), false},
		{"duplicate schema slug", kitWith(`,"schemas":[{"slug":"a","name":"T","schema":{}},{"slug":"a","name":"T","schema":{}}]`), false},
		{"schema not an object", kitWith(`,"schemas":[{"slug":"a","name":"T","schema":[]}]`), false},
		{"url_pattern slug", kitWith(`,` + storySchema(`{"url_pattern":"/item/{slug}"}`)), true},
		{"url_pattern path", kitWith(`,` + storySchema(`{"url_pattern":"/item/{path}"}`)), true},
		{"url_pattern id", kitWith(`,` + storySchema(`{"url_pattern":"/item/{id}"}`)), false},
		{"grant all", kitWith(`,` + pluginGrant(`"all"`)), true},
		{"grant string db", kitWith(`,` + pluginGrant(`"db"`)), false},
		{"grant list db", kitWith(`,` + pluginGrant(`["db"]`)), true},
		{"grant list nope", kitWith(`,` + pluginGrant(`["nope"]`)), false},
		{"grant empty list", kitWith(`,` + pluginGrant(`[]`)), false},
		{"grant duplicate", kitWith(`,` + pluginGrant(`["db","db"]`)), false},
		{"permissions key", kitWith(`,"permissions":["db"]`), false},
		{"kit format 2", `{"name":"community","version":"1.0.0","kit":2,"sdk":"^0.27"}`, false},
		{"bad sdk range", `{"name":"community","version":"1.0.0","kit":1,"sdk":"banana"}`, false},
		{"bad plugin range", kitWith(`,"plugins":[{"name":"aa","range":"banana"}]`), false},
		{"unknown requires ok", kitWith(`,"requires":["future.feature"]`), true},
		{"malformed requires", kitWith(`,"requires":["Nope"]`), false},
		{"page 64 KiB ok", kitWith(`,"pages":[{"slug":"a","name":"A","blocks":[` + smallBlock + `]}]`), true},
		{"page 65 KiB", kitWith(`,"pages":[{"slug":"a","name":"A","blocks":[` + bigBlock + `]}]`), false},
		{"empty menu location", kitWith(`,"menus":[{"location":"","name":"H"}]`), false},
		{"bad menu location", kitWith(`,"menus":[{"location":"Top Bar","name":"H"}]`), false},
		{"menu url relative", kitWith(`,"menus":[{"location":"header","name":"H","items":[{"label":"x","url":"latest"}]}]`), false},
		{"menu url scheme-relative", kitWith(`,"menus":[{"location":"header","name":"H","items":[{"label":"x","url":"//x.test"}]}]`), false},
		{"menu url https", kitWith(`,"menus":[{"location":"header","name":"H","items":[{"label":"x","url":"https://x.test/"}]}]`), true},
		{"menu url http", kitWith(`,"menus":[{"location":"header","name":"H","items":[{"label":"x","url":"http://x.test/"}]}]`), false},
		{"comment_policy known", kitWith(`,` + schemas(1) + `,"switches":{"comment_policy":["type-0"]}`), true},
		{"comment_policy unknown", kitWith(`,` + schemas(1) + `,"switches":{"comment_policy":["story"]}`), false},
		{"switches.homepage unknown", kitWith(`,` + pages(1) + `,"switches":{"homepage":"nope"}`), false},
		{"switches.homepage known", kitWith(`,` + pages(1) + `,"switches":{"homepage":"page-0"}`), true},
		{"two homepage pages", kitWith(`,"pages":[{"slug":"a","name":"A","homepage":true},{"slug":"b","name":"B","homepage":true}]`), false},
		{"homepage mismatch", kitWith(`,"pages":[{"slug":"a","name":"A","homepage":true},{"slug":"b","name":"B"}],"switches":{"homepage":"b"}`), false},
		{"homepage match", kitWith(`,"pages":[{"slug":"a","name":"A","homepage":true}],"switches":{"homepage":"a"}`), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseKit([]byte(c.json))
			if c.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("want refusal")
			}
		})
	}
}

func TestKitPageSchemaDefaultsToPage(t *testing.T) {
	k, err := parseKit([]byte(kitWith(`,` + pages(1))))
	if err != nil {
		t.Fatal(err)
	}
	if k.Pages[0].Schema != "page" {
		t.Fatalf("schema = %q", k.Pages[0].Schema)
	}
}

func TestKitJSONSizeLimit(t *testing.T) {
	pad := strings.Repeat(" ", MaxKitJSON)
	if _, err := parseKit([]byte(kitWith(``) + pad)); err == nil {
		t.Fatal("a kit.json above the limit must be refused")
	}
}

func TestKitErrorsNameTheField(t *testing.T) {
	_, err := parseKit([]byte(kitWith(`,"menus":[{"location":"header","name":"H","items":[{"label":"x","url":"nope"}]}]`)))
	if err == nil || !strings.Contains(err.Error(), "menus[0].items[0].url") {
		t.Fatalf("err = %v", err)
	}
}
