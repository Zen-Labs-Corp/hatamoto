package admin

import (
	"encoding/json"
	"html/template"
	"net/http"
	"reflect"
	"slices"
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
		"name": siteName, "tax_id": site.TaxID, "vat_id": site.VATID, "description": site.Description, "phone": site.Phone, "fax": site.Fax, "email": site.Email,
		"hours_note": site.HoursNote, "currency": site.Currency, "price_range": site.PriceRange, "geo_region": site.GeoRegion,
		"naics": site.Naics, "isic": site.Isic, "duns": site.Duns, "lei": site.Lei, "gln": site.Gln,
		"booking_url": site.BookingURL, "order_url": site.OrderURL, "menu_url": site.MenuURL,
	} {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// inheritedFields are the locale card fields that cannot show a placeholder
// (lists, numbers, switches); their help says where an empty field falls back.
var inheritedFields = []string{
	"founders", "brands", "memberships", "payment_methods", "accepts_reservations", "service_radius_km",
	"services", "knows_about", "awards", "amenity_presets", "amenities", "public_access", "free_entry",
}

// localeUI is businessUI plus the help only a locale card needs.
func localeUI() map[string]schemaform.UIHint {
	h := make(map[string]schemaform.UIHint, len(businessUI)+7+len(inheritedFields))
	for k, v := range businessUI {
		h[k] = v
	}
	h["business_type"] = schemaform.UIHint{Help: "Empty prints " + bizprofile.DefaultType + "."}
	h["no_office"] = schemaform.UIHint{Help: "This locale prints the organisation only, no local business."}
	h["name"] = schemaform.UIHint{Help: "Names this locale's local business and is printed as its legal name. Empty uses the site name."}
	h["description"] = schemaform.UIHint{Help: "Describes this locale's local business. Empty uses the site description."}
	h["vat_id"] = schemaform.UIHint{Help: "Empty uses the site VAT ID."}
	h["country_name"] = schemaform.UIHint{Help: "The country name printed in the areas served, like France."}
	h["image"] = schemaform.UIHint{Help: "A photo of this locale's business."}
	for _, k := range inheritedFields {
		help := h[k].Help
		if help != "" {
			help += " "
		}
		h[k] = schemaform.UIHint{Help: help + "Empty uses the site-wide value."}
	}
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
		form, err := schemaform.Render(businessSchema(), vals, schemaform.Options{Sections: true, AddIcon: plusIcon(), Prefix: locPrefix(anchor), UI: hints, Placeholders: ph, Errors: errs, OpenPath: open})
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
	p.Lei = strings.ToUpper(p.Lei)
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

// orgAddressChoices are the settings of the Organisation address radios, in
// display order. The empty value is today's behaviour.
var orgAddressChoices = []ui.Option{
	{Value: "", Label: "Site address on every page"},
	{Value: bizprofile.OrgAddressLocale, Label: "The locale's office; none without one"},
	{Value: bizprofile.OrgAddressLocaleOrSite, Label: "The locale's office; site address without one"},
}

// orgAddressOptions marks the stored choice.
func orgAddressOptions(selected string) []ui.Option {
	out := make([]ui.Option, len(orgAddressChoices))
	for i, o := range orgAddressChoices {
		o.Selected = o.Value == selected
		out[i] = o
	}
	return out
}

// businessData is the Business details tab.
type businessData struct {
	Locales         []localeCard
	Empty           ui.Empty
	OrgAddress      []ui.Option
	OrgAddressError string
}

// handleOrgAddressSave stores which address the Organization prints on locale
// pages. It rewrites the site profile like the General business form does, so
// every other field of it stays as stored.
func (a *Admin) handleOrgAddressSave(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	v := r.PostForm.Get("org_address")
	if !slices.ContainsFunc(orgAddressChoices, func(o ui.Option) bool { return o.Value == v }) {
		a.renderBusinessWith(w, r, http.StatusUnprocessableEntity, nil, "Choose one of the options.")
		return
	}
	p, err := siteProfile(cfg)
	if err != nil {
		a.logger().Error("save organisation address: stored business profile unreadable", "err", err)
		a.Error(w, r, http.StatusUnprocessableEntity, "Nothing was saved", "The stored business details cannot be read, so nothing was saved. Fix them in Settings, General first.")
		return
	}
	p.OrgAddress = v
	// The legacy columns hold phone, hours note and the five address keys; the
	// profile column holds everything else.
	p.Phone, p.HoursNote = "", ""
	p.Address.Street, p.Address.City, p.Address.State, p.Address.PostalCode, p.Address.Country = "", "", "", "", ""
	raw, _ := json.Marshal(p)
	if _, err := a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{BusinessProfile: raw}); err != nil {
		a.logger().Error("save organisation address failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The setting could not be saved.")
		return
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Organisation address saved.")
	http.Redirect(w, r, a.url("settings", "locales", "business")+"#organisation-address", http.StatusSeeOther)
}

// renderBusiness is the Business details tab: one card per locale, the card of
// edit (a failed or structural save) carrying its submitted values.
func (a *Admin) renderBusiness(w http.ResponseWriter, r *http.Request, status int, edit *localeEdit) {
	a.renderBusinessWith(w, r, status, edit, "")
}

// renderBusinessWith is renderBusiness with an error on the organisation
// address choice.
func (a *Admin) renderBusinessWith(w http.ResponseWriter, r *http.Request, status int, edit *localeEdit, orgErr string) {
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
	site, _ := siteProfile(cfg)
	v := a.NewView(w, r, "Business details")
	v.Width = "compact"
	v.Section, v.Tab = "locales", "locale-business"
	v.Data = businessData{Locales: cards, OrgAddress: orgAddressOptions(site.OrgAddress), OrgAddressError: orgErr, Empty: ui.Empty{Icon: "globe", Title: "Add a second locale first",
		Body:    "Per-locale business details apply once your site has two or more locales.",
		Actions: []ui.Button{{Label: "Add a locale", Variant: "primary", Href: a.url("settings", "locales")}}}}
	a.Render(w, r, status, "settings/locales-business", v)
}

func (a *Admin) handleBusiness(w http.ResponseWriter, r *http.Request) {
	a.renderBusiness(w, r, http.StatusOK, nil)
}
