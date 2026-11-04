package admin

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/tags"
)

const trackingPath = "/settings/tracking"

// TrackingService is the tag manager settings store behind Settings > Tracking.
type TrackingService interface {
	Get(ctx context.Context) (tags.Settings, error)
	Save(ctx context.Context, s tags.Settings) error
}

func (a *Admin) mountTrackingSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+trackingPath, a.handleTracking)
	owner.HandleFunc("POST "+trackingPath, a.handleTrackingSave)
	owner.HandleFunc("POST "+trackingPath+"/notice", a.handleTrackingNotice)
}

// trackingRow is one vendor fieldset of the page.
type trackingRow struct {
	Key, Label, Value, Aux, IDLabel, AuxLabel, IDHelp, AuxHelp string
	Enabled, HasAux                                            bool
	IDErr, AuxErr                                              string
	// AdsTxtURL is set on the AdSense row when the ads.txt settings tab exists.
	AdsTxtURL string
}

type trackingData struct {
	Summary         []ui.FieldError
	ExcludeSignedIn bool
	Rows            []trackingRow
	MovedNotice     string
	Legacy          []string
	// NeverLoad names the enabled tags whose category the consent banner does not ask.
	NeverLoad []string
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
	for _, v := range tags.Vendors {
		if !f.Has(v.ID + "_present") {
			continue
		}
		st := s.Vendors[v.ID]
		st.Enabled = f.Get(v.ID+"_enabled") == "true"
		st.ID = strings.TrimSpace(f.Get(v.ID + "_id"))
		if v.Aux != nil {
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
	a.SetFlash(w, r, "success", "Tracking settings saved.")
	http.Redirect(w, r, a.adminPath+trackingPath, http.StatusSeeOther)
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
	for _, v := range tags.Vendors {
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
func trackingHosts(v tags.Vendor) string {
	var hosts []string
	for _, list := range [][]string{v.Script, v.Connect, v.Style, v.Font} {
		for _, h := range list {
			h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "wss://")
			if !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	return strings.Join(hosts, ", ")
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
		return "The " + names[0] + " plugin was replaced by Settings > Tracking. Your ID was copied; the plugin was removed."
	}
	return "The " + strings.Join(names, " and ") + " plugins were replaced by Settings > Tracking. Your IDs were copied; the plugins were removed."
}

func (a *Admin) renderTracking(w http.ResponseWriter, r *http.Request, status int, set tags.Settings, errs []ui.FieldError) {
	d := trackingData{Summary: errs, ExcludeSignedIn: set.ExcludeSignedIn}
	errBy := map[string]string{}
	for _, e := range errs {
		errBy[e.ID] = e.Message
	}
	adsTxt := a.settingsTabURL("seo-ads-txt")
	var never func(string) bool
	if a.deps.TrackingNeverLoads != nil {
		never = a.deps.TrackingNeverLoads(r.Context())
	}
	for _, v := range tags.Vendors {
		st := set.Vendors[v.ID]
		row := trackingRow{
			Key: v.ID, Label: v.Label, Enabled: st.Enabled, Value: st.ID, Aux: st.Aux,
			IDLabel: v.IDLabel,
			IDHelp:  v.Help + " Category: " + strings.ToUpper(v.Category[:1]) + v.Category[1:] + ". Allows: " + trackingHosts(v) + ".",
			IDErr:   errBy[trackingFieldID(v.ID)],
		}
		if v.Aux != nil {
			row.HasAux, row.AuxLabel, row.AuxHelp = true, v.Aux.Label, v.Aux.Help
			row.AuxErr = errBy[trackingFieldID(v.ID+".aux")]
		}
		if v.ID == "adsense" {
			row.AdsTxtURL = adsTxt
		}
		if never != nil && st.Enabled && never(v.Category) {
			d.NeverLoad = append(d.NeverLoad, v.Label)
		}
		d.Rows = append(d.Rows, row)
	}
	if len(set.MovedFrom) > 0 && !set.NoticeDismissed {
		d.MovedNotice = trackingMovedNotice(set.MovedFrom)
	}
	if a.deps.LegacyTrackingPlugins != nil {
		d.Legacy = a.deps.LegacyTrackingPlugins(r.Context())
	}
	view := a.NewView(w, r, "Tracking")
	view.Section, view.Tab = "tracking", "tracking"
	view.Width = "narrow"
	view.Data = d
	a.Render(w, r, status, "settings/tracking", view)
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
