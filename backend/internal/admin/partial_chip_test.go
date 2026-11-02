package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
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

func TestPartialCalloutIconName(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	def, err := a.RenderPartial("hm/callout", ui.Callout{Kind: "info", Body: "Read only."})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := a.RenderPartial("hm/callout", ui.Callout{Kind: "info", Body: "Read only.", IconName: "lock"})
	if err != nil {
		t.Fatal(err)
	}
	if string(def) == string(lock) || !strings.Contains(string(lock), "<rect") {
		t.Errorf("IconName must swap the glyph:\n%s\n%s", def, lock)
	}
	viaDict, err := a.RenderPartial("hm/callout-start", map[string]any{"Kind": "info", "IconName": "lock"})
	if err != nil || !strings.Contains(string(viaDict), "<rect") {
		t.Errorf("IconName from a dict: %v %s", err, viaDict)
	}
}

func TestPartialDangerConfirmButtons(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	dc := ui.DangerConfirm{ID: "dc", Action: "/x", Back: "/y", Phrase: "go", Title: "Sure?", Prompt: "Type go.", Label: "Do it"}
	out, err := a.RenderPartial("hm/danger-confirm", dc)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); !strings.Contains(s, "hm-btn--danger") || strings.Contains(s, "hm-btn--ghost") || strings.Contains(s, "hm-btn--primary") {
		t.Errorf("the default submit is the red danger button with a plain Cancel: %s", s)
	}
	dc.Tone = "ink"
	out, err = a.RenderPartial("hm/danger-confirm", dc)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`hm-btn--ghost`, `hm-btn--primary`} {
		if !strings.Contains(s, want) {
			t.Errorf("Tone ink: missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, "hm-btn--danger") {
		t.Errorf("Tone ink: the submit is ink, not red: %s", s)
	}
}
