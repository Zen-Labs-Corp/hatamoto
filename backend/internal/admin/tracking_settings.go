package admin

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/tags"
)

const trackingPath = "/settings/tracking"

// TrackingService is the tag manager settings store behind Settings > Privacy & tracking > Tracking.
type TrackingService interface {
	Get(ctx context.Context) (tags.Settings, error)
	Save(ctx context.Context, s tags.Settings) error
}

func (a *Admin) mountTrackingSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+trackingPath, a.handleTracking)
	owner.HandleFunc("POST "+trackingPath, a.handleTrackingSave)
	owner.HandleFunc("POST "+trackingPath+"/notice", a.handleTrackingNotice)
	owner.HandleFunc("GET "+trackingPath+"/tags/{key}", a.handleTrackingTag)
}

// trackingRow is one tag: its card in the directory and its set-up form
// (the drawer on the directory, the page at /settings/tracking/tags/<key>).
type trackingRow struct {
	Key, Label, Value, Aux, IDLabel, AuxLabel, IDHelp, AuxHelp string
	// Summary is the one line under the name on the card.
	Summary string
	// Consent is the raw consent category ("analytics"), as the cookie banner names it.
	Consent string
	// Hosts lists the hosts the tag adds to the page policy.
	Hosts []string
	// Source is "Tag pack v1.0.0" for a pack tag, "" for a built-in one.
	Source string
	// Href is the tag's own page, the no-script target of its Set up and Edit links.
	Href            string
	Enabled, HasAux bool
	IDErr, AuxErr   string
	// AdsTxtURL is set on the AdSense row when the ads.txt settings tab exists.
	AdsTxtURL string
}

// trackingGroup is one category of the directory.
type trackingGroup struct {
	ID, Title string
	Rows      []trackingRow
}

type trackingData struct {
	Summary         []ui.FieldError
	ExcludeSignedIn bool
	// Rows is every tag in table order; On the enabled ones; Groups the rest by category.
	Rows        []trackingRow
	On          []trackingRow
	Groups      []trackingGroup
	MovedNotice string
	// AllNote describes the All tags card, with a link to Cookie consent.
	AllNote template.HTML
	Legacy  []string
	// NeverLoad names the enabled tags whose category the consent banner does not ask.
	NeverLoad []string
}

// trackingTagData is the page of one tag.
type trackingTagData struct {
	Summary []ui.FieldError
	Row     trackingRow
	Back    string
}

// trackingFromForm applies the posted fields over stored. A vendor the post
// does not carry (no <id>_present) keeps its stored state.
func trackingFromForm(r *http.Request, stored tags.Settings) tags.Settings {
	s := stored
	s.Vendors = make(map[string]tags.VendorState, len(stored.Vendors))
	for k, v := range stored.Vendors {
		s.Vendors[k] = v
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	f := r.PostForm
	if f.Has("exclude_signed_in") {
		s.ExcludeSignedIn = f.Get("exclude_signed_in") == "true"
	}
	for _, v := range tags.All() {
		if !f.Has(v.ID + "_present") {
			continue
		}
		st := s.Vendors[v.ID]
		st.Enabled = f.Get(v.ID+"_enabled") == "true"
		// The one-click Turn on and Turn off forms post no id: the stored one stays.
		if f.Has(v.ID + "_id") {
			st.ID = strings.TrimSpace(f.Get(v.ID + "_id"))
		}
		if v.Aux != nil && f.Has(v.ID+"_aux") {
			st.Aux = strings.TrimSpace(f.Get(v.ID + "_aux"))
		}
		s.Vendors[v.ID] = st
	}
	return s.Normalize()
}

func (a *Admin) handleTracking(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Tracking
	if svc == nil {
		a.unavailable(w, r, "Tracking settings")
		return
	}
	set, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("tracking settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The tracking settings could not be loaded.")
		return
	}
	a.renderTracking(w, r, http.StatusOK, set, nil)
}

func (a *Admin) handleTrackingSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Tracking
	if svc == nil {
		a.unavailable(w, r, "Tracking settings")
		return
	}
	stored, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("tracking settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The tracking settings could not be loaded.")
		return
	}
	next := trackingFromForm(r, stored)
	if verrs := next.Validate(); len(verrs) > 0 {
		errs := make([]ui.FieldError, 0, len(verrs))
		for _, fe := range verrs {
			errs = append(errs, ui.FieldError{ID: trackingFieldID(fe.Field), Label: trackingFieldLabel(fe.Field), Message: fe.Message})
		}
		// The form that failed is one tag's: show that tag's page with its errors.
		// It keeps the stored on/off state, so a tag that was off still offers Turn on.
		if v, ok := tags.ByID(trackingErrVendor(verrs[0].Field)); ok {
			st := next.Vendors[v.ID]
			st.Enabled = stored.Vendors[v.ID].Enabled
			next.Vendors[v.ID] = st
			a.renderTrackingTag(w, r, http.StatusUnprocessableEntity, next, v, errs)
			return
		}
		a.renderTracking(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	if err := svc.Save(r.Context(), next); err != nil {
		a.logger().Error("tracking settings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The tracking settings could not be saved.")
		return
	}
	enabled, disabled, changed := trackingDiff(stored, next)
	a.audit(r, "tracking.settings", "tracking", map[string]any{
		"enabled": enabled, "disabled": disabled, "changed": changed,
		"exclude_signed_in": next.ExcludeSignedIn,
	})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", trackingSavedFlash(enabled, disabled, changed))
	http.Redirect(w, r, a.adminPath+trackingPath, http.StatusSeeOther)
}

// trackingSavedFlash names the tag a one-tag save changed, else stays general.
func trackingSavedFlash(enabled, disabled, changed []string) string {
	label := func(id string) string {
		if v, ok := tags.ByID(id); ok {
			return v.Label
		}
		return id
	}
	switch {
	case len(enabled) == 1 && len(disabled)+len(changed) == 0:
		return label(enabled[0]) + " is on. It loads after a visitor accepts cookies."
	case len(disabled) == 1 && len(enabled)+len(changed) == 0:
		return label(disabled[0]) + " is off."
	case len(changed) == 1 && len(enabled)+len(disabled) == 0:
		return label(changed[0]) + " saved."
	}
	return "Tracking settings saved."
}

// trackingErrVendor is the vendor id of a validation field ("ga4" or "ga4.aux").
func trackingErrVendor(field string) string {
	id, _ := strings.CutSuffix(field, ".aux")
	return id
}

func (a *Admin) handleTrackingTag(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Tracking
	if svc == nil {
		a.unavailable(w, r, "Tracking settings")
		return
	}
	v, ok := tags.ByID(r.PathValue("key"))
	if !ok {
		a.notFound(w, r, "Tag", a.adminPath+trackingPath, "Back to tracking")
		return
	}
	set, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("tracking settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The tracking settings could not be loaded.")
		return
	}
	a.renderTrackingTag(w, r, http.StatusOK, set, v, nil)
}

func (a *Admin) handleTrackingNotice(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Tracking
	if svc == nil {
		a.unavailable(w, r, "Tracking settings")
		return
	}
	set, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("tracking settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The tracking settings could not be loaded.")
		return
	}
	if len(set.MovedFrom) > 0 && !set.NoticeDismissed {
		set.NoticeDismissed = true
		if err := svc.Save(r.Context(), set); err != nil {
			a.logger().Error("tracking notice dismiss", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The notice could not be dismissed.")
			return
		}
		a.audit(r, "tracking.notice_dismiss", "tracking", map[string]any{"moved_from": set.MovedFrom})
	}
	http.Redirect(w, r, a.adminPath+trackingPath, http.StatusSeeOther)
}

// trackingDiff names, by vendor id only, what a save turned on, turned off and
// changed (an id or second id edited on a vendor that is on before and after).
func trackingDiff(before, after tags.Settings) (enabled, disabled, changed []string) {
	enabled, disabled, changed = []string{}, []string{}, []string{}
	for _, v := range tags.All() {
		b, a := before.Vendors[v.ID], after.Vendors[v.ID]
		switch {
		case a.Enabled && !b.Enabled:
			enabled = append(enabled, v.ID)
		case !a.Enabled && b.Enabled:
			disabled = append(disabled, v.ID)
		case a.Enabled && (a.ID != b.ID || a.Aux != b.Aux):
			changed = append(changed, v.ID)
		}
	}
	return enabled, disabled, changed
}

// trackingFieldID is the id of the control that carries a validation field:
// "<vendor>" is its id input and "<vendor>.aux" its second input.
func trackingFieldID(field string) string {
	if id, ok := strings.CutSuffix(field, ".aux"); ok {
		return "tracking-" + id + "-aux"
	}
	return "tracking-" + field + "-id"
}

func trackingFieldLabel(field string) string {
	id, aux := strings.CutSuffix(field, ".aux")
	v, ok := tags.ByID(id)
	switch {
	case !ok:
		return "Tags"
	case aux && v.Aux != nil:
		return v.Label + ": " + v.Aux.Label
	}
	return v.Label + ": " + v.IDLabel
}

// trackingHosts lists the vendor's CSP hosts without the scheme, in table order.
func trackingHosts(v tags.Vendor) []string {
	var hosts []string
	for _, list := range [][]string{v.Script, v.Connect, v.Style, v.Font} {
		for _, h := range list {
			h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "wss://")
			if !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// trackingMovedLabels names a folded-in plugin the way the notice shows it.
var trackingMovedLabels = map[string]string{"gtm": "Google Tag Manager", "google-analytics": "Google Analytics"}

func trackingMovedNotice(moved []string) string {
	names := make([]string, 0, len(moved))
	for _, m := range moved {
		if l, ok := trackingMovedLabels[m]; ok {
			m = l
		}
		names = append(names, m)
	}
	if len(names) == 1 {
		return "The " + names[0] + " plugin was replaced by Settings > Privacy & tracking > Tracking. Your ID was copied; the plugin was removed."
	}
	return "The " + strings.Join(names, " and ") + " plugins were replaced by Settings > Privacy & tracking > Tracking. Your IDs were copied; the plugins were removed."
}

// trackingGroups orders and names the directory groups, which sort tags by
// what they are for. A tag's group is its consent category unless
// trackingGroupOf says otherwise; an unknown one gets its own group at the end.
var trackingGroups = []struct{ ID, Title string }{
	{"analytics", "Analytics"},
	{"marketing", "Ads and marketing"},
	{"functional", "Chat and support"},
}

// trackingGroupMoves files ad platform tags that wait for analytics consent
// under ads, where people look for them.
var trackingGroupMoves = map[string]string{"uet": "marketing", "linkedin": "marketing"}

func trackingGroupOf(v tags.Vendor) string {
	if g, ok := trackingGroupMoves[v.ID]; ok {
		return g
	}
	return v.Category
}

func trackingGroupTitle(g string) string {
	for _, k := range trackingGroups {
		if k.ID == g {
			return k.Title
		}
	}
	if g == "" {
		return "Other"
	}
	return strings.ToUpper(g[:1]) + g[1:]
}

// trackingRowFor builds the card and form of one vendor.
func (a *Admin) trackingRowFor(v tags.Vendor, st tags.VendorState, errBy map[string]string, packVersions map[string]string) trackingRow {
	row := trackingRow{
		Key: v.ID, Label: v.Label, Enabled: st.Enabled, Value: st.ID, Aux: st.Aux,
		Summary: v.Summary, Consent: v.Category,
		Hosts:   trackingHosts(v),
		Source:  trackingSource(v.ID, packVersions),
		Href:    a.adminPath + trackingPath + "/tags/" + url.PathEscape(v.ID),
		IDLabel: v.IDLabel,
		IDHelp:  v.Help,
		IDErr:   errBy[trackingFieldID(v.ID)],
	}
	if row.Summary == "" {
		row.Summary = "Loads " + v.Label + " after a visitor accepts cookies."
	}
	if v.Aux != nil {
		row.HasAux, row.AuxLabel, row.AuxHelp = true, v.Aux.Label, v.Aux.Help
		row.AuxErr = errBy[trackingFieldID(v.ID+".aux")]
	}
	if v.ID == "adsense" {
		row.AdsTxtURL = a.settingsTabURL("seo-ads-txt")
		if row.AdsTxtURL != "" {
			if pub := seo.AdSensePubID(st.ID); pub != "" {
				row.AdsTxtURL += "?add=" + url.QueryEscape(pub)
			}
		}
	}
	return row
}

func trackingPackVersions() map[string]string {
	out := map[string]string{}
	for _, pv := range tags.Packs() {
		out[pv.ID] = pv.PluginVersion
	}
	return out
}

func trackingErrIndex(errs []ui.FieldError) map[string]string {
	out := map[string]string{}
	for _, e := range errs {
		out[e.ID] = e.Message
	}
	return out
}

func (a *Admin) renderTracking(w http.ResponseWriter, r *http.Request, status int, set tags.Settings, errs []ui.FieldError) {
	d := trackingData{Summary: errs, ExcludeSignedIn: set.ExcludeSignedIn,
		AllNote: template.HTML("Tags never load in previews. Each one waits until a visitor accepts its category in <a href=\"" +
			template.HTMLEscapeString(a.adminPath+"/settings/privacy") + "\">Cookie consent</a>.")}
	errBy := trackingErrIndex(errs)
	var never func(string) bool
	if a.deps.TrackingNeverLoads != nil {
		never = a.deps.TrackingNeverLoads(r.Context())
	}
	packVersions := trackingPackVersions()
	byGroup := map[string][]trackingRow{}
	var extra []string
	for _, v := range tags.All() {
		st := set.Vendors[v.ID]
		row := a.trackingRowFor(v, st, errBy, packVersions)
		if never != nil && st.Enabled && never(v.Category) {
			d.NeverLoad = append(d.NeverLoad, v.Label)
		}
		d.Rows = append(d.Rows, row)
		if row.Enabled {
			d.On = append(d.On, row)
			continue
		}
		g := trackingGroupOf(v)
		if _, seen := byGroup[g]; !seen && !slices.ContainsFunc(trackingGroups, func(k struct{ ID, Title string }) bool { return k.ID == g }) {
			extra = append(extra, g)
		}
		byGroup[g] = append(byGroup[g], row)
	}
	for _, k := range trackingGroups {
		if rows := byGroup[k.ID]; len(rows) > 0 {
			d.Groups = append(d.Groups, trackingGroup{ID: k.ID, Title: k.Title, Rows: rows})
		}
	}
	for _, g := range extra {
		d.Groups = append(d.Groups, trackingGroup{ID: g, Title: trackingGroupTitle(g), Rows: byGroup[g]})
	}
	if len(set.MovedFrom) > 0 && !set.NoticeDismissed {
		d.MovedNotice = trackingMovedNotice(set.MovedFrom)
	}
	if a.deps.LegacyTrackingPlugins != nil {
		d.Legacy = a.deps.LegacyTrackingPlugins(r.Context())
	}
	view := a.NewView(w, r, "Tracking")
	view.Section, view.Tab = "privacy-tracking", "tracking"
	view.Data = d
	a.Render(w, r, status, "settings/tracking", view)
}

// renderTrackingTag renders the page of one tag; errs other than that tag's are dropped.
func (a *Admin) renderTrackingTag(w http.ResponseWriter, r *http.Request, status int, set tags.Settings, v tags.Vendor, errs []ui.FieldError) {
	var mine []ui.FieldError
	for _, e := range errs {
		if e.ID == trackingFieldID(v.ID) || e.ID == trackingFieldID(v.ID+".aux") {
			mine = append(mine, e)
		}
	}
	d := trackingTagData{Summary: mine, Back: a.adminPath + trackingPath,
		Row: a.trackingRowFor(v, set.Vendors[v.ID], trackingErrIndex(mine), trackingPackVersions())}
	view := a.NewView(w, r, v.Label)
	view.Section, view.Tab = "privacy-tracking", "tracking"
	view.Width = "compact"
	view.Data = d
	a.Render(w, r, status, "settings/tracking-tag", view)
}

// settingsTabURL is the admin URL of a settings tab, "" when no such tab is
// registered yet.
func (a *Admin) settingsTabURL(tabID string) string {
	for _, sec := range settingsRegistry {
		for _, t := range sec.Tabs {
			if t.ID == tabID && settingsLive[tabID] {
				return a.adminPath + t.Final
			}
		}
	}
	return ""
}

// trackingSource labels a pack tag row, as text (not colour alone); built-in rows have no label.
func trackingSource(id string, packVersions map[string]string) string {
	if ver, ok := packVersions[id]; ok {
		return "Tag pack v" + strings.TrimPrefix(ver, "v")
	}
	return ""
}
