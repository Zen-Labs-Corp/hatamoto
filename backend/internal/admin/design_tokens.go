package admin

import (
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/siteconfig"
)

// tokenField is one input of the Design system page.
type tokenField struct {
	ID, Name, Label, Placeholder, Value, Error, Type string
}

type tokenGroup struct {
	ID     string
	Title  string
	Intro  string
	Fields []tokenField
}

type tokensData struct {
	Groups  []tokenGroup
	OptedIn bool
	Summary []ui.FieldError
}

// tokenLayout is the page: group title, intro, token class and the names in
// order. The names are the closed vocabulary of the builder.
var tokenLayout = []struct {
	title, intro, class, label string
	names                      []string
	typ                        string
}{
	{"Colours", "Extra colours for blocks. The brand, background and text colours stay in Colours and fonts.", "color", "Colour ", []string{"surface", "muted", "accent"}, "color"},
	{"Spacing", "The spacing scale used for padding, margin and gaps, step 0 to 12.", "space", "Space ", spaceNames(), ""},
	{"Text sizes", "The type scale from extra small to 4xl.", "fs", "Text size ", []string{"xs", "sm", "md", "lg", "xl", "2xl", "3xl", "4xl"}, ""},
	{"Shadows", "Soft shadows for cards. Up to two layers, for example: 0 4px 12px rgba(0,0,0,.1)", "shadow", "Shadow ", []string{"sm", "md", "lg"}, ""},
}

// builderColourNames is the one plain name of each builder colour token, shared by the Design system
// page and the builder (js/builder/colournames.js mirrors it).
var builderColourNames = map[string]string{"primary": "Brand", "bg": "Background", "text": "Text", "surface": "Surface", "muted": "Muted", "accent": "Accent"}

func spaceNames() []string {
	n := make([]string, 13)
	for i := range n {
		n[i] = strconv.Itoa(i)
	}
	return n
}

// tokenValues returns the stored value of a token, falling back to the default.
func tokenValues(t builder.Tokens, class string) (stored, def map[string]string) {
	d := builder.DefaultTokens()
	switch class {
	case "color":
		return t.Color, d.Color
	case "space":
		return t.Space, d.Space
	case "fs":
		return t.FS, d.FS
	}
	return t.Shadow, d.Shadow
}

// hexColor is the 6-digit lower-case form a colour input needs; other values pass through.
func hexColor(v string) string {
	v = strings.ToLower(v)
	if len(v) == 4 && v[0] == '#' {
		return "#" + strings.Repeat(string(v[1]), 2) + strings.Repeat(string(v[2]), 2) + strings.Repeat(string(v[3]), 2)
	}
	return v
}

// tokenLabel is the field label: the plain colour name, or the class prefix and the step.
func tokenLabel(class, prefix, name string) string {
	if class == "color" {
		return builderColourNames[name]
	}
	return prefix + name
}

func tokenFieldID(class, name string) string   { return "tok-" + class + "-" + name }
func tokenFieldName(class, name string) string { return class + "_" + name }

// tokensView builds the page groups from the stored tokens and, when set, the
// rejected values the owner just typed.
func tokensView(stored builder.Tokens, posted map[string]string, errs map[string]string) tokensData {
	var d tokensData
	for _, l := range tokenLayout {
		vals, defs := tokenValues(stored, l.class)
		g := tokenGroup{ID: "tokens-" + l.class, Title: l.title, Intro: l.intro}
		for _, name := range l.names {
			// A colour input cannot be empty: it always shows the current value
			// (the default when none is stored). Every other field is empty until
			// overridden and shows the default as its placeholder.
			v, stored := vals[name]
			if l.class == "color" {
				if !stored {
					v = defs[name]
				}
				v = hexColor(v)
			}
			f := tokenField{ID: tokenFieldID(l.class, name), Name: tokenFieldName(l.class, name), Label: tokenLabel(l.class, l.label, name), Type: l.typ, Value: v}
			if l.class != "color" {
				f.Placeholder = defs[name]
			}
			if p, ok := posted[f.Name]; ok {
				f.Value = p
			}
			if m := errs[f.Name]; m != "" {
				f.Error = m
				d.Summary = append(d.Summary, ui.FieldError{ID: f.ID, Label: f.Label, Message: m})
			}
			g.Fields = append(g.Fields, f)
		}
		d.Groups = append(d.Groups, g)
	}
	return d
}

func (a *Admin) renderTokens(w http.ResponseWriter, r *http.Request, status int, d tokensData) {
	d.OptedIn = a.deps.BuilderTokens != nil && a.deps.BuilderTokens()
	v := a.NewView(w, r, "Design system")
	v.Section, v.Tab = "design-system", "design-system"
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "settings/designtokens", v)
}

func (a *Admin) handleTokensForm(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	// Invalid stored entries are dropped from the form, as the page render drops them.
	design, _ := builder.ParseDesign(cfg.ThemeExtra)
	a.renderTokens(w, r, http.StatusOK, tokensView(design.Tokens, nil, nil))
}

func (a *Admin) handleTokensSave(w http.ResponseWriter, r *http.Request) {
	var tokens builder.Tokens
	posted, errs := map[string]string{}, map[string]string{}
	var current builder.Tokens
	fetch := fetchMode(r)
	if fetch {
		// A JS save posts only the fields it shows: the others keep their stored value (no lost update).
		cfg, err := a.loadSite(r.Context())
		if err != nil {
			a.fetchOrError(w, r, http.StatusInternalServerError, "The settings could not be loaded.")
			return
		}
		design, _ := builder.ParseDesign(cfg.ThemeExtra)
		current = design.Tokens
	}
	for _, l := range tokenLayout {
		_, defs := tokenValues(builder.Tokens{}, l.class)
		for _, name := range l.names {
			field := tokenFieldName(l.class, name)
			v := strings.TrimSpace(r.PostFormValue(field))
			if _, ok := r.PostForm[field]; fetch && !ok {
				stored, _ := tokenValues(current, l.class)
				v = stored[name]
			}
			posted[field] = v
			if v == "" {
				continue // empty means the built-in default
			}
			if err := builder.ValidateToken(l.class, name, v); err != nil {
				errs[field] = "Not a valid value. Example: " + defs[name]
				continue
			}
			if hexColor(v) == hexColor(defs[name]) {
				continue // unchanged defaults are not stored: the built-in value keeps applying
			}
			setToken(&tokens, l.class, name, v)
		}
	}
	if len(errs) > 0 {
		if fetchMode(r) {
			a.writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
			return
		}
		a.renderTokens(w, r, http.StatusUnprocessableEntity, tokensView(builder.Tokens{}, posted, errs))
		return
	}
	a.writeTokens(w, r, tokens, "Design tokens saved.")
}

func (a *Admin) handleTokensReset(w http.ResponseWriter, r *http.Request) {
	a.writeTokens(w, r, builder.Tokens{}, "Design tokens reset to the defaults.")
}

// writeTokens stores tokens under theme_extra.builder.tokens (every other key
// and the presets stay as stored), purges the rendered pages and redirects.
func (a *Admin) writeTokens(w http.ResponseWriter, r *http.Request, tokens builder.Tokens, flash string) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.fetchOrError(w, r, http.StatusInternalServerError, "The settings could not be loaded.")
		return
	}
	raw, err := builder.MergeTokens(cfg.ThemeExtra, tokens)
	if err == nil {
		_, err = a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{ThemeExtra: raw})
	}
	if err != nil {
		a.logger().Error("save design tokens failed", "err", err)
		a.fetchOrError(w, r, http.StatusInternalServerError, "The design tokens could not be saved.")
		return
	}
	a.invalidate(r.Context())
	if fetchMode(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	a.SetFlash(w, r, "success", flash)
	http.Redirect(w, r, a.adminPath+"/theme/design-system", http.StatusSeeOther)
}

func setToken(t *builder.Tokens, class, name, v string) {
	m := map[string]*map[string]string{"color": &t.Color, "space": &t.Space, "fs": &t.FS, "shadow": &t.Shadow}[class]
	if *m == nil {
		*m = map[string]string{}
	}
	(*m)[name] = v
}

// bootSiteStyles is what the builder's Site styles drawer starts from, owner only. Theme holds the ten
// Colours and fonts fields by form name; Tokens the Design system fields by form name (the three builder
// colours and every stored space, size and shadow), so the drawer can post each form whole.
type bootSiteStyles struct {
	Theme  map[string]string `json:"theme"`
	Tokens map[string]string `json:"tokens"`
}

func siteStylesBoot(cfg *siteconfig.Config) *bootSiteStyles {
	f := designFrom(cfg)
	out := &bootSiteStyles{
		Theme: map[string]string{
			"primary": f.Primary, "secondary": f.Secondary, "accent": f.Accent, "background": f.Background, "text": f.Text,
			"font": f.Font, "heading_font": f.HeadingFont, "radius_sm": f.RadiusSm, "radius_md": f.RadiusMd, "radius_lg": f.RadiusLg,
		},
		Tokens: map[string]string{},
	}
	design, _ := builder.ParseDesign(cfg.ThemeExtra)
	for class, m := range map[string]map[string]string{"space": design.Tokens.Space, "fs": design.Tokens.FS, "shadow": design.Tokens.Shadow} {
		for name, v := range m {
			out.Tokens[tokenFieldName(class, name)] = v
		}
	}
	palette := builderPalette(cfg)
	for _, name := range []string{"surface", "muted", "accent"} {
		out.Tokens[tokenFieldName("color", name)] = palette.Colors[name]
	}
	return out
}
