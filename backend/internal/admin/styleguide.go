package admin

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sort"

	"hatamoto.dev/core/internal/admin/ui"
)

// The styleguide is a development page (HATAMOTO_DEV=1) that shows every
// component in components.json in each of its variants. UI-D14 in the browser
// checks computed values on it, light and dark, so a variant that no real
// screen renders is still held to its contract.

// sample is one rendering of a component: a partial with its data, or, for a
// component with no partial (or one that only shows when open), fixed markup.
type sample struct {
	Label   string
	Variant string        // the components.json variants key this sample shows; empty for the base
	Data    any           // data for the component's partial
	HTML    template.HTML // used instead of the partial when set
}

// sgGuessable is the address the danger-confirm sample warns about: the default,
// whatever prefix this instance is mounted on.
const sgGuessable = DefaultAdminPath

// styleguideSamples maps a components.json key to its samples. new-component.sh adds
// an entry here; TestStyleguideCoversTheSpec fails for a component without one.
func styleguideSamples(prefix string) map[string][]sample {
	return map[string][]sample{
		"badge": {
			{Label: "Neutral", Data: ui.Badge{Label: "Draft"}},
			{Label: "Success", Data: ui.Badge{Label: "Published", Kind: "success"}},
			{Label: "Warning", Data: ui.Badge{Label: "Scheduled", Kind: "warning"}},
			{Label: "Danger", Data: ui.Badge{Label: "Failed", Kind: "danger"}},
			{Label: "Small", Variant: "sm", Data: ui.Badge{Label: "First-party", Plain: true, Size: "sm", Icon: "star"}},
		},
		"button": {
			{Label: "Primary", Variant: "primary", Data: ui.Button{Label: "Save", Variant: "primary"}.Dict()},
			{Label: "Secondary", Data: ui.Button{Label: "Cancel", Variant: "secondary"}.Dict()},
			{Label: "Ghost", Variant: "ghost", Data: ui.Button{Label: "Skip", Variant: "ghost"}.Dict()},
			{Label: "Danger", Variant: "danger", Data: ui.Button{Label: "Delete", Variant: "danger"}.Dict()},
			{Label: "Danger text", Data: ui.Button{Label: "Remove", Variant: "danger-text"}.Dict()},
			{Label: "Small", Variant: "sm", Data: ui.Button{Label: "Edit", Variant: "secondary", Size: "sm"}.Dict()},
			{Label: "Icon only", Variant: "icon", Data: ui.Button{Label: "Delete", Variant: "secondary", Icon: "trash", IconOnly: true, AriaLabel: "Delete item"}.Dict()},
			{Label: "Pressed", Variant: "pressed", Data: ui.Button{Label: "Bold", Variant: "secondary", Attrs: map[string]string{"aria-pressed": "true"}}.Dict()},
		},
		"card": {
			{Label: "Card", HTML: `<section class="hm-card"><div class="hm-card__head"><h3 class="hm-card__title">Card title</h3></div><div class="hm-card__body"><p>Card body.</p></div></section>`},
		},
		"chip": {
			{Label: "Chip and more", HTML: `<span class="hm-msel__chip">Design</span> <span class="hm-msel__more">+2 more</span>`},
		},
		"colophon": {
			{Label: "Colophon", HTML: `<footer class="hm-colophon"><a href="https://hatamoto.dev" rel="noopener" target="_blank" title="Hatamoto v1.4.3">Hatamoto v1.4.3 <span aria-hidden="true">&middot;</span> Stand close. Serve well.</a></footer>`},
		},
		"dialog": {
			{Label: "Dialog panel", HTML: `<div class="hm-dialog" role="group" aria-label="Dialog sample"><div class="hm-dialog__panel"><h3 class="hm-dialog__title">Dialog title</h3><p class="hm-dialog__body">Dialog body.</p></div></div>`},
			{Label: "Drawer", Variant: "drawer", HTML: `<div class="hm-dialog" data-side="end" role="group" aria-label="Drawer sample"><div class="hm-dialog__panel"><h3 class="hm-dialog__title">Drawer title</h3><p class="hm-dialog__body">Drawer body.</p></div></div>`},
		},
		"input": {
			{Label: "Text", Data: map[string]any{"ID": "sg-text", "Name": "sg_text", "Label": "Title", "Value": "A page title"}},
			{Label: "Select", Variant: "select", Data: map[string]any{"ID": "sg-select", "Name": "sg_select", "Label": "Status", "Control": "select", "Options": []ui.Option{{Value: "draft", Label: "Draft", Selected: true}, {Value: "live", Label: "Live"}}}},
			{Label: "Search", Data: map[string]any{"ID": "sg-search", "Name": "sg_search", "Label": "Search", "Type": "search"}},
		},
		"menu": {
			{Label: "Action menu", HTML: `<div class="hm-menu" role="menu" aria-label="Menu sample"><button type="button" class="hm-menu__item" role="menuitem">Edit</button><button type="button" class="hm-menu__item" role="menuitem">Duplicate</button></div>`},
		},
		"popover": {
			{Label: "Popover panel", HTML: `<div class="hm-popover-panel" role="group" aria-label="Popover sample"><header><h3>Filters</h3></header><div class="hm-popover-panel__body"><p>Panel body.</p></div></div>`},
		},
		"searchselect-list": {
			{Label: "Results list", HTML: `<ul class="hm-searchselect__list" role="listbox" aria-label="Search results sample"><li class="hm-searchselect__option" role="option" aria-selected="false">About us</li><li class="hm-searchselect__option" role="option" aria-selected="true">Contact</li></ul>`},
		},
		"select-list": {
			{Label: "Options list", HTML: `<ul class="hm-select__list" role="listbox" aria-label="Options sample"><li class="hm-select__option" role="option" aria-selected="false">First</li><li class="hm-select__option" role="option" aria-selected="true">Second</li></ul>`},
		},
		"tabs": {
			{Label: "Underline row", Data: map[string]any{"Label": "Styleguide tabs", "Items": []map[string]any{
				{"Href": "#one", "Label": "One", "Active": true}, {"Href": "#two", "Label": "Two"},
			}}},
			{Label: "Segmented", Variant: "segmented", Data: map[string]any{"Label": "Styleguide segmented tabs", "Variant": "segmented", "Changes": "view", "Items": []map[string]any{
				{"Href": "?view=one", "Label": "One", "Active": true}, {"Href": "?view=two", "Label": "Two"},
			}}},
			{Label: "Segmented, current tab", Variant: "segmented-current", Data: map[string]any{"Label": "Styleguide segmented current tab", "Variant": "segmented", "Changes": "view", "Items": []map[string]any{
				{"Href": "?view=one", "Label": "One"}, {"Href": "?view=two", "Label": "Two", "Active": true}, {"Href": "?view=three", "Label": "Three"},
			}}},
			{Label: "Sub row", Variant: "sub", Data: map[string]any{"Label": "Styleguide sub tabs", "Variant": "sub", "Items": []map[string]any{
				{"Href": "#one", "Label": "One", "Active": true}, {"Href": "#two", "Label": "Two"},
			}}},
			{Label: "JS tablist", Variant: "tablist", HTML: `<div class="hm-tablist" role="tablist" aria-label="Styleguide tablist"><button type="button" class="hm-tablist__tab" role="tab" id="sg-tab-one" aria-controls="sg-panel-one" aria-selected="true" tabindex="0">One</button><button type="button" class="hm-tablist__tab" role="tab" id="sg-tab-two" aria-controls="sg-panel-two" aria-selected="false" tabindex="-1">Two</button></div><div role="tabpanel" id="sg-panel-one" aria-labelledby="sg-tab-one" tabindex="0">One panel</div><div role="tabpanel" id="sg-panel-two" aria-labelledby="sg-tab-two" tabindex="0" hidden>Two panel</div>`},
		},
		"dropzone": {
			{Label: "Default", Data: ui.Dropzone{ID: "sample-file", Accept: "image/png", MaxMB: 10, Help: "PNG, up to 10 MB each."}},
		},
		"savestate": {
			{Label: "Saved", Data: ui.Savestate{State: "saved", Text: "Saved 10:42"}},
			{Label: "Unsaved", Data: ui.Savestate{State: "unsaved", Text: "Unsaved changes"}},
			{Label: "Saving", Data: ui.Savestate{State: "saving", Text: "Saving…"}},
			{Label: "Error", Data: ui.Savestate{State: "error", Text: "Couldn't save, retrying"}},
		},
		"confirmcard": {
			{Label: "Danger", Data: ui.Confirmcard{Post: "#", Back: "#", Label: "Delete theme", Message: "The theme is removed from this site. Its files are deleted.", Danger: true}},
		},
		"segmented": {
			{Label: "Default", Variant: "pressed", Data: ui.Segmented{Label: "Status", Action: prefix + "/styleguide", Name: "status", Items: []ui.SegmentedItem{{Label: "New", Value: "new", Pressed: true}, {Label: "Read", Value: "read"}, {Label: "Archived", Value: "archived"}}}},
			{Label: "Radio", Variant: "checked", Data: ui.Segmented{Mode: "radio", Label: "Access", Name: "sg_access", Items: []ui.SegmentedItem{{Label: "Allow", Value: "allow", Pressed: true}, {Label: "Block", Value: "deny"}}}},
			{Label: "Buttons", Data: ui.Segmented{Mode: "buttons", Label: "Preview width", Items: []ui.SegmentedItem{{Label: "Desktop", Pressed: true}, {Label: "Phone"}}}},
			{Label: "Disabled item", Variant: "disabled", Data: ui.Segmented{Mode: "buttons", Label: "Attribution", Items: []ui.SegmentedItem{{Label: "First touch", Disabled: true}, {Label: "Last touch", Disabled: true}}}},
		},
		"swatches": {
			{Label: "Default", Data: map[string]any{"Label": "Styleguide colour", "Items": []map[string]any{
				{"Label": "Ink", "Value": "ink", "Swatch": "#1c1c1c", "Pressed": true}, {"Label": "Sky", "Value": "sky", "Swatch": "#2a6fdb"}, {"Label": "Leaf", "Value": "leaf", "Swatch": "#2f8f4e"},
			}}},
		},
		"radiocard": {
			{Label: "Default", Data: ui.RadioCard{Legend: "Theme", Name: "theme", Items: []ui.RadioCardItem{{Value: "default", Title: "Default", Description: "The built-in theme.", Checked: true, Tag: &ui.Badge{Label: "Active", Kind: "success"}}, {Value: "journal", Title: "Journal", Description: "Serif, calm blog."}}}},
		},
		"danger-confirm": {
			// The phrase is the address the user is choosing, not where the admin is
			// mounted, so it is the default address and never the live prefix.
			{Label: "Default", Data: ui.DangerConfirm{
				ID: "sg-dc-default", Action: "#", Back: "#", Phrase: sgGuessable,
				Title: sgGuessable + " is the first address attack tools try",
				Body: []ui.DangerText{
					{Parts: []ui.DangerPart{{Text: "Bots scan the whole internet for "}, {Text: sgGuessable, Code: true}, {Text: ", "}, {Text: "/wp-admin", Code: true}, {Text: " and "}, {Text: "/login", Code: true}, {Text: " every day. On a guessable address they hammer your login page with password guesses and probe for exploits, and they learn which software you run. With a private address, they get a plain \"page not found\"."}}},
					{Parts: []ui.DangerPart{{Text: "Your login is still protected by your password, rate limits and two-factor authentication. A private address keeps the bots away from it in the first place."}}},
				},
				Prompt: "To use this address anyway, type " + sgGuessable + " below.",
				Label:  "I understand, use " + sgGuessable,
			}},
		},
		// new-components-above (scripts/new-component.sh inserts here)
	}
}

type styleguideSection struct {
	Name, Selector string
	Samples        []styleguideSample
}

type styleguideSample struct {
	Label string
	HTML  template.HTML
}

// styleguideSections renders every component of the spec, in name order.
func (a *Admin) styleguideSections() ([]styleguideSection, error) {
	raw, err := fs.ReadFile(a.adminFS, "components.json")
	if err != nil {
		return nil, err
	}
	var spec map[string]struct{ Selector, Partial string }
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("components.json: %w", err)
	}
	names := make([]string, 0, len(spec))
	for n := range spec {
		names = append(names, n)
	}
	sort.Strings(names)
	samples := styleguideSamples(a.adminPath)
	var out []styleguideSection
	for _, n := range names {
		if len(samples[n]) == 0 {
			continue // ownership only, no contract to show (TestStyleguideCoversTheSpec)
		}
		sec := styleguideSection{Name: n, Selector: spec[n].Selector}
		for _, s := range samples[n] {
			h := s.HTML
			if h == "" {
				if h, err = a.RenderPartial(spec[n].Partial, s.Data); err != nil {
					return nil, fmt.Errorf("styleguide %s %q: %w", n, s.Label, err)
				}
			}
			sec.Samples = append(sec.Samples, styleguideSample{Label: s.Label, HTML: h})
		}
		out = append(out, sec)
	}
	return out, nil
}

func (a *Admin) handleStyleguide(w http.ResponseWriter, r *http.Request) {
	secs, err := a.styleguideSections()
	if err != nil {
		a.logger().Error("styleguide failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The styleguide could not be rendered.")
		return
	}
	v := a.NewView(w, r, "Styleguide")
	v.Data = secs
	a.Render(w, r, http.StatusOK, "dev/styleguide", v)
}
