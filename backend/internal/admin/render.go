package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/web"
)

// pageSet is one page template plus the shared layouts and partials, parsed
// into its own set so pages never collide on {{define "main"}}. src is parsed
// once and never executed (with embedded templates it is shared by every Admin
// in the process); each Admin clones it on first use and binds its own funcs,
// because html/template escapes a set in place the first time it runs.
type pageSet struct {
	src  *template.Template
	fm   template.FuncMap
	once sync.Once
	t    *template.Template
}

// tmpl returns this Admin's executable copy of the page set.
func (ps *pageSet) tmpl() *template.Template {
	ps.once.Do(func() {
		t, err := ps.src.Clone()
		if err != nil {
			// src is never executed, so Clone cannot fail.
			panic(fmt.Sprintf("admin: clone page set: %v", err))
		}
		ps.t = t.Funcs(ps.fm)
	})
	return ps.t
}

// embedded caches what build derives from the embedded admin tree (no template
// dir): it cannot change within a process, and parsing it is most of what New
// costs. The assets are keyed by admin path, the URL prefix they are built for.
var embedded struct {
	once   sync.Once
	pages  map[string]*template.Template
	err    error
	mu     sync.Mutex
	assets map[string]*Assets
}

// Crumb is one breadcrumb.
type Crumb = ui.Crumb

// View is the root template data of every page. Partials never read it
// implicitly: they take explicit dict arguments.
type View struct {
	Title    string
	Crumbs   []Crumb
	SiteName string
	// SiteURL is the public site the sidebar's "Open site" button opens.
	SiteURL string
	Lang    string
	Theme   string
	Path    string
	// adminPath is the admin URL prefix of the instance that built the view;
	// use prefix(), which falls back to the default for a hand-made View.
	adminPath string
	// speculation is the instance's prebuilt speculation rules JSON; empty for a hand-made View.
	speculation template.HTML
	// appVersion is the running build as the colophon shows it ("v1.4.2" or
	// "dev build"); read it with AppVersion.
	appVersion string
	Nonce      string
	CSRF       string
	Assets     *Assets
	Scripts    []string // extra vendored scripts, e.g. "sortablejs"
	// AreaCarry is the query the secondary sidebar's links and phone select
	// keep (the analytics range and compare switch); empty elsewhere.
	AreaCarry url.Values
	// Modules are the page's entry modules beyond "@hm/admin", declared by the
	// page template's "modules" block; Render fills them in. The head preloads
	// their static import closure.
	Modules []string
	Flash   *Flash
	User    *CurrentUser
	Nav     []NavEntry // flat; breadcrumbs read it, the sidebar uses NavGroups
	// Section and Tab place the page in the Settings frame (IDs from
	// settingsRegistry); empty means the page is not a Settings page.
	Section, Tab string
	// localeCount is the number of configured locales on a multi-locale site
	// (0 otherwise); the Locales section of the Settings sidebar shows it.
	localeCount int
	// pluginNav is the Plugins subnav, set by NewView on /admin/plugins* pages.
	pluginNav []PluginNavEntry
	// entityNav is the Entities subnav and entityManage whether it links to the
	// entity types settings (owner); renderEntities sets both on the list page.
	entityNav    []EntityNavEntry
	entityManage bool
	Wide         bool // bare layout: wider panel
	// Width is the page column in the shell: "narrow" (one readable form
	// column), "compact" (the same column, kept at that width inside settings),
	// "wide" (tables, grids, charts; the default) or "full"
	// (editors). Render normalises anything else to "wide".
	Width string
	// Refresh, in seconds, reloads the page without JavaScript (a live status
	// page); htmx pages poll instead.
	Refresh int
	// NoSiteAddress asks the owner to set the site address (emails need it).
	NoSiteAddress bool
	// SetupPending reminds the owner that the wizard was not finished.
	SetupPending bool
	// SkipMarkupHooks keeps the markup scan from adding modules: a page whose
	// markup carries hooks it must not load code for (the builder shell, whose
	// no-JS outline sits in <noscript>) declares what it needs itself.
	SkipMarkupHooks bool
	// UpdateBanner is the owner's update notice (nil when nothing needs
	// attention); NewView fills it for owner GET requests off the Updates page.
	UpdateBanner *UpdateBanner
	// AddressBanner nags the owner about a weak admin address nobody accepted
	// (nil otherwise); NewView fills it for owner GET requests off the Security pages.
	AddressBanner *AddressBanner
	// StatsBanner asks the owner of an existing install about install stats
	// (nil otherwise); NewView fills it for owner GET requests off the Updates page.
	StatsBanner *StatsBanner
	// DemoBanner is the public demo notice ("" outside demo mode).
	DemoBanner string
	Data       any // page-specific data
}

// NavGroups is the sidebar: Nav folded into titled groups.
func (v *View) NavGroups() []NavGroup { return GroupNav(v.Nav) }

// Preloads is the modulepreload list of the page: the static import closure of
// the admin entry plus the page's declared entry modules (see BootModules).
func (v *View) Preloads() []Preload {
	if v.Assets == nil {
		return nil
	}
	p, _ := v.Assets.Preloads(append([]string{"@hm/admin"}, v.Modules...))
	return p
}

// BootModules are the page's own screen modules (top-level "@hm/name"
// specifiers) as <script type="module"> tags. The nested editor modules
// ("@hm/editor/kit", "@hm/canvas/host") are only preloaded: the screen module
// imports them itself.
func (v *View) BootModules() []Preload {
	if v.Assets == nil {
		return nil
	}
	var out []Preload
	for _, m := range v.Modules {
		if strings.Contains(strings.TrimPrefix(m, "@hm/"), "/") {
			continue
		}
		if p, ok := v.Assets.Module(m); ok {
			out = append(out, p)
		}
	}
	return out
}

// pageModules runs the page template's optional "modules" block: a
// whitespace-separated list of import-map specifiers, e.g.
// {{define "modules"}}@hm/editor/kit @hm/editor/slash{{end}}.
func pageModules(t *template.Template, as *Assets) ([]string, error) {
	mt := t.Lookup("modules")
	if mt == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := mt.Execute(&buf, nil); err != nil {
		return nil, err
	}
	mods := strings.Fields(buf.String())
	if as != nil {
		if _, err := as.Preloads(mods); err != nil {
			return nil, err
		}
	}
	return mods, nil
}

// pageScripts runs the page template's optional "scripts" block: a
// whitespace-separated list of classic vendored scripts the page needs beyond
// htmx, e.g. {{define "scripts"}}sortablejs{{end}}.
func pageScripts(t *template.Template) ([]string, error) {
	st := t.Lookup("scripts")
	if st == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := st.Execute(&buf, nil); err != nil {
		return nil, err
	}
	return strings.Fields(buf.String()), nil
}

// markupHooks maps a data-hm-* hook found in a page's markup to the entry
// modules and vendored scripts that give it behaviour. Schemaform output can
// land on any page (entity editor, theme and plugin settings), so the needs
// are read from the rendered HTML instead of listed per page.
var markupHooks = []struct {
	marker  string
	modules []string
	scripts []string
}{
	{`data-hm-editor="tiptap"`, []string{"@hm/editors"}, nil},
	{`data-hm-editor="html"`, []string{"@hm/editors"}, nil},
	{`data-hm-sortable`, []string{"@hm/repeater"}, []string{"sortablejs"}},
	{`data-hm-repeater`, []string{"@hm/repeater"}, nil},
	{`data-hm-media-picker`, []string{"@hm/content"}, nil},
	{`data-hm-pluginframe`, []string{"@hm/pluginframe"}, nil},
}

// addMarkupNeeds adds the modules and scripts body's hooks need and reports
// whether it added any.
func (v *View) addMarkupNeeds(body string) bool {
	added := false
	for _, h := range markupHooks {
		if !strings.Contains(body, h.marker) {
			continue
		}
		for _, m := range h.modules {
			if !slices.Contains(v.Modules, m) {
				v.Modules, added = append(v.Modules, m), true
			}
		}
		for _, s := range h.scripts {
			if !slices.Contains(v.Scripts, s) {
				v.Scripts, added = append(v.Scripts, s), true
			}
		}
	}
	return added
}

// HTMXConfig is the content of <meta name="htmx-config">: nonces for what htmx
// injects, no eval, no script tags, no history snapshots (stale CSRF / secrets).
func (v *View) HTMXConfig() string {
	b, _ := json.Marshal(map[string]any{
		"includeIndicatorStyles": false,
		"inlineScriptNonce":      v.Nonce,
		"inlineStyleNonce":       v.Nonce,
		"allowEval":              false,
		"allowScriptTags":        false,
		"historyCacheSize":       0,
		// Validation errors come back as 422 and are swapped in place; other
		// 4xx/5xx are reported as toasts by the server (HX-Reswap: none).
		"responseHandling": []map[string]any{
			{"code": "204", "swap": false},
			{"code": "[23]..", "swap": true},
			{"code": "422", "swap": true, "error": false},
			{"code": "[45]..", "swap": false, "error": true},
		},
		"refreshOnHistoryMiss": true,
	})
	return string(b)
}

// funcMap is per instance because adminPath closes over the prefix.
func (a *Admin) funcMap() template.FuncMap {
	return template.FuncMap{
		// adminPath() is the prefix; adminPath "users" .ID joins parts under it.
		"adminPath":   func(parts ...string) string { return a.url(parts...) },
		"fmtDate":     fmtDate,
		"fmtDateTime": fmtDateTime,
		"fmtTime":     fmtTime,
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
		"icon":        iconSVG,
		"callKind":    calloutKind,
		"callIcon":    calloutIcon,
		"streq":       func(a any, b string) bool { s, _ := a.(string); return s == b },
		"required":    required,
		"describedby": describedBy,
		"attrs":       attrs,
		"attrmap": func(kv ...string) map[string]string {
			m := make(map[string]string, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i]] = kv[i+1]
			}
			return m
		},
		"crumbs": crumbList,
		"list":   func(v ...any) []any { return v },
		"add":    func(a, b int) int { return a + b },
		"sub":    func(a, b int) int { return a - b },
		"join":   strings.Join,
		"cond": func(c bool, a, b any) any {
			if c {
				return a
			}
			return b
		},
	}
}

func (a *Admin) build() error {
	as, err := a.buildAssets()
	if err != nil {
		return fmt.Errorf("admin assets: %w", err)
	}
	var srcs map[string]*template.Template
	if a.deps.Dir == "" {
		embedded.once.Do(func() { embedded.pages, embedded.err = parsePages(a.adminFS) })
		srcs, err = embedded.pages, embedded.err
	} else {
		srcs, err = parsePages(a.adminFS)
	}
	if err != nil {
		return err
	}
	fm := a.funcMap()
	pages := make(map[string]*pageSet, len(srcs))
	for key, src := range srcs {
		pages[key] = &pageSet{src: src, fm: fm}
	}
	a.mu.Lock()
	a.assets, a.pages, a.loaded = as, pages, true
	a.mu.Unlock()
	return nil
}

// buildAssets builds the asset bundle, once per admin path for the embedded tree.
func (a *Admin) buildAssets() (*Assets, error) {
	if a.deps.Dir != "" {
		return buildAssets(a.adminFS, web.Vendor(), a.adminPath)
	}
	embedded.mu.Lock()
	defer embedded.mu.Unlock()
	if as, ok := embedded.assets[a.adminPath]; ok {
		return as, nil
	}
	as, err := buildAssets(a.adminFS, web.Vendor(), a.adminPath)
	if err != nil {
		return nil, err
	}
	if embedded.assets == nil {
		embedded.assets = map[string]*Assets{}
	}
	embedded.assets[a.adminPath] = as
	return as, nil
}

// parsePages parses every page under pages/ with the shared layouts and
// partials. The shared files are parsed once and cloned into each page's set.
// The templates are parsed with placeholder funcs (only the names matter to the
// parser) and never executed here: pageSet.tmpl clones them and binds an
// Admin's real funcs.
func parsePages(fsys fs.FS) (map[string]*template.Template, error) {
	base := template.New("").Funcs((&Admin{}).funcMap())
	for _, dir := range []string{"layouts", "partials"} {
		names, err := fs.Glob(fsys, dir+"/*.html")
		if err != nil {
			return nil, err
		}
		sort.Strings(names)
		for _, n := range names {
			b, err := fs.ReadFile(fsys, n)
			if err != nil {
				return nil, err
			}
			if _, err := base.New(n).Parse(string(b)); err != nil {
				return nil, fmt.Errorf("admin template %s: %w", n, err)
			}
		}
	}
	var pageFiles []string
	err := fs.WalkDir(fsys, "pages", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".html") {
			pageFiles = append(pageFiles, p)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(pageFiles)
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, pf := range pageFiles {
		body, err := fs.ReadFile(fsys, pf)
		if err != nil {
			return nil, err
		}
		t, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := t.New("page").Parse(string(body)); err != nil {
			return nil, fmt.Errorf("admin template %s: %w", pf, err)
		}
		pages[strings.TrimSuffix(strings.TrimPrefix(pf, "pages/"), ".html")] = t
	}
	return pages, nil
}

// state returns the current assets and pages; with a template dir it rebuilds
// first so edits show without a restart.
func (a *Admin) state() (*Assets, map[string]*pageSet) {
	if a.deps.Dir != "" {
		if err := a.build(); err != nil {
			a.logger().Error("admin rebuild failed", "err", err)
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.assets, a.pages
}

// NewView builds the root view for a request: chrome data from the request
// context and the site config, and the flash message (consumed here).
func (a *Admin) NewView(w http.ResponseWriter, r *http.Request, title string) *View {
	as, _ := a.state()
	ctx := r.Context()
	v := &View{
		Title: title, SiteName: a.deps.SiteNameFallback, Lang: "en", Theme: themeOf(r), Path: r.URL.Path, adminPath: a.adminPath, speculation: a.speculation, appVersion: a.appVersion,
		Nonce: Nonce(ctx), CSRF: csrfToken(ctx), Assets: as, User: userFrom(ctx),
	}
	if a.deps.Demo.Enabled {
		v.DemoBanner = config.DemoBanner
	}
	if v.SiteName == "" {
		v.SiteName = "Hatamoto"
	}
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(ctx); err == nil && cfg != nil {
			if cfg.CompanyName != "" {
				v.SiteName = cfg.CompanyName
			}
			if len(cfg.Locales) >= 2 {
				v.localeCount = len(cfg.Locales)
			}
			// Installs from before the wizard have no site address; without it
			// reset and invitation emails are not sent.
			v.SetupPending = cfg.SetupCompletedAt == nil
			v.NoSiteAddress = cfg.SetupCompletedAt != nil && a.deps.BaseURL != nil && a.siteAddress(r) == ""
		}
	}
	v.SiteURL = "/"
	if addr := a.siteAddress(r); addr != "" {
		v.SiteURL = addr + "/"
	}
	v.Section, v.Tab = areaLocateIn(a.adminPath, r.URL.Path)
	if v.User != nil {
		v.Nav = a.nav.For(v.User.Role, r.URL.Path)
		if v.Section == "" && isPluginsPath(a.adminPath, r.URL.Path) {
			v.pluginNav = a.pluginsNavFor(v.User.Role, r.URL.Path)
		}
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		v.Flash = a.takeFlash(w, r)
		v.UpdateBanner = a.updateBanner(r, v.User)
		v.AddressBanner = a.addressBanner(r, v.User)
		v.StatsBanner = a.statsBanner(r, v.User, v.SetupPending)
	}
	return v
}

// Render writes page with status. Normal requests get the whole
// document; an htmx request targeting a region gets only the page's "main"
// block. A template error becomes a 500 page, never a half-written response.
func (a *Admin) Render(w http.ResponseWriter, r *http.Request, status int, page string, v *View) {
	as, pages := a.state()
	ps, ok := pages[page]
	if !ok {
		a.logger().Error("admin: unknown page", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(v.Crumbs) == 0 {
		v.Crumbs = defaultAreaCrumbs(a.adminPath, v.Path, v.Section, v.Tab, v.Title)
	}
	name := "page"
	if isFragment(r) {
		name = "main"
	}
	mods, err := pageModules(ps.tmpl(), as)
	if err != nil {
		a.logger().Error("admin: page modules", "page", page, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	v.Modules = mods
	scripts, err := pageScripts(ps.tmpl())
	if err != nil {
		a.logger().Error("admin: page scripts", "page", page, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	v.Scripts = append(v.Scripts, scripts...)
	v.Width = pageWidth(v.Width)
	var buf bytes.Buffer
	if err := ps.tmpl().ExecuteTemplate(&buf, name, v); err != nil {
		a.logger().Error("admin: render failed", "page", page, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if name == "page" && !v.SkipMarkupHooks && v.addMarkupNeeds(buf.String()) {
		// The markup carries hooks (schemaform fields) whose scripts the page
		// did not declare: the head is rendered again with them.
		buf.Reset()
		if err := ps.tmpl().ExecuteTemplate(&buf, name, v); err != nil {
			a.logger().Error("admin: render failed", "page", page, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	if path.Ext(r.URL.Path) == "" {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(buf.Bytes())
	}
}

// pageWidth maps a View.Width to one of the layout widths.
func pageWidth(w string) string {
	switch w {
	case "narrow", "compact", "full":
		return w
	}
	return "wide"
}

// RenderPartial executes one named partial (tests, and handlers that return a
// fragment such as a table row after an htmx action).
func (a *Admin) RenderPartial(name string, data any) (template.HTML, error) {
	_, pages := a.state()
	for _, ps := range pages {
		var buf bytes.Buffer
		if err := ps.tmpl().ExecuteTemplate(&buf, name, data); err != nil {
			return "", err
		}
		return template.HTML(buf.String()), nil //nolint:gosec // produced by html/template
	}
	return "", fmt.Errorf("admin: no page templates loaded")
}

// prefix is the admin URL prefix of the view.
func (v *View) prefix() string {
	if v.adminPath == "" {
		return DefaultAdminPath
	}
	return v.adminPath
}
