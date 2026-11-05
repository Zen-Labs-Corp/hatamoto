package admin

import (
	"context"
	"encoding/json/jsontext"
	"html/template"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/jsonx"
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
	// Default marks the default locale's card, the organisation. Office adds
	// the Office badge to it.
	Default, Office bool
	Open            bool
	Action          string
	Summary         []ui.FieldError
	// Inherit lists the fields this card leaves deliberately empty, each with a
	// checkbox that inherits it again; InheritName is the checkbox field name.
	Inherit     []inheritChoice
	InheritName string
	Form        template.HTML
}

// inheritChoice is one field of a card that does not inherit.
type inheritChoice struct{ ID, Key, Label string }

// notInherited is what a field of a card shows when it is left empty on
// purpose instead of falling back to the default locale.
const notInherited = "Not inherited, empty"

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

// localePlaceholders are the default locale's values an empty locale field falls back
// to, shown as ghost text. The address is a block and never falls back.
func localePlaceholders(site bizprofile.Profile, siteName string) map[string]string {
	out := map[string]string{}
	for k, v := range map[string]string{
		"name": siteName, "catalog_name": site.CatalogName, "tax_id": site.TaxID, "vat_id": site.VATID, "description": site.Description, "phone": site.Phone, "fax": site.Fax, "email": site.Email,
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
	"additional_properties",
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
	h["description"] = schemaform.UIHint{Help: "Describes this locale's local business. Empty uses the default locale's description."}
	h["catalog_name"] = schemaform.UIHint{Help: "Empty uses the default locale's value, or \"Services\" when that is empty too."}
	h["vat_id"] = schemaform.UIHint{Help: "Empty uses the default locale's VAT ID."}
	h["country_name"] = schemaform.UIHint{Help: "The country name printed in the areas served, like France."}
	h["service_radius_km"] = schemaform.UIHint{Help: "Kilometres around this locale's coordinates. Needs both coordinates."}
	h["image"] = schemaform.UIHint{Help: "A photo of this locale's business."}
	for _, k := range inheritedFields {
		help := h[k].Help
		if help != "" {
			help += " "
		}
		hint := h[k]
		hint.Help = help + "Empty uses the default locale's value."
		h[k] = hint
	}
	// A tri-state's empty choice is "Not stated": on a card it inherits.
	for _, k := range []string{"accepts_reservations", "service_provider", "public_access", "free_entry", "amenity_presets"} {
		hint := h[k]
		hint.Help = notStated + " uses the default locale's value."
		h[k] = hint
	}
	return h
}

// defaultTag is the tag of the default locale: the first entry on a site with
// one locale, the set's default otherwise.
func defaultTag(cfg *siteconfig.Config, entries []siteconfig.LocaleEntry) string {
	if set := localeSetOf(cfg); set.Multi() {
		return set.Default()
	}
	return entries[0].Tag
}

// notInheritedUI is hints with the help of every field in keys replaced: the
// card leaves it empty on purpose.
func notInheritedUI(hints map[string]schemaform.UIHint, keys []string) map[string]schemaform.UIHint {
	out := make(map[string]schemaform.UIHint, len(hints))
	for k, v := range hints {
		out[k] = v
	}
	for _, k := range keys {
		hint := out[k]
		hint.Help = notInherited + "."
		out[k] = hint
	}
	return out
}

// localeCards builds the cards of the Business details tab, the default
// locale's first. edit, when set, replaces one card's values.
func (a *Admin) localeCards(cfg *siteconfig.Config, edit *localeEdit) ([]localeCard, error) {
	entries, err := localeEntries(cfg)
	if err != nil {
		return nil, err
	}
	site, err := siteProfile(cfg)
	if err != nil {
		return nil, err
	}
	stored, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		stored = nil
	}
	def := defaultTag(cfg, entries)
	// The default locale's card leads.
	slices.SortStableFunc(entries, func(x, y siteconfig.LocaleEntry) int {
		return b2i(y.Tag == def) - b2i(x.Tag == def)
	})
	ph, hints := localePlaceholders(site, cfg.CompanyName), localeUI()
	cards := make([]localeCard, 0, len(entries))
	for _, e := range entries {
		anchor := localeAnchor(e)
		isDefault := e.Tag == def
		c := localeCard{Tag: e.Tag, Name: localeDisplayName(e.Tag), Anchor: anchor, Default: isDefault, Open: isDefault,
			Action: a.url("settings", "locales", "business", e.Tag), InheritName: locPrefix(anchor) + "inherit"}
		p, schema, cardHints, cardPh := stored[e.Tag], businessSchema(), hints, ph
		if isDefault {
			p, schema, cardHints, cardPh = site, defaultSchema(), defaultUI(), map[string]string{"name": cfg.CompanyName}
		}
		vals, errs, open := profileValues(p), schemaform.Errors(nil), ""
		if edit != nil && edit.Tag == e.Tag {
			vals, errs, open = edit.Vals, edit.Errs, edit.Open
			c.Open = true
			c.Summary = formErrors(locPrefix(anchor), errs)
			if edited, err := valuesProfile(vals); err == nil {
				p = edited
			}
		}
		if isDefault {
			c.Badge, c.BadgeKind = "Default", "success"
			c.Office = p.Office
		} else {
			c.Badge, c.BadgeKind = localeBadge(p)
			if len(p.NoInherit) > 0 {
				cardHints = notInheritedUI(hints, p.NoInherit)
				cardPh = maps.Clone(ph)
				for _, k := range p.NoInherit {
					cardPh[k] = notInherited
					c.Inherit = append(c.Inherit, inheritChoice{ID: "inherit-" + anchor + "-" + strings.ReplaceAll(k, ".", "-"), Key: k, Label: pathLabel(k) + ": use the default locale's value"})
				}
			}
		}
		form, err := schemaform.Render(schema, vals, schemaform.Options{Sections: true, AddIcon: plusIcon(), Prefix: locPrefix(anchor), UI: cardHints, Placeholders: cardPh, Errors: errs, OpenPath: open})
		if err != nil {
			return nil, err
		}
		c.Form = form
		cards = append(cards, c)
	}
	return cards, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// dropInherited removes from p.NoInherit the keys the owner chose to inherit
// again (the posted inherit checkboxes) and the keys of fields that now hold a
// value of their own.
func dropInherited(p *bizprofile.Profile, posted []string) {
	raw, _ := jsonx.Marshal(*p)
	m := map[string]any{}
	_ = jsonx.Unmarshal(raw, &m, jsonx.Stored)
	p.NoInherit = slices.DeleteFunc(p.NoInherit, func(k string) bool {
		return slices.Contains(posted, k) || profileKeySet(m, k)
	})
}

// profileKeySet reports whether the profile map holds a value at a JSON key
// ("amenity_presets.parking" is nested). A rating counts from its value and the
// parent organisation from its name, the keys the merge overrides on.
func profileKeySet(m map[string]any, key string) bool {
	v := any(m)
	for _, part := range strings.Split(key, ".") {
		mm, ok := v.(map[string]any)
		if !ok {
			return false
		}
		v = mm[part]
	}
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return true // a false only survives marshalling on a tri-state answer
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		switch key {
		case "rating":
			return profileKeySet(x, "value")
		case "parent_organisation":
			return profileKeySet(x, "name")
		}
		return len(x) > 0
	}
	return true
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
	entries, err := localeEntries(cfg)
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	i := slices.IndexFunc(entries, func(e siteconfig.LocaleEntry) bool { return e.Tag == tag })
	if i < 0 {
		a.Error(w, r, http.StatusNotFound, "Not found", "That locale is not on your site.")
		return
	}
	entry, isDefault := entries[i], tag == defaultTag(cfg, entries)
	prefix := locPrefix(localeAnchor(entry))
	stored, _ := bizprofile.ParseLocales(cfg.LocaleProfiles)
	prev, schema := stored[tag], businessSchema()
	if isDefault {
		site, err := siteProfile(cfg)
		if err != nil {
			site = bizprofile.Profile{}
		}
		prev, schema = site, defaultSchema()
	}
	vals, perrs := schemaform.Decode(schema, r.PostForm, profileValues(prev), schemaform.Options{Prefix: prefix})
	p, perr := valuesProfile(vals)
	p.Lei = strings.ToUpper(p.Lei)
	edit := &localeEdit{Tag: tag, Vals: vals, Open: openAfterAdd(r.PostForm, prefix, vals)}
	if schemaform.Structural(r.PostForm) {
		a.renderBusiness(w, r, http.StatusOK, edit)
		return
	}
	if !isDefault {
		dropInherited(&p, r.PostForm[prefix+"inherit"])
	}
	if perr == nil && len(perrs) == 0 {
		if isDefault {
			perrs = profileErrors(bizprofile.ValidateDefault(p))
		} else {
			perrs = profileErrors(bizprofile.Validate(p))
		}
	}
	if len(perrs) > 0 || perr != nil {
		edit.Errs = perrs
		a.renderBusiness(w, r, http.StatusUnprocessableEntity, edit)
		return
	}
	if isDefault {
		err = a.saveDefaultBusiness(r.Context(), p)
	} else {
		var raw jsontext.Value
		if !reflect.DeepEqual(p, bizprofile.Profile{}) {
			raw, _ = jsonx.Marshal(p)
		}
		err = a.deps.Site.SetLocaleProfile(r.Context(), tag, raw)
	}
	if err != nil {
		a.logger().Error("save locale business details failed", "locale", tag, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The business details could not be saved.")
		return
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Saved "+localeDisplayName(tag)+".")
	http.Redirect(w, r, a.url("settings", "locales", "business")+"#locale-"+localeAnchor(entry), http.StatusSeeOther)
}

// saveDefaultBusiness stores the default locale's business: phone, hours note
// and address go to their legacy columns, which win at render time, and the
// rest (the organisation address choice and coordinates included) to the
// profile column.
func (a *Admin) saveDefaultBusiness(ctx context.Context, p bizprofile.Profile) error {
	stored, phone, hours, addr := bizprofile.SplitLegacy(p)
	raw, _ := jsonx.Marshal(stored)
	_, err := a.deps.Site.Update(ctx, siteconfig.UpdateInput{Phone: &phone, BusinessHours: &hours, PostalAddress: addr, BusinessProfile: raw})
	return err
}

// orgAddressChoices are the settings of the Organisation address radios, in
// display order. The empty value is today's behaviour.
var orgAddressChoices = []ui.Option{
	{Value: "", Label: "The default locale's address on every page"},
	{Value: bizprofile.OrgAddressLocale, Label: "The locale's office; none without one"},
	{Value: bizprofile.OrgAddressLocaleOrSite, Label: "The locale's office; the default locale's address without one"},
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
	Locales []localeCard
	// Multi shows the organisation address choice, which only matters when
	// there are locale pages to choose for.
	Multi           bool
	OrgAddress      []ui.Option
	OrgAddressError string
}

// handleOrgAddressSave stores which address the Organization prints on locale
// pages. It rewrites the default business like the default card does, so
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
		a.Error(w, r, http.StatusUnprocessableEntity, "Nothing was saved", "The stored business details cannot be read, so nothing was saved. Fix them on the default locale's card first.")
		return
	}
	p.OrgAddress = v
	// The legacy columns hold phone, hours note and the five address keys; the
	// profile column holds everything else.
	stored, _, _, _ := bizprofile.SplitLegacy(p)
	raw, _ := jsonx.Marshal(stored)
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

// renderBusiness is the Business details tab: one card per locale, the default first, the card of
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
	v.Data = businessData{Locales: cards, Multi: localeSetOf(cfg).Multi(), OrgAddress: orgAddressOptions(site.OrgAddress), OrgAddressError: orgErr}
	a.Render(w, r, status, "settings/locales-business", v)
}

func (a *Admin) handleBusiness(w http.ResponseWriter, r *http.Request) {
	a.renderBusiness(w, r, http.StatusOK, nil)
}
