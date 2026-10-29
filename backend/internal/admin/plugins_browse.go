package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/permtext"
)

// pluginsNow is the clock of the plugin pages ("refreshed N ago"); tests pin it.
var pluginsNow = time.Now

// mountPluginBrowse registers the Browse pages and the registry refresh
// (owner only).
func (a *Admin) mountPluginBrowse(owner *httpx.Group) {
	owner.HandleFunc("GET /plugins/browse", a.handleBrowse)
	// The detail page is /plugins/<name>/about, not under /plugins/browse/:
	// GET /plugins/browse/{name} is ambiguous with GET /plugins/{name}/settings
	// and the like, and the mux refuses such a pair at mount.
	owner.HandleFunc("GET /plugins/{name}/about", a.handleBrowseDetail)
	owner.HandleFunc("POST /plugins/check-updates", a.handleCheckUpdates)
}

// browseFacet is one checkbox of the filter column.
type browseFacet struct {
	Value, Label string
	Checked      bool
}

// browseCard is one plugin card.
type browseCard struct {
	Name, Title, Author, Description, Href, Initial string
	FirstParty                                      bool
	// Action is the label in the card footer: Install, Installed or Update.
	Action string
	// Perms is the footer's permission count text.
	Perms string
}

type pluginBrowsePage struct {
	Host, Refreshed string
	Off             bool
	Err             string
	Query           BrowseQuery
	Tiers, Perms    []browseFacet
	Cats            []browseFacet
	HideInstalled   bool
	SortOptions     []ui.Option
	Cards, Featured []browseCard
	Pager           ui.Pagination
	Total           int
	Filtered        bool
	ClearActions    []ui.Button
	RefreshReturn   string
}

func (a *Admin) handleBrowse(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	reg := pk.Registry(r.Context())
	q := ParseBrowseQuery(r.URL.Query())
	res := RunBrowse(reg.Entries, q)
	base := a.adminPath + "/plugins/browse"
	d := pluginBrowsePage{
		Host: reg.Host, Off: reg.Off, Err: reg.Err, Query: q, HideInstalled: q.HideInstalled,
		Total: res.Total, Filtered: res.Filtered, ClearActions: []ui.Button{{Label: "Clear filters", Href: base}}, RefreshReturn: "browse",
		Tiers: []browseFacet{
			{"first-party", "First-party", slices.Contains(q.Tiers, "first-party")},
			{"community", "Community", slices.Contains(q.Tiers, "community")},
		},
		Perms: []browseFacet{
			{browsePermNoNet, "No network access", slices.Contains(q.Perms, browsePermNoNet)},
			{browsePermNoScripts, "No front-end scripts", slices.Contains(q.Perms, browsePermNoScripts)},
		},
		SortOptions: []ui.Option{
			{Value: browseSortName, Label: "Name A to Z", Selected: q.Sort == browseSortName},
			{Value: browseSortNameDesc, Label: "Name Z to A", Selected: q.Sort == browseSortNameDesc},
		},
	}
	for _, c := range res.Facets.Categories {
		d.Cats = append(d.Cats, browseFacet{c, c, slices.Contains(q.Cats, c)})
	}
	if !reg.FetchedAt.IsZero() {
		d.Refreshed = agoText(pluginsNow().Sub(reg.FetchedAt))
	}
	res.Pager.BaseURL, res.Pager.Query = base, q.Values()
	d.Pager = res.Pager
	for _, e := range res.Items {
		d.Cards = append(d.Cards, a.browseCard(e))
	}
	for _, e := range res.Featured {
		d.Featured = append(d.Featured, a.browseCard(e))
	}
	v := a.NewView(w, r, "Browse plugins")
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: "Browse"}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/browse", v)
}

// browseCard shapes a registry entry for the card grid.
func (a *Admin) browseCard(e RegistryEntry) browseCard {
	c := browseCard{
		Name: e.Name, Title: e.Title, Author: e.Author, Description: e.Description,
		Href: a.pluginURL(e.Name, "about"), FirstParty: e.Tier == "first-party", Action: "Install",
	}
	if c.Title == "" {
		c.Title = e.Name
	}
	r, _ := utf8.DecodeRuneInString(c.Title)
	c.Initial = string(unicode.ToUpper(r))
	switch n := len(e.Permissions); {
	case n == 0:
		c.Perms = "No permissions"
	case n == 1:
		c.Perms = "1 permission"
	default:
		c.Perms = fmt.Sprintf("%d permissions", n)
	}
	switch {
	case e.Update:
		c.Action = "Update"
	case e.Installed:
		c.Action = "Installed"
	}
	return c
}

// handleCheckUpdates re-reads the registry now instead of waiting for the
// cache, then goes back to the page the request came from.
func (a *Admin) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	reg := pk.RefreshRegistry(r.Context())
	switch {
	case reg.Off:
		a.SetFlash(w, r, "info", "No registry is set up, so there is nothing to check.")
	case reg.Err != "":
		a.SetFlash(w, r, "error", "The registry could not be reached.")
	default:
		a.SetFlash(w, r, "success", "Checked for updates.")
	}
	to := a.adminPath + "/plugins"
	if r.PostFormValue("return") == "browse" {
		to += "/browse"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// detailPerm is one permission of a registry entry in plain English.
type detailPerm struct {
	Name, Help, Short string
	Warn              bool
}

// detailPrimary is the header's POST action: Install or Review update.
type detailPrimary struct {
	Post   string
	Fields map[string]string
	Label  string
	Icon   string
}

type pluginDetailPage struct {
	Entry    RegistryEntry
	Title    string
	Byline   string
	Badge    *ui.Badge
	About    []string
	Perms    []detailPerm
	Size     string
	Homepage string
	Source   string
	Primary  *detailPrimary
	Actions  []ui.Button
}

// handleBrowseDetail is the no-script detail page of a registry plugin. A
// request from the panel (HX-Request) gets the main block only.
func (a *Admin) handleBrowseDetail(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	browse := a.adminPath + "/plugins/browse"
	reg := pk.Registry(r.Context())
	switch {
	case reg.Off:
		a.ErrorBack(w, r, http.StatusNotFound, "No registry is set up", "Set HATAMOTO_REGISTRY_URL to browse packages.", a.adminPath+"/plugins", "Back to plugins")
		return
	case reg.Err != "":
		a.ErrorBack(w, r, http.StatusServiceUnavailable, "The registry is not reachable right now", "Installed plugins keep working. Try again in a moment.", browse, "Back to browse")
		return
	}
	name := r.PathValue("name")
	i := slices.IndexFunc(reg.Entries, func(e RegistryEntry) bool { return e.Kind == "plugin" && e.Name == name })
	if i < 0 {
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "The registry does not list a plugin with that name.", browse, "Back to browse")
		return
	}
	e := reg.Entries[i]
	d := pluginDetailPage{
		Entry: e, Title: e.Title,
		Homepage: httpURL(e.Homepage), Source: httpURL(e.Source),
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
	for _, p := range e.Permissions {
		t := permtext.Lookup(p)
		d.Perms = append(d.Perms, detailPerm{Name: p, Help: t.Help, Short: t.Short, Warn: t.Warn})
	}
	fetch := a.adminPath + "/plugins/fetch"
	switch {
	case e.Update:
		d.Primary = &detailPrimary{Post: fetch, Fields: map[string]string{"name": e.Name}, Label: "Review update"}
	case !e.Installed:
		d.Primary = &detailPrimary{Post: fetch, Fields: map[string]string{"name": e.Name}, Label: "Install…"}
	}
	if e.Installed {
		d.Actions = []ui.Button{{Label: "Settings", Href: a.pluginURL(e.Name, "settings")}}
	}
	v := a.NewView(w, r, d.Title)
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: "Browse", Href: browse}, {Label: d.Title}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/browse-detail", v)
}

// httpURL returns s when it is an absolute http or https address, else "".
func httpURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

// notBuilt answers a route whose page is not implemented yet.
func (a *Admin) notBuilt(w http.ResponseWriter, r *http.Request) {
	a.Error(w, r, http.StatusNotImplemented, "Not available yet", "This page is not available yet.")
}
