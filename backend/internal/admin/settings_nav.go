package admin

import (
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/admin/ui"
)

// SettingsTab is one page of a Settings section. Roles empty means every
// signed-in user, exactly as the route behind it is gated.
type SettingsTab struct {
	ID, Label, Href string
	Roles           []string
}

// SettingsSection is one entry of the Settings secondary sidebar. Href is the
// first tab the role may open.
type SettingsSection struct {
	ID, Label, Icon, Desc, Href string
	// Group is the sidebar heading the section sits under (Site, Content,
	// System); empty for areas that show no headings.
	Group string
	// Badge is a short count shown beside the label ("" shows none); AreaNav
	// fills it per request.
	Badge string
	Tabs  []SettingsTab
}

// settingsTabDef is a registry row. Final is the tab's URL below the admin
// prefix once its page has moved under /settings; Legacy is where it lives
// until then. A tab with
// no Legacy route is hidden until it is live. Each route-move todo adds its IDs
// to settingsLive.
type settingsTabDef struct {
	ID, Label, Final, Legacy string
	Roles                    []string
}

type settingsSectionDef struct {
	ID, Label, Icon, Desc, Group string
	Tabs                         []settingsTabDef
}

var ownerOnly = []string{"owner"}

// settingsLive is the set of tab IDs whose page already answers on Final.
var settingsLive = map[string]bool{
	"types": true, "taxonomies": true, "users": true,
	"menus": true, "forms": true, "submissions": true,
	"redirects": true, "linking": true, "templates": true,
	"themes": true, "blocks": true, "colours": true, "design-system": true, "presets": true, "parts": true,
	"import": true, "media": true, "general": true, "export": true, "performance": true,
	"ai-overview": true, "ai-traffic": true, "ai-conversions": true, "ai-crawlers": true, "ai-google": true, "ai-content": true, "ai-referrals": true, "ai-health": true,
	"admin-address": true, "api": true, "updates": true, "backups": true, "spam": true, "subscriptions": true, "deliveries": true, "email": true,
	"seo-general": true, "seo-titles": true, "seo-social": true, "seo-schema": true, "seo-kg": true,
	"locales": true, "locale-addresses": true, "locale-business": true,
}

// adminArea is a primary nav item with a secondary sidebar of sections and
// tabs: Settings, and Theme. The two share the sidebar, the section header and
// the breadcrumbs; section and tab IDs are unique across areas. Entry and the
// tabs' Final paths are relative to the admin prefix: the registries are shared
// by every Admin, so the prefix is joined in at use time.
type adminArea struct {
	ID, Label, Entry string
	Registry         []settingsSectionDef
}

var settingsRegistry = []settingsSectionDef{
	{ID: "general", Label: "General", Icon: "general", Desc: "Site identity, homepage and business details.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "general", Label: "General", Final: "/settings/general", Roles: ownerOnly},
	}},
	{ID: "locales", Label: "Locales", Icon: "globe", Desc: "Languages, addresses and search engines.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "locales", Label: "Locales", Final: "/settings/locales", Roles: ownerOnly},
		{ID: "locale-addresses", Label: "Addresses and search engines", Final: "/settings/locales/addresses", Roles: ownerOnly},
		{ID: "locale-business", Label: "Business details", Final: "/settings/locales/business", Roles: ownerOnly},
	}},
	{ID: "email", Label: "Email", Icon: "mail", Desc: "Password resets, invitations and form notices.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "email", Label: "Email", Final: "/settings/email", Roles: ownerOnly},
	}},
	{ID: "users", Label: "Users", Icon: "users", Desc: "Who can sign in and what they can do.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "users", Label: "Users", Final: "/settings/users", Roles: ownerOnly},
	}},
	{ID: "media", Label: "Media", Icon: "image", Desc: "Upload, browse and describe the files on your site.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "media", Label: "Media", Final: "/settings/media"},
	}},
	{ID: "entity-models", Label: "Entity models", Icon: "content-model", Desc: "The kinds of content your site holds and how it is grouped.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "types", Label: "Entity types", Final: "/settings/types", Roles: ownerOnly},
		{ID: "taxonomies", Label: "Taxonomies", Final: "/settings/taxonomies", Roles: ownerOnly},
	}},
	{ID: "forms", Label: "Forms", Icon: "inbox", Desc: "Forms on your site and what visitors send through them.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "forms", Label: "Forms", Final: "/settings/forms"},
		{ID: "submissions", Label: "Submissions", Final: "/settings/submissions"},
		{ID: "spam", Label: "Spam protection", Final: "/settings/spam", Roles: ownerOnly},
	}},
	{ID: "seo", Label: "SEO", Icon: "seo", Desc: "Titles, descriptions and the variables they use.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "seo-general", Label: "General", Final: "/settings/seo/general", Roles: ownerOnly},
		{ID: "seo-titles", Label: "Titles & meta", Final: "/settings/seo/titles", Roles: ownerOnly},
		{ID: "seo-social", Label: "Social", Final: "/settings/seo/social", Roles: ownerOnly},
		{ID: "seo-schema", Label: "Schema", Final: "/settings/seo/schema", Roles: ownerOnly},
		{ID: "seo-kg", Label: "Knowledge graph", Final: "/settings/seo/knowledge-graph", Roles: ownerOnly},
	}},
	{ID: "redirects", Label: "Redirects", Icon: "corner-up-right", Desc: "Send old and changed addresses to the right page.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "redirects", Label: "Redirects", Final: "/settings/redirects"},
	}},
	{ID: "linking", Label: "Internal linking", Icon: "link", Desc: "Links between your pages.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "linking", Label: "Internal linking", Final: "/settings/linking"},
	}},
	{ID: "api", Label: "API", Icon: "key", Desc: "Keys that let scripts and integrations use the API.", Group: "System", Tabs: []settingsTabDef{
		{ID: "api", Label: "API", Final: "/settings/api-keys", Roles: ownerOnly},
	}},
	{ID: "webhooks", Label: "Webhooks", Icon: "send", Desc: "Send signed POSTs to your systems when content changes.", Group: "System", Tabs: []settingsTabDef{
		{ID: "subscriptions", Label: "Subscriptions", Final: "/settings/webhooks", Roles: ownerOnly},
		{ID: "deliveries", Label: "Delivery log", Final: "/settings/webhooks/deliveries", Roles: ownerOnly},
	}},
	{ID: "performance", Label: "Performance", Icon: "database", Desc: "Page cache, size limit and preload.", Group: "System", Tabs: []settingsTabDef{
		{ID: "performance", Label: "Performance", Final: "/settings/performance", Roles: ownerOnly},
	}},
	{ID: "updates", Label: "Updates", Icon: "refresh", Desc: "Version, channel and automatic updates.", Group: "System", Tabs: []settingsTabDef{
		{ID: "updates", Label: "Updates", Final: "/settings/updates", Roles: ownerOnly},
	}},
	{ID: "backups", Label: "Backups", Icon: "archive", Desc: "Download a full copy of your site, database and files.", Group: "System", Tabs: []settingsTabDef{
		{ID: "backups", Label: "Backups", Final: "/settings/backups", Roles: ownerOnly},
	}},
	{ID: "import-export", Label: "Import & export", Icon: "import-export", Desc: "Bring a site in from WordPress or take yours out.", Group: "System", Tabs: []settingsTabDef{
		{ID: "import", Label: "WordPress import", Final: "/settings/import", Roles: ownerOnly},
		{ID: "export", Label: "Site export & import", Final: "/settings/export", Roles: ownerOnly},
	}},
	{ID: "security", Label: "Security", Icon: "shield-check", Desc: "Where you sign in and who can change it.", Group: "System", Tabs: []settingsTabDef{
		{ID: "admin-address", Label: "Admin address", Final: "/settings/security/admin-address", Roles: ownerOnly},
	}},
}

var ownerEditor = []string{"owner", "editor"}

// themeRegistry is the Theme sidebar: one tab per section, the order of the
// owner's brief. Roles mirror the routes behind each page.
var themeRegistry = []settingsSectionDef{
	{ID: "themes", Label: "Themes", Icon: "palette", Desc: "Install, preview and activate the theme of your site.", Tabs: []settingsTabDef{
		{ID: "themes", Label: "Themes", Final: "/theme/themes", Roles: ownerOnly},
	}},
	{ID: "colours", Label: "Colours and fonts", Icon: "sun", Desc: "Brand colours, fonts and corners.", Tabs: []settingsTabDef{
		{ID: "colours", Label: "Colours and fonts", Final: "/theme/colours", Roles: ownerOnly},
	}},
	{ID: "design-system", Label: "Design system", Icon: "layers", Desc: "Named colours, sizes and spacing that blocks use through their style settings.", Tabs: []settingsTabDef{
		{ID: "design-system", Label: "Design system", Final: "/theme/design-system", Roles: ownerOnly},
	}},
	{ID: "presets", Label: "Style presets", Icon: "sparkles", Desc: "Ready-made looks for the elements of the builder.", Tabs: []settingsTabDef{
		{ID: "presets", Label: "Style presets", Final: "/theme/presets", Roles: ownerOnly},
	}},
	{ID: "parts", Label: "Layout parts", Icon: "layout", Desc: "Choose the header, footer and page layouts of your site.", Tabs: []settingsTabDef{
		{ID: "parts", Label: "Layout parts", Final: "/theme/parts", Roles: ownerOnly},
	}},
	{ID: "blocks", Label: "Blocks", Icon: "copy", Desc: "Reusable blocks you can place on many pages.", Tabs: []settingsTabDef{
		{ID: "blocks", Label: "Blocks", Final: "/theme/blocks", Roles: ownerEditor},
	}},
	{ID: "templates", Label: "Templates", Icon: "file-text", Desc: "Saved layouts and sections to start a page from.", Tabs: []settingsTabDef{
		{ID: "templates", Label: "Templates", Final: "/theme/templates"},
	}},
	{ID: "menus", Label: "Menus", Icon: "menu", Desc: "The navigation menus of your site.", Tabs: []settingsTabDef{
		{ID: "menus", Label: "Menus", Final: "/theme/menus"},
	}},
}

var themeArea = &adminArea{ID: "theme", Label: "Theme", Entry: "/theme", Registry: themeRegistry}

func themeFor(prefix, role string) []SettingsSection { return themeArea.forPrefix(prefix, role) }

var settingsArea = &adminArea{ID: "settings", Label: "Settings", Entry: "/settings", Registry: settingsRegistry}

// analyticsRegistry is the Analytics sidebar: one tab per section, no role
// gate (the pages are open to every signed-in role).
var analyticsRegistry = []settingsSectionDef{
	{ID: "ai-overview", Label: "Overview", Icon: "analytics", Desc: "Are AI systems reading your site?", Tabs: []settingsTabDef{
		{ID: "ai-overview", Label: "Overview", Final: "/analytics"},
	}},
	{ID: "ai-traffic", Label: "Traffic", Icon: "globe", Desc: "Where do visitors come from?", Tabs: []settingsTabDef{
		{ID: "ai-traffic", Label: "Traffic", Final: "/analytics/traffic"},
	}},
	{ID: "ai-conversions", Label: "Conversions", Icon: "filter", Desc: "What turns into leads?", Tabs: []settingsTabDef{
		{ID: "ai-conversions", Label: "Conversions", Final: "/analytics/conversions"},
	}},
	{ID: "ai-content", Label: "Content", Icon: "file-text", Desc: "What are they reading?", Tabs: []settingsTabDef{
		{ID: "ai-content", Label: "Content", Final: "/analytics/content"},
	}},
	{ID: "ai-crawlers", Label: "Crawlers", Icon: "bot", Desc: "Who is reading your site?", Tabs: []settingsTabDef{
		{ID: "ai-crawlers", Label: "Crawlers", Final: "/analytics/crawlers"},
	}},
	{ID: "ai-google", Label: "Google", Icon: "search", Desc: "What is Google doing?", Tabs: []settingsTabDef{
		{ID: "ai-google", Label: "Google", Final: "/analytics/google"},
	}},
	{ID: "ai-referrals", Label: "Referrals", Icon: "link", Desc: "What comes back?", Tabs: []settingsTabDef{
		{ID: "ai-referrals", Label: "Referrals", Final: "/analytics/referrals"},
	}},
	{ID: "ai-health", Label: "Health", Icon: "shield-check", Desc: "Is it running cleanly?", Tabs: []settingsTabDef{
		{ID: "ai-health", Label: "Health", Final: "/analytics/health"},
	}},
}

var analyticsArea = &adminArea{ID: "analytics", Label: "Analytics", Entry: "/analytics", Registry: analyticsRegistry}

// adminAreas are the areas with a secondary sidebar.
var adminAreas = []*adminArea{settingsArea, themeArea, analyticsArea}

// areaOfSection is the area that owns section, or nil.
func areaOfSection(section string) *adminArea {
	for _, ar := range adminAreas {
		for _, sd := range ar.Registry {
			if sd.ID == section {
				return ar
			}
		}
	}
	return nil
}

// areaOfTab is the area that owns tab, or nil.
func areaOfTab(tab string) *adminArea {
	for _, ar := range adminAreas {
		for _, sd := range ar.Registry {
			for _, td := range sd.Tabs {
				if td.ID == tab {
					return ar
				}
			}
		}
	}
	return nil
}

// settingsSectionAliases are the section IDs before the v1-26 split; their
// <prefix>/settings/<id> entry URLs (and ?section=) still land on the new home.
var settingsSectionAliases = map[string]string{
	"content-model": "entity-models", "team": "users",
}

func settingsFor(prefix, role string) []SettingsSection { return settingsArea.forPrefix(prefix, role) }

// forPrefix is the area's sidebar for role under prefix: only the tabs the
// role may open, and only the sections that keep at least one. It builds fresh
// slices, so the shared registry is never touched.
func (ar *adminArea) forPrefix(prefix, role string) []SettingsSection {
	var out []SettingsSection
	for _, sd := range ar.Registry {
		sec := SettingsSection{ID: sd.ID, Label: sd.Label, Icon: sd.Icon, Desc: sd.Desc, Group: sd.Group}
		for _, td := range sd.Tabs {
			if len(td.Roles) > 0 && !contains(td.Roles, role) {
				continue
			}
			href := td.Legacy
			if settingsLive[td.ID] {
				href = prefix + td.Final
			}
			if href == "" {
				continue
			}
			sec.Tabs = append(sec.Tabs, SettingsTab{ID: td.ID, Label: td.Label, Href: href, Roles: td.Roles})
		}
		if len(sec.Tabs) == 0 {
			continue
		}
		sec.Href = sec.Tabs[0].Href
		out = append(out, sec)
	}
	return out
}

// areaLocateIn finds the live tab whose Final path is path or a parent of it on
// a "/" boundary, so a moved page (and its edit, confirm and error views)
// lands in its area's frame without each handler naming its section.
func areaLocateIn(prefix, full string) (section, tab string) {
	path, ok := strings.CutPrefix(full, prefix)
	if !ok || (path != "" && path[0] != '/') {
		return "", ""
	}
	// An agent page is a Crawlers sub-page with no tab path of its own.
	if settingsLive["ai-crawlers"] && strings.HasPrefix(path, "/analytics/agents/") {
		return "ai-crawlers", "ai-crawlers"
	}
	// The media detail and delete pages keep their <prefix>/media/{id} paths but
	// belong to the Settings library; the picker and upload are fragments.
	if settingsLive["media"] && strings.HasPrefix(path, "/media/") && path != "/media/picker" && path != "/media/upload" {
		return "media", "media"
	}
	// The longest Final wins, so a tab that lives below another tab's path
	// is not taken for a sub-page of it.
	best := 0
	for _, ar := range adminAreas {
		for _, sd := range ar.Registry {
			for _, td := range sd.Tabs {
				if settingsLive[td.ID] && len(td.Final) > best && (path == td.Final || strings.HasPrefix(path, td.Final+"/")) {
					section, tab, best = sd.ID, td.ID, len(td.Final)
				}
			}
		}
	}
	return section, tab
}

// areaCrumbs is the breadcrumb chain of an area sub-page under this instance's
// prefix: the area, the section, the tab, then tail. The section and tab
// collapse into one crumb when they share a label (General, Media, Blocks).
func (a *Admin) areaCrumbs(tab string, tail ...Crumb) []Crumb {
	return areaCrumbsFor(a.adminPath, tab, tail...)
}

func areaCrumbsFor(prefix, tab string, tail ...Crumb) []Crumb {
	ar := areaOfTab(tab)
	if ar == nil {
		return tail
	}
	out := []Crumb{{Label: ar.Label, Href: prefix + ar.Entry}}
	for _, sd := range ar.Registry {
		for _, td := range sd.Tabs {
			if td.ID != tab {
				continue
			}
			if sd.Label != td.Label {
				href := sd.Tabs[0].Final
				for _, t := range sd.Tabs {
					if len(t.Roles) == 0 {
						href = t.Final
						break
					}
				}
				out = append(out, Crumb{Label: sd.Label, Href: prefix + href})
			}
			out = append(out, Crumb{Label: td.Label, Href: prefix + td.Final})
		}
	}
	return append(out, tail...)
}

// defaultAreaCrumbs gives an area sub-page that set no crumbs of its own the
// chain ending in its title. The tab's own page gets none.
func defaultAreaCrumbs(prefix, path, section, tab, title string) []Crumb {
	ar := areaOfTab(tab)
	if section == "" || ar == nil {
		return nil
	}
	for _, sd := range ar.Registry {
		for _, td := range sd.Tabs {
			if td.ID == tab && path == prefix+td.Final {
				return nil
			}
		}
	}
	return areaCrumbsFor(prefix, tab, Crumb{Label: title})
}

// area is the area of the page's section, or nil.
func (v *View) area() *adminArea {
	if v.Section == "" || v.User == nil {
		return nil
	}
	return areaOfSection(v.Section)
}

// AreaNav is the secondary sidebar of the signed-in user; the shell renders it
// around a page that set View.Section.
func (v *View) AreaNav() []SettingsSection {
	ar := v.area()
	if ar == nil {
		return nil
	}
	secs := ar.forPrefix(v.prefix(), v.User.Role)
	if v.localeCount > 0 {
		for i := range secs {
			if secs[i].ID == "locales" {
				secs[i].Badge = strconv.Itoa(v.localeCount)
			}
		}
	}
	if len(v.AreaCarry) == 0 {
		return secs
	}
	// For builds fresh slices, so the registry is not touched.
	suffix := "?" + v.AreaCarry.Encode()
	for i := range secs {
		secs[i].Href += suffix
		for j := range secs[i].Tabs {
			secs[i].Tabs[j].Href += suffix
		}
	}
	return secs
}

// AreaID, AreaLabel and AreaEntry name the area of the sidebar.
func (v *View) AreaID() string    { return v.area().ID }
func (v *View) AreaLabel() string { return v.area().Label }
func (v *View) AreaEntry() string { return v.prefix() + v.area().Entry }

// CurrentSection is the section the page belongs to, if the role may see it.
func (v *View) CurrentSection() *SettingsSection {
	for _, s := range v.AreaNav() {
		if s.ID == v.Section {
			return &s
		}
	}
	return nil
}

// SectionTabs are the link tabs of the current section (none when it has one).
func (v *View) SectionTabs() []ui.Tab {
	s := v.CurrentSection()
	if s == nil || len(s.Tabs) < 2 {
		return nil
	}
	tabs := make([]ui.Tab, len(s.Tabs))
	for i, t := range s.Tabs {
		tabs[i] = ui.Tab{Label: t.Label, Href: t.Href, Active: t.ID == v.Tab}
	}
	return tabs
}

// themeMoved maps the Settings sections that moved to Theme to the Theme
// section that replaces them, so <prefix>/settings?section=appearance (and the
// no-JS select of an old page) still lands.
var themeMoved = map[string]string{"appearance": "themes", "menus": "menus", "menus-forms": "menus"}

// areaHref is the URL of the section of area ar that role opens, or "" when
// the role has no section in it. first is used when want is not visible.
func areaHref(prefix string, ar *adminArea, role, want string) string {
	secs := ar.forPrefix(prefix, role)
	if len(secs) == 0 {
		return ""
	}
	for _, s := range secs {
		if s.ID == want {
			return s.Href
		}
	}
	return secs[0].Href
}

func (a *Admin) forbidden(w http.ResponseWriter, r *http.Request) {
	a.Error(w, r, http.StatusForbidden, "Not allowed", "Your role does not have access to this page.")
}

// areaEntry is the entry point of an area for every signed-in role: it goes
// to the first section the role may reach, or the one chosen with the no-JS
// select (?section=). A Settings section that moved to Theme lands there.
func (a *Admin) areaEntry(ar *adminArea) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		role := userFrom(r.Context()).Role
		want := r.URL.Query().Get("section")
		if ar == settingsArea {
			if to, ok := themeMoved[want]; ok {
				if href := areaHref(a.adminPath, themeArea, role, to); href != "" {
					http.Redirect(w, r, href, http.StatusSeeOther)
					return
				}
			}
		}
		if alias, ok := settingsSectionAliases[want]; ok && ar == settingsArea {
			want = alias
		}
		to := areaHref(a.adminPath, ar, role, want)
		if to == "" {
			a.forbidden(w, r)
			return
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
}

// mountSettingsFrame registers the section entry points for every signed-in
// role: /settings, and each multi-tab section URL, which redirects to its
// first reachable tab.
func (a *Admin) mountSettingsFrame(authed *httpx.Group) {
	authed.HandleFunc("GET /settings", a.areaEntry(settingsArea))
	// Multi-tab sections whose ID is not already a tab URL, plus the old IDs.
	for _, id := range []string{"entity-models", "import-export", "security", "content-model", "seo", "team"} {
		target := id
		if alias, ok := settingsSectionAliases[id]; ok {
			target = alias
		}
		authed.HandleFunc("GET /settings/"+id, func(w http.ResponseWriter, r *http.Request) {
			for _, s := range settingsFor(a.adminPath, userFrom(r.Context()).Role) {
				if s.ID == target {
					http.Redirect(w, r, s.Href, http.StatusFound)
					return
				}
			}
			a.forbidden(w, r)
		})
	}
}

// mountThemeFrame registers /theme, the entry point of the Theme area. The
// POST /theme of the admin colour-scheme switch is a different route.
func (a *Admin) mountThemeFrame(authed *httpx.Group) {
	authed.HandleFunc("GET /theme", a.areaEntry(themeArea))
}
