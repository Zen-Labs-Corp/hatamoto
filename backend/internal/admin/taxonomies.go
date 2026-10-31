package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/taxonomy"
)

func (a *Admin) mountTaxonomies(authed *httpx.Group) {
	owner := authed.Group("", a.RequireRole("owner"))
	owner.HandleFunc("GET /settings/taxonomies", a.handleTaxonomies)
	owner.HandleFunc("GET /settings/taxonomies/new", a.handleTermNew)
	owner.HandleFunc("POST /settings/taxonomies/terms", a.handleTermCreate)
	owner.HandleFunc("GET /settings/taxonomies/terms/{id}", a.handleTermEdit)
	owner.HandleFunc("POST /settings/taxonomies/terms/{id}", a.handleTermSave)
	owner.HandleFunc("GET /settings/taxonomies/terms/{id}/merge", a.handleTermMergeForm)
	owner.HandleFunc("POST /settings/taxonomies/terms/{id}/merge", a.handleTermMerge)
	owner.HandleFunc("GET /settings/taxonomies/terms/{id}/delete", a.handleTermDeleteConfirm)
	owner.HandleFunc("POST /settings/taxonomies/terms/{id}/delete", a.handleTermDelete)
	a.mountTermLocales(owner)
}

type termForm struct {
	ID                                      string
	Kind, NewKind, Name, Slug, Desc, Parent string
	Errors                                  map[string]string
	Summary                                 []ui.FieldError
	ParentOptions, KindOptions              []ui.Option
	Back                                    string
	Crumbs                                  []ui.Crumb
	SEO                                     seoForm
	Panel                                   seoPanel
	// Locales is the Locale field and copies block; nil on a single-locale site.
	Locales *termLocales
}

type taxonomiesData struct {
	Kind  string
	Tabs  []ui.Tab
	Table ui.Table
	Form  termForm
	// Actions is the header row: the "New term" link that opens the dialog.
	Actions []ui.Button
}

func (a *Admin) taxBase(kind string) string {
	return a.adminPath + "/settings/taxonomies?kind=" + url.QueryEscape(kind)
}

// kindsOf lists the kinds that exist (tag and category always do).
func kindsOf(nodes []termNode) []string {
	set := map[string]bool{taxonomy.KindTag: true, taxonomy.KindCategory: true}
	for _, n := range nodes {
		set[n.Kind] = true
	}
	var kinds []string
	for k := range set {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	return kinds
}

func parentOptions(nodes []termNode, kind, selected, exclude string) []ui.Option {
	opts := []ui.Option{{Value: "", Label: "No parent (top level)", Selected: selected == ""}}
	skip := map[string]bool{}
	if exclude != "" {
		skip[exclude] = true
	}
	for _, n := range nodes {
		if n.Kind != kind {
			continue
		}
		if n.ParentID != nil && skip[n.ParentID.String()] {
			skip[n.ID.String()] = true
		}
		if skip[n.ID.String()] {
			continue
		}
		opts = append(opts, ui.Option{Value: n.ID.String(), Label: strings.Repeat("— ", n.Depth) + n.Name, Selected: selected == n.ID.String()})
	}
	return opts
}

// currentKind picks the kind a page works on: the asked one when it exists,
// else category, else the first.
func currentKind(kinds []string, kind string) string {
	if slices.Contains(kinds, kind) {
		return kind
	}
	if slices.Contains(kinds, taxonomy.KindCategory) {
		return taxonomy.KindCategory
	}
	return kinds[0]
}

// termFormOptions fills the kind and parent selects of the create form.
func termFormOptions(nodes []termNode, kinds []string, kind string, f termForm) termForm {
	f.Kind = kind
	f.ParentOptions = parentOptions(nodes, kind, f.Parent, "")
	f.KindOptions = kindOptions(kindSet(kinds), kind)
	return f
}

// kindLabel is a term kind as a tab label: "category" reads "Categories", "tag" reads "Tags".
func kindLabel(kind string) string {
	if kind == "" {
		return kind
	}
	title := strings.ToUpper(kind[:1]) + kind[1:]
	switch {
	case len(kind) > 1 && strings.HasSuffix(kind, "y") && !strings.ContainsRune("aeiou", rune(kind[len(kind)-2])):
		return title[:len(title)-1] + "ies"
	case strings.HasSuffix(kind, "s"):
		return title
	}
	return title + "s"
}

func (a *Admin) renderTaxonomies(w http.ResponseWriter, r *http.Request, kind string) {
	ctx := r.Context()
	nodes, err := a.termNodes(ctx)
	if err != nil {
		a.serverError(w, r, "The terms could not be loaded", err)
		return
	}
	kinds := kindsOf(nodes)
	kind = currentKind(kinds, kind)
	counts := map[string]int{}
	for _, n := range nodes {
		counts[n.Kind]++
	}
	d := taxonomiesData{Kind: kind}
	for _, k := range kinds {
		d.Tabs = append(d.Tabs, ui.Tab{Label: kindLabel(k), Href: a.taxBase(k), Count: counts[k], Active: k == kind})
	}
	t := ui.Table{
		ID: "terms", Caption: "Terms of kind " + kind, BaseURL: a.adminPath + "/settings/taxonomies", CSRF: csrfToken(ctx),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "slug", Label: "Slug"}, {Key: "entities", Label: "Entities", Align: "right"}},
		Empty: ui.Empty{Icon: "tag", Title: "No " + kind + " terms yet", Body: "Add the first one.",
			Actions: []ui.Button{{Label: "New term", Href: a.adminPath + "/settings/taxonomies/new?kind=" + url.QueryEscape(kind), Variant: "primary", Attrs: map[string]string{"data-hm-open": "new-term"}}}},
	}
	for _, n := range nodes {
		if n.Kind != kind {
			continue
		}
		id := n.ID.String()
		label := n.Name
		if n.Depth > 0 {
			label = n.Name + " (level " + strconv.Itoa(n.Depth+1) + ")"
		}
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: label, Cells: []ui.Cell{
			{Text: n.Name, Depth: min(n.Depth, 8), Link: a.adminPath + "/settings/taxonomies/terms/" + id}, {Text: n.Slug}, {Text: strconv.Itoa(n.Count)},
		}, Actions: []ui.Action{
			{Label: "Edit", Href: a.adminPath + "/settings/taxonomies/terms/" + id},
			{Label: "Merge", Href: a.adminPath + "/settings/taxonomies/terms/" + id + "/merge"},
			{Label: "Delete", Href: a.adminPath + "/settings/taxonomies/terms/" + id + "/delete", Post: a.adminPath + "/settings/taxonomies/terms/" + id + "/delete", Danger: true, ConfirmLabel: "Delete term",
				Confirm: deleteTermWarning(n)},
		}})
	}
	// Tree order stays, but the term just created leads its kind (page 1, no paging here);
	// its row keeps the depth indent.
	t.CreatedID = createdID(r)
	t.Rows = pinFirst(t.Rows, t.CreatedID)
	d.Table = t
	d.Form = termFormOptions(nodes, kinds, kind, termForm{})
	d.Actions = []ui.Button{{Label: "New term", Href: a.adminPath + "/settings/taxonomies/new?kind=" + url.QueryEscape(kind), Variant: "primary", Icon: "plus", Attrs: map[string]string{"data-hm-open": "new-term"}}}
	v := a.NewView(w, r, "Taxonomies")
	v.Data = d
	a.Render(w, r, http.StatusOK, "taxonomies/list", v)
}

// renderTermNew renders the New term page, the no-JS twin of the list dialog
// and the 422 target of a failed create.
func (a *Admin) renderTermNew(w http.ResponseWriter, r *http.Request, status int, kind string, f termForm) {
	nodes, err := a.termNodes(r.Context())
	if err != nil {
		a.serverError(w, r, "The terms could not be loaded", err)
		return
	}
	kinds := kindsOf(nodes)
	kind = currentKind(kinds, kind)
	if f.Errors == nil {
		f.Errors = map[string]string{}
	}
	f = termFormOptions(nodes, kinds, kind, f)
	f.Back = a.taxBase(kind)
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"slug", "Slug"}, {"new_kind", "New kind"}, {"parent", "Parent"}, {"description", "Description"}, {"seo_title", "SEO title"}, {"seo_description", "SEO description"}, {"seo_canonical", "Canonical URL"}, {"seo_og_image", "Social image"}} {
		if m := f.Errors[k.id]; m != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	v := a.NewView(w, r, "New term")
	v.Data = f
	v.Width = "narrow"
	a.Render(w, r, status, "taxonomies/new", v)
}

func (a *Admin) handleTermNew(w http.ResponseWriter, r *http.Request) {
	if a.deps.Content.Terms == nil {
		a.unavailable(w, r, "Taxonomies")
		return
	}
	a.renderTermNew(w, r, http.StatusOK, r.URL.Query().Get("kind"), termForm{})
}

func kindSet(kinds []string) map[string]bool {
	m := map[string]bool{}
	for _, k := range kinds {
		m[k] = true
	}
	return m
}

func deleteTermWarning(n termNode) string {
	msg := "“" + n.Name + "” is removed from " + strconv.Itoa(n.Count) + " "
	if n.Count == 1 {
		msg += "entity."
	} else {
		msg += "entities."
	}
	if n.HasChildren {
		msg += " Its child terms move to the top level."
	}
	return msg + " This cannot be undone."
}

func (a *Admin) handleTaxonomies(w http.ResponseWriter, r *http.Request) {
	if a.deps.Content.Terms == nil {
		a.unavailable(w, r, "Taxonomies")
		return
	}
	a.renderTaxonomies(w, r, r.URL.Query().Get("kind"))
}

// termFieldError maps a taxonomy service error onto the form.
func termFieldError(f *termForm, err error) bool {
	switch {
	case errors.Is(err, taxonomy.ErrTermSlugConflict):
		f.Errors["slug"] = "Another term of this kind already uses that slug"
	case errors.Is(err, taxonomy.ErrTermParentInvalid):
		f.Errors["parent"] = "That parent is not allowed: it would nest too deeply or loop back"
	case errors.Is(err, taxonomy.ErrTermParentNotFound):
		f.Errors["parent"] = "That parent no longer exists"
	case errors.Is(err, taxonomy.ErrInvalidKind):
		f.Errors["new_kind"] = "Use a short lower-case word (letters, digits, - or _) that the site does not already use"
	case errors.Is(err, taxonomy.ErrInvalidName):
		f.Errors["name"] = "Enter a name of up to 128 characters"
	case errors.Is(err, taxonomy.ErrInvalidSlug):
		f.Errors["slug"] = "Use lower-case letters, digits and hyphens"
	case errors.Is(err, taxonomy.ErrInvalidDescription):
		f.Errors["description"] = "Use at most 2000 characters"
	case errors.Is(err, taxonomy.ErrInvalidSEO):
		id, msg := "seo_canonical", "Use a full address starting with https:// or a path starting with /"
		var fe *taxonomy.SEOFieldError
		if errors.As(err, &fe) {
			switch fe.Field {
			case taxonomy.SEOFieldTitle:
				id, msg = "seo_title", "Use at most 255 characters"
			case taxonomy.SEOFieldDescription:
				id, msg = "seo_description", "Use at most 500 characters"
			case taxonomy.SEOFieldOGImage:
				id, msg = "seo_og_image", "Use at most 2048 characters"
			}
		}
		f.Errors[id] = msg
	default:
		return false
	}
	return true
}

func readTermForm(r *http.Request) termForm {
	_ = r.ParseForm()
	g := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	return termForm{Kind: g("kind"), NewKind: strings.ToLower(g("new_kind")), Name: g("name"), Slug: g("slug"), Desc: g("description"), Parent: g("parent"), SEO: seoFromForm(r), Errors: map[string]string{}}
}

func (a *Admin) handleTermCreate(w http.ResponseWriter, r *http.Request) {
	ts := a.deps.Content.Terms
	if ts == nil {
		a.unavailable(w, r, "Taxonomies")
		return
	}
	f := readTermForm(r)
	kind := f.Kind
	if f.NewKind != "" {
		kind = f.NewKind
	}
	in := taxonomy.CreateTermInput{Kind: kind, Name: f.Name, Slug: f.Slug, Description: f.Desc}
	if id, err := uuid.Parse(f.Parent); err == nil && f.NewKind == "" {
		in.ParentID = &id
	}
	if f.Name == "" {
		f.Errors["name"] = "Enter a name"
		a.renderTermNew(w, r, http.StatusUnprocessableEntity, f.Kind, f)
		return
	}
	t, err := ts.CreateTerm(r.Context(), in)
	if err != nil {
		if termFieldError(&f, err) {
			a.renderTermNew(w, r, http.StatusUnprocessableEntity, f.Kind, f)
			return
		}
		a.serverError(w, r, "The term could not be created", err)
		return
	}
	a.SetFlash(w, r, "success", "Added “"+t.Name+"”.")
	Redirect(w, r, createdRedirect(a.taxBase(t.Kind), t.ID.String()))
}

func (a *Admin) loadTerm(w http.ResponseWriter, r *http.Request) (*taxonomy.Term, bool) {
	ts := a.deps.Content.Terms
	id, ok := uuidParam(r, "id")
	if !ok || ts == nil {
		a.notFound(w, r, "Term", a.adminPath+"/settings/taxonomies", "Back to taxonomies")
		return nil, false
	}
	t, err := ts.GetTerm(r.Context(), id)
	if err != nil {
		if errors.Is(err, taxonomy.ErrTermNotFound) {
			a.notFound(w, r, "Term", a.adminPath+"/settings/taxonomies", "Back to taxonomies")
		} else {
			a.serverError(w, r, "The term could not be loaded", err)
		}
		return nil, false
	}
	return t, true
}

func (a *Admin) renderTermEdit(w http.ResponseWriter, r *http.Request, status int, t *taxonomy.Term, f termForm) {
	nodes, _ := a.termNodes(r.Context())
	f.ID, f.Kind = t.ID.String(), t.Kind
	f.ParentOptions = parentOptions(nodes, t.Kind, f.Parent, t.ID.String())
	f.Back = a.taxBase(t.Kind)
	f.Crumbs = a.areaCrumbs("taxonomies", Crumb{Label: t.Kind, Href: a.taxBase(t.Kind)}, Crumb{Label: t.Name})
	if f.Errors == nil {
		f.Errors = map[string]string{}
	}
	f.Summary = nil
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"slug", "Slug"}, {"parent", "Parent"}, {"description", "Description"}, {"seo_title", "SEO title"}, {"seo_description", "SEO description"}, {"seo_canonical", "Canonical URL"}, {"seo_og_image", "Social image"}} {
		if m := f.Errors[k.id]; m != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	in := seoInput{Title: t.Name, MetaTitle: f.SEO.Title, MetaDescription: f.SEO.Description, Slug: t.Slug, Canonical: f.SEO.Canonical,
		Mode: ModeMetaOnly, NoKeyword: true, NoLLMs: true, Kind: seo.KindTaxonomy, TermKind: t.Kind, Term: t.Name,
		Path: termPath(nodes, t)}
	if a.deps.BaseURL != nil {
		in.BaseURL = a.deps.BaseURL(r)
	}
	a.withSEOSettings(r, &in, "")
	f.Panel = buildPanel(in)
	f.Locales = a.termLocalesFor(r, t)
	f.Panel.JSONLDNote = "The public archive page adds its own structured data: a collection page and breadcrumbs."
	v := a.NewView(w, r, "Edit "+t.Name)
	v.Data = f
	v.Width = "narrow"
	v.Crumbs = f.Crumbs
	a.Render(w, r, status, "taxonomies/term", v)
}

// termPath is the public path of a term: its kind, then the slugs from the
// top-level ancestor down to the term.
func termPath(nodes []termNode, t *taxonomy.Term) string {
	byID := make(map[uuid.UUID]termNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	chain := []string{t.Slug}
	seen := map[uuid.UUID]bool{t.ID: true}
	for pid := t.ParentID; pid != nil && !seen[*pid]; {
		p, ok := byID[*pid]
		if !ok {
			break
		}
		seen[p.ID] = true
		chain = append([]string{p.Slug}, chain...)
		pid = p.ParentID
	}
	return "/" + t.Kind + "/" + strings.Join(chain, "/")
}

func (a *Admin) handleTermEdit(w http.ResponseWriter, r *http.Request) {
	t, ok := a.loadTerm(w, r)
	if !ok {
		return
	}
	f := termForm{Name: t.Name, Slug: t.Slug, Desc: t.Description}
	if t.SEO != nil {
		f.SEO = seoForm{Title: t.SEO.MetaTitle, Description: t.SEO.MetaDescription, Canonical: t.SEO.CanonicalURL, OGImage: t.SEO.OGImage, NoIndex: t.SEO.NoIndex}
	}
	if t.ParentID != nil {
		f.Parent = t.ParentID.String()
	}
	a.renderTermEdit(w, r, http.StatusOK, t, f)
}

func (a *Admin) handleTermSave(w http.ResponseWriter, r *http.Request) {
	t, ok := a.loadTerm(w, r)
	if !ok {
		return
	}
	f := readTermForm(r)
	if f.Name == "" {
		f.Errors["name"] = "Enter a name"
		a.renderTermEdit(w, r, http.StatusUnprocessableEntity, t, f)
		return
	}
	desc := f.Desc
	in := taxonomy.UpdateTermInput{ID: t.ID, Name: f.Name, Slug: f.Slug, Description: &desc,
		SEO: &taxonomy.TermSEO{MetaTitle: f.SEO.Title, MetaDescription: f.SEO.Description, CanonicalURL: f.SEO.Canonical, OGImage: f.SEO.OGImage, NoIndex: f.SEO.NoIndex}}
	if pid, err := uuid.Parse(f.Parent); err == nil {
		in.ParentID = &pid
	} else {
		in.ClearParent = true
	}
	if f.Slug == "" {
		in.Slug = taxonomy.Slugify(f.Name)
	}
	if _, err := a.deps.Content.Terms.UpdateTerm(r.Context(), in); err != nil {
		if termFieldError(&f, err) {
			a.renderTermEdit(w, r, http.StatusUnprocessableEntity, t, f)
			return
		}
		if errors.Is(err, taxonomy.ErrTermNotFound) {
			a.notFound(w, r, "Term", a.adminPath+"/settings/taxonomies", "Back to taxonomies")
			return
		}
		a.serverError(w, r, "The term could not be saved", err)
		return
	}
	a.SetFlash(w, r, "success", "Term saved.")
	Redirect(w, r, a.taxBase(t.Kind))
}

type mergeData struct {
	ID, Name, Kind string
	Count          int
	Targets        []ui.Option
	Crumbs         []ui.Crumb
	Error          string
	Summary        []ui.FieldError
	Warning        string // what the merge does, in numbers
}

func (a *Admin) renderMerge(w http.ResponseWriter, r *http.Request, status int, t *taxonomy.Term, errMsg string) {
	nodes, _ := a.termNodes(r.Context())
	d := mergeData{ID: t.ID.String(), Name: t.Name, Kind: t.Kind, Error: errMsg, Crumbs: a.areaCrumbs("taxonomies", Crumb{Label: t.Kind, Href: a.taxBase(t.Kind)}, Crumb{Label: "Merge " + t.Name})}
	d.Targets = []ui.Option{{Value: "", Label: "Choose a term", Selected: true}}
	for _, n := range nodes {
		if n.ID == t.ID {
			d.Count = n.Count
			continue
		}
		if n.Kind == t.Kind {
			d.Targets = append(d.Targets, ui.Option{Value: n.ID.String(), Label: strings.Repeat("— ", n.Depth) + n.Name})
		}
	}
	if errMsg != "" {
		d.Summary = []ui.FieldError{{ID: "into", Label: "Merge into", Message: errMsg}}
	}
	d.Warning = fmt.Sprintf("%d %s will be re-tagged with the term you choose, then “%s” is deleted.", d.Count, plural(d.Count, "entity", "entities"), t.Name)
	v := a.NewView(w, r, "Merge "+t.Name)
	v.Data = d
	v.Width = "narrow"
	v.Crumbs = d.Crumbs
	a.Render(w, r, status, "taxonomies/merge", v)
}

func (a *Admin) handleTermMergeForm(w http.ResponseWriter, r *http.Request) {
	t, ok := a.loadTerm(w, r)
	if !ok {
		return
	}
	a.renderMerge(w, r, http.StatusOK, t, "")
}

func (a *Admin) handleTermMerge(w http.ResponseWriter, r *http.Request) {
	t, ok := a.loadTerm(w, r)
	if !ok {
		return
	}
	into, err := uuid.Parse(r.PostFormValue("into"))
	if err != nil {
		a.renderMerge(w, r, http.StatusUnprocessableEntity, t, "Choose the term to merge into")
		return
	}
	target, err := a.deps.Content.Terms.GetTerm(r.Context(), into)
	if err != nil {
		a.renderMerge(w, r, http.StatusUnprocessableEntity, t, "That term no longer exists")
		return
	}
	switch err := a.deps.Content.Terms.MergeTerms(r.Context(), t.ID, into); {
	case err == nil:
	case errors.Is(err, taxonomy.ErrMergeHasChilds):
		a.renderMerge(w, r, http.StatusUnprocessableEntity, t, "This term has child terms. Move or delete them first.")
		return
	case errors.Is(err, taxonomy.ErrMergeKind), errors.Is(err, taxonomy.ErrMergeSameTerm):
		a.renderMerge(w, r, http.StatusUnprocessableEntity, t, "Choose another term of the same kind")
		return
	default:
		a.serverError(w, r, "The terms could not be merged", err)
		return
	}
	a.SetFlash(w, r, "success", "Merged “"+t.Name+"” into “"+target.Name+"”.")
	Redirect(w, r, a.taxBase(t.Kind))
}

func (a *Admin) handleTermDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	t, ok := a.loadTerm(w, r)
	if !ok {
		return
	}
	msg := "The term is removed from every entity that uses it. Its child terms move to the top level. This cannot be undone."
	a.renderConfirm(w, r, confirmData{Title: "Delete “" + t.Name + "”?", Message: msg, Post: a.adminPath + "/settings/taxonomies/terms/" + t.ID.String() + "/delete", Label: "Delete term", Back: a.taxBase(t.Kind), Danger: true})
}

func (a *Admin) handleTermDelete(w http.ResponseWriter, r *http.Request) {
	t, ok := a.loadTerm(w, r)
	if !ok {
		return
	}
	if err := a.deps.Content.Terms.DeleteTerm(r.Context(), t.ID); err != nil {
		a.serverError(w, r, "The term could not be deleted", err)
		return
	}
	a.SetFlash(w, r, "success", "Deleted “"+t.Name+"”.")
	Redirect(w, r, a.taxBase(t.Kind))
}
