package admin

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
)

// The blog editor (v1-13) is the writing view of the post types (postEditorFor):
// an inline title, a Tiptap body (attributes.content), and a sidebar for
// everything around the text. It is one <form> posted to /admin/entities/{id}/edit,
// so without JavaScript the textarea body and the sidebar fields still save.
// js/blogeditor.js upgrades it: the rich editor, slash and bubble menus, image
// upload, background autosave of drafts (POST .../edit/autosave), the live
// SEO/AEO panel (POST .../edit/analyze, which answers the panel fragment) and a
// sandboxed preview (POST .../edit/preview, answering a one-time frame ticket URL).

// CrawlerStore reads AI-crawler activity for one entity (agents.StatsRepository).
type CrawlerStore interface {
	EntityActivity(ctx context.Context, entityID uuid.UUID, agentIDs []string, today time.Time) (agents.EntityActivityData, error)
}

func blogEditorFor(sc *entity.EntitySchema) bool { return postEditorFor(sc) }

func (a *Admin) mountBlogEditor(authed *httpx.Group) {
	authed.HandleFunc("POST /entities/{id}/edit/autosave", a.handleBlogAutosave)
	authed.HandleFunc("POST /entities/{id}/edit/analyze", a.handleBlogAnalyze)
	authed.HandleFunc("POST /entities/{id}/edit/links/suggest", a.handleBlogLinkSuggest)
	authed.HandleFunc("POST /entities/{id}/edit/links/insert", a.handleBlogLinkInsert)
	authed.HandleFunc("POST /entities/{id}/edit/recovery", a.handleBlogRecoveryPut)
	authed.HandleFunc("POST /entities/{id}/edit/recovery/discard", a.handleBlogRecoveryDiscard)
}

// maxLiveContent bounds the live html the link endpoints accept.
const maxLiveContent = 4 << 20

// linkSuggestionJSON is one row of the editor's link suggestions panel.
type linkSuggestionJSON struct {
	Target     string `json:"target"`
	TargetName string `json:"target_name"`
	Anchor     string `json:"anchor"`
	URL        string `json:"url"`
	Insertable bool   `json:"insertable"`
	Reason     string `json:"reason,omitempty"`
}

// linkSkipText says why a suggestion cannot be inserted, in the editor's words.
func linkSkipText(reason string) string {
	switch reason {
	case "":
		return ""
	case "already_linked":
		return "Already linked"
	case "no_match":
		return "Phrase not found"
	case "in_heading":
		return "Only in a heading"
	case "in_code":
		return "Only in code"
	case "excluded_schema", "not_linkable":
		return "Cannot be linked"
	}
	return strings.ReplaceAll(reason, "_", " ")
}

// blogLinkSource loads the post and reads the live content field for the link endpoints.
func (a *Admin) blogLinkSource(w http.ResponseWriter, r *http.Request) (*entity.Entity, string, LinkingStore, bool) {
	ls := a.deps.Growth.Linking
	if ls == nil {
		a.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Link suggestions are not available."})
		return nil, "", nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.writeJSON(w, http.StatusNotFound, map[string]string{"error": "Post not found."})
		return nil, "", nil, false
	}
	e, err := a.deps.Content.Entities.GetEntity(r.Context(), id)
	if err != nil || e == nil {
		a.writeJSON(w, http.StatusNotFound, map[string]string{"error": "Post not found."})
		return nil, "", nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLiveContent)
	if err := r.ParseForm(); err != nil {
		a.writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "The post is too large to scan."})
		return nil, "", nil, false
	}
	return e, r.PostFormValue("content"), ls, true
}

// handleBlogLinkSuggest lists the link rules that match the editor's live
// content (unsaved edits count), insertable ones first.
func (a *Admin) handleBlogLinkSuggest(w http.ResponseWriter, r *http.Request) {
	e, html, ls, ok := a.blogLinkSource(w, r)
	if !ok {
		return
	}
	list, err := ls.SuggestIn(r.Context(), e.ID, html)
	if err != nil {
		a.logger().Error("editor link suggestions failed", "err", err, "entity", e.ID)
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Link suggestions could not be loaded."})
		return
	}
	out := make([]linkSuggestionJSON, 0, len(list))
	paths := map[uuid.UUID]string{} // the target's public path, as the list shows it (parents included)
	for _, s := range list {
		if s.TargetEntityID == e.ID || s.SkipReason == "no_match" {
			continue // the phrase is not in this post: not worth a row
		}
		u, seen := paths[s.TargetEntityID]
		if !seen {
			u = s.URL
			if pp := a.deps.Content.PublicPath; pp != nil {
				if t, err := a.deps.Content.Entities.GetEntity(r.Context(), s.TargetEntityID); err == nil && t != nil {
					if pth := pp(r.Context(), t); pth != "" {
						u = pth
					}
				}
			}
			paths[s.TargetEntityID] = u
		}
		out = append(out, linkSuggestionJSON{Target: s.TargetEntityID.String(), TargetName: s.TargetName, Anchor: s.AnchorPhrase, URL: u, Insertable: s.Status == "insertable", Reason: linkSkipText(s.SkipReason)})
	}
	slices.SortStableFunc(out, func(x, y linkSuggestionJSON) int {
		if x.Insertable != y.Insertable {
			if x.Insertable {
				return -1
			}
			return 1
		}
		return 0
	})
	a.writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// handleBlogLinkInsert links one suggested phrase in the live content and
// answers the new html; the editor applies it and saves it with the post.
func (a *Admin) handleBlogLinkInsert(w http.ResponseWriter, r *http.Request) {
	_, html, ls, ok := a.blogLinkSource(w, r)
	if !ok {
		return
	}
	target, err := uuid.Parse(r.PostFormValue("target"))
	anchor := strings.TrimSpace(r.PostFormValue("anchor"))
	if err != nil || anchor == "" {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose a suggestion to insert."})
		return
	}
	out, res, err := ls.WrapIn(r.Context(), target, anchor, html)
	if err != nil {
		a.logger().Error("editor link insert failed", "err", err, "target", target)
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "The link could not be inserted."})
		return
	}
	if !res.Inserted {
		a.writeJSON(w, http.StatusOK, map[string]any{"inserted": false, "reason": linkSkipText(res.SkipReason)})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"inserted": true, "html": out})
}

type crawlerRow struct{ Name, Operator, Last, Status string }

// crawlerCard is "AI crawlers" for one published post.
type crawlerCard struct {
	Fetch7d, Fetch30d int64
	FirstCrawl        string
	Rows              []crawlerRow
	Tracked           bool
	Error             bool
}

type blogData struct {
	*entityForm
	Content, Excerpt, Featured, Keyword string
	InLLMs                              bool
	Panel                               seoPanel
	SlugAuto                            bool
	Published, CanPreview               bool
	Noun                                string // what the type is called: "post", "guide"
	Conflict, Notice                    string
	Crawlers                            *crawlerCard
	// RecoveryScript is the caller's recovery copy as a script element (""
	// when none); set on the editor GET only, never on a save re-render.
	RecoveryScript template.HTML
	// AsideTabs are the tabs of the settings aside; Translations is the panel
	// of a multi-locale site, nil otherwise.
	AsideTabs    []asideTab
	Translations *translationsPanelData
	SiteHost     string
	// The editor top bar.
	SchemaName, ListHref, VersionsHref, DetailsHref, ViewHref, UpdatedAt, EditedBy string
	StatusBadge                                                                    *ui.Badge
}

// asideTab is one tab of the blog editor's settings aside.
type asideTab struct {
	ID, Controls, Label string
	Active              bool
}

// ---- reading the form ----------------------------------------------------------

// blogBody is what the writing view owns in the attributes.
type blogBody struct {
	Content, Excerpt, Featured, Keyword string
	InLLMs                              bool
}

func blogBodyFrom(attrs map[string]any) blogBody {
	seo, _ := attrs["seo"].(map[string]any)
	in := true
	if v, ok := seo["include_in_llms_txt"].(bool); ok {
		in = v
	}
	return blogBody{Content: str(attrs, "content"), Excerpt: str(attrs, "excerpt"), Featured: str(attrs, "featured_image"), Keyword: str(seo, "focus_keyword"), InLLMs: in}
}

// mergeBlog writes the body fields into attrs; empty optional values are removed.
func (b blogBody) mergeBlog(attrs map[string]any) {
	attrs["content"] = b.Content
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			attrs[k] = v
		} else {
			delete(attrs, k)
		}
	}
	set("excerpt", b.Excerpt)
	set("featured_image", b.Featured)
}

// mergeSEO writes the focus keyword and the llms.txt choice into attributes.seo.
func (b blogBody) mergeSEO(attrs map[string]any) {
	seo, _ := attrs["seo"].(map[string]any)
	if seo == nil {
		seo = map[string]any{}
	}
	if k := strings.TrimSpace(b.Keyword); k != "" {
		seo["focus_keyword"] = k
	} else {
		delete(seo, "focus_keyword")
	}
	if b.InLLMs {
		delete(seo, "include_in_llms_txt") // listed unless switched off
	} else {
		seo["include_in_llms_txt"] = false
	}
	if len(seo) == 0 {
		delete(attrs, "seo")
	} else {
		attrs["seo"] = seo
	}
}

// readBlogForm decodes the posted writing view over the stored attributes (keys
// the view does not own are kept as they are).
func (a *Admin) readBlogForm(r *http.Request, prev map[string]any) (*posted, blogBody) {
	_ = r.ParseForm()
	p := &posted{picked: map[string]bool{}, attrs: prev, f: &entityForm{
		Name: strings.TrimSpace(r.PostFormValue("name")), Slug: strings.TrimSpace(r.PostFormValue("slug")), Status: r.PostFormValue("status"),
		Schedule: strings.TrimSpace(r.PostFormValue("schedule")), SEO: seoFromForm(r), NewAuthor: strings.TrimSpace(r.PostFormValue("new_author")),
		NewTermName: strings.TrimSpace(r.PostFormValue("new_term_name")), NewTermKind: r.PostFormValue("new_term_kind"), NewTerms: newTermsFrom(r), Errors: map[string]string{},
	}}
	p.authorID = r.PostFormValue("author")
	p.termsSet = r.PostFormValue("_terms") == "1"
	for _, id := range r.PostForm["term"] {
		p.picked[id] = true
	}
	// A field the form did not send keeps its stored value (a script posting
	// only the title must not wipe the body).
	b := blogBodyFrom(prev)
	field := func(name string, dst *string) {
		if _, ok := r.PostForm[name]; ok {
			*dst = r.PostFormValue(name)
		}
	}
	field("content", &b.Content)
	field("excerpt", &b.Excerpt)
	field("featured_image", &b.Featured)
	field("seo_keyword", &b.Keyword)
	if r.PostFormValue("_seo_llms") == "1" { // the form's marker: an unticked box sends nothing
		b.InLLMs = r.PostFormValue("seo_llms") == "true"
	}
	b.mergeBlog(p.attrs)
	b.mergeSEO(p.attrs)
	p.f.SEO.merge(p.attrs)
	return p, b
}

// ---- rendering -------------------------------------------------------------------

func slugifyName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// panelFor builds the SEO and AEO panel for the draft in p.
func (a *Admin) panelFor(r *http.Request, e *entity.Entity, sc *entity.EntitySchema, p *posted, b blogBody) seoPanel {
	in := seoInput{
		Title: p.f.Name, MetaTitle: p.f.SEO.Title, MetaDescription: p.f.SEO.Description, Slug: p.f.Slug, Excerpt: b.Excerpt, Content: b.Content,
		Keyword: b.Keyword, Canonical: p.f.SEO.Canonical, InLLMs: b.InLLMs,
		OGTitle: p.f.SEO.OGTitle, OGDescription: p.f.SEO.OGDescription, OGImage: p.f.SEO.OGImage, Social: true, Published: e.Status == entity.StatusPublished,
	}
	if a.deps.BaseURL != nil {
		in.BaseURL = a.deps.BaseURL(r)
	}
	if a.deps.Content.PublicPath != nil {
		ee := *e
		ee.Schema, ee.Name, ee.Slug = sc, p.f.Name, p.f.Slug
		in.Path = a.deps.Content.PublicPath(r.Context(), &ee)
	}
	if in.Path == "" {
		in.Path = "/" + p.f.Slug
	}
	a.withSEOSettings(r, &in, sc.Slug)
	if e != nil {
		a.markHome(r.Context(), &in, e.ID)
	}
	panel := buildPanel(in)
	panel.JSONLD, panel.JSONLDType = a.jsonLDPreview(e, sc, p, in)
	return panel
}

// jsonLDPreview builds the document with the generator the public page uses,
// including the FAQPage the FAQ blocks add.
func (a *Admin) jsonLDPreview(e *entity.Entity, sc *entity.EntitySchema, p *posted, in seoInput) (string, string) {
	raw, err := json.Marshal(p.attrs)
	if err != nil {
		return "", ""
	}
	ee := *e
	ee.Schema, ee.Name, ee.Slug, ee.Attributes = sc, p.f.Name, p.f.Slug, raw
	doc, err := entity.GenerateJSONLDWithFAQ(&ee, in.BaseURL, entity.ExtractFAQItems(in.Content))
	if err != nil || doc == nil {
		return "", ""
	}
	var types []string
	add := func(m map[string]any) {
		if t, ok := m["@type"].(string); ok {
			types = append(types, t)
		}
	}
	if graph, ok := doc["@graph"].([]map[string]any); ok {
		for _, g := range graph {
			add(g)
		}
	} else {
		add(doc)
	}
	pretty, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", ""
	}
	return string(pretty), strings.Join(types, " + ")
}

// crawlers is the AI-crawler card of a published post (nil otherwise).
func (a *Admin) crawlers(ctx context.Context, e *entity.Entity) *crawlerCard {
	c := a.deps.Content
	if c.Crawlers == nil || e.Status != entity.StatusPublished {
		return nil
	}
	reg := a.deps.Growth.AgentRegistry
	if reg == nil {
		reg = agents.Default()
	}
	var list []agents.Agent
	var ids []string
	for _, ag := range reg.Agents {
		if ag.Purpose.IsAI() && len(ag.UA) > 0 {
			list = append(list, ag)
			ids = append(ids, ag.ID)
		}
	}
	loc := a.siteLocation(ctx)
	d, err := c.Crawlers.EntityActivity(ctx, e.ID, ids, time.Now().UTC().Truncate(24*time.Hour))
	if err != nil {
		a.logger().Error("blog editor: crawler activity failed", "err", err)
		return &crawlerCard{Error: true}
	}
	card := &crawlerCard{Fetch7d: d.UserFetch7d, Fetch30d: d.UserFetch30d, Tracked: d.TrackingFrom != nil}
	if d.FirstAt != nil && d.PublishedAt != nil && d.TrackingFrom != nil && !d.PublishedAt.Before(*d.TrackingFrom) {
		card.FirstCrawl = fmtDateTime(d.FirstAt.In(loc))
	}
	for _, ag := range list {
		lf, ok := d.Last[ag.ID]
		if !ok {
			continue
		}
		row := crawlerRow{Name: ag.Name, Operator: ag.Operator, Last: fmtDateTime(lf.At.In(loc))}
		if lf.Status != nil {
			row.Status = strconv.Itoa(*lf.Status)
		}
		card.Rows = append(card.Rows, row)
	}
	sort.Slice(card.Rows, func(i, j int) bool { return card.Rows[i].Name < card.Rows[j].Name })
	return card
}

// buildBlog assembles the page's view data. version is the version the draft is
// based on.
func (a *Admin) buildBlog(r *http.Request, e *entity.Entity, sc *entity.EntitySchema, p *posted, b blogBody, version int) *blogData {
	return a.buildBlogView(r, e, sc, p, b, version, true)
}

// buildBlogView is buildBlog; full=false builds only what the panel fragment
// shows (the checks), skipping the author, term and crawler queries.
func (a *Admin) buildBlogView(r *http.Request, e *entity.Entity, sc *entity.EntitySchema, p *posted, b blogBody, version int, full bool) *blogData {
	f := p.f
	f.ID, f.Version = e.ID.String(), version
	if f.Status == "" {
		f.Status = string(e.Status)
	}
	if f.Schedule == "" && e.Status == entity.StatusScheduled && e.PublishedAt != nil {
		f.Schedule = e.PublishedAt.In(a.siteLocation(r.Context())).Format(scheduleLayout)
	}
	authorID := p.authorID
	if p.authorID == "" && r.Method == http.MethodGet {
		authorID = bylineValue(e)
	}
	if full {
		a.fill(r, f, sc, e, "", authorID, p.picked)
	}
	f.ShowParent = false
	d := &blogData{entityForm: f, Content: b.Content, Excerpt: b.Excerpt, Featured: b.Featured, Keyword: b.Keyword, InLLMs: b.InLLMs,
		Published: e.Status == entity.StatusPublished, CanPreview: a.deps.Content.Preview != nil}
	// Validation errors on attributes this view has no field for.
	paths := make([]string, 0, len(p.errs))
	for path := range p.errs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		d.Summary = append(d.Summary, ui.FieldError{ID: strings.ReplaceAll(path, ".", "-"), Label: schemaform.Humanize(path), Message: sentence(p.errs[path][0])})
	}
	d.Noun = draftNoun(sc)
	d.SchemaName, d.ListHref = sc.Name+"s", a.adminPath+"/entities?type="+url.QueryEscape(sc.Slug)
	d.VersionsHref, d.DetailsHref = a.adminPath+"/entities/"+f.ID+"/versions", a.adminPath+"/entities/"+f.ID
	d.StatusBadge = statusBadge(e.Status)
	if !e.UpdatedAt.IsZero() {
		d.UpdatedAt = fmtDateTime(e.UpdatedAt.In(a.siteLocation(r.Context())))
	}
	if e.UpdatedBy != nil && a.deps.Users != nil {
		if usr, err := a.deps.Users.GetByID(r.Context(), *e.UpdatedBy); err == nil {
			d.EditedBy = displayName(usr)
		}
	}
	d.SlugAuto = neverPublished(e) && (f.Slug == "" || strings.HasPrefix(f.Slug, "untitled-") || f.Slug == slugifyName(e.Name))
	d.Panel = a.panelFor(r, e, sc, p, b)
	if a.deps.BaseURL != nil {
		if u, err := url.Parse(a.deps.BaseURL(r)); err == nil {
			d.SiteHost = u.Hostname()
		}
	}
	if full {
		d.Crawlers = a.crawlers(r.Context(), e)
	}
	e.Schema = sc
	d.AsideTabs = []asideTab{{ID: "be-tab-post", Controls: "be-tabpanel-post", Label: "Post", Active: true}, {ID: "be-tab-seo", Controls: "be-tabpanel-seo", Label: "SEO and AEO"}}
	if set, ok := a.multiLocale(r.Context()); ok && full {
		if tp, err := a.translationsPanel(r.Context(), r, e, set); err != nil {
			a.logger().Error("translations panel failed", "entity", e.ID, "err", err)
			d.Translations = &translationsPanelData{Err: "The translations could not be loaded."}
			if r.Method != http.MethodPost {
				d.Translations.RetryURL = a.adminPath + "/entities/" + e.ID.String() + "/edit"
			}
			d.AsideTabs = append(d.AsideTabs, asideTab{ID: "be-tab-translations", Controls: "be-tabpanel-translations", Label: "Translations"})
		} else if tp != nil {
			d.Translations = tp
			d.AsideTabs = append(d.AsideTabs, asideTab{ID: "be-tab-translations", Controls: "be-tabpanel-translations", Label: "Translations"})
		}
	}
	if a.deps.Content.PublicPath != nil && e.Status == entity.StatusPublished {
		if pth := a.deps.Content.PublicPath(r.Context(), e); pth != "" {
			d.ViewHref = pth
		}
	}
	return d
}

func (a *Admin) renderBlog(w http.ResponseWriter, r *http.Request, status int, d *blogData) {
	for _, k := range []struct{ id, label string }{{"name", "Title"}, {"slug", "Slug"}, {"status", "Status"}, {"schedule", "Publish date"}, {"new_term_name", "New term"}, {"seo_title", "SEO title"}, {"seo_description", "SEO description"}, {"seo_canonical", "Canonical URL"}, {"seo_og_image", "Social image"}, {"seo_og_title", "Social title"}, {"seo_og_description", "Social description"}} {
		if m := d.Errors[k.id]; m != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	v := a.NewView(w, r, "Write "+d.Name)
	v.Data = d
	v.Width = "full"
	a.Render(w, r, status, "entities/blogedit", v)
}

// handleBlogEditor is GET /admin/entities/{id}/edit for post types.
func (a *Admin) handleBlogEditor(w http.ResponseWriter, r *http.Request, e *entity.Entity, sc *entity.EntitySchema) {
	attrs := attrsMap(e.Attributes)
	p := &posted{picked: map[string]bool{}, attrs: attrs, f: &entityForm{Name: e.Name, Slug: e.Slug, Status: string(e.Status), SEO: seoFrom(attrs), Errors: map[string]string{}}}
	if c := a.deps.Content; c.Terms != nil {
		if ts, err := c.Terms.ListTermsForEntity(r.Context(), e.ID); err == nil {
			for _, t := range ts {
				p.picked[t.ID.String()] = true
			}
		}
	}
	d := a.buildBlog(r, e, sc, p, blogBodyFrom(attrs), e.Version)
	d.RecoveryScript = a.blogRecoveryScript(r, e.ID)
	a.renderBlog(w, r, http.StatusOK, d)
}

// handleBlogSave is POST /admin/entities/{id}/edit for post types.
func (a *Admin) handleBlogSave(w http.ResponseWriter, r *http.Request, e *entity.Entity, sc *entity.EntitySchema) {
	c := a.deps.Content
	p, b := a.readBlogForm(r, attrsMap(e.Attributes))
	op := r.PostFormValue("_op")
	switch {
	case op == "publish":
		p.f.Status = string(entity.StatusPublished)
	case r.PostFormValue("status") == "":
		p.f.Status = string(e.Status)
	}
	posted, _ := strconv.Atoi(r.PostFormValue("version"))
	render := func(status int, conflict bool) {
		d := a.buildBlog(r, e, sc, p, b, posted)
		if conflict {
			d.Version = e.Version
			d.Conflict = staleMessage(posted, e.Version)
		}
		a.renderBlog(w, r, status, d)
	}
	mirrorTitle(sc, p.attrs, p.f.Name)
	a.validateEntityForm(r, p, sc, e)
	if len(p.f.Errors) > 0 || len(p.errs) > 0 {
		render(http.StatusUnprocessableEntity, false)
		return
	}
	if posted != e.Version {
		render(http.StatusConflict, true)
		return
	}
	raw, _ := json.Marshal(p.attrs)
	summary := "Updated in the blog editor"
	in := &entity.UpdateEntityInput{Name: &p.f.Name, Attributes: (*json.RawMessage)(&raw), ChangeSummary: &summary}
	if p.f.Slug != "" {
		in.Slug = &p.f.Slug
	}
	// A slug that follows the title keeps following it on a full save too, not only on
	// autosave: the field in the form may still show the address the draft was made with.
	if r.PostFormValue("slug_auto") == "1" && neverPublished(e) {
		if slug := slugifyName(p.f.Name); slug != "" {
			in.Slug = &slug
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
			render(http.StatusUnprocessableEntity, false)
			return
		}
		a.serverError(w, r, "The post could not be saved", err)
		return
	}
	updated.PublishedAt = e.PublishedAt
	a.dropRecovery(r.Context(), actorID(r), e.ID)
	a.auditEntity(r, auditEntityUpdate, e.ID, updated, map[string]any{"schema": sc.Slug})
	msg, kind := "Saved.", "success"
	switch {
	case e.Status == entity.StatusPublished && p.f.Status == string(entity.StatusPublished):
		msg = "Updated."
	case op == "publish" && e.Status != entity.StatusPublished:
		msg = "Saved and published."
	}
	if note := a.afterSave(r, p, updated, sc, e.Status); note != "" {
		msg, kind = "Saved. "+note, "error"
	}
	a.SetFlash(w, r, kind, msg)
	Redirect(w, r, a.adminPath+"/entities/"+e.ID.String()+"/edit")
}

// ---- autosave -----------------------------------------------------------------------

type autosaveReply struct {
	OK       bool   `json:"ok"`
	Version  int    `json:"version"`
	SavedAt  string `json:"saved_at,omitempty"`
	Conflict bool   `json:"conflict,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleBlogAutosave saves the text of a draft in the background: title, body,
// excerpt and featured image, checked against the version the browser holds
// (409 with the current version when somebody else saved). Published and
// scheduled posts are never touched here: they change only on "Update", and the
// server keeps a recovery copy per user instead (recovery_handlers.go).
func (a *Admin) handleBlogAutosave(w http.ResponseWriter, r *http.Request) {
	reply := func(status int, v autosaveReply) { a.writeJSON(w, status, v) }
	id, ok := uuidParam(r, "id")
	c := a.deps.Content
	if !ok || c.Entities == nil {
		reply(http.StatusNotFound, autosaveReply{Error: "This post does not exist."})
		return
	}
	e, err := c.Entities.GetEntity(r.Context(), id)
	if err != nil {
		reply(http.StatusNotFound, autosaveReply{Error: "This post does not exist."})
		return
	}
	sc := e.Schema
	if sc == nil {
		if sc, err = c.Entities.GetSchema(r.Context(), e.SchemaID); err != nil {
			reply(http.StatusInternalServerError, autosaveReply{Error: "The post type could not be loaded."})
			return
		}
	}
	if !blogEditorFor(sc) {
		reply(http.StatusNotFound, autosaveReply{Error: "This is not a post."})
		return
	}
	if e.Status != entity.StatusDraft {
		reply(http.StatusConflict, autosaveReply{Version: e.Version, Error: "Only drafts autosave. Use Update to save this post."})
		return
	}
	_ = r.ParseForm()
	if posted, _ := strconv.Atoi(r.PostFormValue("version")); posted != e.Version {
		reply(http.StatusConflict, autosaveReply{Conflict: true, Version: e.Version, Error: staleMessage(posted, e.Version)})
		return
	}
	attrs := attrsMap(e.Attributes)
	b := blogBodyFrom(attrs)
	// A field the request did not send keeps its stored value, as in readBlogForm.
	for name, dst := range map[string]*string{"content": &b.Content, "excerpt": &b.Excerpt, "featured_image": &b.Featured} {
		if _, ok := r.PostForm[name]; ok {
			*dst = r.PostFormValue(name)
		}
	}
	b.mergeBlog(attrs)
	name := strings.TrimSpace(r.PostFormValue("name"))
	if len([]rune(name)) <= 255 {
		mirrorTitle(sc, attrs, name)
	}
	raw, _ := json.Marshal(attrs)
	summary := entity.AutosaveSummary
	in := &entity.UpdateEntityInput{Attributes: (*json.RawMessage)(&raw), ChangeSummary: &summary, Autosave: true}
	if name != "" && len([]rune(name)) <= 255 {
		in.Name = &name
		// A slug that follows the title keeps following it: the form says so
		// until the writer edits the slug by hand.
		if r.PostFormValue("slug_auto") == "1" && neverPublished(e) {
			if slug := slugifyName(name); slug != "" {
				in.Slug = &slug
			}
		}
	}
	// Autosave is not audited: it runs on every pause in typing. Only the explicit
	// save in handleBlogSave writes an audit row.
	updated, err := c.Entities.UpdateEntity(r.Context(), e.ID, actorID(r), in)
	if err != nil {
		if errors.Is(err, entity.ErrSlugAlreadyExists) && in.Slug != nil {
			// The title's slug is taken: save the text without it.
			in.Slug = nil
			updated, err = c.Entities.UpdateEntity(r.Context(), e.ID, actorID(r), in)
		}
	}
	if err != nil {
		if errors.Is(err, entity.ErrValidationFailed) || errors.Is(err, entity.ErrRejectedByPlugin) {
			reply(http.StatusUnprocessableEntity, autosaveReply{Version: e.Version, Error: sentence(err.Error())})
			return
		}
		a.logger().Error("blog autosave failed", "err", err)
		reply(http.StatusInternalServerError, autosaveReply{Version: e.Version, Error: "The draft could not be saved."})
		return
	}
	reply(http.StatusOK, autosaveReply{OK: true, Version: updated.Version, SavedAt: time.Now().UTC().Format(time.RFC3339)})
}

// handleBlogAnalyze answers the SEO and AEO panel for the draft as posted.
func (a *Admin) handleBlogAnalyze(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	if !blogEditorFor(sc) {
		a.notFound(w, r, "Post", a.adminPath+"/entities", "Back to entities")
		return
	}
	p, b := a.readBlogForm(r, attrsMap(e.Attributes))
	d := a.buildBlogView(r, e, sc, p, b, e.Version, false)
	v := a.NewView(w, r, "Search and answer checks")
	v.Data = d
	a.Render(w, r, http.StatusOK, "entities/seopanel", v)
}

// blogPreviewRequest is the render-preview body for the writing view's draft.
func (a *Admin) blogPreviewRequest(r *http.Request, e *entity.Entity, sc *entity.EntitySchema) ([]byte, error) {
	p, _ := a.readBlogForm(r, attrsMap(e.Attributes))
	attrs := p.attrs
	attrs["name"] = p.f.Name
	raw, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	// The draft is sanitised like stored content, so the preview shows what the
	// public page would and runs nothing the sanitiser would drop.
	if raw, err = entity.SanitizeAttributesAgainst(raw, nil); err != nil {
		return nil, err
	}
	req := map[string]any{"entity_id": e.ID.String(), "schema": sc.Slug, "attributes": json.RawMessage(raw)}
	if a.deps.Content.PublicPath != nil && e.Status == entity.StatusPublished {
		e.Schema = sc
		if pth := a.deps.Content.PublicPath(r.Context(), e); pth != "" {
			req["path"] = pth
		}
	}
	return json.Marshal(req)
}

// neverPublished is true while an address may still follow the title: a live or
// once-live entity keeps its slug, since changing it breaks links.
func neverPublished(e *entity.Entity) bool {
	return e.Status == entity.StatusDraft && e.PublishedAt == nil
}
