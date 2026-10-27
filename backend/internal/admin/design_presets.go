package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/siteconfig"
)

var presetBreakpoints = []struct{ id, title string }{{"base", "Desktop"}, {"md", "Tablet"}, {"sm", "Phone"}}

type presetField struct {
	ID, Name, Label, Value, Error, ListID string
}

type presetGroup struct {
	Title  string
	Fields []presetField
}

type presetView struct {
	Name, Label string
	LabelField  presetField
	Groups      []presetGroup // one per breakpoint, listing only the keys that are set
	AddKey      presetField   // the "Add a setting" select, over the keys not set yet
	AddKeyOpts  []ui.Option
	AddValues   []presetField // one value input per breakpoint for the chosen key
	Open        bool
	DeleteHref  string
	SetLine     string // "3 settings set", shown under the card title
	StateNote   string // "Also has state styles: Hover (3) ...", empty when the preset has none
}

type presetList struct {
	ID      string
	Options []string
}

type presetsData struct {
	Presets  []presetView
	Lists    []presetList
	NewName  presetField
	NewLabel presetField
	NewKey   []ui.Option
	NewValue presetField
	Summary  []ui.FieldError
}

// presetEntry is one preset as shown: its name and its (possibly rejected) values.
type presetEntry struct {
	name   string
	preset builder.Preset
	// stored is the preset as saved, set when the page re-renders a failed save: a key
	// cleared in the posted form stays listed, so the next submit still sends the clear.
	stored builder.Preset
}

// presetLists are the datalists: one per listed style key, holding its tokens
// and enum members. pt stays for the add-preset form.
func presetLists(listed map[string]bool) []presetList {
	var out []presetList
	for _, e := range builder.StyleSpecTable() {
		if !listed[e.Key] && e.Key != "pt" {
			continue
		}
		l := presetList{ID: "dl-" + e.Key}
		for k := range e.Tokens {
			l.Options = append(l.Options, k)
		}
		for k := range e.Enum {
			l.Options = append(l.Options, k)
		}
		sort.Strings(l.Options)
		out = append(out, l)
	}
	return out
}

// orderedPresetKeys lists the title of every key of the vocabulary: the curated
// order first, then any key the curated list does not know.
func orderedPresetKeys() []struct{ key, title string } {
	known := map[string]bool{}
	out := append([]struct{ key, title string }(nil), styleKeyList...)
	for _, k := range styleKeyList {
		known[k.key] = true
	}
	for _, e := range builder.StyleSpecTable() {
		if !known[e.Key] {
			out = append(out, struct{ key, title string }{e.Key, e.Key})
		}
	}
	return out
}

func presetFieldName(name, bp, key string) string { return "s." + name + "." + bp + "." + key }
func presetFieldID(name, bp, key string) string   { return "pr-" + name + "-" + bp + "-" + key }

// The add row of a card: add.<name>.key picks a setting, add.<name>.<bp> holds its values.
func presetAddName(name, part string) string { return "add." + name + "." + part }
func presetAddID(name, part string) string   { return "pr-" + name + "-add-" + part }

// setKeys is the set of style keys the preset sets at any breakpoint. State
// styles (base:hover ...) are not listed on this page, so they add no row.
func setKeys(p builder.Preset) map[string]bool {
	set := map[string]bool{}
	for _, bp := range presetBreakpoints {
		for k, v := range p.S[bp.id] {
			if v != "" {
				set[k] = true
			}
		}
	}
	return set
}

// stateNote names the state styles of a preset ("Also has state styles: Hover
// (3), Focus (1). Edit them in the builder."), "" when it has none. The page
// keeps them on save but only the builder edits them.
func stateNote(p builder.Preset) string {
	counts := map[string]int{}
	for at, m := range p.S {
		if _, st, ok := builder.ParseSKey(at); ok && st != "" {
			counts[st] += len(m)
		}
	}
	var parts []string
	for _, st := range []string{"hover", "focus", "active", "parent-hover"} {
		if counts[st] > 0 {
			parts = append(parts, stateTitles[st]+" ("+strconv.Itoa(counts[st])+")")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Also has state styles: " + strings.Join(parts, ", ") + ". Edit them in the page editor."
}

// presetsViewFor builds the page from presets and the errors keyed by field name.
// A card lists the keys its preset (or, after a failed save, the stored one) sets, at every
// breakpoint, plus an add row that keeps what was posted in it.
func presetsViewFor(prefix string, entries []presetEntry, errs map[string]string, post url.Values) presetsData {
	d := presetsData{}
	keys := orderedPresetKeys()
	listed := map[string]bool{}
	for _, en := range entries {
		v := presetView{Name: en.name, Label: en.preset.Label, DeleteHref: prefix + "/theme/presets/" + en.name + "/delete"}
		labelName := "label." + en.name
		v.LabelField = presetField{ID: "pr-" + en.name + "-label", Name: labelName, Label: "Name shown in the editor", Value: en.preset.Label, Error: errs[labelName]}
		if v.LabelField.Error != "" {
			v.Open = true
			d.Summary = append(d.Summary, ui.FieldError{ID: v.LabelField.ID, Label: v.LabelField.Label + " (" + en.name + ")", Message: v.LabelField.Error})
		}
		set := setKeys(en.preset)
		for k := range setKeys(en.stored) {
			set[k] = true
		}
		count := 0
		for _, bp := range presetBreakpoints {
			g := presetGroup{Title: bp.title}
			for _, k := range keys {
				if !set[k.key] {
					continue
				}
				listed[k.key] = true
				f := presetField{
					ID: presetFieldID(en.name, bp.id, k.key), Name: presetFieldName(en.name, bp.id, k.key),
					Label: k.title, Value: en.preset.S[bp.id][k.key], ListID: "dl-" + k.key,
				}
				if m := errs[f.Name]; m != "" {
					f.Error, v.Open = m, true
					d.Summary = append(d.Summary, ui.FieldError{ID: f.ID, Label: k.title + ", " + bp.title + " (" + en.name + ")", Message: m})
				}
				if f.Value != "" {
					count++
				}
				g.Fields = append(g.Fields, f)
			}
			v.Groups = append(v.Groups, g)
		}
		v.AddKey = presetField{ID: presetAddID(en.name, "key"), Name: presetAddName(en.name, "key"), Label: "Add a setting", Error: errs[presetAddName(en.name, "key")]}
		if v.AddKey.Error != "" {
			v.Open = true
			d.Summary = append(d.Summary, ui.FieldError{ID: v.AddKey.ID, Label: v.AddKey.Label + " (" + en.name + ")", Message: v.AddKey.Error})
		}
		v.AddKeyOpts = []ui.Option{{Value: "", Label: "Choose a setting"}}
		for _, k := range keys {
			if !set[k.key] {
				v.AddKeyOpts = append(v.AddKeyOpts, ui.Option{Value: k.key, Label: k.title, Selected: k.key == post.Get(v.AddKey.Name)})
			}
		}
		for _, bp := range presetBreakpoints {
			f := presetField{ID: presetAddID(en.name, bp.id), Name: presetAddName(en.name, bp.id), Label: "Value, " + strings.ToLower(bp.title)}
			if post.Get(v.AddKey.Name) != "" && !set[post.Get(v.AddKey.Name)] {
				f.Value = strings.TrimSpace(post.Get(f.Name))
			}
			v.AddValues = append(v.AddValues, f)
		}
		v.SetLine = countNoun(count, "setting") + " set"
		v.StateNote = stateNote(en.preset)
		d.Presets = append(d.Presets, v)
	}
	d.Lists = presetLists(listed)
	return d
}

// addFormData fills the add-preset form from the posted values and errors.
func (d *presetsData) addForm(posted map[string]string, errs map[string]string) {
	d.NewName = presetField{ID: "new-name", Name: "new_name", Label: "Preset name", Value: posted["new_name"], Error: errs["new_name"]}
	d.NewLabel = presetField{ID: "new-label", Name: "new_label", Label: "Name shown in the editor", Value: posted["new_label"], Error: errs["new_label"]}
	d.NewValue = presetField{ID: "new-value", Name: "new_value", Label: "Value (desktop)", Value: posted["new_value"], Error: errs["new_value"], ListID: "dl-pt"}
	opts := make([]ui.Option, 0, len(styleKeyList))
	for _, k := range orderedPresetKeys() {
		opts = append(opts, ui.Option{Value: k.key, Label: k.title, Selected: k.key == posted["new_key"]})
	}
	d.NewKey = opts
	for _, f := range []presetField{d.NewName, d.NewLabel, d.NewValue} {
		if f.Error != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: f.ID, Label: f.Label, Message: f.Error})
		}
	}
}

// storedPresets reads the presets as the page shows them (invalid stored ones are dropped).
func (a *Admin) storedPresets(r *http.Request) ([]presetEntry, *siteconfig.Config, error) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		return nil, nil, err
	}
	design, _ := builder.ParseDesign(cfg.ThemeExtra)
	names := make([]string, 0, len(design.Presets))
	for n := range design.Presets {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]presetEntry, 0, len(names))
	for _, n := range names {
		out = append(out, presetEntry{name: n, preset: design.Presets[n]})
	}
	return out, cfg, nil
}

func (a *Admin) renderPresets(w http.ResponseWriter, r *http.Request, status int, d presetsData) {
	v := a.NewView(w, r, "Style presets")
	v.Section, v.Tab = "presets", "presets"
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "settings/designpresets", v)
}

func (a *Admin) handlePresetsForm(w http.ResponseWriter, r *http.Request) {
	entries, _, err := a.storedPresets(r)
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	d := presetsViewFor(a.adminPath, entries, nil, nil)
	d.addForm(nil, nil)
	a.renderPresets(w, r, http.StatusOK, d)
}

// presetFromForm applies one preset's posted form to its stored version. A cell
// that is on the form is the new value, and an empty one clears it; a cell that
// is not on the form (a key the card does not list) keeps its stored value, so a
// save never drops what the page did not show. The add row sets a key at the
// breakpoints it has a value for. Values are checked cell by cell so each error
// lands on its field.
func presetFromForm(r *http.Request, name string, stored builder.Preset, errs map[string]string) builder.Preset {
	p := builder.Preset{Label: strings.TrimSpace(r.PostFormValue("label." + name)), S: map[string]map[string]string{}}
	for bp, m := range stored.S {
		p.S[bp] = make(map[string]string, len(m))
		for k, v := range m {
			p.S[bp][k] = v
		}
	}
	set := func(bp, key, field, v string) {
		if v == "" {
			delete(p.S[bp], key)
			return
		}
		if p.S[bp] == nil {
			p.S[bp] = map[string]string{}
		}
		p.S[bp][key] = v
		if builder.ValidateStyleValue(key, v) != nil {
			errs[field] = "Not a valid value. Pick one from the suggestions or use a size like 1rem."
		}
	}
	for _, bp := range presetBreakpoints {
		for _, k := range orderedPresetKeys() {
			field := presetFieldName(name, bp.id, k.key)
			if _, posted := r.PostForm[field]; posted {
				set(bp.id, k.key, field, strings.TrimSpace(r.PostFormValue(field)))
			}
		}
	}
	if key := strings.TrimSpace(r.PostFormValue(presetAddName(name, "key"))); key != "" {
		keyField := presetAddName(name, "key")
		if _, ok := styleKeyTitles[key]; !ok {
			errs[keyField] = "Choose a setting from the list."
		} else {
			any := false
			for _, bp := range presetBreakpoints {
				if v := strings.TrimSpace(r.PostFormValue(presetAddName(name, bp.id))); v != "" {
					any = true
					set(bp.id, key, presetFieldName(name, bp.id, key), v)
				}
			}
			if !any {
				errs[keyField] = "Enter a value for at least one device."
			}
		}
	}
	for bp, m := range p.S {
		if len(m) == 0 {
			delete(p.S, bp)
		}
	}
	return p
}

// checkPreset adds the label and "no style set" errors; ValidatePreset is the one validator.
func checkPreset(name string, p builder.Preset, errs map[string]string, labelField string) {
	if err := builder.ValidatePreset(name, p); err != nil {
		cellBad := false
		for k := range errs {
			cellBad = cellBad || strings.HasPrefix(k, "s."+name+".")
		}
		if !cellBad {
			errs[labelField] = err.Error()
		}
	}
}

func (a *Admin) handlePresetsSave(w http.ResponseWriter, r *http.Request) {
	entries, cfg, err := a.storedPresets(r)
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	errs := map[string]string{}
	updates := map[string]*builder.Preset{}
	shown := make([]presetEntry, 0, len(entries))
	_ = r.ParseForm()
	for _, en := range entries {
		if _, posted := r.PostForm["label."+en.name]; !posted {
			shown = append(shown, en) // not on the posted form (deleted meanwhile): left as stored
			continue
		}
		p := presetFromForm(r, en.name, en.preset, errs)
		checkPreset(en.name, p, errs, "label."+en.name)
		shown = append(shown, presetEntry{name: en.name, preset: p, stored: en.preset})
		updates[en.name] = &p
	}
	if len(errs) > 0 {
		d := presetsViewFor(a.adminPath, shown, errs, r.PostForm)
		d.addForm(nil, nil)
		a.renderPresets(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	a.writePresets(w, r, cfg, updates, "Style presets saved.")
}

func (a *Admin) handlePresetAdd(w http.ResponseWriter, r *http.Request) {
	entries, cfg, err := a.storedPresets(r)
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	posted := map[string]string{}
	for _, k := range []string{"new_name", "new_label", "new_key", "new_value"} {
		posted[k] = strings.TrimSpace(r.PostFormValue(k))
	}
	errs := map[string]string{}
	p := builder.Preset{Label: posted["new_label"], S: map[string]map[string]string{"base": {posted["new_key"]: posted["new_value"]}}}
	if err := builder.ValidatePreset(posted["new_name"], p); err != nil {
		switch {
		case !builder.ValidPresetName(posted["new_name"]):
			errs["new_name"] = "Use lowercase letters, digits and hyphens, starting with a letter (32 characters at most)."
		case len(posted["new_label"]) > 60:
			errs["new_label"] = "Keep the name under 60 characters."
		default:
			errs["new_value"] = "Not a valid value for this setting. Pick one from the suggestions."
		}
	}
	var raw []byte
	if len(errs) == 0 {
		raw, err = builder.AddPreset(cfg.ThemeExtra, posted["new_name"], p)
		switch {
		case errors.Is(err, builder.ErrPresetExists):
			errs["new_name"] = "A preset with this name exists."
		case errors.Is(err, builder.ErrTooManyPresets):
			errs["new_name"] = "A site can have at most 48 presets."
		case err != nil:
			a.logger().Error("add preset failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The preset could not be saved.")
			return
		}
	}
	if len(errs) > 0 {
		d := presetsViewFor(a.adminPath, entries, nil, nil)
		d.addForm(posted, errs)
		a.renderPresets(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	a.storePresets(w, r, raw, "Preset added. Open it below to set more styles.")
}

func (a *Admin) handlePresetDeleteForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !a.presetExists(w, r, name) {
		return
	}
	a.renderConfirm(w, r, confirmData{
		Title: "Delete preset " + name + "?", Message: "Pages that use it lose its styles. Their own style settings stay.",
		Post: a.adminPath + "/theme/presets/" + name + "/delete", Label: "Delete preset", Back: a.adminPath + "/theme/presets", Danger: true,
	})
}

func (a *Admin) handlePresetDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !a.presetExists(w, r, name) {
		return
	}
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	a.writePresets(w, r, cfg, map[string]*builder.Preset{name: nil}, "Preset "+name+" deleted.")
}

func (a *Admin) presetExists(w http.ResponseWriter, r *http.Request, name string) bool {
	entries, _, err := a.storedPresets(r)
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return false
	}
	for _, en := range entries {
		if en.name == name {
			return true
		}
	}
	a.Error(w, r, http.StatusNotFound, "Preset not found", "This preset does not exist.")
	return false
}

func (a *Admin) writePresets(w http.ResponseWriter, r *http.Request, cfg *siteconfig.Config, updates map[string]*builder.Preset, flash string) {
	raw, err := builder.MergePresets(cfg.ThemeExtra, updates)
	if err != nil {
		a.logger().Error("save presets failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The presets could not be saved.")
		return
	}
	a.storePresets(w, r, raw, flash)
}

// storePresets writes the theme settings JSON, purges the rendered pages and redirects.
func (a *Admin) storePresets(w http.ResponseWriter, r *http.Request, raw []byte, flash string) {
	if _, err := a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{ThemeExtra: json.RawMessage(raw)}); err != nil {
		a.logger().Error("save presets failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The presets could not be saved.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", flash)
	http.Redirect(w, r, a.adminPath+"/theme/presets", http.StatusSeeOther)
}

// presetReply is the JSON of the editor's create endpoint.
type presetReply struct {
	Name  string                       `json:"name,omitempty"`
	Label string                       `json:"label,omitempty"`
	S     map[string]map[string]string `json:"s,omitempty"`
	Code  string                       `json:"code,omitempty"`
	Error string                       `json:"error,omitempty"`
}

const presetBodyMax = 16 << 10

// handlePresetCreate is POST /admin/builder/presets: the editor's "Save as
// preset". Create only; an existing name is a 409, updates and deletes are the
// owner's, in Settings.
func (a *Admin) handlePresetCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, presetBodyMax)
	var req struct {
		Name  string                       `json:"name"`
		Label string                       `json:"label"`
		S     map[string]map[string]string `json:"s"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.writeJSON(w, http.StatusBadRequest, presetReply{Code: "bad_request", Error: "Invalid request body."})
		return
	}
	p := builder.Preset{Label: strings.TrimSpace(req.Label), S: req.S}
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.writeJSON(w, http.StatusInternalServerError, presetReply{Code: "server_error", Error: "The settings could not be loaded."})
		return
	}
	raw, err := builder.AddPreset(cfg.ThemeExtra, req.Name, p)
	switch {
	case errors.Is(err, builder.ErrPresetExists):
		a.writeJSON(w, http.StatusConflict, presetReply{Code: "exists", Error: "A preset named " + req.Name + " already exists."})
		return
	case errors.Is(err, builder.ErrTooManyPresets):
		a.writeJSON(w, http.StatusBadRequest, presetReply{Code: "too_many", Error: "A site can have at most 48 presets."})
		return
	case err != nil:
		a.writeJSON(w, http.StatusUnprocessableEntity, presetReply{Code: "invalid", Error: err.Error()})
		return
	}
	if _, err := a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{ThemeExtra: json.RawMessage(raw)}); err != nil {
		a.logger().Error("create preset failed", "err", err)
		a.writeJSON(w, http.StatusInternalServerError, presetReply{Code: "server_error", Error: "The preset could not be saved."})
		return
	}
	a.invalidate(r.Context())
	a.writeJSON(w, http.StatusCreated, presetReply{Name: req.Name, Label: p.Label, S: p.S})
}

// bootDesign is boot.design of the editor.
type bootDesign struct {
	Tokens    bool         `json:"tokens"`
	Presets   []bootPreset `json:"presets"`
	CanCreate bool         `json:"canCreate"`
	// Palette is what the Style tab's colour and length controls show: values resolved from the
	// site settings (the theme may paint differently), never read from the canvas.
	Palette *bootPalette `json:"palette,omitempty"`
}

// bootPalette holds the resolved token values: colours by name (primary bg text surface muted accent),
// spacing by step ("0".."12") and font sizes by name (xs..4xl).
type bootPalette struct {
	Colors map[string]string `json:"colors"`
	Space  map[string]string `json:"space"`
	FS     map[string]string `json:"fs"`
}

// builderPalette resolves the palette: the built-in token defaults overlaid by the site's own
// tokens (ParseDesign returns only the overrides), and the theme's primary, background and
// text colours. A nil cfg gives the defaults.
func builderPalette(cfg *siteconfig.Config) *bootPalette {
	t := builder.DefaultTokens()
	colors := siteconfig.DefaultThemeConfig().Colors
	p := &bootPalette{Colors: map[string]string{"primary": colors.Primary, "bg": colors.Background, "text": colors.Text}, Space: t.Space, FS: t.FS}
	if cfg != nil {
		design, _ := builder.ParseDesign(cfg.ThemeExtra)
		for k, v := range design.Tokens.Space {
			p.Space[k] = v
		}
		for k, v := range design.Tokens.FS {
			p.FS[k] = v
		}
		for k, v := range design.Tokens.Color {
			t.Color[k] = v
		}
		f := designFrom(cfg)
		// A stored theme colour that is not #rrggbb (hand-edited JSON) keeps the default swatch.
		for k, v := range map[string]string{"primary": f.Primary, "bg": f.Background, "text": f.Text} {
			if hexColour.MatchString(v) {
				p.Colors[k] = v
			}
		}
	}
	for _, k := range []string{"surface", "muted", "accent"} {
		p.Colors[k] = t.Color[k]
	}
	return p
}

// BuilderPalette is the resolved palette of the site config for callers outside the admin (the MCP
// builder_design tool): colours by name, spacing by step and font sizes by name.
func BuilderPalette(cfg *siteconfig.Config) (colors, space, fs map[string]string) {
	p := builderPalette(cfg)
	return p.Colors, p.Space, p.FS
}

type bootPreset struct {
	Name  string                       `json:"name"`
	Label string                       `json:"label"`
	S     map[string]map[string]string `json:"s"`
}

// builderDesign is the design data the editor starts from.
func (a *Admin) builderDesign(r *http.Request) bootDesign {
	d := bootDesign{Presets: []bootPreset{}, CanCreate: true}
	d.Tokens = a.deps.BuilderTokens != nil && a.deps.BuilderTokens()
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		cfg = nil
	}
	d.Palette = builderPalette(cfg)
	if cfg != nil {
		design, _ := builder.ParseDesign(cfg.ThemeExtra)
		names := make([]string, 0, len(design.Presets))
		for n := range design.Presets {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p := design.Presets[n]
			d.Presets = append(d.Presets, bootPreset{Name: n, Label: p.Label, S: p.S})
		}
	}
	return d
}
