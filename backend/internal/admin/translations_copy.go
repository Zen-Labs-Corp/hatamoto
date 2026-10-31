package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
)

// copyForm feeds hm/form-copy: the Create copy dialog of the Translations tab
// and the /new page behind it.
type copyForm struct {
	// IDPrefix keeps field ids unique when several dialogs share a page.
	IDPrefix, DialogID string
	Action, NewURL     string
	Back               string
	Title              string
	Locale             string
	Name, Slug, From   string
	FromOptions        []ui.Option
	Missing            []entity.MissingParent
	MissingList        string
	Parents, Publish   bool
	Errs               map[string]string
	Summary            []ui.FieldError
}

// newCopyForm is the form with its defaults for a copy of e in tag: it starts
// from the original of e's group (e itself when it is the original or has no
// group), with the parents ticked when some are missing.
func (a *Admin) newCopyForm(ctx context.Context, e *entity.Entity, tag string, members []entity.Member, grouped bool, set *entity.LocaleSet) copyForm {
	base := a.translationsURL(e.ID)
	f := copyForm{
		IDPrefix: "copy-" + tag, DialogID: "create-copy-" + tag,
		Action: base + "/copy", NewURL: base + "/new?locale=" + url.QueryEscape(tag), Back: base,
		Locale: tag, Name: e.Name, Slug: e.Slug, Parents: true, Errs: map[string]string{},
	}
	f.Title = "Create a " + tag + " copy of “" + e.Name + "”"
	this := ui.Option{Value: string(entity.CopyFromThis), Label: "Copy of this page (" + e.Locale + ")"}
	f.From = string(entity.CopyFromThis)
	if grouped {
		if orig := entity.OriginalOf(members, set.Default()); orig != nil && orig.ID != e.ID {
			f.From, f.Name, f.Slug = string(entity.CopyFromOriginal), orig.Name, orig.Slug
			f.FromOptions = append(f.FromOptions, ui.Option{Value: f.From, Label: "Copy of the original (" + orig.Locale + ")"})
		}
	}
	f.FromOptions = append(f.FromOptions, this, ui.Option{Value: string(entity.CopyFromEmpty), Label: "Empty page"})
	f.setFrom(f.From)
	if schemaIsNested(e.Schema) && e.ParentID != nil {
		if missing, err := a.deps.Content.Copier.MissingParents(ctx, e.ID, tag); err == nil {
			f.Missing = missing
			names := make([]string, len(missing))
			for i, m := range missing {
				names[i] = m.Name
			}
			f.MissingList = strings.Join(names, ", ")
		}
	}
	return f
}

// setFrom selects the radio option with value v.
func (f *copyForm) setFrom(v string) {
	f.From = v
	for i := range f.FromOptions {
		f.FromOptions[i].Selected = f.FromOptions[i].Value == v
	}
}

// copyTarget loads the entity of the route and the locale set, refusing the
// request (and writing the response) when copies are off for this site, the
// person's role or the entry.
func (a *Admin) copyTarget(w http.ResponseWriter, r *http.Request) (*entity.Entity, *entity.LocaleSet, bool) {
	e, ok := a.translationTarget(w, r)
	if !ok {
		return nil, nil, false
	}
	set, multi := a.multiLocale(r.Context())
	if !multi || a.deps.Content.Copier == nil || !entity.CanCopy(e) {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return nil, nil, false
	}
	return e, set, true
}

func (a *Admin) renderCopyNew(w http.ResponseWriter, r *http.Request, status int, e *entity.Entity, f copyForm) {
	v := a.NewView(w, r, f.Title)
	v.Width = "narrow"
	v.Crumbs = a.entityCrumbs(e.Name, e.ID.String(), Crumb{Label: "Translations", Href: f.Back}, Crumb{Label: "Create copy"})
	v.Data = f
	a.Render(w, r, status, "entities/translation-new", v)
}

// handleTranslationCopyNew is the page behind the Create row action: what a
// browser without JavaScript sees instead of the dialog.
func (a *Admin) handleTranslationCopyNew(w http.ResponseWriter, r *http.Request) {
	e, set, ok := a.copyTarget(w, r)
	if !ok {
		return
	}
	tag := r.URL.Query().Get("locale")
	if !slices.Contains(set.Tags(), tag) {
		a.notFound(w, r, "Locale", a.translationsURL(e.ID), "Back to translations")
		return
	}
	members, err := a.deps.Content.Groups.Members(r.Context(), e.ID)
	if err != nil {
		a.serverError(w, r, "The translations could not be loaded", err)
		return
	}
	a.renderCopyNew(w, r, http.StatusOK, e, a.newCopyForm(r.Context(), e, tag, members, len(members) > 1, set))
}

// handleTranslationCopy creates the copy asked for in the dialog or the /new
// page and opens it. A refused copy renders the /new page again with the
// reason on the field.
func (a *Admin) handleTranslationCopy(w http.ResponseWriter, r *http.Request) {
	e, set, ok := a.copyTarget(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tag := strings.TrimSpace(r.PostFormValue("locale"))
	if !slices.Contains(set.Tags(), tag) {
		a.SetFlash(w, r, "error", "Pick one of the site's languages.")
		Redirect(w, r, a.translationsURL(e.ID))
		return
	}
	members, err := a.deps.Content.Groups.Members(ctx, e.ID)
	if err != nil {
		a.serverError(w, r, "The translations could not be loaded", err)
		return
	}
	f := a.newCopyForm(ctx, e, tag, members, len(members) > 1, set)
	f.Name, f.Slug = strings.TrimSpace(r.PostFormValue("name")), strings.TrimSpace(r.PostFormValue("slug"))
	f.setFrom(r.PostFormValue("from"))
	f.Parents, f.Publish = r.PostFormValue("parents") == "true", r.PostFormValue("publish") == "true"
	fail := func(errs map[string]string) {
		f.Errs = errs
		for _, k := range []struct{ key, id, label string }{{"name", "name", "Title"}, {"slug", "slug", "Address"}, {"from", "from", "Start from"}} {
			if m := errs[k.key]; m != "" {
				f.Summary = append(f.Summary, ui.FieldError{ID: f.IDPrefix + "-" + k.id, Label: k.label, Message: m})
			}
		}
		a.renderCopyNew(w, r, http.StatusUnprocessableEntity, e, f)
	}
	from := entity.CopyFrom(f.From)
	if from != entity.CopyFromOriginal && from != entity.CopyFromThis && from != entity.CopyFromEmpty {
		fail(map[string]string{"from": "Pick what the copy starts from."})
		return
	}
	errs := map[string]string{}
	if f.Name == "" {
		errs["name"] = "Enter a title."
	}
	if f.Slug == "" {
		errs["slug"] = "Enter an address."
	}
	if len(errs) > 0 {
		fail(errs)
		return
	}
	uid := uuid.Nil
	if u := userFrom(ctx); u != nil {
		uid, _ = uuid.Parse(u.ID)
	}
	var warnings []string
	cp, err := a.deps.Content.Copier.CreateCopy(ctx, uid, e.ID, entity.CopyOptions{
		Locale: tag, Name: f.Name, Slug: f.Slug, From: from, DefaultLocale: set.Default(),
		CreateParents: f.Parents, Publish: f.Publish, Warn: func(m string) { warnings = append(warnings, m) },
	})
	switch {
	case errors.Is(err, entity.ErrSlugAlreadyExists):
		fail(map[string]string{"slug": "That address is already used in " + tag + ". Pick another."})
		return
	case errors.Is(err, entity.ErrLocaleTaken):
		a.SetFlash(w, r, "error", "This page already has a "+tag+" copy.")
		Redirect(w, r, a.translationsURL(e.ID))
		return
	case errors.Is(err, entity.ErrValidationFailed):
		fail(map[string]string{"name": err.Error()})
		return
	case err != nil:
		a.serverError(w, r, "The copy could not be created", err)
		return
	}
	a.auditEntity(r, auditEntityCreateCopy, cp.ID, cp, map[string]any{"source": e.ID.String(), "locale": tag, "from": string(from), "published": f.Publish})
	a.invalidate(ctx)
	msg := "Created the " + tag + " copy."
	if f.Publish {
		msg = "Created and published the " + tag + " copy."
	}
	a.SetFlash(w, r, "success", strings.TrimSpace(msg+" "+strings.Join(warnings, " ")))
	Redirect(w, r, a.adminPath+"/entities/"+cp.ID.String())
}
