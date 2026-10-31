package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/taxonomy"
)

func (a *Admin) mountTermLocales(owner *httpx.Group) {
	owner.HandleFunc("POST /settings/taxonomies/terms/{id}/locale", a.handleTermLocale)
	owner.HandleFunc("GET /settings/taxonomies/terms/{id}/copy/new", a.handleTermCopyNew)
	owner.HandleFunc("POST /settings/taxonomies/terms/{id}/copy", a.handleTermCopy)
	owner.HandleFunc("POST /settings/taxonomies/terms/{id}/link", a.handleTermLink)
	owner.HandleFunc("GET /settings/taxonomies/terms/{id}/unlink", a.handleTermUnlinkConfirm)
	owner.HandleFunc("POST /settings/taxonomies/terms/{id}/unlink", a.handleTermUnlink)
}

// termCopyForm feeds hm/form-term-copy: the Create copy dialog on the term page
// and the /new page behind it.
type termCopyForm struct {
	IDPrefix, DialogID, Action, Back, Title, Locale string
	Name, Slug                                      string
	Errs                                            map[string]string
	Summary                                         []ui.FieldError
}

// termLocales is the Locale field and the copies block of the term page.
type termLocales struct {
	Action, LinkAction, UnlinkAction string
	Options                          []ui.Option
	Current                          string
	Copies                           ui.Table
	Missing                          []termCopyForm
	LinkOptions                      []ui.Option
	LinkTarget                       []string
}

func (a *Admin) termGate(w http.ResponseWriter, r *http.Request) (*taxonomy.Term, *entity.LocaleSet, bool) {
	t, ok := a.loadTerm(w, r)
	if !ok {
		return nil, nil, false
	}
	set, multi := a.multiLocale(r.Context())
	if !multi || a.deps.Content.TermGroups == nil {
		a.notFound(w, r, "Term", a.adminPath+"/settings/taxonomies", "Back to taxonomies")
		return nil, nil, false
	}
	return t, set, true
}

func (a *Admin) termURL(id uuid.UUID) string {
	return a.adminPath + "/settings/taxonomies/terms/" + id.String()
}

func (a *Admin) newTermCopyForm(ctx context.Context, t *taxonomy.Term, tag string) termCopyForm {
	seg := strings.ToLower(tag)
	if cfg, err := a.loadSite(ctx); err == nil {
		for _, l := range cfg.Locales {
			if l.Tag == tag && l.Segment != "" {
				seg = l.Segment
			}
		}
	}
	return termCopyForm{IDPrefix: "tcopy-" + tag, DialogID: "create-term-copy-" + tag, Locale: tag, Name: t.Name, Slug: t.Slug + "-" + seg,
		Action: a.termURL(t.ID) + "/copy", Back: a.termURL(t.ID), Title: "Create a copy in " + tag}
}

// localeLabel is the display name of a site locale.
func localeLabel(set *entity.LocaleSet, tag string) string {
	if name := set.Name(tag); name != "" {
		return name
	}
	return localeDisplayName(tag)
}

// termLocalesFor builds the block, or nil when term locales are off here.
func (a *Admin) termLocalesFor(r *http.Request, t *taxonomy.Term) *termLocales {
	set, ok := a.multiLocale(r.Context())
	g := a.deps.Content.TermGroups
	if !ok || g == nil {
		return nil
	}
	ctx := r.Context()
	cur, _ := g.Locale(ctx, t.ID)
	base := a.termURL(t.ID)
	tl := &termLocales{Action: base + "/locale", LinkAction: base + "/link", UnlinkAction: base + "/unlink", Current: cur}
	tl.Options = append(tl.Options, ui.Option{Value: "", Label: "All locales", Selected: cur == ""})
	for _, tag := range set.Tags() {
		tl.Options = append(tl.Options, ui.Option{Value: tag, Label: localeLabel(set, tag) + " " + tag, Selected: cur == tag})
	}
	if cur == "" {
		return tl
	}
	members, _ := g.Members(ctx, t.ID)
	byLocale := map[string]taxonomy.TermMember{}
	for _, m := range members {
		byLocale[m.Locale] = m
	}
	tags := set.Tags()
	if !slices.Contains(tags, cur) { // a locale removed from the settings still shows
		tags = append(tags, cur)
	}
	for _, m := range members {
		if !slices.Contains(tags, m.Locale) {
			tags = append(tags, m.Locale)
		}
	}
	tl.Copies = ui.Table{
		ID: "term-copies", Caption: "Copies of " + t.Name + " by language", CSRF: csrfToken(ctx),
		Columns: []ui.Col{{Key: "locale", Label: "Language"}, {Key: "term", Label: "Term"}},
	}
	for _, tag := range tags {
		lang := ui.Cell{Text: localeLabel(set, tag), Sub: tag}
		row := ui.Row{ID: tag, Label: localeLabel(set, tag) + " copy"}
		m, has := byLocale[tag]
		switch {
		case tag == cur:
			row.Cells = []ui.Cell{lang, {Text: t.Name, Tag: &ui.Badge{Label: "This term", Kind: "info"}}}
		case has:
			row.Cells = []ui.Cell{lang, {Text: m.Name}}
			row.Actions = []ui.Action{
				{Label: "Open", Href: a.termURL(m.ID), Button: true, AriaLabel: "Open " + localeLabel(set, tag) + " " + tag + " copy"},
				{Label: "Unlink", Href: base + "/unlink?term=" + m.ID.String(), Post: base + "/unlink?term=" + m.ID.String(),
					Confirm: "The term stays as it is, but it is no longer listed as a copy of this one.", ConfirmLabel: "Unlink", Danger: true, Button: true,
					AriaLabel: "Unlink " + localeLabel(set, tag) + " " + tag + " copy"},
			}
		default:
			f := a.newTermCopyForm(ctx, t, tag)
			tl.Missing = append(tl.Missing, f)
			row.Cells = []ui.Cell{lang, {Text: "no copy"}}
			row.Actions = []ui.Action{{Label: "Create", Href: base + "/copy/new?locale=" + tag, Open: f.DialogID, Button: true,
				AriaLabel: "Create " + localeLabel(set, tag) + " " + tag + " copy"}}
		}
		tl.Copies.Rows = append(tl.Copies.Rows, row)
	}
	if len(members) > 1 {
		return tl
	}
	if cands, err := g.Candidates(ctx, t.ID, "", 50); err == nil {
		tl.LinkOptions = append(tl.LinkOptions, ui.Option{Value: "", Label: "Pick a term"})
		for _, c := range cands {
			tl.LinkOptions = append(tl.LinkOptions, ui.Option{Value: c.ID.String(), Label: c.Name + " (" + c.Locale + ")"})
		}
	}
	return tl
}

func (a *Admin) handleTermLocale(w http.ResponseWriter, r *http.Request) {
	t, set, ok := a.termGate(w, r)
	if !ok {
		return
	}
	loc := strings.TrimSpace(r.PostFormValue("locale"))
	if loc != "" && !slices.Contains(set.Tags(), loc) {
		a.SetFlash(w, r, "error", "Pick one of the site's languages.")
		Redirect(w, r, a.termURL(t.ID))
		return
	}
	if err := a.deps.Content.TermGroups.SetLocale(r.Context(), t.ID, loc); err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrTermLocaleTaken):
			a.SetFlash(w, r, "error", "A copy of this term already has that locale.")
		case errors.Is(err, taxonomy.ErrTermGroupInvalid):
			a.SetFlash(w, r, "error", "A term linked to copies needs a locale. Unlink its copies first.")
		default:
			a.logger().Error("set term locale failed", "term", t.ID, "locale", loc, "err", err)
			a.SetFlash(w, r, "error", "The locale could not be saved.")
		}
	} else {
		a.invalidate(r.Context())
		a.SetFlash(w, r, "success", "Locale saved.")
	}
	Redirect(w, r, a.termURL(t.ID))
}

func (a *Admin) renderTermCopyNew(w http.ResponseWriter, r *http.Request, status int, t *taxonomy.Term, f termCopyForm) {
	v := a.NewView(w, r, f.Title)
	v.Width = "narrow"
	v.Crumbs = a.areaCrumbs("taxonomies", Crumb{Label: t.Kind, Href: a.taxBase(t.Kind)}, Crumb{Label: t.Name, Href: f.Back}, Crumb{Label: "Create copy"})
	v.Data = f
	a.Render(w, r, status, "taxonomies/term-copy-new", v)
}

func (a *Admin) handleTermCopyNew(w http.ResponseWriter, r *http.Request) {
	t, set, ok := a.termGate(w, r)
	if !ok {
		return
	}
	tag := r.URL.Query().Get("locale")
	if !slices.Contains(set.Tags(), tag) {
		a.notFound(w, r, "Locale", a.termURL(t.ID), "Back to the term")
		return
	}
	a.renderTermCopyNew(w, r, http.StatusOK, t, a.newTermCopyForm(r.Context(), t, tag))
}

func (a *Admin) handleTermCopy(w http.ResponseWriter, r *http.Request) {
	t, set, ok := a.termGate(w, r)
	if !ok {
		return
	}
	tag := strings.TrimSpace(r.PostFormValue("locale"))
	if !slices.Contains(set.Tags(), tag) {
		a.SetFlash(w, r, "error", "Pick one of the site's languages.")
		Redirect(w, r, a.termURL(t.ID))
		return
	}
	f := a.newTermCopyForm(r.Context(), t, tag)
	f.Name, f.Slug = strings.TrimSpace(r.PostFormValue("name")), strings.TrimSpace(r.PostFormValue("slug"))
	g := a.deps.Content.TermGroups
	if cur, _ := g.Locale(r.Context(), t.ID); cur == "" {
		a.SetFlash(w, r, "error", "Set a locale first.")
		Redirect(w, r, a.termURL(t.ID))
		return
	}
	cp, err := g.CreateTermCopy(r.Context(), t.ID, tag, f.Name, f.Slug)
	if err != nil {
		tf := termForm{Errors: map[string]string{}}
		switch {
		case termFieldError(&tf, err):
			f.Errs = map[string]string{}
			for k, v := range tf.Errors {
				f.Errs[k] = v
			}
			for _, k := range []struct{ key, label string }{{"name", "Name"}, {"slug", "Slug"}} {
				if m := f.Errs[k.key]; m != "" {
					f.Summary = append(f.Summary, ui.FieldError{ID: f.IDPrefix + "-" + k.key, Label: k.label, Message: m})
				}
			}
			a.renderTermCopyNew(w, r, http.StatusUnprocessableEntity, t, f)
		case errors.Is(err, taxonomy.ErrTermLocaleTaken):
			a.SetFlash(w, r, "error", "This term already has a "+tag+" copy.")
			Redirect(w, r, a.termURL(t.ID))
		default:
			a.serverError(w, r, "The copy could not be created", err)
		}
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Created the "+tag+" copy.")
	Redirect(w, r, a.termURL(cp.ID))
}

func (a *Admin) handleTermLink(w http.ResponseWriter, r *http.Request) {
	t, _, ok := a.termGate(w, r)
	if !ok {
		return
	}
	other, err := uuid.Parse(r.PostFormValue("term"))
	if err != nil || other == t.ID {
		a.SetFlash(w, r, "error", "Pick the term to link first.")
	} else if _, err := a.deps.Content.TermGroups.Link(r.Context(), t.ID, other); err != nil {
		a.SetFlash(w, r, "error", "That term cannot be linked: it needs the same kind and a locale this term does not have yet.")
	} else {
		a.invalidate(r.Context())
		a.SetFlash(w, r, "success", "Linked the term.")
	}
	Redirect(w, r, a.termURL(t.ID))
}

// handleTermUnlinkConfirm is the no-JS confirmation of an unlink.
func (a *Admin) handleTermUnlinkConfirm(w http.ResponseWriter, r *http.Request) {
	t, _, ok := a.termGate(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{
		Title: "Unlink this copy of “" + t.Name + "”?", Message: "The term stays as it is, but it is no longer listed as a copy of this one.",
		Post: a.termURL(t.ID) + "/unlink", Label: "Unlink", Back: a.termURL(t.ID), Danger: true,
		Hidden: []hiddenField{{Name: "term", Value: r.URL.Query().Get("term")}},
	})
}

func (a *Admin) handleTermUnlink(w http.ResponseWriter, r *http.Request) {
	t, _, ok := a.termGate(w, r)
	if !ok {
		return
	}
	g := a.deps.Content.TermGroups
	id, err := uuid.Parse(r.FormValue("term")) // the confirm dialog posts it in the query
	if err != nil {
		a.SetFlash(w, r, "error", "Pick the term to unlink.")
		Redirect(w, r, a.termURL(t.ID))
		return
	}
	if id != t.ID { // only this term or a member of its group
		members, err := g.Members(r.Context(), t.ID)
		if err != nil {
			a.serverError(w, r, "The term could not be unlinked", err)
			return
		}
		if !slices.ContainsFunc(members, func(m taxonomy.TermMember) bool { return m.ID == id }) {
			a.SetFlash(w, r, "error", "That term is not linked to this one.")
			Redirect(w, r, a.termURL(t.ID))
			return
		}
	}
	switch err := g.Unlink(r.Context(), id); {
	case errors.Is(err, taxonomy.ErrTermNotFound):
		a.SetFlash(w, r, "error", "That term no longer exists.")
	case err != nil:
		a.serverError(w, r, "The term could not be unlinked", err)
		return
	default:
		a.invalidate(r.Context())
		a.SetFlash(w, r, "success", "Unlinked the term.")
	}
	Redirect(w, r, a.termURL(t.ID))
}
