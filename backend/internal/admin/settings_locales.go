package admin

import (
	"encoding/json"
	"html/template"
	"net/http"
	"reflect"
	"strings"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/siteconfig"
)

// localeEdit carries a failed or structural save of one locale card back into
// the Business details tab: the card re-renders with the submitted values, open.
type localeEdit struct {
	Tag  string
	Vals map[string]any
	Errs schemaform.Errors
	Open string
}

// localeCard is one collapsible card of the Business details tab.
type localeCard struct {
	Tag, Name, Anchor string
	Badge, BadgeKind  string
	Open              bool
	Action            string
	Summary           []ui.FieldError
	Form              template.HTML
}

// locPrefix namespaces the fields of one locale's form.
func locPrefix(anchor string) string { return "loc_" + anchor + "." }

// localeAnchor is the HTML id suffix of a locale: its URL segment, or the
// lower-cased tag for a default locale without one.
func localeAnchor(e siteconfig.LocaleEntry) string {
	if e.Segment != "" {
		return e.Segment
	}
	return strings.ToLower(e.Tag)
}

// localeBadge says where a locale's output comes from.
func localeBadge(p bizprofile.Profile) (label, kind string) {
	switch {
	case p.NoOffice:
		return "No office", "neutral"
	case p.Address.Street != "" || p.Address.City != "":
		return "Own office", "success"
	}
	return "Defaults", "neutral"
}

// localePlaceholders are the site-wide values an empty locale field falls back
// to, shown as ghost text. The address is a block and never falls back.
func localePlaceholders(site bizprofile.Profile, siteName string) map[string]string {
	out := map[string]string{}
	for k, v := range map[string]string{
		"name": siteName, "tax_id": site.TaxID, "phone": site.Phone, "fax": site.Fax, "email": site.Email,
		"hours_note": site.HoursNote, "currency": site.Currency, "price_range": site.PriceRange, "geo_region": site.GeoRegion,
	} {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// localeUI is businessUI plus the help only a locale card needs.
func localeUI() map[string]schemaform.UIHint {
	h := make(map[string]schemaform.UIHint, len(businessUI)+3)
	for k, v := range businessUI {
		h[k] = v
	}
	h["business_type"] = schemaform.UIHint{Help: "Empty prints " + bizprofile.DefaultType + "."}
	h["no_office"] = schemaform.UIHint{Help: "This locale prints the organisation only, no local business."}
	h["name"] = schemaform.UIHint{Help: "Names this locale's local business. Empty uses the site name."}
	return h
}

// localeCards builds the cards of the Locales section; it is empty on a site
// with fewer than two locales. edit, when set, replaces one card's values.
func (a *Admin) localeCards(cfg *siteconfig.Config, edit *localeEdit) ([]localeCard, error) {
	if !localeSetOf(cfg).Multi() {
		return nil, nil
	}
	site, err := siteProfile(cfg)
	if err != nil {
		return nil, err
	}
	stored, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		stored = nil
	}
	ph, hints := localePlaceholders(site, cfg.CompanyName), localeUI()
	cards := make([]localeCard, 0, len(cfg.Locales))
	for _, e := range cfg.Locales {
		anchor := localeAnchor(e)
		c := localeCard{Tag: e.Tag, Name: localeDisplayName(e.Tag), Anchor: anchor, Action: a.url("settings", "locales", "business", e.Tag)}
		p := stored[e.Tag]
		vals, errs, open := profileValues(p), schemaform.Errors(nil), ""
		if edit != nil && edit.Tag == e.Tag {
			vals, errs, open = edit.Vals, edit.Errs, edit.Open
			c.Open = true
			c.Summary = formErrors(locPrefix(anchor), errs)
			if edited, err := valuesProfile(vals); err == nil {
				p = edited
			}
		}
		c.Badge, c.BadgeKind = localeBadge(p)
		form, err := schemaform.Render(businessSchema(), vals, schemaform.Options{Prefix: locPrefix(anchor), UI: hints, Placeholders: ph, Errors: errs, OpenPath: open})
		if err != nil {
			return nil, err
		}
		c.Form = form
		cards = append(cards, c)
	}
	return cards, nil
}

func (a *Admin) handleBusinessSave(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	tag := r.PathValue("tag")
	var entry *siteconfig.LocaleEntry
	for i := range cfg.Locales {
		if cfg.Locales[i].Tag == tag {
			entry = &cfg.Locales[i]
		}
	}
	if entry == nil || !localeSetOf(cfg).Multi() {
		a.Error(w, r, http.StatusNotFound, "Not found", "That locale is not on your site.")
		return
	}
	stored, _ := bizprofile.ParseLocales(cfg.LocaleProfiles)
	vals, perrs := schemaform.Decode(businessSchema(), r.PostForm, profileValues(stored[tag]), schemaform.Options{Prefix: locPrefix(localeAnchor(*entry))})
	p, perr := valuesProfile(vals)
	prefix := locPrefix(localeAnchor(*entry))
	edit := &localeEdit{Tag: tag, Vals: vals, Open: openAfterAdd(r.PostForm, prefix, vals)}
	if schemaform.Structural(r.PostForm) {
		a.renderBusiness(w, r, http.StatusOK, edit)
		return
	}
	if perr == nil && len(perrs) == 0 {
		perrs = profileErrors(bizprofile.Validate(p))
	}
	if len(perrs) > 0 || perr != nil {
		edit.Errs = perrs
		a.renderBusiness(w, r, http.StatusUnprocessableEntity, edit)
		return
	}
	var raw json.RawMessage
	if !reflect.DeepEqual(p, bizprofile.Profile{}) {
		raw, _ = json.Marshal(p)
	}
	if err := a.deps.Site.SetLocaleProfile(r.Context(), tag, raw); err != nil {
		a.logger().Error("save locale business details failed", "locale", tag, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The business details could not be saved.")
		return
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Saved "+localeDisplayName(tag)+".")
	http.Redirect(w, r, a.url("settings", "locales", "business")+"#locale-"+localeAnchor(*entry), http.StatusSeeOther)
}

// businessData is the Business details tab.
type businessData struct {
	Locales []localeCard
	Empty   ui.Empty
}

// renderBusiness is the Business details tab: one card per locale, the card of
// edit (a failed or structural save) carrying its submitted values.
func (a *Admin) renderBusiness(w http.ResponseWriter, r *http.Request, status int, edit *localeEdit) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	cards, err := a.localeCards(cfg, edit)
	if err != nil {
		a.logger().Error("locale cards failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be shown.")
		return
	}
	v := a.NewView(w, r, "Business details")
	v.Width = "compact"
	v.Section, v.Tab = "locales", "locale-business"
	v.Data = businessData{Locales: cards, Empty: ui.Empty{Icon: "globe", Title: "Add a second locale first",
		Body:    "Per-locale business details apply once your site has two or more locales.",
		Actions: []ui.Button{{Label: "Add a locale", Variant: "primary", Href: a.url("settings", "locales")}}}}
	a.Render(w, r, status, "settings/locales-business", v)
}

func (a *Admin) handleBusiness(w http.ResponseWriter, r *http.Request) {
	a.renderBusiness(w, r, http.StatusOK, nil)
}
