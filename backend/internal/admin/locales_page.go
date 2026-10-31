package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

// mountLocaleSettings registers the pages of Settings > Locales. Each tab adds
// its routes here.
func (a *Admin) mountLocaleSettings(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/locales", a.handleLocales)
	owner.HandleFunc("GET /settings/locales/new", a.handleLocaleNew)
	owner.HandleFunc("POST /settings/locales/add", a.handleLocaleAdd)
	owner.HandleFunc("POST /settings/locales/order", a.handleLocaleOrder)
	owner.HandleFunc("GET /settings/locales/business", a.handleBusiness)
	owner.HandleFunc("POST /settings/locales/business/{tag}", a.handleBusinessSave)
	owner.HandleFunc("GET /settings/locales/addresses", a.handleAddresses)
	owner.HandleFunc("POST /settings/locales/addresses", a.handleAddressesSave)
	owner.HandleFunc("GET /settings/locales/edit/{tag}", a.handleLocaleEdit)
	owner.HandleFunc("POST /settings/locales/update/{tag}", a.handleLocaleUpdate)
	owner.HandleFunc("GET /settings/locales/remove/{tag}", a.handleLocaleRemoveConfirm)
	owner.HandleFunc("POST /settings/locales/remove/{tag}", a.handleLocaleRemove)
}

// saveLocales is the one write path of the locale list: validate against the
// running site, store, then clear the rendered pages so a change (a Public
// toggle, say) shows at once. It returns the form error per field ("locales",
// "xdefault") when the input is rejected, and an error when the save itself
// fails. An empty list makes the site single-locale and drops the flags.
func (a *Admin) saveLocales(ctx context.Context, cfg *siteconfig.Config, entries []siteconfig.LocaleEntry, prefixAll bool, xdef string) (map[string]string, error) {
	if len(entries) == 0 {
		entries, xdef, prefixAll = []siteconfig.LocaleEntry{}, "", false
	} else {
		if a.deps.ValidateLocales == nil {
			return map[string]string{"locales": "Locales are not available here"}, nil
		}
		var err error
		if entries, xdef, err = a.deps.ValidateLocales(ctx, cfg.Language, entries, prefixAll, xdef); err != nil {
			field := "locales"
			var lf localeFielder
			if errors.As(err, &lf) && lf.LocaleField() == "x_default_locale" {
				field = "xdefault"
			}
			return map[string]string{field: err.Error()}, nil
		}
	}
	in := siteconfig.UpdateInput{Locales: &entries, PrefixAll: &prefixAll, XDefaultLocale: &xdef}
	if _, err := a.deps.Site.Update(ctx, in); err != nil {
		return nil, err
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(ctx)
	}
	return nil, nil
}

// localeEntries is the stored list, or just the default locale on a site that
// has none yet, so the first locale added keeps the site's own language.
func localeEntries(cfg *siteconfig.Config) ([]siteconfig.LocaleEntry, error) {
	if len(cfg.Locales) > 0 {
		return slices.Clone(cfg.Locales), nil
	}
	def, err := entity.NormalizeLocale(cfg.Language)
	if err != nil {
		return nil, err
	}
	return []siteconfig.LocaleEntry{{Tag: def}}, nil
}

// localeForm is the add and edit form of one locale.
type localeForm struct {
	Tag, Segment, Name string
	Public             bool
	// Default marks the default locale: it is always public.
	Default bool
	Errs    map[string]string
	Summary []ui.FieldError
	Action  string
	Catalog []ui.Option
	Title   string
	Submit  string
	Cancel  string
}

// localesData is the Locales tab.
type localesData struct {
	Table       ui.Table
	Form        localeForm
	Actions     []ui.Button
	ShowConvert bool
	ConvertHref string
}

// localeRowActions are the row menu entries of the locale at index i of n.
func (a *Admin) localeRowActions(entries []siteconfig.LocaleEntry, i int, def string) []ui.Action {
	e := entries[i]
	base := a.url("settings", "locales")
	acts := []ui.Action{{Label: "Edit", Href: base + "/edit/" + e.Tag}}
	move := func(label, dir string, to int) {
		order := make([]string, len(entries))
		for k, x := range entries {
			order[k] = x.Tag
		}
		order[i], order[to] = order[to], order[i]
		acts = append(acts, ui.Action{Label: label, Post: base + "/order", Fields: map[string]string{"order": strings.Join(order, ","), "moved": e.Tag, "dir": dir}})
	}
	if i > 0 {
		move("Move up", "up", i-1)
	}
	if i < len(entries)-1 {
		move("Move down", "down", i+1)
	}
	if e.Tag != def {
		pub := ui.Action{Label: "Make public", Post: base + "/update/" + e.Tag, Fields: map[string]string{"public": "true"}}
		if !e.Hidden {
			pub = ui.Action{Label: "Hide from visitors", Post: base + "/update/" + e.Tag, Fields: map[string]string{"public": "false"}}
		}
		acts = append(acts, pub)
		remove := base + "/remove/" + e.Tag
		acts = append(acts, ui.Action{Label: "Remove", Href: remove, Post: remove, ConfirmLabel: "Remove locale", Danger: true, Confirm: removeLocaleWarning(e.Tag)})
	}
	return acts
}

func (a *Admin) localesTable(r *http.Request, cfg *siteconfig.Config) ui.Table {
	set := localeSetOf(cfg)
	multi := set.Multi()
	entries := cfg.Locales
	if !multi {
		// A single-locale site lists its own language and nothing else.
		if def, err := localeEntries(cfg); err == nil {
			entries = def
		}
	}
	defTag := set.Default()
	if !multi && len(entries) > 0 {
		defTag = entries[0].Tag
	}
	stored, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		stored = nil
	}
	t := ui.Table{
		ID: "locales", Caption: "Your locales", BaseURL: a.url("settings", "locales"), CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "locale", Label: "Locale"}, {Key: "address", Label: "Address"}, {Key: "status", Label: "Visitors"}, {Key: "business", Label: "Business details"}},
		Empty:   ui.Empty{Icon: "globe", Title: "No locales", Body: "Add a locale to publish your site in another language."},
	}
	for i, e := range entries {
		name := e.Name
		if name == "" {
			name = localeDisplayName(e.Tag)
		}
		first := ui.Cell{Text: name, Sub: e.Tag}
		switch isXDefault := e.Tag == cfg.XDefaultLocale; {
		case e.Tag == defTag && isXDefault:
			first.Tag = &ui.Badge{Label: "Default, also for search engines", Kind: "neutral"}
		case e.Tag == defTag:
			first.Tag = &ui.Badge{Label: "Default", Kind: "neutral"}
		case isXDefault:
			first.Tag = &ui.Badge{Label: "Search engine default", Kind: "neutral"}
		}
		if multi {
			first.Link = a.url("settings", "locales", "edit", e.Tag)
		}
		addr := ui.Cell{Text: "/", Sub: "no prefix"}
		if p := set.Prefix(e.Tag); p != "" {
			addr = ui.Cell{Text: p + "/"}
		} else if !multi && e.Segment != "" {
			addr = ui.Cell{Text: "/" + e.Segment + "/"}
		}
		vis := ui.Badge{Label: "Public", Kind: "success"}
		if e.Hidden {
			vis = ui.Badge{Label: "Hidden, not public yet", Kind: "warning"}
		}
		label, kind := localeBadge(stored[e.Tag])
		row := ui.Row{ID: e.Tag, Label: name, Cells: []ui.Cell{first, addr, {Badge: &vis}, {Badge: &ui.Badge{Label: label, Kind: kind}}}}
		if multi {
			row.Actions = a.localeRowActions(entries, i, defTag)
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func (a *Admin) handleLocales(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	d := localesData{
		Table:       a.localesTable(r, cfg),
		Form:        a.newLocaleForm(false),
		Actions:     []ui.Button{{Label: "Add locale", Variant: "primary", Icon: "plus", Href: a.url("settings", "locales", "new"), Attrs: map[string]string{"data-hm-open": "add-locale"}}},
		ShowConvert: a.convertCandidates(r, cfg),
		ConvertHref: a.convertURL(),
	}
	v := a.NewView(w, r, "Locales")
	v.Section, v.Tab = "locales", "locales"
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/locales", v)
}

func (a *Admin) newLocaleForm(public bool) localeForm {
	return localeForm{Public: public, Action: a.url("settings", "locales", "add"), Catalog: commonLocales(), Title: "Add a locale", Submit: "Add locale"}
}

// handleLocaleNew is the page behind the header's Add locale link: what a
// browser without JavaScript sees instead of the dialog.
func (a *Admin) handleLocaleNew(w http.ResponseWriter, r *http.Request) {
	a.renderLocaleNew(w, r, http.StatusOK, a.newLocaleForm(false))
}

func (a *Admin) renderLocaleNew(w http.ResponseWriter, r *http.Request, status int, f localeForm) {
	v := a.NewView(w, r, "Add a locale")
	v.Width = "narrow"
	v.Section, v.Tab = "locales", "locales"
	v.Crumbs = a.areaCrumbs("locales", Crumb{Label: "Add a locale"})
	v.Data = f
	a.Render(w, r, status, "settings/locale-new", v)
}

// localeFormErrs places the errors saveLocales reports on the fields of the
// add and edit forms. A rejected list is about the address prefix, the only
// part of an entry the validator can refuse; a rejected search engine default
// goes to xdefaultField, the field the owner changed to cause it.
func localeFormErrs(errs map[string]string, xdefaultField string) map[string]string {
	out := map[string]string{}
	if m := errs["locales"]; m != "" {
		out["segment"] = m
	}
	if m := errs["xdefault"]; m != "" {
		out[xdefaultField] = m
	}
	return out
}

// localeSummary lists the errors of f the way the form shows them.
func localeSummary(errs map[string]string) []ui.FieldError {
	var out []ui.FieldError
	for _, k := range []struct{ key, label string }{{"tag", "Language and region"}, {"segment", "Address prefix"}, {"name", "Display name"}, {"public", "Public"}} {
		if m := errs[k.key]; m != "" {
			out = append(out, ui.FieldError{ID: "locale-" + k.key, Label: k.label, Message: m})
		}
	}
	return out
}

func (a *Admin) handleLocaleAdd(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	f := a.newLocaleForm(r.PostFormValue("public") == "true")
	f.Tag, f.Segment, f.Name = strings.TrimSpace(r.PostFormValue("tag")), strings.TrimSpace(r.PostFormValue("segment")), strings.TrimSpace(r.PostFormValue("name"))
	fail := func(errs map[string]string) {
		f.Errs, f.Summary = errs, localeSummary(errs)
		a.renderLocaleNew(w, r, http.StatusUnprocessableEntity, f)
	}
	tag, nerr := entity.NormalizeLocale(f.Tag)
	if nerr != nil {
		fail(map[string]string{"tag": "Enter a language code like fr, or a language and region like fr-FR."})
		return
	}
	entries, err := localeEntries(cfg)
	if err != nil {
		fail(map[string]string{"tag": "Set the site language in Settings > General first."})
		return
	}
	for _, e := range entries {
		if e.Tag == tag {
			fail(map[string]string{"tag": "That locale is already on your site."})
			return
		}
	}
	if utf8.RuneCountInString(f.Name) > 80 {
		fail(map[string]string{"name": "Use at most 80 characters."})
		return
	}
	seg := strings.ToLower(f.Segment)
	if seg == "" {
		seg = strings.ToLower(tag)
	}
	entries = append(entries, siteconfig.LocaleEntry{Tag: tag, Segment: seg, Name: f.Name, Hidden: !f.Public})
	errs, err := a.saveLocales(r.Context(), cfg, entries, cfg.PrefixAll, cfg.XDefaultLocale)
	if err != nil {
		a.logger().Error("add locale failed", "locale", tag, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The locale could not be added.")
		return
	}
	if len(errs) > 0 {
		fail(localeFormErrs(errs, "tag"))
		return
	}
	a.SetFlash(w, r, "success", "Added "+localeDisplayName(tag)+".")
	http.Redirect(w, r, a.url("settings", "locales"), http.StatusSeeOther)
}

func (a *Admin) handleLocaleOrder(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	order := strings.Split(r.PostFormValue("order"), ",")
	byTag := make(map[string]siteconfig.LocaleEntry, len(cfg.Locales))
	for _, e := range cfg.Locales {
		byTag[e.Tag] = e
	}
	entries := make([]siteconfig.LocaleEntry, 0, len(order))
	known := true
	for _, tag := range order {
		e, ok := byTag[tag]
		if !ok {
			known = false
			break
		}
		delete(byTag, tag)
		entries = append(entries, e)
	}
	if len(cfg.Locales) < 2 || !known || len(entries) != len(cfg.Locales) || len(byTag) != 0 {
		a.Error(w, r, http.StatusUnprocessableEntity, "Not saved", "The order must list each of your locales once. Reload the page and try again.")
		return
	}
	done := "Locale order saved."
	if moved, dir := r.PostFormValue("moved"), r.PostFormValue("dir"); (dir == "up" || dir == "down") && slices.ContainsFunc(entries, func(e siteconfig.LocaleEntry) bool { return e.Tag == moved }) {
		done = "Moved " + localeDisplayName(moved) + " " + dir + "."
	}
	a.saveLocaleList(w, r, cfg, entries, done)
}

// saveLocaleList stores entries and answers the Locales tab: a flash and a
// redirect on success, an error page when the list is rejected or not saved.
func (a *Admin) saveLocaleList(w http.ResponseWriter, r *http.Request, cfg *siteconfig.Config, entries []siteconfig.LocaleEntry, done string) {
	errs, err := a.saveLocales(r.Context(), cfg, entries, cfg.PrefixAll, cfg.XDefaultLocale)
	if err != nil {
		a.logger().Error("save locales failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The locales could not be saved.")
		return
	}
	if msg := errs["locales"] + errs["xdefault"]; msg != "" {
		a.Error(w, r, http.StatusUnprocessableEntity, "Not saved", msg)
		return
	}
	a.SetFlash(w, r, "success", done)
	http.Redirect(w, r, a.url("settings", "locales"), http.StatusSeeOther)
}

// localeIndex finds the stored locale named by the {tag} of the request, on a
// site with two or more locales.
func (a *Admin) localeIndex(w http.ResponseWriter, r *http.Request) (*siteconfig.Config, int, bool) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return nil, 0, false
	}
	if localeSetOf(cfg).Multi() {
		tag := r.PathValue("tag")
		for i, e := range cfg.Locales {
			if e.Tag == tag {
				return cfg, i, true
			}
		}
	}
	a.Error(w, r, http.StatusNotFound, "Not found", "That locale is not on your site.")
	return nil, 0, false
}

func (a *Admin) editLocaleForm(cfg *siteconfig.Config, e siteconfig.LocaleEntry) localeForm {
	return localeForm{
		Tag: e.Tag, Segment: e.Segment, Name: e.Name, Public: !e.Hidden, Default: e.Tag == localeSetOf(cfg).Default(),
		Action: a.url("settings", "locales", "update", e.Tag), Title: localeDisplayName(e.Tag), Submit: "Save locale", Cancel: a.url("settings", "locales"),
	}
}

func (a *Admin) renderLocaleEdit(w http.ResponseWriter, r *http.Request, status int, f localeForm) {
	v := a.NewView(w, r, f.Title)
	v.Width = "narrow"
	v.Section, v.Tab = "locales", "locales"
	v.Crumbs = a.areaCrumbs("locales", Crumb{Label: f.Title})
	v.Data = f
	a.Render(w, r, status, "settings/locale-edit", v)
}

func (a *Admin) handleLocaleEdit(w http.ResponseWriter, r *http.Request) {
	cfg, i, ok := a.localeIndex(w, r)
	if !ok {
		return
	}
	a.renderLocaleEdit(w, r, http.StatusOK, a.editLocaleForm(cfg, cfg.Locales[i]))
}

// handleLocaleUpdate changes the fields the request carries: the edit page
// posts all of them, a row action only public. The tag itself never changes.
func (a *Admin) handleLocaleUpdate(w http.ResponseWriter, r *http.Request) {
	cfg, i, ok := a.localeIndex(w, r)
	if !ok {
		return
	}
	if r.ParseForm() != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	entries := slices.Clone(cfg.Locales)
	e := &entries[i]
	if r.PostForm.Has("name") {
		e.Name = strings.TrimSpace(r.PostFormValue("name"))
	}
	if r.PostForm.Has("segment") {
		e.Segment = strings.ToLower(strings.TrimSpace(r.PostFormValue("segment")))
	}
	if r.PostForm.Has("public") {
		e.Hidden = r.PostFormValue("public") != "true"
	}
	if r.PostForm.Has("segment") {
		// A save from the edit page: a default locale kept without a prefix may
		// leave it empty, every other locale falls back to its tag.
		if e.Segment == "" && e.Tag != localeSetOf(cfg).Default() {
			e.Segment = strings.ToLower(e.Tag)
		}
	}
	errs, err := a.saveLocales(r.Context(), cfg, entries, cfg.PrefixAll, cfg.XDefaultLocale)
	if err != nil {
		a.logger().Error("update locale failed", "locale", e.Tag, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The locale could not be saved.")
		return
	}
	if len(errs) > 0 {
		if !r.PostForm.Has("segment") && !r.PostForm.Has("name") {
			// A row menu action: there is no form to show the message on.
			a.SetFlash(w, r, "error", "Not saved: "+errs["locales"]+errs["xdefault"])
			http.Redirect(w, r, a.url("settings", "locales"), http.StatusSeeOther)
			return
		}
		f := a.editLocaleForm(cfg, *e)
		f.Errs = localeFormErrs(errs, "public")
		f.Summary = localeSummary(f.Errs)
		a.renderLocaleEdit(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	a.SetFlash(w, r, "success", "Saved "+localeDisplayName(e.Tag)+".")
	http.Redirect(w, r, a.url("settings", "locales"), http.StatusSeeOther)
}

// removeLocaleWarning is what removing a locale does, shown before it happens.
func removeLocaleWarning(tag string) string {
	return "Pages in " + localeDisplayName(tag) + " stop being reachable on your site. They are not deleted."
}

// removableLocale finds the locale of the request and refuses the two that
// cannot go: the default locale, and the one search engines are pointed at.
func (a *Admin) removableLocale(w http.ResponseWriter, r *http.Request) (*siteconfig.Config, int, bool) {
	cfg, i, ok := a.localeIndex(w, r)
	if !ok {
		return nil, 0, false
	}
	tag := cfg.Locales[i].Tag
	switch {
	case tag == localeSetOf(cfg).Default():
		a.Error(w, r, http.StatusUnprocessableEntity, "Not removed", "The default locale is your site language and cannot be removed. Change the site language in Settings > General first.")
	case tag == cfg.XDefaultLocale:
		a.Error(w, r, http.StatusUnprocessableEntity, "Not removed", localeDisplayName(tag)+" is the default for search engines. Pick another one in Addresses and search engines first.")
	default:
		return cfg, i, true
	}
	return nil, 0, false
}

func (a *Admin) handleLocaleRemoveConfirm(w http.ResponseWriter, r *http.Request) {
	cfg, i, ok := a.removableLocale(w, r)
	if !ok {
		return
	}
	tag := cfg.Locales[i].Tag
	a.renderConfirm(w, r, confirmData{
		Title: "Remove " + localeDisplayName(tag) + "?", Label: "Remove locale", Danger: true, Message: removeLocaleWarning(tag),
		Post: a.url("settings", "locales", "remove", tag), Back: a.url("settings", "locales"),
	})
}

// handleLocaleRemove drops the locale from the list. When only the default
// would be left it stores an empty list: the site becomes single-locale.
func (a *Admin) handleLocaleRemove(w http.ResponseWriter, r *http.Request) {
	cfg, i, ok := a.removableLocale(w, r)
	if !ok {
		return
	}
	tag := cfg.Locales[i].Tag
	entries := slices.Delete(slices.Clone(cfg.Locales), i, i+1)
	if len(entries) < 2 {
		entries = nil
	}
	a.saveLocaleList(w, r, cfg, entries, "Removed "+localeDisplayName(tag)+".")
}

// addressesData is the Addresses and search engines tab.
type addressesData struct {
	Default   string
	PrefixAll bool
	XDefault  []ui.Option
	Errs      map[string]string
	Summary   []ui.FieldError
	Multi     bool
	Empty     ui.Empty
}

// xDefaultOptions offers Automatic and every public locale, never a hidden one.
func xDefaultOptions(cfg *siteconfig.Config, current string) []ui.Option {
	out := []ui.Option{{Value: "", Label: "Automatic", Selected: current == ""}}
	for _, e := range cfg.Locales {
		if !e.Hidden {
			out = append(out, ui.Option{Value: e.Tag, Label: localeDisplayName(e.Tag) + " (" + e.Tag + ")", Selected: e.Tag == current})
		}
	}
	return out
}

func (a *Admin) renderAddresses(w http.ResponseWriter, r *http.Request, status int, cfg *siteconfig.Config, prefixAll bool, xdef string, errs map[string]string) {
	d := addressesData{
		Default: localeDisplayName(localeSetOf(cfg).Default()), PrefixAll: prefixAll, XDefault: xDefaultOptions(cfg, xdef),
		Errs: errs, Multi: len(cfg.Locales) > 1,
		Empty: ui.Empty{Icon: "globe", Title: "Add a second locale first",
			Body:    "Address prefixes and the search engine default apply once your site has two or more locales.",
			Actions: []ui.Button{{Label: "Add a locale", Variant: "primary", Href: a.url("settings", "locales", "new")}}},
	}
	for _, k := range []struct{ key, label string }{{"locales", "Address prefix"}, {"xdefault", "Default for search engines"}} {
		if m := errs[k.key]; m != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: "locale-" + k.key, Label: k.label, Message: m})
		}
	}
	v := a.NewView(w, r, "Addresses and search engines")
	v.Section, v.Tab = "locales", "locale-addresses"
	v.Data = d
	a.Render(w, r, status, "settings/locales-addresses", v)
}

func (a *Admin) handleAddresses(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	a.renderAddresses(w, r, http.StatusOK, cfg, cfg.PrefixAll, cfg.XDefaultLocale, nil)
}

// handleAddressesSave stores the two flags with the stored locales unchanged,
// so hidden flags and names survive.
func (a *Admin) handleAddressesSave(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	if len(cfg.Locales) < 2 {
		a.Error(w, r, http.StatusUnprocessableEntity, "Not saved", "Add a second locale first: these settings apply to sites with two or more locales.")
		return
	}
	prefixAll, xdef := r.PostFormValue("prefix_all") == "true", strings.TrimSpace(r.PostFormValue("xdefault"))
	errs, err := a.saveLocales(r.Context(), cfg, cfg.Locales, prefixAll, xdef)
	if err != nil {
		a.logger().Error("save addresses failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be saved.")
		return
	}
	if len(errs) > 0 {
		a.renderAddresses(w, r, http.StatusUnprocessableEntity, cfg, prefixAll, xdef, errs)
		return
	}
	a.SetFlash(w, r, "success", "Address settings saved.")
	http.Redirect(w, r, a.url("settings", "locales", "addresses"), http.StatusSeeOther)
}
