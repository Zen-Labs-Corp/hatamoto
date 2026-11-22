package admin

import (
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/adminpagestate"
	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/admin/ui"
)

// SettingsTab is one page of a Settings section. Cap empty means every
// signed-in user, exactly as the route behind it is gated.
type SettingsTab struct {
	ID, Label, Href string
	Cap             authz.Action
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
	Cap                      authz.Action
	// Live marks a tab that answers on Final without a settingsLive entry
	// (module pages, which exist per Admin).
	Live bool
}

// tabLive reports whether the tab's page answers on its Final path.
func tabLive(td settingsTabDef) bool { return td.Live || settingsLive[td.ID] }

type settingsSectionDef struct {
	ID, Label, Icon, Desc, Group string
	Tabs                         []settingsTabDef
}

// settingsLive is the set of tab IDs whose page already answers on Final.
var settingsLive = map[string]bool{
	"types": true, "taxonomies": true, "users": true,
	"menus":     true,
	"redirects": true, "linking": true, "comments": true, "comment-blocklists": true, "commenters": true, "templates": true,
	"themes": true, "blocks": true, "colours": true, "fonts": true, "additional-css": true, "design-system": true, "presets": true, "parts": true,
	"import": true, "general": true, "branding": true, "export": true, "performance": true, "memory": true,
	"ai-overview": true, "ai-traffic": true, "ai-conversions": true, "ai-crawlers": true, "ai-google": true, "ai-content": true, "ai-referrals": true, "ai-health": true,
	"admin-address": true, "site-access": true, "versions": true, "api": true, "updates": true, "jobs": true, "hosting": true, "backups": true, "changes": true, "subscriptions": true, "deliveries": true, "email": true, "email-log": true,
	"seo-general": true, "seo-titles": true, "seo-social": true, "seo-schema": true, "seo-kg": true, "seo-ai-files": true, "seo-robots": true, "seo-ads-txt": true, "seo-verification": true, "seo-sitemap": true, "seo-breadcrumbs": true, "seo-indexnow": true, "seo-404": true,
	"privacy": true, "tracking": true, "contact-button": true, "theme-strings": true,
	"review": true, "user-sign-in": true, "locales": true, "locale-addresses": true, "locale-business": true, "search-replace": true,
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
	{ID: "general", Label: "General", Icon: "general", Desc: "Site identity and homepage.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "general", Label: "General", Final: "/settings/general", Cap: authz.ActionSettingsManage},
	}},
	{ID: "branding", Label: "Branding", Icon: "palette", Desc: "Admin name, logo, accent colour and sign-in page.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "branding", Label: "Branding", Final: "/settings/admin-branding", Cap: authz.ActionSettingsManage},
	}},
	{ID: "locales", Label: "Locales", Icon: "globe", Desc: "Languages, addresses, search engines and business details.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "locales", Label: "Locales", Final: "/settings/locales", Cap: authz.ActionSettingsManage},
		{ID: "locale-addresses", Label: "Addresses and search engines", Final: "/settings/locales/addresses", Cap: authz.ActionSettingsManage},
		{ID: "locale-business", Label: "Business details", Final: "/settings/locales/business", Cap: authz.ActionSettingsManage},
	}},
	{ID: "users", Label: "Users", Icon: "users", Desc: "Who can sign in and what they can do.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "users", Label: "Users", Final: "/settings/users", Cap: authz.ActionUserManage},
		{ID: "user-sign-in", Label: "Sign-in", Final: "/settings/users/sign-in", Cap: authz.ActionUserManage},
	}},
	{ID: "access-security", Label: "Access & security", Icon: "lock", Desc: "Coming soon, maintenance and password modes, and where you sign in.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "site-access", Label: "Site access", Final: "/settings/site-access", Cap: authz.ActionSettingsManage},
		{ID: "admin-address", Label: "Admin address", Final: "/settings/security/admin-address", Cap: authz.ActionSettingsManage},
	}},
	{ID: "email", Label: "Email", Icon: "mail", Desc: "Password resets, invitations and form notices.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "email", Label: "Email", Final: "/settings/email", Cap: authz.ActionSettingsManage},
		{ID: "email-log", Label: "Email log", Final: "/settings/email/log", Cap: authz.ActionSettingsManage},
	}},
	{ID: "privacy-tracking", Label: "Privacy & tracking", Icon: "shield", Desc: "Analytics, ad and chat tags, and cookie consent for your visitors.", Group: "Site", Tabs: []settingsTabDef{
		{ID: "tracking", Label: "Tracking", Final: "/settings/tracking", Cap: authz.ActionSettingsManage},
		{ID: "privacy", Label: "Cookie consent", Final: "/settings/privacy", Cap: authz.ActionSettingsManage},
	}},
	{ID: "entity-models", Label: "Entity models", Icon: "content-model", Desc: "The kinds of content your site holds and how it is grouped.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "types", Label: "Entity types", Final: "/settings/types", Cap: authz.ActionSchemaManage},
		{ID: "taxonomies", Label: "Taxonomies", Final: "/settings/taxonomies", Cap: authz.ActionTermManage},
		{ID: "review", Label: "Review", Final: "/settings/review", Cap: authz.ActionSettingsManage},
	}},
	{ID: "seo", Label: "SEO", Icon: "seo", Desc: "Titles, descriptions and the variables they use.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "seo-general", Label: "General", Final: "/settings/seo/general", Cap: authz.ActionSettingsManage},
		{ID: "seo-titles", Label: "Titles & meta", Final: "/settings/seo/titles", Cap: authz.ActionSettingsManage},
		{ID: "seo-social", Label: "Social", Final: "/settings/seo/social", Cap: authz.ActionSettingsManage},
		{ID: "seo-schema", Label: "Schema", Final: "/settings/seo/schema", Cap: authz.ActionSettingsManage},
		{ID: "seo-kg", Label: "Knowledge graph", Final: "/settings/seo/knowledge-graph", Cap: authz.ActionSettingsManage},
		{ID: "seo-ai-files", Label: "AI files", Final: "/settings/seo/ai-files", Cap: authz.ActionSettingsManage},
		{ID: "seo-robots", Label: "Robots", Final: "/settings/seo/robots", Cap: authz.ActionSettingsManage},
		{ID: "seo-ads-txt", Label: "ads.txt", Final: "/settings/seo/ads-txt", Cap: authz.ActionSettingsManage},
		{ID: "seo-verification", Label: "Verification", Final: "/settings/seo/verification", Cap: authz.ActionSettingsManage},
		{ID: "seo-sitemap", Label: "Sitemap", Final: "/settings/seo/sitemap", Cap: authz.ActionSettingsManage},
		{ID: "seo-breadcrumbs", Label: "Breadcrumbs", Final: "/settings/seo/breadcrumbs", Cap: authz.ActionSettingsManage},
		{ID: "seo-indexnow", Label: "IndexNow", Final: "/settings/seo/indexnow", Cap: authz.ActionSettingsManage},
		{ID: "seo-404", Label: "404 monitor", Final: "/settings/seo/404-monitor", Cap: authz.ActionSettingsManage},
	}},
	{ID: "redirects", Label: "Redirects", Icon: "corner-up-right", Desc: "Send old and changed addresses to the right page.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "redirects", Label: "Redirects", Final: "/settings/redirects"},
	}},
	{ID: "linking", Label: "Internal linking", Icon: "link", Desc: "Links between your pages.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "linking", Label: "Internal linking", Final: "/settings/linking"},
	}},
	{ID: "comments", Label: "Comments", Icon: "quote", Desc: "Turn comments on, set trust levels and limits, and manage blocklists and commenters.", Group: "Content", Tabs: []settingsTabDef{
		{ID: "comments", Label: "Comments", Final: "/settings/comments", Cap: authz.ActionCommentSettingsRead},
		{ID: "comment-blocklists", Label: "Blocklists", Final: "/settings/comments/blocklists", Cap: authz.ActionCommentBlocklistManage},
		{ID: "commenters", Label: "Commenters", Final: "/settings/comments/commenters", Cap: authz.ActionCommentRead},
	}},
	{ID: "integrations", Label: "Integrations", Icon: "key", Desc: "API keys for scripts and webhooks that send signed POSTs when content changes.", Group: "System", Tabs: []settingsTabDef{
		{ID: "api", Label: "API keys", Final: "/settings/api-keys", Cap: authz.ActionAPIKeyManage},
		{ID: "subscriptions", Label: "Webhooks", Final: "/settings/webhooks", Cap: authz.ActionWebhookManage},
		{ID: "deliveries", Label: "Delivery log", Final: "/settings/webhooks/deliveries", Cap: authz.ActionWebhookManage},
	}},
	{ID: "performance", Label: "Performance", Icon: "database", Desc: "Page cache, size limit and preload.", Group: "System", Tabs: []settingsTabDef{
		{ID: "performance", Label: "Performance", Final: "/settings/performance", Cap: authz.ActionSettingsManage},
		{ID: "memory", Label: "Memory", Final: "/settings/performance/memory", Cap: authz.ActionSettingsManage},
	}},
	{ID: "updates", Label: "Updates", Icon: "refresh", Desc: "Version, channel and automatic updates.", Group: "System", Tabs: []settingsTabDef{
		{ID: "updates", Label: "Updates", Final: "/settings/updates", Cap: authz.ActionSettingsManage},
	}},
	{ID: "jobs", Label: "Jobs", Icon: "refresh", Desc: "Scheduled jobs, when they last ran and when they run next.", Group: "System", Tabs: []settingsTabDef{
		{ID: "jobs", Label: "Jobs", Final: "/settings/jobs", Cap: authz.ActionSettingsManage},
	}},
	{ID: "hosting", Label: "Hosting", Icon: "layers", Desc: "Serve pages from this server or publish them as static files.", Group: "System", Tabs: []settingsTabDef{
		{ID: "hosting", Label: "Hosting", Final: "/settings/hosting", Cap: authz.ActionSettingsManage},
	}},
	{ID: "backups-recovery", Label: "Backups & recovery", Icon: "archive", Desc: "Download a full copy of your site, how many earlier versions are kept, and undo imports.", Group: "System", Tabs: []settingsTabDef{
		{ID: "backups", Label: "Backups", Final: "/settings/backups", Cap: authz.ActionSettingsManage},
		{ID: "versions", Label: "Versions", Final: "/settings/versions", Cap: authz.ActionSettingsManage},
		{ID: "changes", Label: "Changes", Final: "/settings/changes", Cap: authz.ActionSettingsManage},
	}},
	{ID: "tools", Label: "Tools", Icon: "search", Desc: "Bring a site in from WordPress, take yours out, and find and replace text across your content.", Group: "System", Tabs: []settingsTabDef{
		{ID: "import", Label: "WordPress import", Final: "/settings/import", Cap: authz.ActionImportRun},
		{ID: "export", Label: "Export", Final: "/settings/export", Cap: authz.ActionSettingsManage},
		{ID: "search-replace", Label: "Search and replace", Final: "/settings/search-replace", Cap: authz.ActionSettingsManage},
	}},
}

// themeRegistry is the Theme sidebar: one tab per section, the order of the
// owner's brief. Caps mirror the routes behind each page.
var themeRegistry = []settingsSectionDef{
	{ID: "themes", Label: "Themes", Icon: "palette", Desc: "Install, preview and activate the theme of your site.", Tabs: []settingsTabDef{
		{ID: "themes", Label: "Themes", Final: "/theme/themes", Cap: authz.ActionThemeManage},
	}},
	{ID: "colours", Label: "Colours and fonts", Icon: "sun", Desc: "Brand colours, fonts and corners, applied by the live theme to every page.", Tabs: []settingsTabDef{
		{ID: "colours", Label: "Colours and fonts", Final: "/theme/colours", Cap: authz.ActionThemeManage},
		{ID: "fonts", Label: "Fonts", Final: "/theme/fonts", Cap: authz.ActionThemeManage},
	}},
	{ID: "additional-css", Label: "Additional CSS", Icon: "code", Desc: "Your own CSS, loaded after the theme's.", Tabs: []settingsTabDef{
		{ID: "additional-css", Label: "Additional CSS", Final: "/theme/additional-css", Cap: authz.ActionThemeManage},
	}},
	{ID: "design-system", Label: "Design system", Icon: "layers", Desc: "Named colours, sizes and spacing that blocks use through their style settings.", Tabs: []settingsTabDef{
		{ID: "design-system", Label: "Design system", Final: "/theme/design-system", Cap: authz.ActionThemeManage},
	}},
	{ID: "presets", Label: "Style presets", Icon: "sparkles", Desc: "Ready-made looks for the elements of the builder.", Tabs: []settingsTabDef{
		{ID: "presets", Label: "Style presets", Final: "/theme/presets", Cap: authz.ActionThemeManage},
	}},
	{ID: "parts", Label: "Layout parts", Icon: "layout", Desc: "Choose the header, footer and page layouts of your site.", Tabs: []settingsTabDef{
		{ID: "parts", Label: "Layout parts", Final: "/theme/parts", Cap: authz.ActionThemeManage},
	}},
	{ID: "blocks", Label: "Blocks", Icon: "copy", Desc: "Reusable blocks you can place on many pages.", Tabs: []settingsTabDef{
		{ID: "blocks", Label: "Blocks", Final: "/theme/blocks", Cap: authz.ActionBuilderBlockManage},
	}},
	{ID: "templates", Label: "Templates", Icon: "file-text", Desc: "Saved layouts and sections to start a page from.", Tabs: []settingsTabDef{
		{ID: "templates", Label: "Templates", Final: "/theme/templates"},
	}},
	{ID: "menus", Label: "Menus", Icon: "menu", Desc: "The navigation menus of your site.", Tabs: []settingsTabDef{
		{ID: "menus", Label: "Menus", Final: "/theme/menus"},
	}},
	{ID: "theme-strings", Label: "Strings", Icon: "globe", Desc: "Translate the theme's own words for each locale.", Tabs: []settingsTabDef{
		{ID: "theme-strings", Label: "Strings", Final: "/theme/strings", Cap: authz.ActionThemeManage},
	}},
	{ID: "contact-button", Label: "Contact button", Icon: "send", Desc: "A floating button that opens WhatsApp, a call, a text or an email.", Tabs: []settingsTabDef{
		{ID: "contact-button", Label: "Contact button", Final: "/theme/contact-button", Cap: authz.ActionThemeManage},
	}},
}

var themeArea = &adminArea{ID: "theme", Label: "Theme", Entry: "/theme", Registry: themeRegistry}

func themeFor(prefix string, v viewer) []SettingsSection { return themeArea.forPrefix(prefix, v) }

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

// moduleSettingsArea is the Settings area with one section per module page
// appended after the core sections, in the System group, owners only. The core
// registry is copied, never modified: it is shared by every Admin.
func moduleSettingsArea(ps []adminpagestate.Page) *adminArea {
	reg := make([]settingsSectionDef, len(settingsRegistry), len(settingsRegistry)+len(ps))
	copy(reg, settingsRegistry)
	for _, p := range ps {
		icon := p.Icon
		if _, err := iconSVG(icon); err != nil {
			icon = "puzzle"
		}
		id := "module-" + p.ID
		reg = append(reg, settingsSectionDef{ID: id, Label: p.Label, Icon: icon, Desc: p.Desc, Group: "System", Tabs: []settingsTabDef{
			{ID: id, Label: p.Label, Final: "/settings/modules/" + p.ID, Cap: authz.ActionSettingsManage, Live: true},
		}})
	}
	return &adminArea{ID: settingsArea.ID, Label: settingsArea.Label, Entry: settingsArea.Entry, Registry: reg}
}

// areaOfSection is the area that owns section, or nil.
func areaOfSection(section string) *adminArea { return areaOfSectionIn(adminAreas, section) }

func areaOfSectionIn(areas []*adminArea, section string) *adminArea {
	for _, ar := range areas {
		for _, sd := range ar.Registry {
			if sd.ID == section {
				return ar
			}
		}
	}
	return nil
}

// areaOfTabIn is the area in areas that owns tab, or nil.
func areaOfTabIn(areas []*adminArea, tab string) *adminArea {
	for _, ar := range areas {
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
	"import-export": "tools", "security": "access-security", "search-replace": "tools",
	"api": "integrations", "webhooks": "integrations", "site-access": "access-security",
	"privacy": "privacy-tracking", "tracking": "privacy-tracking",
	"backups": "backups-recovery", "versions": "backups-recovery", "changes": "backups-recovery",
}

// settingsOldPages are the old section IDs that were a single page and are now
// a tab of a merged section: their entry URL and ?section= land on that tab, not
// on the first tab of the section that absorbed them.
var settingsOldPages = map[string]string{
	"security": "admin-address", "import-export": "import", "search-replace": "search-replace",
	"api": "api", "webhooks": "subscriptions", "site-access": "site-access",
	"privacy": "privacy", "tracking": "tracking",
	"backups": "backups", "versions": "versions", "changes": "changes",
}

// settingsMovedOut are the old Settings section IDs that became main-menu pages.
var settingsMovedOut = map[string]string{
	"forms": "/forms", "media": "/media", "spam": "/forms/spam", "submissions": "/forms/submissions",
}

// settingsTabHref is the URL of tab for the viewer, or "" when they cannot open it.
func settingsTabHref(prefix string, v viewer, tab string) string {
	for _, s := range settingsFor(prefix, v) {
		for _, t := range s.Tabs {
			if t.ID == tab {
				return t.Href
			}
		}
	}
	return ""
}

func settingsFor(prefix string, v viewer) []SettingsSection { return settingsArea.forPrefix(prefix, v) }

// forPrefix is the area's sidebar for the viewer under prefix: only the tabs the
// viewer may open, and only the sections that keep at least one. It builds fresh
// slices, so the shared registry is never touched.
func (ar *adminArea) forPrefix(prefix string, v viewer) []SettingsSection {
	var out []SettingsSection
	for _, sd := range ar.Registry {
		sec := SettingsSection{ID: sd.ID, Label: sd.Label, Icon: sd.Icon, Desc: sd.Desc, Group: sd.Group}
		for _, td := range sd.Tabs {
			if td.Cap != "" && !v.Can(td.Cap) {
				continue
			}
			href := td.Legacy
			if tabLive(td) {
				href = prefix + td.Final
			}
			if href == "" {
				continue
			}
			sec.Tabs = append(sec.Tabs, SettingsTab{ID: td.ID, Label: td.Label, Href: href, Cap: td.Cap})
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
	return areaLocateAmong(adminAreas, prefix, full)
}

// areaLocateAmong is areaLocateIn over the given areas.
func areaLocateAmong(areas []*adminArea, prefix, full string) (section, tab string) {
	path, ok := strings.CutPrefix(full, prefix)
	if !ok || (path != "" && path[0] != '/') {
		return "", ""
	}
	// An agent page is a Crawlers sub-page with no tab path of its own.
	if settingsLive["ai-crawlers"] && strings.HasPrefix(path, "/analytics/agents/") {
		return "ai-crawlers", "ai-crawlers"
	}
	// The longest Final wins, so a tab that lives below another tab's path
	// is not taken for a sub-page of it.
	best := 0
	for _, ar := range areas {
		for _, sd := range ar.Registry {
			for _, td := range sd.Tabs {
				if tabLive(td) && len(td.Final) > best && (path == td.Final || strings.HasPrefix(path, td.Final+"/")) {
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
	return areaCrumbsIn(a.areaSet(), a.adminPath, tab, tail...)
}

func areaCrumbsFor(prefix, tab string, tail ...Crumb) []Crumb {
	return areaCrumbsIn(adminAreas, prefix, tab, tail...)
}

func areaCrumbsIn(areas []*adminArea, prefix, tab string, tail ...Crumb) []Crumb {
	ar := areaOfTabIn(areas, tab)
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
					if t.Cap == "" {
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

// defaultAreaCrumbsIn gives an area sub-page that set no crumbs of its own the
// chain ending in its title. The tab's own page gets none.
func defaultAreaCrumbsIn(areas []*adminArea, prefix, path, section, tab, title string) []Crumb {
	ar := areaOfTabIn(areas, tab)
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
	return areaCrumbsIn(areas, prefix, tab, Crumb{Label: title})
}

// area is the area of the page's section, or nil.
func (v *View) area() *adminArea {
	if v.Section == "" || v.User == nil {
		return nil
	}
	if v.areas == nil {
		return areaOfSection(v.Section)
	}
	return areaOfSectionIn(v.areas, v.Section)
}

// AreaNav is the secondary sidebar of the signed-in user; the shell renders it
// around a page that set View.Section.
func (v *View) AreaNav() []SettingsSection {
	ar := v.area()
	if ar == nil {
		return nil
	}
	secs := ar.forPrefix(v.prefix(), v.viewer())
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
var themeMoved = map[string]string{"appearance": "themes", "menus": "menus", "menus-forms": "menus", "contact-button": "contact-button"}

// areaHref is the URL of the section of area ar that the viewer opens, or "" when
// they have no section in it. first is used when want is not visible.
func areaHref(prefix string, ar *adminArea, v viewer, want string) string {
	secs := ar.forPrefix(prefix, v)
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
// select (?section=). A Settings section that moved to Theme lands there;
// Forms moved to the main menu.
func (a *Admin) areaEntry(ar *adminArea) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := a.viewerOf(r.Context())
		want := r.URL.Query().Get("section")
		if ar == a.settingsArea() {
			if to, ok := themeMoved[want]; ok {
				if href := areaHref(a.adminPath, themeArea, v, to); href != "" {
					http.Redirect(w, r, href, http.StatusSeeOther)
					return
				}
			}
		}
		if to, ok := settingsMovedOut[want]; ok && ar == a.settingsArea() {
			http.Redirect(w, r, a.adminPath+to, http.StatusSeeOther)
			return
		}
		if tab, ok := settingsOldPages[want]; ok && ar == a.settingsArea() {
			if href := settingsTabHref(a.adminPath, v, tab); href != "" {
				http.Redirect(w, r, href, http.StatusSeeOther)
				return
			}
		}
		if alias, ok := settingsSectionAliases[want]; ok && ar == a.settingsArea() {
			want = alias
		}
		to := areaHref(a.adminPath, ar, v, want)
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
	authed.HandleFunc("GET /settings", a.areaEntry(a.settingsArea()))
	// Multi-tab sections whose ID is not already a tab URL, plus the old IDs.
	for _, id := range []string{
		"entity-models", "import-export", "security", "content-model", "seo", "team",
		"integrations", "access-security", "privacy-tracking", "backups-recovery", "tools",
	} {
		target := id
		if alias, ok := settingsSectionAliases[id]; ok {
			target = alias
		}
		authed.HandleFunc("GET /settings/"+id, func(w http.ResponseWriter, r *http.Request) {
			v := a.viewerOf(r.Context())
			if tab, ok := settingsOldPages[id]; ok {
				if href := settingsTabHref(a.adminPath, v, tab); href != "" {
					http.Redirect(w, r, href, http.StatusFound)
					return
				}
			}
			for _, s := range settingsFor(a.adminPath, v) {
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

// areaSet is the areas of this Admin: the shared ones, or with module pages the
// Settings area replaced by its per-Admin copy.
func (a *Admin) areaSet() []*adminArea {
	if a.areas == nil {
		return adminAreas
	}
	return a.areas
}

// settingsArea is this Admin's Settings area.
func (a *Admin) settingsArea() *adminArea {
	if a.settings == nil {
		return settingsArea
	}
	return a.settings
}
