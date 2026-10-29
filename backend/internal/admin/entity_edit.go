package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/core/internal/urlpattern"
)

// seoKeys are the entity attributes the SEO panel edits (the keys the public
// site reads). They are removed from the attribute form so a field is shown once.
var seoKeys = []string{"meta_title", "meta_description", "canonical_url", "og_image", "no_index"}

const (
	attrPrefix     = "attr."
	scheduleLayout = "2006-01-02T15:04"
)

type seoForm struct {
	Title, Description, Canonical, OGImage string
	NoIndex                                bool
}

type termPick struct {
	ID, Name string
	Depth    int
	Checked  bool
}

// newTerm is a term the writer typed into a token input; it is created when the form is saved.
type newTerm struct{ Kind, Name string }

// newTermsFrom reads the repeated new_term fields ("kind:name"); a bare name is a tag.
func newTermsFrom(r *http.Request) []newTerm {
	var out []newTerm
	for _, raw := range r.PostForm["new_term"] {
		kind, name, ok := strings.Cut(raw, ":")
		if !ok {
			kind, name = taxonomy.KindTag, raw
		}
		if kind, name = strings.TrimSpace(kind), strings.TrimSpace(name); name != "" && kind != "" {
			out = append(out, newTerm{Kind: kind, Name: name})
		}
	}
	return out
}

type termGroup struct {
	Kind  string
	Terms []termPick
}

type entityForm struct {
	Parent               parentChoice
	UpdatedAt            string // when the entity was last saved, for the save indicator; empty on a new or re-rendered form
	ParentOrig           string // the parent when the form was opened; a save that posts it back leaves the parent alone
	New                  bool
	ID, SchemaSlug       string
	SchemaName           string
	Name, Slug           string
	Status               string
	IsPerson             bool // a person's visibility is its Public switch: no status or schedule
	StatusOptions        []ui.Option
	Schedule, Zone       string
	ShowParent           bool
	ParentOptions        []ui.Option
	ShowAuthor           bool
	AuthorOptions        []ui.Option
	NewAuthor            string
	TermGroups           []termGroup
	ShowTerms            bool
	CanCreateTerm        bool
	NewTermName          string
	NewTerms             []newTerm // typed into the chips as "kind:name"; created on save
	NewTermKind          string
	KindOptions          []ui.Option
	SEO                  seoForm
	Attrs                template.HTML
	Errors               map[string]string
	Summary              []ui.FieldError
	Tabs                 []ui.Tab
	PublicURL, EditorURL string
	Version              int
	Actions              []ui.Button
	Subtitle             string
	// Panel is the shared SEO panel; AnalyzeURL is where it refreshes (empty on
	// a new entity, which has no address yet) and Noun what the entry is called.
	Panel      seoPanel
	AnalyzeURL string
	Noun       string
}

func (a *Admin) loadEntity(w http.ResponseWriter, r *http.Request) (*entity.Entity, *entity.EntitySchema, bool) {
	c := a.deps.Content
	id, ok := uuidParam(r, "id")
	if !ok || c.Entities == nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return nil, nil, false
	}
	e, err := c.Entities.GetEntity(r.Context(), id)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		} else {
			a.serverError(w, r, "The entity could not be loaded", err)
		}
		return nil, nil, false
	}
	sc := e.Schema
	if sc == nil {
		if sc, err = c.Entities.GetSchema(r.Context(), e.SchemaID); err != nil {
			a.serverError(w, r, "The entity type could not be loaded", err)
			return nil, nil, false
		}
	}
	return e, sc, true
}

// schemaIsNested reports whether the schema's URL pattern uses {parent} or
// {path}, i.e. whether entities of it have a parent.
func schemaIsNested(sc *entity.EntitySchema) bool {
	st, err := sc.URLSettings()
	if err != nil || st.URLPattern == "" {
		return false
	}
	p, err := urlpattern.Parse(st.URLPattern)
	return err == nil && (p.HasParent() || p.HasPath())
}

func attrsMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// robotsNoIndex reports whether attributes.seo.robots (written by the React
// SEO tab, a comma separated list such as "noindex, follow") asks for noindex.
func robotsNoIndex(m map[string]any) bool {
	seo, _ := m["seo"].(map[string]any)
	return strings.Contains(strings.ToLower(str(seo, "robots")), "noindex")
}

// dropNoIndexRobots removes the noindex directive from attributes.seo.robots,
// keeping its other directives (and removing the key when none are left).
func dropNoIndexRobots(m map[string]any) {
	seo, _ := m["seo"].(map[string]any)
	if seo == nil {
		return
	}
	var keep []string
	for _, d := range strings.Split(str(seo, "robots"), ",") {
		if d = strings.TrimSpace(d); d != "" && !strings.EqualFold(d, "noindex") {
			keep = append(keep, d)
		}
	}
	if len(keep) > 0 {
		seo["robots"] = strings.Join(keep, ", ")
	} else {
		delete(seo, "robots")
	}
}

func seoFrom(m map[string]any) seoForm {
	b, _ := m["no_index"].(bool)
	b = b || robotsNoIndex(m)
	return seoForm{Title: str(m, "meta_title"), Description: str(m, "meta_description"), Canonical: str(m, "canonical_url"), OGImage: str(m, "og_image"), NoIndex: b}
}

// merge writes the SEO panel into attrs, dropping empty values.
func (s seoForm) merge(m map[string]any) {
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			m[k] = v
		} else {
			delete(m, k)
		}
	}
	set("meta_title", s.Title)
	set("meta_description", s.Description)
	set("canonical_url", s.Canonical)
	set("og_image", s.OGImage)
	if s.NoIndex {
		m["no_index"] = true
	} else {
		delete(m, "no_index")
		dropNoIndexRobots(m)
	}
}

func seoFromForm(r *http.Request) seoForm {
	return seoForm{Title: r.PostFormValue("seo_title"), Description: r.PostFormValue("seo_description"), Canonical: r.PostFormValue("seo_canonical"),
		OGImage: r.PostFormValue("seo_og_image"), NoIndex: r.PostFormValue("seo_noindex") == "true"}
}

func entityStatusOptions(selected string) []ui.Option {
	var out []ui.Option
	for _, s := range []struct{ v, l string }{{"draft", "Draft"}, {"published", "Published"}, {"scheduled", "Scheduled"}, {"archived", "Archived"}} {
		out = append(out, ui.Option{Value: s.v, Label: s.l, Selected: s.v == selected})
	}
	return out
}

// fill loads the choices of the form (parents, authors, terms) around f.
func (a *Admin) fill(r *http.Request, f *entityForm, sc *entity.EntitySchema, self *entity.Entity, parentID, authorID string, picked map[string]bool) {
	c := a.deps.Content
	ctx := r.Context()
	loc := a.siteLocation(ctx)
	f.Zone = loc.String()
	f.StatusOptions = entityStatusOptions(f.Status)
	f.SchemaSlug, f.SchemaName = sc.Slug, sc.Name
	f.IsPerson = sc.Slug == person.SchemaSlug

	if schemaIsNested(sc) && c.Entities != nil {
		f.ShowParent = true
		var exclude uuid.UUID
		if self != nil {
			exclude = self.ID
		}
		f.Parent = a.findParents(ctx, "", parentID, exclude)
		f.Parent.Error = f.Errors["parent"]
	}
	// A person is their own byline: the picker is for everything else.
	if c.Authors != nil && sc.Slug != person.SchemaSlug {
		if list, err := c.Authors.List(ctx); err == nil {
			f.ShowAuthor = true
			f.AuthorOptions = []ui.Option{
				{Value: "", Label: "No author", Selected: authorID == ""},
				{Value: bylineOrgValue, Label: "Site organisation", Selected: authorID == bylineOrgValue},
			}
			for _, au := range list {
				f.AuthorOptions = append(f.AuthorOptions, ui.Option{Value: au.ID.String(), Label: au.Name, Selected: authorID == au.ID.String()})
			}
		}
	}
	if c.Terms != nil {
		if nodes, err := a.termNodes(ctx); err == nil {
			f.ShowTerms = true
			kinds := map[string]bool{"tag": true, "category": true}
			for _, n := range nodes {
				kinds[n.Kind] = true
				if len(f.TermGroups) == 0 || f.TermGroups[len(f.TermGroups)-1].Kind != n.Kind {
					f.TermGroups = append(f.TermGroups, termGroup{Kind: n.Kind})
				}
				g := &f.TermGroups[len(f.TermGroups)-1]
				g.Terms = append(g.Terms, termPick{ID: n.ID.String(), Name: n.Name, Depth: min(n.Depth, 8), Checked: picked[n.ID.String()]})
			}
			f.KindOptions = kindOptions(kinds, f.NewTermKind)
		}
		if u := userFrom(ctx); u != nil && u.Role == "owner" {
			f.CanCreateTerm = f.ShowTerms
		}
	}
}

// parentSearchLimit caps the matches the parent picker lists at once; typing
// narrows them (server side), so no site size makes the list unusable.
const parentSearchLimit = 20

// parentChoice is the parent picker's select: "no parent", the chosen parent
// (always present, whatever its type or name) and the entities matching q.
type parentChoice struct {
	Q       string
	Exclude string
	Error   string // set on the edit form, not in search results
	Options []ui.Option
}

// findParents lists candidate parents of every entity type (the service allows
// cross-type parents). The selected parent is always among the options, loaded
// by id when the search does not return it.
func (a *Admin) findParents(ctx context.Context, q, selected string, exclude uuid.UUID) parentChoice {
	c := a.deps.Content
	pc := parentChoice{Q: q}
	if exclude != uuid.Nil {
		pc.Exclude = exclude.String()
	}
	typeNames := map[uuid.UUID]string{}
	var reuse []uuid.UUID
	if schemas, err := c.Entities.ListSchemas(ctx); err == nil {
		for _, sc := range schemas {
			typeNames[sc.ID] = sc.Name
		}
		reuse = reuseSchemaIDs(schemas)
	}
	label := func(p *entity.Entity) string {
		l := p.Name + " (/" + p.Slug + ")"
		if t := typeNames[p.SchemaID]; t != "" {
			l += " · " + t
		}
		return l
	}
	pc.Options = []ui.Option{{Value: "", Label: "No parent (top level)", Selected: selected == ""}}
	seen := map[string]bool{}
	if id, err := uuid.Parse(selected); err == nil && id != exclude {
		if cur, err := c.Entities.GetEntity(ctx, id); err == nil {
			pc.Options = append(pc.Options, ui.Option{Value: selected, Label: label(cur), Selected: true})
		} else {
			pc.Options = append(pc.Options, ui.Option{Value: selected, Label: "Current parent", Selected: true})
		}
		seen[selected] = true
	}
	res, err := c.Entities.ListEntitiesWithOffset(ctx, &entity.ListEntitiesFilter{Search: q, Sort: "name", Asc: true, ExcludeSchemaIDs: reuse}, &entity.OffsetPage{Page: 1, Limit: parentSearchLimit + 1})
	if err != nil {
		return pc
	}
	for _, p := range res.Items {
		if p.ID == exclude || seen[p.ID.String()] {
			continue
		}
		pc.Options = append(pc.Options, ui.Option{Value: p.ID.String(), Label: label(p)})
	}
	return pc
}

// handleParentOptions answers the parent picker's search box with a fresh
// select. The chosen parent stays in it.
func (a *Admin) handleParentOptions(w http.ResponseWriter, r *http.Request) {
	if a.deps.Content.Entities == nil {
		a.unavailable(w, r, "Entities")
		return
	}
	q := r.URL.Query()
	exclude, _ := uuid.Parse(q.Get("exclude"))
	v := a.NewView(w, r, "Parent")
	v.Data = a.findParents(r.Context(), strings.TrimSpace(q.Get("parent_q")), q.Get("parent"), exclude)
	a.Render(w, r, http.StatusOK, "entities/parent", v)
}

func kindOptions(kinds map[string]bool, selected string) []ui.Option {
	var names []string
	for k := range kinds {
		names = append(names, k)
	}
	sortStrings(names)
	if selected == "" {
		selected = "tag"
	}
	var out []ui.Option
	for _, k := range names {
		out = append(out, ui.Option{Value: k, Label: k, Selected: k == selected})
	}
	return out
}

func (a *Admin) entityTabs(id, active string) []ui.Tab {
	return []ui.Tab{
		{Label: "Details", Href: a.adminPath + "/entities/" + id, Active: active == "details"},
		{Label: "Versions", Href: a.adminPath + "/entities/" + id + "/versions", Active: active == "versions"},
	}
}

// renderAttrs renders the schema's own fields. SEO keys are shown by the SEO panel.
func renderAttrs(sc *entity.EntitySchema, values map[string]any, errs schemaform.Errors) (template.HTML, error) {
	schema, err := schemaform.WithoutProperties(sc.Schema, seoKeys...)
	if err != nil {
		return "", err
	}
	return schemaform.Render(schema, values, schemaform.Options{Prefix: attrPrefix, Errors: errs})
}

func (a *Admin) renderEntityForm(w http.ResponseWriter, r *http.Request, status int, f *entityForm, e *entity.Entity, sc *entity.EntitySchema, values map[string]any, errs schemaform.Errors, parentID, authorID string, picked map[string]bool) {
	a.fill(r, f, sc, e, parentID, authorID, picked)
	html, err := renderAttrs(sc, values, errs)
	if err != nil {
		a.serverError(w, r, "The form for this entity type could not be built", err)
		return
	}
	f.Attrs = html
	f.Subtitle = sc.Name
	if !f.New {
		f.Subtitle = fmt.Sprintf("%s · version %d", sc.Name, f.Version)
	}
	f.Actions = nil
	if f.PublicURL != "" {
		f.Actions = append(f.Actions, ui.Button{Label: "View on site", Href: f.PublicURL})
	}
	if f.EditorURL != "" {
		f.Actions = append(f.Actions, ui.Button{Label: "Visual editor", Href: f.EditorURL})
	}
	if f.Errors == nil {
		f.Errors = map[string]string{}
	}
	f.Noun = draftNoun(sc)
	if !f.New {
		f.AnalyzeURL = a.adminPath + "/entities/" + f.ID + "/analyze"
	}
	f.Panel = a.entityPanel(r, e, sc, f, values)
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"slug", "Slug"}, {"status", "Status"}, {"schedule", "Publish date"}, {"parent", "Parent"}, {"new_term_name", "New term"}, {"seo_title", "SEO title"}, {"seo_description", "SEO description"}, {"seo_canonical", "Canonical URL"}, {"seo_og_image", "Social image"}} {
		if m := f.Errors[k.id]; m != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	for path, msgs := range errs {
		if len(msgs) > 0 {
			id := attrPrefix + path
			f.Summary = append(f.Summary, ui.FieldError{ID: strings.ReplaceAll(id, ".", "-"), Label: schemaform.Humanize(path), Message: msgs[0]})
		}
	}
	title := f.Name
	if f.New {
		title = "New " + strings.ToLower(sc.Name)
	}
	if title == "" && e != nil {
		title = e.Name // a name cleared in the form must not rename the page
	}
	if title == "" {
		title = "Edit entity"
	}
	v := a.NewView(w, r, title)
	v.Data = f
	v.Crumbs = []Crumb{{Label: "Entities", Href: a.adminPath + "/entities"}, {Label: title}}
	a.Render(w, r, status, "entities/edit", v)
}

// bodyProperty is the schema's text property the SEO checks read as the body:
// "content" or "body" when the type has one of type string, else "".
func bodyProperty(sc *entity.EntitySchema) string {
	var s struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if json.Unmarshal(sc.Schema, &s) != nil {
		return ""
	}
	for _, name := range []string{"content", "body"} {
		if s.Properties[name].Type == "string" {
			return name
		}
	}
	return ""
}

// entityPanel builds the shared SEO panel for the entity as the form holds it.
// A type with no text body is checked on its title, description and address
// only. e is nil on a new entity.
func (a *Admin) entityPanel(r *http.Request, e *entity.Entity, sc *entity.EntitySchema, f *entityForm, attrs map[string]any) seoPanel {
	in := seoInput{
		Title: f.Name, MetaTitle: f.SEO.Title, MetaDescription: f.SEO.Description, Slug: f.Slug, Excerpt: str(attrs, "excerpt"),
		Canonical: f.SEO.Canonical, Published: f.Status == string(entity.StatusPublished), Mode: ModeMetaOnly, NoKeyword: true, NoLLMs: true,
	}
	if prop := bodyProperty(sc); prop != "" {
		in.Mode, in.Content = ModeBody, str(attrs, prop)
	}
	if a.deps.BaseURL != nil {
		in.BaseURL = a.deps.BaseURL(r)
	}
	if e != nil && a.deps.Content.PublicPath != nil {
		ee := *e
		ee.Schema, ee.Name, ee.Slug = sc, f.Name, f.Slug
		in.Path = a.deps.Content.PublicPath(r.Context(), &ee)
	}
	if in.Path == "" {
		in.Path = "/" + f.Slug
	}
	a.withSEOSettings(r, &in, sc.Slug)
	if e != nil {
		a.markHome(r.Context(), &in, e.ID)
	}
	panel := buildPanel(in)
	if e != nil {
		panel.JSONLD, panel.JSONLDType = a.jsonLDPreview(e, sc, &posted{f: f, attrs: attrs}, in)
	}
	return panel
}

// handleEntityAnalyze is POST /admin/entities/{id}/analyze: the SEO panel for
// the entity form as posted, an htmx fragment.
func (a *Admin) handleEntityAnalyze(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	p := a.readEntityForm(r, sc, attrsMap(e.Attributes), false)
	p.f.ID, p.f.Status = e.ID.String(), string(e.Status)
	v := a.NewView(w, r, "Search checks")
	v.Data = struct{ Panel seoPanel }{a.entityPanel(r, e, sc, p.f, p.attrs)}
	a.Render(w, r, http.StatusOK, "entities/seopanel", v)
}

func (a *Admin) handleEntityEdit(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	c := a.deps.Content
	attrs := attrsMap(e.Attributes)
	f := &entityForm{ID: e.ID.String(), Name: e.Name, Slug: e.Slug, Status: string(e.Status), SEO: seoFrom(attrs), Version: e.Version, Tabs: a.entityTabs(e.ID.String(), "details")}
	loc := a.siteLocation(r.Context())
	if !e.UpdatedAt.IsZero() {
		f.UpdatedAt = fmtDateTime(e.UpdatedAt.In(loc))
	}
	if e.Status == entity.StatusScheduled && e.PublishedAt != nil {
		f.Schedule = e.PublishedAt.In(loc).Format(scheduleLayout)
	}
	if usesVisualEditor(sc, e) {
		f.EditorURL = a.adminPath + "/entities/" + e.ID.String() + "/edit"
	}
	e.Schema = sc
	if c.PublicPath != nil && e.Status == entity.StatusPublished {
		f.PublicURL = c.PublicPath(r.Context(), e)
	}
	parentID, authorID := "", ""
	if e.ParentID != nil {
		parentID = e.ParentID.String()
	}
	authorID = bylineValue(e)
	f.ParentOrig = parentID
	picked := map[string]bool{}
	if c.Terms != nil {
		if ts, err := c.Terms.ListTermsForEntity(r.Context(), e.ID); err == nil {
			for _, t := range ts {
				picked[t.ID.String()] = true
			}
		}
	}
	a.renderEntityForm(w, r, http.StatusOK, f, e, sc, attrs, nil, parentID, authorID, picked)
}

func (a *Admin) handleEntityNew(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("type") == person.SchemaSlug {
		// People have their own list, with Add person.
		http.Redirect(w, r, a.adminPath+"/people", http.StatusSeeOther)
		return
	}
	c := a.deps.Content
	if c.Entities == nil {
		a.unavailable(w, r, "Entities")
		return
	}
	schemas, err := c.Entities.ListSchemas(r.Context())
	if err != nil {
		a.serverError(w, r, "The entity types could not be loaded", err)
		return
	}
	slug := r.URL.Query().Get("type")
	for _, sc := range schemas {
		if entity.IsReuseSchema(sc) {
			continue // blocks and layout parts are made under Theme
		}
		if sc.Slug == slug {
			if draftType(sc) {
				// A GET must not write: confirm, then POST to the draft route.
				v := a.NewView(w, r, "Start a new "+draftNoun(sc))
				v.Data = struct{ Type, Noun, Name string }{slug, draftNoun(sc), sc.Name}
				v.Width = "narrow"
				v.Crumbs = []Crumb{{Label: "Entities", Href: a.adminPath + "/entities"}, {Label: "New"}}
				a.Render(w, r, http.StatusOK, "entities/start", v)
				return
			}
			f := &entityForm{New: true, Status: "draft"}
			attrs := map[string]any{}
			a.renderEntityForm(w, r, http.StatusOK, f, nil, sc, attrs, nil, "", "", nil)
			return
		}
	}
	type choice struct {
		Name, Description, Href, Icon, Slug string
		Draft                               bool // one click creates the draft (a POST), as the Create menu does
	}
	var choices []choice
	for _, sc := range schemas {
		if entity.IsReuseSchema(sc) {
			continue
		}
		d := ""
		if sc.Description != nil {
			d = *sc.Description
		}
		choices = append(choices, choice{Name: sc.Name, Description: d, Href: a.adminPath + "/entities/new?type=" + url.QueryEscape(sc.Slug), Icon: typeIcon(sc.Slug), Slug: sc.Slug, Draft: draftType(sc)})
	}
	empty := ui.Empty{Icon: "file-text", Title: "No entity types yet", Body: "An entity type describes the fields of a kind of content."}
	if u := userFrom(r.Context()); u != nil && u.Role == "owner" {
		empty.Actions = []ui.Button{{Label: "Create an entity type", Variant: "primary", Href: a.adminPath + "/settings/types/new"}}
	}
	v := a.NewView(w, r, "New entity")
	v.Data = struct {
		Choices []choice
		Empty   ui.Empty
	}{choices, empty}
	v.Width = "narrow"
	v.Crumbs = []Crumb{{Label: "Entities", Href: a.adminPath + "/entities"}, {Label: "New"}}
	a.Render(w, r, http.StatusOK, "entities/new", v)
}

// typeIcon is the glyph on a type's card: a fixed pick for the built-in types, a page for the rest.
func typeIcon(slug string) string {
	if slug == "post" {
		return "edit"
	}
	return "file-text"
}

// readEntityForm reads the posted entity form against the schema: the
// attributes (schema fields plus the SEO panel), and the plain fields.
type posted struct {
	f        *entityForm
	attrs    map[string]any
	errs     schemaform.Errors
	parentID string
	authorID string
	picked   map[string]bool
	termsSet bool
	// authorNote is set when the new author could not be created.
	authorNote string
}

func (a *Admin) readEntityForm(r *http.Request, sc *entity.EntitySchema, prev map[string]any, isNew bool) *posted {
	_ = r.ParseForm()
	p := &posted{picked: map[string]bool{}, f: &entityForm{
		New: isNew, Name: strings.TrimSpace(r.PostFormValue("name")), Slug: strings.TrimSpace(r.PostFormValue("slug")), Status: r.PostFormValue("status"),
		Schedule: strings.TrimSpace(r.PostFormValue("schedule")), SEO: seoFromForm(r), NewAuthor: strings.TrimSpace(r.PostFormValue("new_author")),
		NewTermName: strings.TrimSpace(r.PostFormValue("new_term_name")), NewTermKind: r.PostFormValue("new_term_kind"), NewTerms: newTermsFrom(r), Errors: map[string]string{},
	}}
	if p.f.Status == "" {
		p.f.Status = "draft" // a form that omits it (the API client of a script) keeps drafts as drafts
	}
	p.parentID, p.authorID = r.PostFormValue("parent"), r.PostFormValue("author")
	if _, err := uuid.Parse(r.PostFormValue("parent_orig")); err == nil {
		p.f.ParentOrig = r.PostFormValue("parent_orig")
	}
	p.termsSet = r.PostFormValue("_terms") == "1"
	for _, id := range r.PostForm["term"] {
		p.picked[id] = true
	}
	schema, err := schemaform.WithoutProperties(sc.Schema, seoKeys...)
	if err != nil {
		p.f.Errors["name"] = "This entity type's schema is invalid."
		return p
	}
	attrs, errs := schemaform.Decode(schema, r.PostForm, prev, schemaform.Options{Prefix: attrPrefix})
	p.attrs, p.errs = attrs, errs
	p.f.SEO.merge(p.attrs)
	return p
}

// validate fills p.f.Errors for the plain fields.
func (a *Admin) validateEntityForm(r *http.Request, p *posted, sc *entity.EntitySchema, current *entity.Entity) {
	f := p.f
	if f.Name == "" {
		f.Errors["name"] = "Enter a name"
	} else if len([]rune(f.Name)) > 255 {
		f.Errors["name"] = "Use at most 255 characters"
	}
	if _, err := entity.ParseStatus(f.Status); err != nil {
		f.Errors["status"] = "Choose a status"
		f.Status = "draft"
	}
	if f.Status == "scheduled" {
		if msg := a.scheduleError(r, f.Schedule, current); msg != "" {
			f.Errors["schedule"] = msg
		}
	}
	if f.SEO.Canonical != "" && !strings.HasPrefix(f.SEO.Canonical, "http://") && !strings.HasPrefix(f.SEO.Canonical, "https://") && !(strings.HasPrefix(f.SEO.Canonical, "/") && !strings.HasPrefix(f.SEO.Canonical, "//")) {
		f.Errors["seo_canonical"] = "Use a full address starting with https:// or a path starting with /"
	}
	if raw, err := json.Marshal(p.attrs); err == nil {
		if verrs := schemaform.Validate(sc.Schema, raw); len(verrs) > 0 {
			if p.errs == nil {
				p.errs = schemaform.Errors{}
			}
			for path, msgs := range verrs {
				switch path {
				case "meta_title":
					f.Errors["seo_title"] = msgs[0]
				case "meta_description":
					f.Errors["seo_description"] = msgs[0]
				case "canonical_url":
					f.Errors["seo_canonical"] = msgs[0]
				case "og_image":
					f.Errors["seo_og_image"] = msgs[0]
				case "no_index":
				default:
					if _, dup := p.errs[path]; !dup {
						p.errs[path] = msgs
					}
				}
			}
		}
	}
}

// scheduleError explains why the typed publish time cannot be used ("" when it
// can). A time that is already scheduled for exactly that moment stays valid
// even if it has just passed.
func (a *Admin) scheduleError(r *http.Request, typed string, current *entity.Entity) string {
	if typed == "" {
		return "Pick the date and time to publish"
	}
	loc := a.siteLocation(r.Context())
	at, err := time.ParseInLocation(scheduleLayout, typed, loc)
	switch {
	case err != nil:
		return "Enter a valid date and time"
	case at.Format(scheduleLayout) != typed:
		// ParseInLocation moves a time that does not exist (the hour skipped
		// when the clocks go forward) instead of failing.
		return "That time does not exist in " + loc.String() + " (the clocks change then). Pick another"
	case at.After(time.Now()):
		return ""
	case current != nil && current.Status == entity.StatusScheduled && current.PublishedAt != nil && current.PublishedAt.Equal(at):
		return ""
	}
	return "Pick a time in the future"
}

func (a *Admin) handleEntityCreate(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	if c.Entities == nil {
		a.unavailable(w, r, "Entities")
		return
	}
	sc := a.schemaBySlug(r, r.PostFormValue("type"))
	if sc == nil {
		a.Error(w, r, http.StatusBadRequest, "Unknown type", "Choose what kind of entity to create.")
		return
	}
	p := a.readEntityForm(r, sc, map[string]any{}, true)
	a.validateEntityForm(r, p, sc, nil)
	if schemaform.Structural(r.PostForm) || len(p.f.Errors) > 0 || len(p.errs) > 0 {
		status := http.StatusUnprocessableEntity
		if schemaform.Structural(r.PostForm) {
			status, p.errs = http.StatusOK, nil
			p.f.Errors = map[string]string{}
		}
		a.renderEntityForm(w, r, status, p.f, nil, sc, p.attrs, p.errs, p.parentID, p.authorID, p.picked)
		return
	}
	raw, _ := json.Marshal(p.attrs)
	in := &entity.CreateEntityInput{SchemaID: sc.ID, Name: p.f.Name, Slug: p.f.Slug, Attributes: raw}
	if p.f.Name != "" && schemaIsNested(sc) {
		if id, err := uuid.Parse(p.parentID); err == nil {
			in.ParentID = &id
		}
	}
	if b := a.resolveAuthor(r, sc, p); b.set {
		in.BylinePersonID, in.BylineOrg = b.person, b.org
	}
	created, err := c.Entities.CreateEntity(r.Context(), actorID(r), in)
	if err != nil {
		if a.entityFormError(r, p.f, err) {
			a.renderEntityForm(w, r, http.StatusUnprocessableEntity, p.f, nil, sc, p.attrs, p.errs, p.parentID, p.authorID, p.picked)
			return
		}
		a.serverError(w, r, "The entity could not be created", err)
		return
	}
	msg := "Entity created."
	if note := a.afterSave(r, p, created, sc, created.Status); note != "" {
		msg += " " + note
	}
	kind := "success"
	if strings.Contains(msg, "could not") {
		kind = "error"
	}
	a.SetFlash(w, r, kind, msg)
	Redirect(w, r, a.entityEditHref(usesVisualEditor(sc, created), created.ID.String()))
}

func (a *Admin) schemaBySlug(r *http.Request, slug string) *entity.EntitySchema {
	if a.deps.Content.Entities == nil {
		return nil
	}
	schemas, err := a.deps.Content.Entities.ListSchemas(r.Context())
	if err != nil {
		return nil
	}
	for _, s := range schemas {
		if s.Slug == slug {
			return s
		}
	}
	return nil
}

// bylineOrgValue is the author picker's value for the site organisation.
const bylineOrgValue = "org"

// bylineValue is the author picker value of an entity: the credited person's
// id, "org" for the site organisation, "" for nobody.
func bylineValue(e *entity.Entity) string {
	switch {
	case e.BylinePersonID != nil:
		return e.BylinePersonID.String()
	case e.BylineOrg:
		return bylineOrgValue
	}
	return ""
}

// bylineChoice is what the author picker (and the new-author name) asks for.
// set is false when the form leaves the byline alone.
type bylineChoice struct {
	set    bool
	person *uuid.UUID
	org    bool
}

// resolveAuthor reads the author picker. A person's own form has none, so a
// posted value is ignored there: the byline of a person is not theirs to edit.
func (a *Admin) resolveAuthor(r *http.Request, sc *entity.EntitySchema, p *posted) bylineChoice {
	c := a.deps.Content
	if c.Authors == nil || sc.Slug == person.SchemaSlug || (r.PostForm["author"] == nil && p.f.NewAuthor == "") {
		return bylineChoice{}
	}
	if p.f.NewAuthor != "" {
		b, err := c.Authors.Resolve(r.Context(), p.f.NewAuthor)
		if err != nil {
			a.logger().Error("create author failed", "err", err)
			p.authorNote = "The new author could not be created, so the author was left as it was."
			return bylineChoice{}
		}
		return bylineChoice{set: true, person: b.PersonID, org: b.Org}
	}
	if p.authorID == bylineOrgValue {
		return bylineChoice{set: true, org: true}
	}
	if id, err := uuid.Parse(p.authorID); err == nil {
		return bylineChoice{set: true, person: &id}
	}
	return bylineChoice{set: true} // cleared
}

// apply writes the choice into an update: a person or the organisation, or
// nobody (both columns cleared).
func (b bylineChoice) apply(in *entity.UpdateEntityInput) {
	switch {
	case !b.set:
	case b.person != nil:
		in.BylinePersonID = b.person
	case b.org:
		on := true
		in.BylineOrg = &on
	default:
		in.ClearAuthor = true
	}
}

// entityFormError maps a save error onto a field; false when it is not one the
// editor can fix.
func (a *Admin) entityFormError(r *http.Request, f *entityForm, err error) bool {
	switch {
	case errors.Is(err, entity.ErrSlugAlreadyExists):
		f.Errors["slug"] = "That slug is already used by another entity"
	case errors.Is(err, entity.ErrParentCycle), errors.Is(err, entity.ErrParentTooDeep), errors.Is(err, entity.ErrParentNotFound):
		f.Errors["parent"] = sentence(entityErrorText(err))
	case errors.Is(err, entity.ErrRejectedByPlugin):
		f.Errors["name"] = sentence(err.Error())
	case errors.Is(err, entity.ErrValidationFailed), errors.Is(err, entity.ErrInvalidDoc), errors.Is(err, entity.ErrPageSectionsRemoved):
		f.Errors["name"] = sentence(err.Error())
	default:
		return false
	}
	return true
}

// afterSave applies status, schedule and terms to a saved entity and returns
// a note for the flash message when something could not be done.
func (a *Admin) afterSave(r *http.Request, p *posted, e *entity.Entity, sc *entity.EntitySchema, before entity.EntityStatus) string {
	c := a.deps.Content
	ctx := r.Context()
	var notes []string
	if p.authorNote != "" {
		notes = append(notes, p.authorNote)
	}
	want := entity.EntityStatus(p.f.Status)
	switch {
	case sc.Slug == person.SchemaSlug:
		// The Public switch decides; the entity service refuses a status change.
	case want == entity.StatusScheduled:
		at, err := time.ParseInLocation(scheduleLayout, p.f.Schedule, a.siteLocation(ctx))
		if err == nil && (before != entity.StatusScheduled || e.PublishedAt == nil || !e.PublishedAt.Equal(at)) {
			if _, err := c.Entities.ScheduleEntity(ctx, e.ID, actorID(r), at); err != nil {
				notes = append(notes, "The status could not be set: "+entityErrorText(err))
			} else {
				a.auditEntity(r, auditEntitySchedule, e.ID, e, map[string]any{"schema": sc.Slug, "from": string(before), "to": string(want), "publish_at": at.UTC().Format(time.RFC3339)})
			}
		}
	case want != before && entity.ValidStatus(want):
		if _, err := c.Entities.UpdateEntityStatus(ctx, e.ID, actorID(r), want); err != nil {
			notes = append(notes, "The status could not be set: "+entityErrorText(err))
		} else {
			a.auditEntity(r, auditStatusAction(want), e.ID, e, map[string]any{"schema": sc.Slug, "from": string(before), "to": string(want)})
		}
	}
	if c.Terms != nil && p.termsSet {
		ids := make([]uuid.UUID, 0, len(p.picked))
		for raw := range p.picked {
			if id, err := uuid.Parse(raw); err == nil {
				ids = append(ids, id)
			}
		}
		created := p.f.NewTerms
		if name := p.f.NewTermName; name != "" {
			kind := p.f.NewTermKind
			if kind == "" {
				kind = taxonomy.KindTag
			}
			created = append(created, newTerm{Kind: kind, Name: name})
		}
		if len(created) > 0 {
			if u := userFrom(ctx); u == nil || u.Role != "owner" {
				notes = append(notes, "Only owners can create terms.")
				created = nil
			}
		}
		for _, nt := range created {
			t, err := c.Terms.CreateTerm(ctx, taxonomy.CreateTermInput{Kind: nt.Kind, Name: nt.Name})
			switch {
			case err == nil:
				ids = append(ids, t.ID)
			case errors.Is(err, taxonomy.ErrTermSlugConflict):
				notes = append(notes, "The term “"+nt.Name+"” already exists: tick it in the list.")
			default:
				notes = append(notes, "The new term could not be created.")
			}
		}
		if err := c.Terms.ReplaceEntityTerms(ctx, e.ID, ids); err != nil {
			a.logger().Error("replace entity terms failed", "err", err)
			notes = append(notes, "The terms could not be saved.")
		}
	}
	return strings.Join(notes, " ")
}

func (a *Admin) handleEntitySave(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	c := a.deps.Content
	prev := attrsMap(e.Attributes)
	p := a.readEntityForm(r, sc, prev, false)
	if r.PostFormValue("status") == "" {
		p.f.Status = string(e.Status)
	}
	p.f.ID, p.f.Version, p.f.Tabs = e.ID.String(), e.Version, a.entityTabs(e.ID.String(), "details")
	if usesVisualEditor(sc, e) {
		p.f.EditorURL = a.adminPath + "/entities/" + e.ID.String() + "/edit"
	}
	if schemaform.Structural(r.PostForm) {
		p.f.Errors = map[string]string{}
		a.renderEntityForm(w, r, http.StatusOK, p.f, e, sc, p.attrs, nil, p.parentID, p.authorID, p.picked)
		return
	}
	a.validateEntityForm(r, p, sc, e)
	if len(p.f.Errors) > 0 || len(p.errs) > 0 {
		a.renderEntityForm(w, r, http.StatusUnprocessableEntity, p.f, e, sc, p.attrs, p.errs, p.parentID, p.authorID, p.picked)
		return
	}
	raw, _ := json.Marshal(p.attrs)
	summary := "Updated in the admin"
	in := &entity.UpdateEntityInput{Name: &p.f.Name, Attributes: (*json.RawMessage)(&raw), ChangeSummary: &summary}
	if p.f.Slug != "" {
		in.Slug = &p.f.Slug
	}
	// Only a parent the editor actually changed is written: a form that posts
	// the parent it was opened with (even one the picker no longer lists) is a no-op.
	if schemaIsNested(sc) && r.PostForm["parent"] != nil && p.parentID != p.f.ParentOrig {
		if id, err := uuid.Parse(p.parentID); err == nil {
			in.ParentID = &id
		} else {
			in.ClearParent = true
		}
	}
	a.resolveAuthor(r, sc, p).apply(in)
	updated, err := c.Entities.UpdateEntity(r.Context(), e.ID, actorID(r), in)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
			return
		}
		if a.entityFormError(r, p.f, err) {
			a.renderEntityForm(w, r, http.StatusUnprocessableEntity, p.f, e, sc, p.attrs, p.errs, p.parentID, p.authorID, p.picked)
			return
		}
		a.serverError(w, r, "The entity could not be saved", err)
		return
	}
	updated.PublishedAt = e.PublishedAt
	a.auditEntity(r, auditEntityUpdate, e.ID, updated, map[string]any{"schema": sc.Slug})
	msg := "Saved."
	kind := "success"
	if note := a.afterSave(r, p, updated, sc, e.Status); note != "" {
		msg, kind = "Saved. "+note, "error"
	}
	a.SetFlash(w, r, kind, msg)
	Redirect(w, r, a.adminPath+"/entities/"+e.ID.String())
}
