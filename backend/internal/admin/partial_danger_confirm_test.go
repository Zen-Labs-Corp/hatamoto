package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func sampleDanger() ui.DangerConfirm {
	return ui.DangerConfirm{
		ID: "dc", Action: "/x", Back: "/y", Phrase: "/admin", CSRF: "tok",
		Title: "/admin is the first address attack tools try",
		Body: []ui.DangerText{
			{Parts: []ui.DangerPart{{Text: "Bots scan for "}, {Text: "/admin", Code: true}, {Text: " and "}, {Text: "everything", Strong: true}, {Text: "."}}},
		},
		Prompt: "To use this address anyway, type /admin below.",
		Label:  "I understand, use /admin",
		Hidden: []ui.ConfirmHidden{{Name: "id", Value: "7"}},
	}
}

func TestPartialDangerConfirm(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	got := checkPartial(t, a, "hm/danger-confirm", "default", sampleDanger())
	e := sampleDanger()
	e.Error = "The text does not match."
	errOut := checkPartial(t, a, "hm/danger-confirm", "error", e)

	for _, want := range []string{`name="confirm_phrase"`, `data-hm-danger-submit`, `data-hm-danger-input`, `data-hm-danger-phrase="/admin"`, `<code>/admin</code>`, `<strong>everything</strong>`, `name="_csrf"`, `name="id" value="7"`} {
		if !strings.Contains(got, want) {
			t.Errorf("default render lacks %s", want)
		}
	}
	for _, out := range []string{got, errOut} {
		for _, want := range []string{`autocapitalize="off"`, `autocorrect="off"`, `spellcheck="false"`} {
			if !strings.Contains(out, want) {
				t.Errorf("phrase input lacks %s", want)
			}
		}
	}
	if strings.Contains(got, "disabled") {
		t.Error("server HTML must never disable the submit: the form has to work without JS")
	}
	cancel, submit := strings.Index(got, ">Cancel<"), strings.Index(got, "data-hm-danger-submit")
	if cancel < 0 || submit < 0 || cancel > submit {
		t.Errorf("Cancel must come before the danger submit (cancel=%d submit=%d)", cancel, submit)
	}

	if strings.Contains(got, "aria-invalid") {
		t.Error("the default render must not be marked invalid")
	}
	if !strings.Contains(errOut, `aria-invalid="true"`) || !strings.Contains(errOut, "The text does not match.") {
		t.Errorf("error render must mark the input invalid and show the text:\n%s", errOut)
	}
	if !strings.Contains(errOut, `aria-describedby="dc-phrase-error"`) || !strings.Contains(errOut, `id="dc-phrase-error"`) {
		t.Errorf("aria-describedby must point at an id in the output:\n%s", errOut)
	}
	if !strings.Contains(errOut, "autofocus") {
		t.Error("the error render must focus the invalid phrase input")
	}
	if strings.Contains(got, "autofocus") {
		t.Error("the default render must not steal focus")
	}
	if strings.Contains(errOut, "disabled") {
		t.Error("the error render must not disable the submit either")
	}
}

// The required fields fail the render, so a danger card cannot ship without an
// action, a phrase, a way back or a verb on its button.
func TestPartialDangerConfirmRequiresItsContract(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	for name, c := range map[string]struct {
		mod  func(*ui.DangerConfirm)
		want string
	}{
		"action": {func(d *ui.DangerConfirm) { d.Action = "" }, "Action"},
		"phrase": {func(d *ui.DangerConfirm) { d.Phrase = "" }, "Phrase"},
		"title":  {func(d *ui.DangerConfirm) { d.Title = "" }, "Title"},
		"back":   {func(d *ui.DangerConfirm) { d.Back = "" }, "Back"},
		"id":     {func(d *ui.DangerConfirm) { d.ID = "" }, "ID"},
		"label":  {func(d *ui.DangerConfirm) { d.Label = "" }, "Label"},
		"prompt": {func(d *ui.DangerConfirm) { d.Prompt = "" }, "Prompt"},
	} {
		d := sampleDanger()
		c.mod(&d)
		if _, err := a.RenderPartial("hm/danger-confirm", d); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error naming %s, got %v", name, c.want, err)
		}
	}
	if _, err := a.RenderPartial("hm/danger-confirm", sampleDanger()); err != nil {
		t.Errorf("a full contract must render: %v", err)
	}
}
