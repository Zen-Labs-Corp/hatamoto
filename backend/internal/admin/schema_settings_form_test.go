package admin

import (
	"encoding/json"
	"testing"
)

func TestSchemaSettingsFormKeepsTheCrumbLabel(t *testing.T) {
	t.Parallel()

	f := schemaSettingsForm{URLPattern: "/team/{slug}", ArchivePath: "/team", ArchiveCrumbLabel: "Team"}
	raw, errs := f.settingsJSON(nil)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m["archive_crumb_label"] != "Team" {
		t.Fatalf("settings = %s (%v)", raw, err)
	}
	if got := settingsFormFrom(raw).ArchiveCrumbLabel; got != "Team" {
		t.Errorf("form reads %q back", got)
	}
	// Emptying the field removes the key.
	f.ArchiveCrumbLabel = ""
	raw, _ = f.settingsJSON(raw)
	m = nil
	_ = json.Unmarshal(raw, &m)
	if _, has := m["archive_crumb_label"]; has {
		t.Errorf("an empty label stays in the settings: %s", raw)
	}
}

func TestSchemaSettingsFormEditorRoundTrip(t *testing.T) {
	t.Parallel()

	base := json.RawMessage(`{"overrides":[{"attribute":"format","equals":"report","url_pattern":"/r/{slug}"}]}`)
	raw, errs := schemaSettingsForm{Editor: "post"}.settingsJSON(base)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m["editor"] != "post" || m["overrides"] == nil {
		t.Fatalf("settings = %s (%v)", raw, err)
	}
	if got := settingsFormFrom(raw).Editor; got != "post" {
		t.Errorf("form reads %q back", got)
	}
	// Choosing Form removes the key and keeps the rest.
	raw, errs = schemaSettingsForm{}.settingsJSON(raw)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	m = nil
	_ = json.Unmarshal(raw, &m)
	if _, has := m["editor"]; has || m["overrides"] == nil {
		t.Errorf("settings after Form = %s", raw)
	}
	if _, errs = (schemaSettingsForm{Editor: "tiptap"}).settingsJSON(nil); errs["editor"] == "" {
		t.Errorf("a bad editor value is accepted: %v", errs)
	}
}

func TestSchemaEditBuiltinPostTypeShowsItsEditor(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	sc := c.ents.addSchema(60, "Post", "post", `{"type":"object","properties":{"content":{"type":"string"}}}`, `{}`, true)
	body := c.get(t, "owner-token", "/admin/settings/types/"+sc.ID.String(), "")
	has(t, body, `Post editor (built in)`, `id="editor" name="editor" disabled`)
	lacks(t, body, `<option value="">Form</option>`)
}
