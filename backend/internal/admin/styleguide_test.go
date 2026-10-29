package admin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestStyleguideServedOnlyInDev(t *testing.T) {
	t.Parallel()

	dev := newRig(t, func(d *Deps) { d.Dev = true })
	rec := dev.do("GET", "/admin/_styleguide", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("dev owner: %d\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for name, samples := range styleguide {
		for _, s := range samples {
			if !strings.Contains(body, `<p class="hm-sg__label">`+s.Label+`</p>`) {
				t.Errorf("styleguide lacks %s sample %q", name, s.Label)
			}
		}
	}
	a11yOK(t, body)

	if rec := dev.do("GET", "/admin/_styleguide", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("the styleguide is the owner's: an editor got %d, want 403", rec.Code)
	}
	prod := newRig(t)
	if rec := prod.do("GET", "/admin/_styleguide", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("without Dev the styleguide is the session-gated 404, got %d", rec.Code)
	}
}

// Every component in the spec with a contract (props or variants) needs samples, or
// UI-D14 never sees its variants. A component with neither only names its owner
// (UI-06, DC-02): there is nothing for UI-D14 to check, so it needs no sample.
func TestStyleguideCoversTheSpec(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../web/admin/components.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]struct {
		Variants map[string]string
		Props    map[string]string
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	for name, c := range spec {
		if len(styleguide[name]) == 0 && (len(c.Props) > 0 || len(c.Variants) > 0) {
			t.Errorf("components.json has %q but styleguide.go has no samples for it", name)
		}
		shown := map[string]bool{}
		for _, s := range styleguide[name] {
			if s.Variant != "" {
				if _, ok := c.Variants[s.Variant]; !ok {
					t.Errorf("styleguide %s sample %q names variant %q, which is not in components.json", name, s.Label, s.Variant)
				}
				shown[s.Variant] = true
			}
		}
		for v := range c.Variants {
			if !shown[v] {
				t.Errorf("components.json has variant %s/%s but styleguide.go has no sample with Variant %q", name, v, v)
			}
		}
	}
	for name := range styleguide {
		if _, ok := spec[name]; !ok {
			t.Errorf("styleguide.go has samples for %q, which is not in components.json", name)
		}
	}
}

// The danger-confirm sample shows the spec copy about the default address,
// whatever prefix this instance is mounted on.
func TestStyleguideDangerConfirmUsesTheDefaultAddress(t *testing.T) {
	t.Parallel()

	const private = "/x7k"
	for _, s := range styleguideSamples(private)["danger-confirm"] {
		dc, ok := s.Data.(ui.DangerConfirm)
		if !ok {
			t.Fatalf("sample %q is %T, want ui.DangerConfirm", s.Label, s.Data)
		}
		var text strings.Builder
		text.WriteString(dc.Phrase + dc.Title + dc.Prompt + dc.Label)
		for _, p := range dc.Body {
			for _, part := range p.Parts {
				text.WriteString(part.Text)
			}
		}
		if dc.Phrase != DefaultAdminPath || strings.Contains(text.String(), private) {
			t.Errorf("sample %q uses the live prefix: phrase %q", s.Label, dc.Phrase)
		}
	}
}
