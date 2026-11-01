package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

// renderChips renders partial with data and returns its markup after the
// accessibility check.
func renderChips(t *testing.T, partial string, data any) string {
	t.Helper()
	out, err := newPartialAdmin(t).RenderPartial(partial, data)
	if err != nil {
		t.Fatalf("%s: %v", partial, err)
	}
	s := string(out)
	if issues := a11y.Check(s, a11y.Options{}); len(issues) > 0 {
		t.Errorf("%s a11y: %s", partial, strings.Join(issues, "; "))
	}
	return s
}

func TestPartialFieldChips(t *testing.T) {
	t.Parallel()

	one := renderChips(t, "hm/field", map[string]any{"ID": "scope-a", "Name": "scopes", "Value": "posts:read", "Label": "Read posts", "Code": "posts:read", "Control": "chip", "Checked": true})
	for _, want := range []string{`class="hm-field__chip"`, `type="checkbox"`, `value="posts:read"`, ` checked`, `<code class="hm-field__code">posts:read</code>`} {
		if !strings.Contains(one, want) {
			t.Errorf("chip: missing %q in %s", want, one)
		}
	}

	group := renderChips(t, "hm/field", map[string]any{"ID": "sep", "Name": "sep", "Label": "Separator", "Control": "chipradio", "Mono": true,
		"Options": []map[string]any{{"Value": "-", "Label": "-", "Selected": true}, {"Value": "|", "Label": "|"}}})
	for _, want := range []string{`<fieldset id="sep"`, `<legend`, `hm-field__chip--mono`, `type="radio"`, `id="sep-1"`, `value="-" checked`} {
		if !strings.Contains(group, want) {
			t.Errorf("chipradio: missing %q in %s", want, group)
		}
	}

	plain := renderChips(t, "hm/field", map[string]any{"ID": "x", "Name": "x", "Label": "X", "Control": "checkbox"})
	if strings.Contains(plain, "hm-field__code") {
		t.Errorf("a checkbox without Code must not render a code element: %s", plain)
	}
}

func TestPartialPair(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	start, err := a.RenderPartial("hm/pair-start", nil)
	if err != nil {
		t.Fatal(err)
	}
	end, err := a.RenderPartial("hm/pair-end", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(start) + string(end); got != `<div class="hm-pair"></div>` {
		t.Errorf("pair markup = %q", got)
	}
}
