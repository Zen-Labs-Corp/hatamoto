package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/urlpattern"
)

// buildPageView assembles the render.PageView for a resolved route: the
// route's page data (pageViewFromRoute) plus site-wide data (name, logo,
// navigation, footer, theme tokens) and the form definitions its
// contact_form sections need. A NotFound route yields a sectionless view that
// still carries the site chrome for the themed 404.
func (s *Server) buildPageView(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
	v := pageViewFromRoute(rr, s.publicBaseURL(r), requestPath(r))
	v.PageNum = min(listingPage(r), render.MaxQueryPage)
	v.Params = r.URL.Query()
	v.Submitted = r.URL.Query().Get("submitted") == "1"
	v.FormError = r.URL.Query().Get("form_error") == "1"

	site, err := s.buildSiteView(ctx)
	if err != nil {
		return nil, err
	}
	v.Site = site
	v.SEO = s.seoSettings(ctx)
	if err := s.fillView(ctx, r, rr, v); err != nil {
		return nil, err
	}
	s.applyLocale(ctx, rr, v, v.BaseURL)
	v.Forms = s.formsForSections(ctx, formSections(v.Sections, v.Doc))
	v.EntityLinks = s.entityLinkTargets(ctx, v)
	v.Uncacheable = new(bool)
	s.applyPluginDirectory(ctx, v)
	v.PagedLoop, v.LoopOutOfRange = new(bool), new(bool)
	v.Related = s.relatedFunc(ctx, v.EntityID, v.SchemaSlug, v.Uncacheable)
	v.Series = s.seriesFunc(ctx, v.SchemaSlug, v.Attrs, v.Uncacheable)
	if v.Entity != nil {
		v.PostsByAuthor = s.postsByAuthorFunc(ctx, v.Entity.Slug, v.Page.Name, v.PageNum, v.Uncacheable, v.PagedLoop, v.LoopOutOfRange)
	}
	return v, nil
}

// seoSettings returns the site's SEO templates from the cached store, or nil
// when there is no store. A read error serves the last good copy or the
// defaults (see seo.Store.Get) and is logged once.
func (s *Server) seoSettings(ctx context.Context) *seo.Settings {
	if s.seoStore == nil {
		return nil
	}
	set, err := s.seoStore.Get(ctx)
	if err != nil {
		s.seoLogOnce.Do(func() { s.logger.Warn("seo settings unavailable, using the last good copy or defaults", "error", err) })
	}
	return &set
}

// requestPath is the request path without a trailing slash ("/" stays "/").
func requestPath(r *http.Request) string {
	p := r.URL.Path
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// pageViewFromRoute maps a routeResult to the page-level part of a PageView.
// It touches no database; URLs come from the loaders' canonical
// output (the URL registry), never from string concatenation here.
func pageViewFromRoute(rr *routeResult, baseURL, path string) *render.PageView {
	v := &render.PageView{BaseURL: baseURL, Path: path}
	switch {
	case rr == nil || rr.NotFound:
	case rr.Page != nil:
		p := rr.Page
		v.Page = pageMeta(p.Meta)
		v.Breadcrumb = crumbs(p.Breadcrumb)
		v.JSONLD = append(jsonLDDocs(p.JSONLD), p.ExtraJSONLD...)
		v.AICrawlerComment = p.AICrawlerComment
		if e := p.Entity; e != nil {
			v.Page.Name = e.Name
			applyAttributeMeta(&v.Page, e.Attributes, baseURL)
			v.EntityID = e.ID.String()
			if e.Schema != nil {
				v.SchemaSlug = e.Schema.Slug
			}
			v.Sections, v.Doc = contentFromAttributes(e.Attributes, e.ID.String())
			v.Attrs = attrsForView(e.Attributes)
			v.PublishedAt, v.UpdatedAt = timeVal(e.PublishedAt), e.UpdatedAt
		}
	case rr.Archive != nil:
		v.Page = render.PageMeta{
			Name: rr.Archive.Title, Title: rr.Archive.Title, Description: rr.Archive.Description,
			CanonicalURL: rr.CanonicalURL,
		}
		v.SchemaSlug = rr.Archive.SchemaSlug
	}
	if v.Page.CanonicalURL == "" && rr != nil {
		v.Page.CanonicalURL = rr.CanonicalURL
	}
	return v
}

// attrsForView decodes stored attributes into a fresh map for the layout
// (unmarshalling into a new map is a deep copy, so a template cannot touch the
// stored value). page_sections and page_doc are dropped: they are already in
// PageView.Sections / PageView.Doc.
func attrsForView(raw json.RawMessage) map[string]any {
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	delete(m, "page_sections")
	delete(m, "page_doc")
	return m
}

// contentFromAttributes reads the page content, preferring attributes.page_doc.
// A v1-expressible doc feeds the legacy section pipeline (plugin filter, forms,
// links unchanged); a builder-only doc is returned as the doc. A missing doc
// falls back to page_sections (legacy pages the CLI has not converted yet). A
// stored doc that fails validation renders an empty body: the page_sections
// mirror is frozen and could show removed content (entityID names the page in
// the warning; empty for an unsaved preview).
func contentFromAttributes(attrs json.RawMessage, entityID string) ([]render.Section, *builder.Doc) {
	if doc := docFromAttributes(attrs, entityID); doc != nil {
		if secs, ok := builder.ToSections(doc); ok {
			out := make([]render.Section, 0, len(secs))
			for _, s := range secs {
				out = append(out, render.Section{ID: s.ID, Type: s.Type, Title: s.Title, Content: s.Content, IsVisible: s.IsVisible, SortOrder: s.SortOrder})
			}
			return out, nil
		}
		return nil, doc
	}
	if hasPageDoc(attrs) {
		return nil, nil
	}
	return sectionsFromAttributes(attrs), nil
}

// hasPageDoc reports whether attrs carries a non-null page_doc key.
func hasPageDoc(attrs json.RawMessage) bool {
	var w map[string]json.RawMessage
	if len(attrs) == 0 || json.Unmarshal(attrs, &w) != nil {
		return false
	}
	raw := w["page_doc"]
	return len(raw) != 0 && string(raw) != "null"
}

// docFromAttributes decodes and validates attributes.page_doc; nil when absent
// or invalid.
func docFromAttributes(attrs json.RawMessage, entityID string) *builder.Doc {
	// Exact key lookup: a struct tag would also match "Page_Doc".
	var w map[string]json.RawMessage
	if len(attrs) == 0 || json.Unmarshal(attrs, &w) != nil {
		return nil
	}
	raw := w["page_doc"]
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	doc, err := builder.Parse(raw)
	if err != nil {
		// The page renders an empty body: leave a trace for the operator.
		slog.Warn("stored page_doc is invalid, rendering an empty body", "entity_id", entityID, "error", err)
		return nil
	}
	return doc
}

// viewSections is the sections plus the component nodes of a doc as sections,
// for readers that look at component content only (forms, entity links).
func viewSections(sections []render.Section, doc *builder.Doc) []render.Section {
	if doc == nil {
		return sections
	}
	out := append([]render.Section(nil), sections...)
	for _, n := range builder.Components(doc) {
		typ, _ := n.P["type"].(string)
		title, _ := n.P["title"].(string)
		content, _ := n.P["content"].(map[string]any)
		out = append(out, render.Section{ID: n.ID, Type: typ, Title: title, Content: content})
	}
	return out
}

// formSections is viewSections plus one contact_form entry per native form
// widget of the doc, so formsForSections loads the forms of both.
func formSections(sections []render.Section, doc *builder.Doc) []render.Section {
	out := viewSections(sections, doc)
	for _, n := range builder.NodesOfKind(doc, "form") {
		if slug, _ := n.P["slug"].(string); slug != "" {
			out = append(out, render.Section{ID: n.ID, Type: "contact_form", Content: map[string]any{"form_slug": slug}})
		}
	}
	return out
}

// pluginDirectoryCacheAge caps the page cache of a page with a plugin
// directory, so a registry publish reaches visitors within minutes however long
// the configured page TTL is (directoryTTL + this + the browser max-age).
const pluginDirectoryCacheAge = 60 * time.Second

// applyPluginDirectory feeds v's directory sections and limits how long the
// page may be cached. v.Uncacheable must be set.
func (s *Server) applyPluginDirectory(ctx context.Context, v *render.PageView) {
	pd, td := s.directoriesFor(ctx, v.Sections, v.Doc)
	if pd == nil && td == nil {
		return
	}
	// The views are shared (cached): link on a copy. Detail pages are served
	// only for the directory on /plugins resp. /themes.
	if pd != nil {
		c := *pd
		c.Linked = v.Path == "/plugins"
		pd = &c
	}
	if td != nil {
		c := *td
		c.Linked = v.Path == "/themes"
		td = &c
	}
	v.PluginDirectory, v.ThemeDirectory = pd, td
	v.MaxCacheAge = pluginDirectoryCacheAge
	// An unavailable directory is served but not cached, so recovery shows on
	// the next request.
	*v.Uncacheable = pd != nil && pd.State == render.PluginDirectoryUnavailable ||
		td != nil && td.State == render.PluginDirectoryUnavailable
}

// directoriesFor returns the views of the directory kinds that sections or the
// doc's components include, nil for the others. Without any directory there is
// no registry fetch; with both there is still one.
func (s *Server) directoriesFor(ctx context.Context, sections []render.Section, doc *builder.Doc) (*render.PluginDirectoryView, *render.ThemeDirectoryView) {
	var wantPlugins, wantThemes bool
	for _, sec := range viewSections(sections, doc) {
		if sec.IsVisible != nil && !*sec.IsVisible {
			continue
		}
		switch sec.Type {
		case "plugin_directory":
			wantPlugins = true
		case "theme_directory":
			wantThemes = true
		}
	}
	if !wantPlugins && !wantThemes {
		return nil, nil
	}
	vs := s.pluginDirectory().Views(ctx)
	var pd *render.PluginDirectoryView
	var td *render.ThemeDirectoryView
	if wantPlugins {
		pd = &vs.plugins
	}
	if wantThemes {
		td = &vs.themes
	}
	return pd, td
}

// pluginDirectoryFor returns the plugin directory view when sections or the
// doc's components include a plugin_directory, and nil otherwise.
func (s *Server) pluginDirectoryFor(ctx context.Context, sections []render.Section, doc *builder.Doc) *render.PluginDirectoryView {
	pd, _ := s.directoriesFor(ctx, sections, doc)
	return pd
}

func timeVal(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// applyAttributeMeta lets the entity's own SEO attributes win over what the
// loader put in the meta: loadHomepage, unlike loadPublicPage, falls back to
// the entity name and drops no_index. The JSON API is untouched; this only
// shapes the rendered head.
func applyAttributeMeta(m *render.PageMeta, attrs json.RawMessage, baseURL string) {
	var a struct {
		MetaTitle    string `json:"meta_title"`
		MetaDesc     string `json:"meta_description"`
		CanonicalURL string `json:"canonical_url"`
		OGTitle      string `json:"og_title"`
		OGDesc       string `json:"og_description"`
		NoIndex      bool   `json:"no_index"`
		SEO          struct {
			Robots string `json:"robots"`
		} `json:"seo"`
	}
	if len(attrs) == 0 || json.Unmarshal(attrs, &a) != nil {
		return
	}
	if t := strings.TrimSpace(a.MetaTitle); t != "" {
		m.Title, m.TitleSet = t, true
	}
	m.DescriptionSet = strings.TrimSpace(a.MetaDesc) != ""
	if c := canonicalFromAttribute(a.CanonicalURL, baseURL); c != "" {
		m.CanonicalURL = c
	}
	if t := strings.TrimSpace(a.OGTitle); t != "" {
		m.OGTitle = t
	}
	if d := strings.TrimSpace(a.OGDesc); d != "" {
		m.OGDescription = d
	}
	if a.NoIndex || strings.Contains(strings.ToLower(a.SEO.Robots), "noindex") {
		m.NoIndex = true
	}
}

// canonicalFromAttribute turns the SEO panel's canonical address into the
// link's href: a full http(s) address as it is, a site path on the site's own
// origin. Anything else is ignored (the page keeps its own address).
func canonicalFromAttribute(raw, baseURL string) string {
	c := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(c, "https://"), strings.HasPrefix(c, "http://"):
		if u, err := url.Parse(c); err == nil && u.Host != "" {
			return c
		}
	case strings.HasPrefix(c, "/") && !strings.HasPrefix(c, "//") && !strings.ContainsAny(c, "\\ \r\n"):
		return strings.TrimRight(baseURL, "/") + c
	}
	return ""
}

func pageMeta(m PageMeta) render.PageMeta {
	return render.PageMeta{
		Title: m.Title, Description: m.Description, OGImage: m.OGImage,
		CanonicalURL: m.CanonicalURL, NoIndex: m.NoIndex,
	}
}

func crumbs(in []BreadcrumbItem) []render.Crumb {
	out := make([]render.Crumb, 0, len(in))
	for _, b := range in {
		out = append(out, render.Crumb{Name: b.Label, URL: b.Href})
	}
	return out
}

func jsonLDDocs(doc map[string]interface{}) []json.RawMessage {
	if len(doc) == 0 {
		return nil
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil
	}
	return []json.RawMessage{b}
}

// sectionsFromAttributes reads attributes.page_sections. Ordering, visibility
// and sanitising are the render pipeline's job; this only decodes, one item at
// a time, so a malformed sibling costs that section and not the page.
func sectionsFromAttributes(attrs json.RawMessage) []render.Section {
	secs := builder.SectionsFromJSON(attrs, false)
	if secs == nil {
		return nil
	}
	out := make([]render.Section, 0, len(secs))
	for _, s := range secs {
		out = append(out, render.Section{ID: s.ID, Type: s.Type, Title: s.Title, Content: s.Content, IsVisible: s.IsVisible, SortOrder: s.SortOrder})
	}
	return out
}

// buildSiteView loads the site-wide data every page shares.
func (s *Server) buildSiteView(ctx context.Context) (render.SiteView, error) {
	cfg, err := s.siteConfig.Get(ctx)
	if err != nil {
		return render.SiteView{}, fmt.Errorf("site config: %w", err)
	}
	theme := effectiveTheme(cfg)
	links := s.newNavLinks(ctx)
	site := render.SiteView{
		Name:        cfg.CompanyName,
		Tagline:     cfg.Tagline,
		LogoURL:     cfg.LogoURL,
		Tokens:      themeTokens(theme),
		SocialLinks: socialLinks(cfg.ThemeExtra),
		Nav:         s.headerNav(ctx, links),
		FooterNav:   s.menuNav(ctx, "footer", links),
		Language:    siteLanguage(cfg),
		Phone:       cfg.Phone,
		Hours:       cfg.BusinessHours,
		Address:     postalAddress(cfg.PostalAddress),
		OrgAddress:  orgAddressMode(cfg),
	}
	var oerr error
	if site.Org, oerr = orgView(cfg); oerr != nil {
		s.logger.Warn("render: invalid business profile dropped from the organization", "error", oerr)
	}
	site.LogoWidth, site.LogoHeight = s.logoSizes.size(ctx, site.Org.LogoObject, s.logoSearch(), site.LogoURL)
	site.Settings = siteSettings(cfg.ThemeExtra)
	var derr error
	if site.Design, derr = builder.ParseDesign(cfg.ThemeExtra); derr != nil {
		s.logger.Warn("render: invalid builder design settings dropped", "error", derr)
	}
	if _, ok := s.urlSnapshot(ctx).Config(feedPostSchema); ok {
		site.Feeds = []render.FeedLink{
			{Title: site.Name, Type: "application/rss+xml", Href: "/feed.xml"},
			{Title: site.Name + " (Atom)", Type: "application/atom+xml", Href: "/atom.xml"},
		}
	}
	if c := theme.HeaderCTA; c != nil && c.Text != "" && c.URL != "" {
		site.HeaderCTA = &render.Link{Label: c.Text, URL: c.URL}
	}
	return site, nil
}

// siteSettings decodes the theme_config blob (theme settings) for .Site.Settings.
// Nil when unset or malformed.
func siteSettings(raw json.RawMessage) map[string]any {
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// themeTokens maps the site theme onto inline --hm-* overrides. Values equal to
// the stock DefaultThemeConfig are dropped: they are what an untouched install
// (or an admin form saved without edits) carries, and emitting them would pin
// light colours over the stylesheet's dark-mode variant. The
// theme's own CSS supplies the defaults; only owner-chosen values go inline.
func themeTokens(t siteconfig.ThemeConfig) render.ThemeTokens {
	d := siteconfig.DefaultThemeConfig()
	set := func(v, def string) string {
		if strings.EqualFold(strings.TrimSpace(v), def) {
			return ""
		}
		return v
	}
	return render.ThemeTokens{
		Primary: set(t.Colors.Primary, d.Colors.Primary), Background: set(t.Colors.Background, d.Colors.Background), Text: set(t.Colors.Text, d.Colors.Text),
		Font: set(t.Typography.FontFamily, d.Typography.FontFamily), FontHeading: set(t.Typography.HeadingFamily, d.Typography.HeadingFamily),
		RadiusSm: set(t.BorderRadius.Sm, d.BorderRadius.Sm), RadiusMd: set(t.BorderRadius.Md, d.BorderRadius.Md), RadiusLg: set(t.BorderRadius.Lg, d.BorderRadius.Lg),
	}
}

// socialLinks reads the optional theme_config "socialLinks" object
// ({"twitter": "https://..."}) in name order.
func socialLinks(raw json.RawMessage) []render.Link {
	var doc struct {
		Links map[string]string `json:"socialLinks"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	names := make([]string, 0, len(doc.Links))
	for n, u := range doc.Links {
		if u != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	out := make([]render.Link, 0, len(names))
	for _, n := range names {
		out = append(out, render.Link{Label: n, URL: doc.Links[n]})
	}
	return out
}

// headerNav is the header menu, or the published-pages fallback the JSON site
// endpoint uses when no header menu exists. Entity links are built through the
// URL registry (see navLinks), not the JSON API's "/{slug}" shortcut.
func (s *Server) headerNav(ctx context.Context, links *navLinks) []render.NavItem {
	if nav := s.menuNav(ctx, "header", links); len(nav) > 0 {
		return nav
	}
	items := s.fallbackNavigationWith(ctx, links.page)
	out := make([]render.NavItem, 0, len(items))
	for _, it := range items {
		out = append(out, toRenderNav(it))
	}
	return out
}

// menuNav returns the menu at location as render nav items; a missing menu or
// a lookup error yields no items (the page still renders).
func (s *Server) menuNav(ctx context.Context, location string, links *navLinks) []render.NavItem {
	if s.menuService == nil {
		return nil
	}
	m, err := s.menuService.GetMenuByLocation(ctx, location)
	if err != nil || m == nil {
		return nil
	}
	full, err := s.menuService.GetMenuWithItems(ctx, m.ID)
	if err != nil || full == nil {
		return nil
	}
	return links.items(full.Items)
}

func toRenderNav(n NavigationItem) render.NavItem {
	out := render.NavItem{Label: n.Label, Href: n.Href, Target: n.Target}
	for _, c := range n.Children {
		out.Children = append(out.Children, toRenderNav(c))
	}
	return out
}

// formsForSections loads the active form definition behind every
// contact_form section (content.form_slug, legacy formSlug), keyed by slug. Unknown or inactive
// forms are left out; the section renders its "unavailable" note.
func (s *Server) formsForSections(ctx context.Context, sections []render.Section) map[string]render.FormView {
	var forms map[string]render.FormView
	if s.formService == nil {
		return nil
	}
	var captcha *captchaWidget // loaded on the first form that needs it
	for _, sec := range sections {
		if sec.Type != "contact_form" {
			continue
		}
		slug := contactFormSlug(sec.Content)
		if slug == "" {
			continue
		}
		if _, done := forms[slug]; done {
			continue
		}
		f, err := s.formService.GetFormBySlug(ctx, slug)
		if err != nil || f == nil || f.Status != form.StatusActive {
			continue
		}
		if forms == nil {
			forms = map[string]render.FormView{}
		}
		v := formView(f)
		if f.Settings.EnableCaptcha {
			if captcha == nil {
				captcha = s.captchaWidgetFor(ctx)
			}
			if captcha.provider != "" {
				v.Captcha, v.CaptchaProvider, v.CaptchaSiteKey = true, captcha.provider, captcha.siteKey
			}
		}
		forms[slug] = v
	}
	return forms
}

// captchaWidget is what a form needs to show the spam check; the zero value
// means no widget.
type captchaWidget struct{ provider, siteKey string }

// captchaWidgetFor returns the provider and site key to render, or the zero
// value when no provider is set up or its secret cannot be read: the server
// accepts such forms unchecked, so showing a widget would be a dead end.
func (s *Server) captchaWidgetFor(ctx context.Context) *captchaWidget {
	if s.spam == nil {
		return &captchaWidget{}
	}
	set, err := s.spam.Settings.Get(ctx)
	if err != nil || !s.spam.CaptchaUsable(set) || set.SiteKey == "" {
		return &captchaWidget{}
	}
	return &captchaWidget{provider: set.Provider, siteKey: set.SiteKey}
}

// contactFormSlug resolves a contact_form section's form the way the render
// pipeline does: canonical form_slug first, the legacy formSlug alias only
// when it is empty. Both may be stored (the editor keeps the old key).
func contactFormSlug(content map[string]any) string {
	if slug, _ := content["form_slug"].(string); slug != "" {
		return slug
	}
	slug, _ := content["formSlug"].(string)
	return slug
}

func formView(f *form.Form) render.FormView {
	fields := append([]form.FormField(nil), f.Fields...)
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Position < fields[j].Position })
	v := render.FormView{
		Slug: f.Slug, Name: f.Name,
		SubmitLabel: f.Settings.SubmitButtonText, SuccessNotice: f.Settings.SuccessMessage,
	}
	for _, fld := range fields {
		rf := render.FormField{
			Name: fld.Name, Label: fld.Label, Type: fld.Type, Required: fld.Required,
			Placeholder: fld.Placeholder, HelpText: fld.HelpText,
		}
		for _, o := range fld.Options {
			rf.Options = append(rf.Options, render.FormOption{Value: o.Value, Label: o.Label})
		}
		v.Fields = append(v.Fields, rf)
	}
	return v
}

// navLinks builds nav hrefs through the URL registry. The homepage entity
// links to "/".
type navLinks struct {
	snap   *entity.URLSnapshot
	homeID uuid.UUID
	// ctx and repo load the target of a menu item whose pattern needs more
	// than the slug (ancestors, publication date). repo may be nil.
	ctx  context.Context
	repo menuTargetRepo
	// decl is the live theme's menu declaration; the zero value resolves no
	// style, colour, badge or icon.
	decl render.MenuDecl
}

// menuTargetRepo is the slice of the public repository navLinks needs.
type menuTargetRepo interface {
	resolveRepo
	GetPublishedByID(ctx context.Context, id uuid.UUID) (*entity.Entity, error)
}

func (s *Server) newNavLinks(ctx context.Context) *navLinks {
	repo := entity.NewPostgresPublicRepository(s.db)
	l := &navLinks{snap: s.urlSnapshot(ctx), ctx: ctx, repo: repo}
	if s.renderEngine != nil {
		l.decl = s.renderEngine.MenuDecl()
	}
	if home, err := repo.GetHomepage(ctx); err == nil && home != nil {
		l.homeID = home.ID
	}
	return l
}

// page is the link for a published "page" entity; false leaves it out.
func (l *navLinks) page(p *entity.Entity) (string, bool) {
	return pageNavHref(l.ctx, l.snap, l.repo, l.homeID, p)
}

// items maps menu items; entity links resolve through the registry.
func (l *navLinks) items(in []*menu.MenuItem) []render.NavItem {
	out := make([]render.NavItem, 0, len(in))
	for _, it := range in {
		n := render.NavItem{Label: it.Label, Href: l.menuHref(it)}
		if it.LinkType == menu.LinkTypeNone {
			n.Href = "#"
		}
		n.NoLink = it.LinkType == menu.LinkTypeNone || n.Href == "#" || n.Href == ""
		if it.OpenInNewTab {
			n.Target = "_blank"
		}
		l.decorate(&n, it)
		n.Children = l.items(it.Children)
		if len(n.Children) == 0 {
			n.Children = nil
		}
		out = append(out, n)
	}
	return out
}

// decorate copies the item's classes and settings onto n. Preset ids the
// theme does not declare are ignored, so a theme switch never leaves a stale
// class or badge behind.
func (l *navLinks) decorate(n *render.NavItem, it *menu.MenuItem) {
	a := it.Attrs
	var classes []string
	if it.CSSClasses != nil {
		classes = strings.Fields(*it.CSSClasses)
	}
	for _, c := range []string{l.decl.ClassFor("style", a.Style), l.decl.ClassFor("color", a.Color)} {
		classes = append(classes, strings.Fields(c)...)
	}
	switch a.ShowOn {
	case "desktop", "mobile":
		n.Show = a.ShowOn
		classes = append(classes, "hm-show-"+a.ShowOn)
	}
	n.Class = strings.Join(classes, " ")
	n.ID, n.Title, n.AriaLabel = a.ID, a.Title, a.AriaLabel
	n.Rel = strings.Join(a.Rel, " ")
	for _, b := range l.decl.Badges {
		if a.Badge != "" && b.ID == a.Badge {
			n.Badge, n.BadgeClass = b.Label, b.Class
		}
	}
	for _, ic := range l.decl.Icons {
		if a.Icon != "" && ic.ID == a.Icon {
			n.Icon = ic.ID
		}
	}
}

func (l *navLinks) menuHref(it *menu.MenuItem) string {
	if it.URL != nil && *it.URL != "" || it.EntitySlug == nil || *it.EntitySlug == "" {
		return getMenuItemHref(it)
	}
	if it.EntityID != nil && *it.EntityID == l.homeID {
		return "/"
	}
	schema := "page"
	if it.EntitySchemaSlug != nil && *it.EntitySchemaSlug != "" {
		schema = *it.EntitySchemaSlug
	}
	if l.repo != nil && it.EntityID != nil && l.snap.NeedsEntityVars(schema) {
		if ent, err := l.repo.GetPublishedByID(l.ctx, *it.EntityID); err == nil && ent.Schema != nil {
			return relatedLocator(l.ctx, l.snap, l.repo, schema)(ent)
		}
	}
	if path, found, err := l.snap.BuildURL(schema, nil, urlpattern.Vars{Slug: *it.EntitySlug}); err == nil && found {
		return path
	}
	return getMenuItemHref(it)
}

// pageNavHref is the registry-built link for a published "page" entity.
func pageNavHref(ctx context.Context, snap *entity.URLSnapshot, repo resolveRepo, homeID uuid.UUID, p *entity.Entity) (string, bool) {
	if p.ID == homeID {
		return "/", true
	}
	path, found, err := snap.BuildURL("page", entity.FlattenAttributes(p.Attributes), snap.VarsFor(p, ancestorSlugs(ctx, snap, repo, p, "page")))
	if err != nil || !found {
		return "", false
	}
	return path, true
}

// siteLanguage is the BCP 47 site language, "en" when unset.
func siteLanguage(cfg *siteconfig.Config) string {
	if l := strings.TrimSpace(cfg.Language); l != "" {
		return l
	}
	return "en"
}

// orgView maps the site-wide business profile to the Organization extras. The
// three legacy contact columns are applied first so they stay the single source.
func orgView(cfg *siteconfig.Config) (render.OrgView, error) {
	p, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		return render.OrgView{}, err
	}
	p = bizprofile.FromLegacy(p, cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
	same := slices.Clone(p.SameAs)
	if u, err := url.Parse(p.Address.GBPURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && !slices.Contains(same, p.Address.GBPURL) {
		same = append(same, p.Address.GBPURL)
	}
	return render.OrgView{
		LegalName: p.Name, TaxID: p.TaxID, Fax: p.Fax, Email: p.Email, POBox: p.Address.POBox,
		SameAs: same, Rating: ratingView(p.Rating),
		Description: p.Description, AlternateName: p.AlternateName, Slogan: p.Slogan,
		FoundingDate: p.FoundingDate, EmployeesMin: p.EmployeesMin, EmployeesMax: p.EmployeesMax,
		VATID: p.VATID, LogoObject: p.LogoObject,
	}, nil
}

// orgAddressMode is the site-wide choice of the Organization address on locale
// pages. An unreadable profile gives "", the site-wide address everywhere.
func orgAddressMode(cfg *siteconfig.Config) string {
	p, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		return ""
	}
	return p.OrgAddress
}

func ratingView(r bizprofile.Rating) render.Rating {
	return render.Rating{Value: r.Value, Count: r.Count, Best: r.Best, Worst: r.Worst, CountType: r.CountType}
}

func contactPointViews(points []bizprofile.ContactPoint) []render.ContactPointView {
	var out []render.ContactPointView
	for _, c := range points {
		out = append(out, render.ContactPointView{
			Type: c.Type, Phone: c.Phone, Email: c.Email,
			Name: c.Name, AreaServed: c.AreaServed, AvailableLanguage: c.AvailableLanguage,
		})
	}
	return out
}

// localeNodeIDs maps every locale tag of set to the suffix of its
// LocalBusiness @id: the stored URL segment, else the lowercased tag. A
// fallback that equals another locale's segment would give two businesses the
// same @id, so it becomes "<tag>-default".
func localeNodeIDs(set *entity.LocaleSet) map[string]string {
	segments := make(map[string]bool)
	for _, tag := range set.Tags() {
		if seg, _ := set.Segment(tag); seg != "" {
			segments[seg] = true
		}
	}
	ids := make(map[string]string, len(segments))
	for _, tag := range set.Tags() {
		seg, _ := set.Segment(tag)
		if seg == "" {
			seg = strings.ToLower(tag)
			if segments[seg] {
				seg += "-default"
			}
		}
		ids[tag] = seg
	}
	return ids
}

// localeSummaries lists every public locale of a multi-locale set with the
// fields the Organization node uses, resolved from the cards whether or not a
// locale has an office. It is nil on a one-locale site or when the profiles
// cannot be read. Home is left empty: the caller knows the URL snapshot.
func localeSummaries(cfg *siteconfig.Config, set *entity.LocaleSet) ([]render.LocaleSummary, error) {
	if !set.Multi() {
		return nil, nil
	}
	site, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		return nil, err
	}
	site = bizprofile.FromLegacy(site, cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
	cards, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		return nil, err
	}
	var out []render.LocaleSummary
	for _, tag := range set.PublicTags() {
		var card *bizprofile.Profile
		var own bizprofile.Profile
		if c, ok := cards[tag]; ok {
			card, own = &c, c
		}
		res := bizprofile.ResolveContact(site, card)
		p := res.Profile
		out = append(out, render.LocaleSummary{
			Tag: tag, Segment: strings.TrimPrefix(set.Prefix(tag), "/"),
			CountryName: p.CountryName, Phone: own.Phone, GeoRegion: p.GeoRegion,
			Language: languageName(tag), PriceRange: p.PriceRange, Currency: p.Currency,
			ContactPoints: contactPointViews(own.ContactPoints), HasOffice: res.Office,
		})
	}
	return out, nil
}

// languageName is the English name of the language of a BCP 47 tag ("French"
// for "fr-CA"), or "" when the tag cannot be parsed or has no name.
func languageName(tag string) string {
	t, err := language.Parse(tag)
	if err != nil {
		return ""
	}
	base, _ := t.Base()
	return display.English.Languages().Name(base)
}

// localeBusinessView resolves the local business of loc for a page view, or nil
// when the locale prints none, and the locale's geo region, which does not
// depend on the office. A profile that cannot be read is logged and
// treated as no office, so the page keeps its Organization and WebSite nodes.
func (s *Server) localeBusinessView(ctx context.Context, set *entity.LocaleSet, loc string) (*render.BusinessView, string) {
	if s.siteConfig == nil {
		return nil, ""
	}
	cfg, err := s.siteConfig.Get(ctx)
	if err != nil {
		return nil, ""
	}
	b, err := localeBusiness(cfg, set, loc)
	if err != nil {
		s.logger.Warn("render: invalid business profile, no local business printed", "locale", loc, "error", err)
		return nil, ""
	}
	return b, localeGeoRegion(cfg, set, loc)
}

// localeGeoRegion is the geo region of locale loc: the card's own value, else
// the site-wide one. It is empty on a site with fewer than two locales, for a
// locale that is not configured and for an unreadable profile. A locale without
// an office or an address of its own still has one.
func localeGeoRegion(cfg *siteconfig.Config, set *entity.LocaleSet, loc string) string {
	if _, ok := set.Segment(loc); !ok || !set.Multi() {
		return ""
	}
	site, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		return ""
	}
	cards, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		return ""
	}
	if c, ok := cards[loc]; ok && c.GeoRegion != "" {
		return c.GeoRegion
	}
	return site.GeoRegion
}

// localeBusiness resolves the card of locale loc against the site-wide profile.
// It returns nil on a site with fewer than two locales, for a locale that is
// not configured, has no office, or has no street or city of its own.
func localeBusiness(cfg *siteconfig.Config, set *entity.LocaleSet, loc string) (*render.BusinessView, error) {
	if _, ok := set.Segment(loc); !ok || !set.Multi() {
		return nil, nil
	}
	seg := localeNodeIDs(set)[loc]
	site, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		return nil, err
	}
	site = bizprofile.FromLegacy(site, cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
	cards, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		return nil, err
	}
	var card *bizprofile.Profile
	if c, ok := cards[loc]; ok {
		card = &c
	}
	res := bizprofile.Resolve(site, card)
	if !res.Office {
		return nil, nil
	}
	p := res.Profile
	b := &render.BusinessView{
		ID: seg, Type: p.BusinessType, Name: p.Name,
		Phone: p.Phone, Fax: p.Fax, Email: p.Email, Hours: p.HoursNote, TaxID: p.TaxID,
		VATID: p.VATID, Description: p.Description, Image: p.Image, CountryName: p.CountryName,
		Address: render.Address{Street: p.Address.Street, City: p.Address.City, State: p.Address.State,
			PostalCode: p.Address.PostalCode, Country: p.Address.Country},
		POBox: p.Address.POBox, Lat: p.Address.Lat, Lng: p.Address.Lng,
		MapURL: p.Address.MapURL, GBPURL: p.Address.GBPURL,
		AreaServed: p.AreaServed, Currency: p.Currency, PriceRange: p.PriceRange, GeoRegion: p.GeoRegion,
		SameAs: p.SameAs, Rating: ratingView(p.Rating),
		ContactPoints: contactPointViews(p.ContactPoints),
	}
	for _, h := range p.OpeningHours {
		b.OpeningHours = append(b.OpeningHours, render.HoursView{Days: h.Days, Opens: h.Opens, Closes: h.Closes})
	}
	for _, h := range p.SpecialHours {
		b.SpecialHours = append(b.SpecialHours, render.SpecialHoursView{From: h.From, To: h.To, Closed: h.Closed, Opens: h.Opens, Closes: h.Closes})
	}
	return b, nil
}

// postalAddress decodes the site config's address JSON for the theme and the
// Organization JSON-LD. Zero when unset or malformed.
func postalAddress(raw json.RawMessage) render.Address {
	var a render.Address
	if len(raw) == 0 || json.Unmarshal(raw, &a) != nil {
		return render.Address{}
	}
	return a
}
