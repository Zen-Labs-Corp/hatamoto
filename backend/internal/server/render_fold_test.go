package server

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

// The A4 harness: the @graph of a shared page, the default locale's page and
// two other locale pages, composed from a config the way buildSiteView and
// applyLocaleSnap compose them, before and after the fold of the default
// locale's card into the default business.

const (
	foldDefault = "en-XX"
	foldBase    = "https://example.test"
)

func foldSet(t *testing.T, multi bool) *entity.LocaleSet {
	t.Helper()
	entries := []entity.LocaleEntry{{Tag: foldDefault}}
	if multi {
		entries = append(entries, entity.LocaleEntry{Tag: "fr-CA", Segment: "fr"}, entity.LocaleEntry{Tag: "de-XX", Segment: "dx"})
	}
	set, err := entity.NewLocaleSet(foldDefault, entries, false, "")
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// legacyViews are the readers as they were before the default business: the
// default locale is an ordinary tag whose card sits in locale_profiles.
type legacyViews struct{}

func (legacyViews) business(cfg *siteconfig.Config, set *entity.LocaleSet, loc string) (*render.BusinessView, error) {
	site, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		return nil, err
	}
	site = bizprofile.FromLegacy(site, cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
	cards, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		return nil, err
	}
	var card *bizprofile.Profile
	if c, ok := cards[loc]; ok {
		card = &c
	}
	res := bizprofile.Resolve(site, card)
	if !res.Office {
		return nil, nil
	}
	servicesCurrency := res.Profile.Currency
	if card == nil || len(card.Services) == 0 {
		servicesCurrency = site.Currency
	}
	return newBusinessView(res.Profile, localeNodeIDs(set)[loc], servicesCurrency), nil
}

func (legacyViews) summaries(cfg *siteconfig.Config, set *entity.LocaleSet) ([]render.LocaleSummary, error) {
	site, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		return nil, err
	}
	site = bizprofile.FromLegacy(site, cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
	cards, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		return nil, err
	}
	var out []render.LocaleSummary
	for _, tag := range set.PublicTags() {
		var card *bizprofile.Profile
		var own bizprofile.Profile
		if c, ok := cards[tag]; ok {
			card, own = &c, c
		}
		res := bizprofile.ResolveContact(site, card)
		p := res.Profile
		out = append(out, render.LocaleSummary{
			Tag: tag, Segment: strings.TrimPrefix(set.Prefix(tag), "/"), Default: tag == set.Default(),
			CountryName: p.CountryName, Phone: own.Phone, GeoRegion: p.GeoRegion,
			Language: languageName(tag), PriceRange: p.PriceRange, Currency: p.Currency,
			ContactPoints: contactPointViews(own.ContactPoints), HasOffice: res.Office,
			CatalogName: p.CatalogName, ServiceProvider: p.ServiceProvider != nil && *p.ServiceProvider,
			AdditionalProperties: propertyViews(p.AdditionalProperties),
		})
	}
	return out, nil
}

func (legacyViews) geoRegion(cfg *siteconfig.Config, loc string) string {
	site, _ := bizprofile.Parse(cfg.BusinessProfile)
	cards, _ := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if c, ok := cards[loc]; ok && c.GeoRegion != "" {
		return c.GeoRegion
	}
	return site.GeoRegion
}

// pageGraphs renders the pages of cfg and returns each page's @graph as
// marshalled JSON-LD, keyed shared, default, fr-CA and de-XX. legacy renders
// with the readers of the release before the default business.
func pageGraphs(t *testing.T, cfg *siteconfig.Config, set *entity.LocaleSet, legacy bool) map[string]string {
	t.Helper()
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	org, err := orgView(cfg)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := localeSummaries(cfg, set)
	if legacy {
		summaries, err = legacyViews{}.summaries(cfg, set)
		if !set.Multi() {
			summaries = nil
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	for i := range summaries {
		summaries[i].Home = foldBase + "/" + summaries[i].Segment
	}
	pages := map[string]string{}
	build := func(name, loc string, shared bool) {
		v := &render.PageView{
			BaseURL: foldBase, Path: "/",
			Site: render.SiteView{
				Name: "Example Site", Language: siteLanguage(cfg), Phone: cfg.Phone, Hours: cfg.BusinessHours,
				Address: postalAddress(cfg.PostalAddress), OrgAddress: orgAddressMode(cfg), Org: org,
			},
		}
		if set.Multi() {
			v.Locale, v.Segment, v.Shared, v.Locales = loc, strings.TrimPrefix(set.Prefix(loc), "/"), shared, summaries
			v.Site.Language = loc
			if !shared {
				if legacy {
					v.Business, err = legacyViews{}.business(cfg, set, loc)
					v.GeoRegion = legacyViews{}.geoRegion(cfg, loc)
				} else {
					v.Business, err = localeBusiness(cfg, set, loc)
					v.GeoRegion = localeGeoRegion(cfg, set, loc)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		var out bytes.Buffer
		if err := engine.RenderPage(&out, v); err != nil {
			t.Fatal(err)
		}
		for _, m := range ldRE.FindAllSubmatch(out.Bytes(), -1) {
			var doc struct {
				Graph json.RawMessage `json:"@graph"`
			}
			if json.Unmarshal(m[1], &doc) == nil && doc.Graph != nil {
				pages[name] = string(doc.Graph)
			}
		}
		if pages[name] == "" {
			t.Fatalf("%s: no @graph printed", name)
		}
	}
	build("shared", foldDefault, true)
	build("default", foldDefault, false)
	if set.Multi() {
		build("fr-CA", "fr-CA", false)
		build("de-XX", "de-XX", false)
	}
	return pages
}

// foldConfig is cfg after bizprofile.FoldRow, the way the upgrade stores it.
func foldConfig(t *testing.T, cfg *siteconfig.Config) *siteconfig.Config {
	t.Helper()
	row, changed, err := bizprofile.FoldRow(bizprofile.Row{
		Default: foldDefault, BusinessProfile: cfg.BusinessProfile, LocaleProfiles: cfg.LocaleProfiles,
		Phone: cfg.Phone, BusinessHours: cfg.BusinessHours, PostalAddress: cfg.PostalAddress,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := *cfg
	if changed {
		out.BusinessProfile, out.LocaleProfiles = row.BusinessProfile, row.LocaleProfiles
		out.Phone, out.BusinessHours, out.PostalAddress = row.Phone, row.BusinessHours, row.PostalAddress
	}
	return &out
}

// nodes decodes a marshalled @graph by @type (the fixtures print one node per
// type, except ContactPoint lists which live inside the Organization).
func nodes(t *testing.T, graph string) map[string]map[string]any {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal([]byte(graph), &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, n := range list {
		typ, _ := n["@type"].(string)
		out[typ] = n
	}
	return out
}

// graphDiff lists "<@type>.<key>" for every top-level key whose value differs
// between two graphs, or that only one graph has.
func graphDiff(t *testing.T, before, after string) []string {
	t.Helper()
	var diff []string
	b, a := nodes(t, before), nodes(t, after)
	for typ, bn := range b {
		an, ok := a[typ]
		if !ok {
			diff = append(diff, typ+" removed")
			continue
		}
		keys := map[string]bool{}
		for k := range bn {
			keys[k] = true
		}
		for k := range an {
			keys[k] = true
		}
		for k := range keys {
			if !reflect.DeepEqual(bn[k], an[k]) {
				diff = append(diff, typ+"."+k)
			}
		}
	}
	for typ := range a {
		if _, ok := b[typ]; !ok {
			diff = append(diff, typ+" added")
		}
	}
	sort.Strings(diff)
	return diff
}

// foldCase is a stored site before the upgrade: the site-wide profile, the
// legacy columns and the cards (the default locale's under foldDefault).
type foldCase struct {
	profile, cards, address, phone, hours string
}

func (c foldCase) config() *siteconfig.Config {
	return &siteconfig.Config{
		Language: foldDefault, Phone: c.phone, BusinessHours: c.hours,
		PostalAddress:   json.RawMessage(c.address),
		BusinessProfile: json.RawMessage(c.profile),
		LocaleProfiles:  json.RawMessage(c.cards),
	}
}

const (
	foldSiteProfile = `{"name":"Example Legal","tax_id":"T1","email":"site@example.test","sameas":["https://example.test/site"],"rating":{"value":"4","count":"9"}}`
	foldSiteAddress = `{"street":"Site Street","city":"Site City","country":"SC"}`
	foldOtherCards  = `"fr-CA":{"address":{"street":"Example Street 1","city":"Sample City"},"phone":"555 0300","country_name":"Exampleland"},"de-XX":{"no_office":true,"country_name":"Otherland"}`
)

func foldBase0(profile, defaultCard string) foldCase {
	cards := `{` + foldOtherCards + `}`
	if defaultCard != "" {
		cards = `{"` + foldDefault + `":` + defaultCard + `,` + foldOtherCards + `}`
	}
	return foldCase{profile: profile, cards: cards, address: foldSiteAddress, phone: "555 0100", hours: "Weekdays 9 to 5"}
}

// requireSamePages fails unless every page prints the same graph after the fold.
func requireSamePages(t *testing.T, c foldCase) {
	t.Helper()
	set := foldSet(t, true)
	before := pageGraphs(t, c.config(), set, true)
	after := pageGraphs(t, foldConfig(t, c.config()), set, false)
	for page, g := range before {
		if after[page] != g {
			t.Errorf("%s page changed:\nbefore %s\nafter  %s", page, g, after[page])
		}
	}
}

func TestFoldGraph_UnchangedShapes(t *testing.T) {
	t.Parallel()
	tests := map[string]foldCase{
		// A4(a): no default card, and a default card equal to the site values.
		"no default card": foldBase0(foldSiteProfile, ""),
		"card equal to site values": foldBase0(foldSiteProfile,
			`{"tax_id":"T1","email":"site@example.test","rating":{"value":"4","count":"9"},"sameas":["https://example.test/site"]}`),
		"empty default card": foldBase0(foldSiteProfile, `{}`),
		// The default card forbids an office: the default page prints none either way.
		"default card without office, org address from the locale": foldBase0(
			strings.Replace(foldSiteProfile, `{`, `{"org_address":"locale",`, 1), `{"no_office":true}`),
		"empty profile": foldBase0(`{}`, ""),
	}
	for name, c := range tests {
		t.Run(name, func(t *testing.T) { t.Parallel(); requireSamePages(t, c) })
	}
}

// A single-language site with an empty profile prints the same graph (A6).
func TestFoldGraph_SingleLanguageEmptyProfile(t *testing.T) {
	t.Parallel()
	cfg := (&siteconfig.Config{Language: foldDefault, BusinessProfile: json.RawMessage(`{}`), LocaleProfiles: json.RawMessage(`{}`)})
	set := foldSet(t, false)
	before := pageGraphs(t, cfg, set, true)
	after := pageGraphs(t, foldConfig(t, cfg), set, false)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("single-language graph changed:\nbefore %v\nafter  %v", before, after)
	}
}

// A4(b): a default card with an office and its own values. The other locales'
// pages stay identical; the Organization takes the card's values, and only the
// fields listed here change. The default page keeps its LocalBusiness.
func TestFoldGraph_DefaultCardWithOffice(t *testing.T) {
	t.Parallel()
	c := foldBase0(foldSiteProfile, `{"name":"Example Branch","tax_id":"T2","email":"branch@example.test","address":{"street":"Branch Street","city":"Branch City"},"phone":"555 0200","country_name":"Homeland"}`)
	set := foldSet(t, true)
	before := pageGraphs(t, c.config(), set, true)
	after := pageGraphs(t, foldConfig(t, c.config()), set, false)

	// Every page differs from before in the Organization only: the default
	// page's LocalBusiness and the other locales' nodes are byte-identical.
	for _, page := range []string{"shared", "default", "fr-CA", "de-XX"} {
		if d := graphDiff(t, before[page], after[page]); !reflect.DeepEqual(d, wantOrgDiff) {
			t.Errorf("%s page differs from before in %v, want %v", page, d, wantOrgDiff)
		}
	}
	for name, g := range map[string]string{"before": before["default"], "after": after["default"]} {
		lb, ok := nodes(t, g)["LocalBusiness"]
		if !ok || lb["@id"] != foldBase+"#localbusiness-en-xx" {
			t.Errorf("%s: default page LocalBusiness = %v, want one with the default id", name, lb)
		}
	}
}

// wantOrgDiff is the one-time Organization change of a site whose default card
// had its own values (here: legal name, tax id, email, phone, address); it is
// listed in the upgrade notes.
var wantOrgDiff = []string{
	"Organization.address", "Organization.contactPoint", "Organization.email",
	"Organization.legalName", "Organization.taxID", "Organization.telephone",
}

// A default card whose phone equals the site phone loses its own default-locale
// ContactPoint (documented): the site point stays and gains the hours.
func TestFoldGraph_DefaultCardPhoneEqualsSitePhone(t *testing.T) {
	t.Parallel()
	c := foldBase0(foldSiteProfile, `{"phone":"555 0100","country_name":"Homeland"}`)
	set := foldSet(t, true)
	before := pageGraphs(t, c.config(), set, true)
	after := pageGraphs(t, foldConfig(t, c.config()), set, false)
	if d := graphDiff(t, before["shared"], after["shared"]); len(d) != 0 {
		t.Errorf("shared page differs in %v, want no change", d)
	}
	for _, page := range []string{"default", "fr-CA", "de-XX"} {
		if d := graphDiff(t, before[page], after[page]); !reflect.DeepEqual(d, []string{"Organization.contactPoint"}) {
			t.Errorf("%s page differs in %v, want only the ContactPoints", page, d)
		}
	}
	cp := func(graph string) []map[string]any {
		var out []map[string]any
		for _, c := range nodes(t, graph)["Organization"]["contactPoint"].([]any) {
			out = append(out, c.(map[string]any))
		}
		return out
	}
	was, now := cp(before["fr-CA"]), cp(after["fr-CA"])
	hasHours := func(cps []map[string]any) bool {
		return slices.ContainsFunc(cps, func(c map[string]any) bool { _, ok := c["hoursAvailable"]; return ok })
	}
	if hasHours(was) || !hasHours(now) {
		t.Errorf("site ContactPoint hours: before %v, after %v; want it only after", was, now)
	}
}

// With the Organization address taken from the locale, locales without an
// office of their own print the default business's address instead of the old
// site-wide one: the Organization address and phone change on those pages.
// The default business has no name of its own here, so it takes the site-wide
// legal name, and the default page's LocalBusiness prints it (name and legalName)
// where it printed the site name before.
func TestFoldGraph_OrgAddressFromLocaleWithDefaultOffice(t *testing.T) {
	t.Parallel()
	profile := strings.Replace(foldSiteProfile, `{`, `{"org_address":"locale_or_site",`, 1)
	c := foldBase0(profile, `{"address":{"street":"Branch Street","city":"Branch City"},"phone":"555 0200"}`)
	set := foldSet(t, true)
	before := pageGraphs(t, c.config(), set, true)
	after := pageGraphs(t, foldConfig(t, c.config()), set, false)
	want := map[string][]string{
		"shared":  {"Organization.address", "Organization.contactPoint", "Organization.telephone"},
		"default": {"LocalBusiness.legalName", "LocalBusiness.name", "Organization.contactPoint"},
		"fr-CA":   {"Organization.contactPoint"},
		"de-XX":   {"Organization.address", "Organization.contactPoint", "Organization.telephone"},
	}
	for page, w := range want {
		if d := graphDiff(t, before[page], after[page]); !reflect.DeepEqual(d, w) {
			t.Errorf("%s page differs in %v, want %v", page, d, w)
		}
	}
}

// The default business's map fields never reach the Organization node.
func TestFoldGraph_OrganizationHasNoGeo(t *testing.T) {
	t.Parallel()
	c := foldBase0(foldSiteProfile, `{"address":{"street":"Branch Street","city":"Branch City","lat":"1.5","lng":"2.5","map_url":"https://example.test/map"}}`)
	after := pageGraphs(t, foldConfig(t, c.config()), foldSet(t, true), false)
	for page, g := range after {
		if org := nodes(t, g)["Organization"]; org["geo"] != nil || org["hasMap"] != nil {
			t.Errorf("%s page Organization carries geo: %v", page, org)
		}
	}
}
