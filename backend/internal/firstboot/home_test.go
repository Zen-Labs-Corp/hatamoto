package firstboot

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/jsonx"
)

// components returns HomeDoc as stored: marshalled and parsed back.
func components(t *testing.T, name string) []*builder.Node {
	t.Helper()
	raw, err := jsonx.Marshal(HomeDoc(name))
	if err != nil {
		t.Fatal(err)
	}
	d, err := builder.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return builder.Components(d)
}

func content(n *builder.Node) map[string]any {
	c, _ := n.P["content"].(map[string]any)
	return c
}

func TestHomeDocSections(t *testing.T) {
	cs := components(t, "Angula")
	want := []struct{ id, typ string }{
		{"welcome", "hero"},
		{"welcome-make", "process_steps"},
		{"welcome-run", "process_steps"},
		{"welcome-import", "cta"},
	}
	if len(cs) != len(want) {
		t.Fatalf("got %d components, want %d", len(cs), len(want))
	}
	for i, w := range want {
		if cs[i].ID != w.id || cs[i].P["type"] != w.typ {
			t.Errorf("component %d = %s/%v, want %s/%s", i, cs[i].ID, cs[i].P["type"], w.id, w.typ)
		}
	}

	hero := content(cs[0])
	for k, v := range map[string]string{
		"eyebrow":         "Hello world",
		"headline":        "Angula",
		"primaryCta":      "Read the guide",
		"ctaUrl":          "https://hatamoto.dev/docs/getting-started/first-run",
		"secondaryCta":    "Take the tour",
		"secondaryCtaUrl": "https://hatamoto.dev/docs/admin/tour",
		"backdrop":        "grid",
	} {
		if hero[k] != v {
			t.Errorf("hero %s = %v, want %q", k, hero[k], v)
		}
	}

	n := 0
	for i, heading := range []string{"Make it yours", "Run it properly"} {
		c := content(cs[1+i])
		if c["heading"] != heading || c["layout"] != "cards" {
			t.Errorf("steps %d: heading %v layout %v", i, c["heading"], c["layout"])
		}
		steps, _ := c["steps"].([]any)
		if len(steps) != 4 {
			t.Fatalf("steps %d: %d steps, want 4", i, len(steps))
		}
		for _, s := range steps {
			n++
			m, _ := s.(map[string]any)
			if got, want := m["number"], "0"+string(rune('0'+n)); got != want {
				t.Errorf("step number = %v, want %s", got, want)
			}
			if m["where"] == "" || m["where"] == nil {
				t.Errorf("step %d has no where", n)
			}
			if u, _ := m["whereUrl"].(string); !strings.HasPrefix(u, "https://hatamoto.dev/docs/") {
				t.Errorf("step %d whereUrl = %q", n, u)
			}
		}
	}
	if n != 8 {
		t.Errorf("%d steps, want 8", n)
	}

	cta := content(cs[3])
	for k, v := range map[string]string{
		"headline":        "Moving from WordPress?",
		"buttonText":      "Import a site",
		"buttonUrl":       "https://hatamoto.dev/docs/migrate-contribute/move-your-site",
		"backgroundStyle": "kasumi",
		"layout":          "split",
	} {
		if cta[k] != v {
			t.Errorf("cta %s = %v, want %q", k, cta[k], v)
		}
	}
}

// TestHomeDocCopyRules: no link to the (hidden, per-install) admin and no
// em or en dashes. Docs URLs may contain "/admin/" as a docs section name.
func TestHomeDocCopyRules(t *testing.T) {
	raw, err := jsonx.Marshal(HomeDoc("x"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, bad := range []string{`"/admin`, "hatamoto.dev/admin", "—", "–", "\\u2014", "\\u2013"} {
		if strings.Contains(s, bad) {
			t.Errorf("seed contains %q", bad)
		}
	}
}

func TestIsSeedIDs(t *testing.T) {
	var home []string
	for _, c := range components(t, "S") {
		home = append(home, c.ID)
	}
	for _, tc := range []struct {
		name string
		ids  []string
		want bool
	}{
		{"legacy single", []string{"welcome"}, true},
		{"home doc", home, true},
		{"extra section", []string{"welcome", "mine"}, false},
		{"empty", nil, false},
		{"prefix without dash", []string{"welcomex"}, false},
	} {
		if got := IsSeedIDs(tc.ids); got != tc.want {
			t.Errorf("%s: IsSeedIDs(%v) = %v, want %v", tc.name, tc.ids, got, tc.want)
		}
	}
}
