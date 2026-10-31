package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// mountThemeBrowse registers the theme Browse list and its detail page (owner
// only). The detail page is /theme/themes/<name>/about, beside activate and
// settings, for the same mux-ambiguity reason as the plugin one.
func (a *Admin) mountThemeBrowse(owner *httpx.Group) {
	owner.HandleFunc("GET /theme/browse", a.handleThemeBrowse)
	owner.HandleFunc("GET /theme/themes/{name}/about", a.handleThemeAbout)
}

// themeFlagLabels names the theme flags: Feature is the filter label, Works
// the "Works with" text of the detail page.
var themeFlagLabels = map[string]struct{ Feature, Works string }{
	"dark":    {"Dark mode", "Dark mode"},
	"nojs":    {"Works without JS", "Works without JavaScript"},
	"builder": {"Visual builder", "Visual builder"},
}

// themeCardView is one theme card of the Browse grid.
type themeCardView struct {
	Name, Title, Author, Description, Href, Initial string
	Screenshot                                      string
	FirstParty                                      bool
	// Type is the footer's "Blog · v1.0.0" text.
	Type string
	// Action is the footer's label; ActionKind (install, installed, update,
	// live) picks its tone.
	Action, ActionKind string
}

type themeBrowsePage struct {
	Host, Refreshed string
	Off             bool
	Err             string
	Query           BrowseQuery
	HideInstalled   bool
	Cards, Featured []themeCardView
	Pager           ui.Pagination
	Total           int
	Filtered        bool
	ClearActions    []ui.Button
	Toolbar         browseToolbar
	RefreshReturn   string
}

// themeRegistry is the registry as the theme pages see it: the entries with
// Active set from the installed themes, and the installed themes by name.
func (a *Admin) themeRegistry(r *http.Request, pk PackageAdmin) (RegistryView, map[string]ThemeInfo) {
	reg := pk.Registry(r.Context())
	info := map[string]ThemeInfo{}
	if themes, err := pk.Themes(r.Context()); err != nil {
		a.logger().Error("list themes failed", "err", err)
	} else {
		for _, t := range themes {
			info[t.Name] = t
		}
	}
	// The built-in theme owns the name "default": a registry theme of that
	// name cannot be installed, so it is not listed.
	entries := make([]RegistryEntry, 0, len(reg.Entries))
	for _, e := range reg.Entries {
		if e.Kind == "theme" {
			if e.Name == "default" {
				continue
			}
			e.Active = info[e.Name].Active
		}
		entries = append(entries, e)
	}
	reg.Entries = entries
	return reg, info
}

func (a *Admin) handleThemeBrowse(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	reg, _ := a.themeRegistry(r, pk)
	q := ParseThemeBrowseQuery(r.URL.Query())
	res := RunThemeBrowse(reg.Entries, q)
	base := a.adminPath + "/theme/browse"
	d := themeBrowsePage{
		Host: reg.Host, Off: reg.Off, Err: reg.Err, Query: q, HideInstalled: q.HideInstalled,
		Total: res.Total, Filtered: res.Filtered, ClearActions: []ui.Button{{Label: "Clear filters", Href: base}}, RefreshReturn: "theme-browse",
	}
	sortOptions := []ui.Option{
		{Value: browseSortName, Label: "Name A to Z", Selected: q.Sort == browseSortName},
		{Value: browseSortNameDesc, Label: "Name Z to A", Selected: q.Sort == browseSortNameDesc},
	}
	var groups []browseGroup
	if res.Facets.HasSiteTypes {
		g := browseGroup{Name: "type", Label: "Site type"}
		for _, c := range res.Facets.SiteTypes {
			name := siteTypeLabel(c.Value)
			g.Facets = append(g.Facets, browseFacet{Value: c.Value, Label: fmt.Sprintf("%s (%d)", name, c.Count), Checked: slices.Contains(q.Types, c.Value), Chip: "Site type: " + name})
		}
		groups = append(groups, g)
	}
	if res.Facets.HasFlags {
		g := browseGroup{Name: "feat", Label: "Features"}
		for _, f := range []string{"dark", "nojs", "builder"} {
			l := themeFlagLabels[f].Feature
			g.Facets = append(g.Facets, browseFacet{Value: f, Label: l, Checked: slices.Contains(q.Feats, f), Chip: "Features: " + l})
		}
		groups = append(groups, g)
	}
	d.Toolbar = browseToolbarFor(base, q, res, groups, sortOptions)
	if !reg.FetchedAt.IsZero() {
		d.Refreshed = agoText(pluginsNow().Sub(reg.FetchedAt))
	}
	res.Pager.BaseURL, res.Pager.Query = base, q.Values()
	d.Pager = res.Pager
	for _, e := range res.Items {
		d.Cards = append(d.Cards, a.themeCard(e))
	}
	for _, e := range res.Featured {
		d.Featured = append(d.Featured, a.themeCard(e))
	}
	v := a.NewView(w, r, "Browse themes")
	v.Section, v.Tab = "themes", "themes"
	v.Crumbs = a.areaCrumbs("themes", Crumb{Label: "Browse"})
	v.Data = d
	a.Render(w, r, http.StatusOK, "theme/browse", v)
}

// siteTypeLabel is the display name of a site type ("blog" -> "Blog").
func siteTypeLabel(s string) string {
	if s == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// themeCard shapes a registry theme for the card grid.
func (a *Admin) themeCard(e RegistryEntry) themeCardView {
	c := themeCardView{
		Name: e.Name, Title: e.Title, Author: e.Author, Description: e.Description, Screenshot: e.Screenshot,
		Href: a.themeURL(e.Name, "about"), FirstParty: e.Tier == "first-party",
		Action: "Install", ActionKind: "install",
	}
	if c.Title == "" {
		c.Title = e.Name
	}
	r, _ := utf8.DecodeRuneInString(c.Title)
	c.Initial = string(unicode.ToUpper(r))
	c.Type = "v" + strings.TrimPrefix(e.Version, "v")
	if st := siteTypeLabel(e.SiteType); st != "" {
		c.Type = st + " · " + c.Type
	}
	switch {
	case e.Active:
		c.Action, c.ActionKind = "● Live", "live"
	case e.Update:
		c.Action, c.ActionKind = "Update to "+strings.TrimPrefix(e.Version, "v"), "update"
	case e.Installed:
		c.Action, c.ActionKind = "✓ Installed", "installed"
	}
	return c
}

func (a *Admin) themeURL(name, page string) string {
	return a.adminPath + "/theme/themes/" + url.PathEscape(name) + "/" + page
}

type themeDetailPage struct {
	Entry         RegistryEntry
	Title, Byline string
	Badge         *ui.Badge
	About         []string
	Note          string
	Screenshot    string
	SiteTypeLabel string
	Size          string
	WorksWith     string
	Source        string
	Primary       *detailPrimary
	Actions       []ui.Button
}

// handleThemeAbout is the no-script detail page of a registry theme. A request
// from the panel (HX-Request) gets the main block only.
func (a *Admin) handleThemeAbout(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	browse := a.adminPath + "/theme/browse"
	reg, info := a.themeRegistry(r, pk)
	switch {
	case reg.Off:
		a.ErrorBack(w, r, http.StatusNotFound, "No registry is set up", "Set HATAMOTO_REGISTRY_URL to browse themes.", a.adminPath+"/theme/themes", "Back to themes")
		return
	case reg.Err != "":
		a.ErrorBack(w, r, http.StatusServiceUnavailable, "The registry is not reachable right now", "Installed themes keep working. Try again in a moment.", browse, "Back to browse")
		return
	}
	name := r.PathValue("name")
	i := slices.IndexFunc(reg.Entries, func(e RegistryEntry) bool { return e.Kind == "theme" && e.Name == name && name != "default" })
	if i < 0 {
		a.ErrorBack(w, r, http.StatusNotFound, "Theme not found", "The registry does not list a theme with that name.", browse, "Back to browse")
		return
	}
	e := reg.Entries[i]
	d := themeDetailPage{
		Entry: e, Title: e.Title, Screenshot: e.Screenshot, SiteTypeLabel: siteTypeLabel(e.SiteType), Source: httpURL(e.Source),
	}
	if d.Title == "" {
		d.Title = e.Name
	}
	if e.Tier == "first-party" {
		d.Badge = &ui.Badge{Label: "First-party", Plain: true}
	}
	if e.Author != "" {
		d.Byline = "By " + e.Author
	}
	if e.Size > 0 {
		d.Size = humanSize(e.Size)
	}
	about := e.About
	if about == "" {
		about = e.Description
	}
	for _, para := range strings.Split(about, "\n") {
		if para = strings.TrimSpace(para); para != "" {
			d.About = append(d.About, para)
		}
	}
	var works []string
	for _, f := range e.Flags {
		if l, ok := themeFlagLabels[f]; ok {
			works = append(works, l.Works)
		}
	}
	d.WorksWith = strings.Join(works, ", ")

	fetch := a.adminPath + "/theme/themes/fetch"
	hasSettings := info[e.Name].HasSettings
	settings := a.themeURL(e.Name, "settings")
	switch {
	case e.Active:
		d.Note = "This is your live theme."
		switch {
		case e.Update:
			d.Primary = &detailPrimary{Post: fetch, Fields: map[string]string{"name": e.Name}, Label: "Review update"}
			if hasSettings {
				d.Actions = append(d.Actions, ui.Button{Label: "Settings", Href: settings})
			}
		case hasSettings:
			d.Primary = &detailPrimary{Href: settings, Label: "Settings"}
		}
	case !e.Installed:
		d.Note = "Installing adds the theme to your Themes list. Your site does not change until you activate it."
		d.Primary = &detailPrimary{Post: fetch, Fields: map[string]string{"name": e.Name}, Label: "Install"}
	case e.Update:
		d.Note = "Installed. Activate it when you are ready; your content stays as it is."
		d.Primary = &detailPrimary{Post: fetch, Fields: map[string]string{"name": e.Name}, Label: "Review update"}
	default:
		d.Note = "Installed. Activate it when you are ready; your content stays as it is."
		d.Primary = &detailPrimary{Href: a.themeURL(e.Name, "activate"), Label: "Activate…"}
	}
	if demo := httpURL(e.Homepage); demo != "" {
		d.Actions = append([]ui.Button{{Label: "Preview demo", Href: demo, Icon: "external", Attrs: map[string]string{"target": "_blank", "rel": "noopener noreferrer"}}}, d.Actions...)
	}
	v := a.NewView(w, r, d.Title)
	v.Section, v.Tab = "themes", "themes"
	v.Crumbs = a.areaCrumbs("themes", Crumb{Label: "Browse", Href: browse}, Crumb{Label: d.Title})
	v.Data = d
	a.Render(w, r, http.StatusOK, "theme/browse-detail", v)
}
