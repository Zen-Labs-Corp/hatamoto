package admin

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/themestrings"
)

const (
	themeStringsPath = "/theme/strings"
	// themeStringsForm is the id of the form and the prefix of its control ids.
	themeStringsForm = "theme-strings"
	// themeStringsField prefixes the name of each posted text (the key follows).
	themeStringsField = "s_"
)

func (a *Admin) mountThemeStrings(owner *httpx.Group) {
	owner.HandleFunc("GET "+themeStringsPath, a.handleThemeStrings)
	owner.HandleFunc("POST "+themeStringsPath, a.handleThemeStringsSave)
}

type themeStringsData struct {
	Summary []ui.FieldError
	Theme   string
	// Multi shows the locale tabs.
	Multi  bool
	Tabs   []ui.Tab
	Tag    string
	Locale string
	Action string
	// Empty is set when the theme declares no strings.
	Empty bool
	Table ui.Table
}

// themeStringsCtx is what both handlers need: the site, the active theme and
// the strings it declares, and the configured locales.
type themeStringsCtx struct {
	theme   string
	decl    themestrings.Decl
	entries []siteconfig.LocaleEntry
	defTag  string
	multi   bool
	stored  themestrings.Overrides
}

func (a *Admin) themeStringsContext(w http.ResponseWriter, r *http.Request) (*themeStringsCtx, bool) {
	if a.deps.Site == nil || a.deps.Growth.Packages == nil {
		a.unavailable(w, r, "Theme strings")
		return nil, false
	}
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.logger().Error("theme strings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The theme strings could not be loaded.")
		return nil, false
	}
	entries, err := localeEntries(cfg)
	if err != nil {
		a.logger().Error("theme strings locales", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The site's locales could not be read.")
		return nil, false
	}
	pk := a.deps.Growth.Packages
	theme := "default"
	if themes, err := pk.Themes(r.Context()); err == nil {
		for _, t := range themes {
			if t.Active {
				theme = t.Name
			}
		}
	}
	decl, _ := pk.ThemeStrings(r.Context(), theme)
	return &themeStringsCtx{
		theme:   theme,
		decl:    decl,
		entries: entries,
		defTag:  defaultTag(cfg, entries),
		multi:   localeSetOf(cfg).Multi(),
		stored:  themestrings.ParseOverrides(cfg.ThemeExtra),
	}, true
}

// has reports whether tag is a configured locale (hidden ones included).
func (c *themeStringsCtx) has(tag string) bool {
	for _, e := range c.entries {
		if e.Tag == tag {
			return true
		}
	}
	return false
}

func (a *Admin) handleThemeStrings(w http.ResponseWriter, r *http.Request) {
	c, ok := a.themeStringsContext(w, r)
	if !ok {
		return
	}
	tag := r.URL.Query().Get("locale")
	if !c.has(tag) || !c.multi {
		tag = c.defTag
	}
	a.renderThemeStrings(w, r, http.StatusOK, c, tag, c.stored.For(c.theme, tag), nil)
}

func (a *Admin) handleThemeStringsSave(w http.ResponseWriter, r *http.Request) {
	c, ok := a.themeStringsContext(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil { // a cut-off body would read as "clear every string"
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read. Nothing was saved.")
		return
	}
	tag := r.PostForm.Get("locale")
	if !c.has(tag) {
		a.renderThemeStrings(w, r, http.StatusUnprocessableEntity, c, c.defTag, c.stored.For(c.theme, c.defTag),
			[]ui.FieldError{{ID: themeStringsForm, Label: "Locale", Message: "That locale is not configured on this site."}})
		return
	}
	posted := map[string]string{}
	var errs []ui.FieldError
	for name, vals := range r.PostForm {
		key, found := strings.CutPrefix(name, themeStringsField)
		if !found || len(vals) == 0 {
			continue
		}
		id := themeStringsForm + "-" + name
		if _, declared := c.decl[key]; !declared {
			errs = append(errs, ui.FieldError{ID: id, Label: key, Message: "The theme does not declare this string."})
			continue
		}
		text := strings.TrimSpace(vals[0])
		if text == "" {
			continue
		}
		posted[key] = text
		if err := themestrings.ValidText(text); err != nil {
			errs = append(errs, ui.FieldError{ID: id, Label: themeStringLabel(key), Message: err.Error()})
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].ID < errs[j].ID })
	if len(errs) > 0 {
		a.renderThemeStrings(w, r, http.StatusUnprocessableEntity, c, tag, posted, errs)
		return
	}

	// Replace this theme's texts for the tag; other themes, other locales and
	// stored keys the theme no longer declares stay as they are. The whole
	// document is written back, so two owners saving at once can lose one save.
	doc := c.stored
	if doc == nil {
		doc = themestrings.Overrides{}
	}
	if doc[c.theme] == nil {
		doc[c.theme] = map[string]map[string]string{}
	}
	next := map[string]string{}
	cleared := 0
	for k, v := range doc[c.theme][tag] {
		if _, declared := c.decl[k]; declared {
			if _, kept := posted[k]; !kept && v != "" {
				cleared++
			}
			continue
		}
		next[k] = v
	}
	for k, v := range posted {
		next[k] = v
	}
	if len(next) == 0 {
		delete(doc[c.theme], tag)
	} else {
		doc[c.theme][tag] = next
	}
	if len(doc[c.theme]) == 0 {
		delete(doc, c.theme)
	}
	raw, err := jsonx.Marshal(doc, jsonx.HTMLSafe)
	if err == nil {
		err = a.deps.Site.PatchThemeExtra(r.Context(), themestrings.ExtraKey, raw)
	}
	if err != nil {
		a.logger().Error("theme strings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The theme strings could not be saved.")
		return
	}
	// Counts only: the owner's text stays out of the audit log.
	a.audit(r, "theme.strings", c.theme, map[string]any{"locale": tag, "set": len(posted), "cleared": cleared})
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Theme strings saved.")
	http.Redirect(w, r, a.adminPath+themeStringsPath+"?locale="+url.QueryEscape(tag), http.StatusSeeOther)
}

// themeStringLabel is a key read as words: "pager_next" is "pager next".
func themeStringLabel(key string) string { return strings.ReplaceAll(key, "_", " ") }

func (a *Admin) renderThemeStrings(w http.ResponseWriter, r *http.Request, status int, c *themeStringsCtx, tag string, values map[string]string, errs []ui.FieldError) {
	locale := localeDisplayName(tag)
	d := themeStringsData{
		Summary: errs,
		Theme:   c.theme,
		Multi:   c.multi,
		Tag:     tag,
		Locale:  locale,
		Action:  a.adminPath + themeStringsPath,
		Empty:   len(c.decl) == 0,
	}
	if c.multi {
		for _, e := range c.entries {
			d.Tabs = append(d.Tabs, ui.Tab{
				Label:  localeDisplayName(e.Tag),
				Href:   a.adminPath + themeStringsPath + "?locale=" + url.QueryEscape(e.Tag),
				Active: e.Tag == tag,
			})
		}
	}
	keys := make([]string, 0, len(c.decl))
	for k := range c.decl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	d.Table = ui.Table{
		ID:      "theme-strings-table",
		Caption: "Strings of the " + c.theme + " theme",
		Columns: []ui.Col{
			{Key: "key", Label: "Where", Role: "plain"},
			{Key: "default", Label: "Theme default", Role: "plain"},
			{Key: "text", Label: locale, Role: "title"},
		},
	}
	cellErr := map[string]string{}
	for _, e := range errs {
		cellErr[e.ID] = e.Message
	}
	for _, k := range keys {
		name := themeStringsField + k
		text := values[k]
		d.Table.Rows = append(d.Table.Rows, ui.Row{
			ID:    k,
			Label: themeStringLabel(k),
			Cells: []ui.Cell{
				{Text: k, Mono: true},
				{Text: c.decl[k]},
				{Edit: &ui.EditCell{Name: name, Value: text, Orig: text, Control: "text", Form: themeStringsForm,
					Label: locale + ": " + themeStringLabel(k), Error: cellErr[themeStringsForm+"-"+name]}},
			},
		})
	}
	v := a.NewView(w, r, "Theme strings")
	v.Section, v.Tab = "theme-strings", "theme-strings"
	v.Data = d
	a.Render(w, r, status, "settings/theme-strings", v)
}
