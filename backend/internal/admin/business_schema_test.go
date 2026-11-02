package admin

import (
	"encoding/json"
	"html"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/bizprofile"
)

func TestBusinessSchemaParsesAndSiteVariantDropsLocaleOnlyFields(t *testing.T) {
	if _, err := schemaform.ParseSchema(businessSchema()); err != nil {
		t.Fatal(err)
	}
	site, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	root, err := schemaform.ParseSchema(site)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range root.Props {
		for _, gone := range localeOnlyProps {
			if p.Name == gone {
				t.Errorf("site-wide schema still has %q", p.Name)
			}
		}
	}
	out, err := schemaform.Render(site, nil, schemaform.Options{Prefix: "biz."})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"Legal name", "Organisation", "Address", "Reach", "Opening hours", "Profiles &amp; rating", "Commerce"} {
		if !strings.Contains(s, want) {
			t.Errorf("site form lacks %q", want)
		}
	}
	for _, gone := range []string{"Latitude", "Longitude", "Map address", "Country name", "Image"} {
		if strings.Contains(s, gone) {
			t.Errorf("site form has the locale-only %q", gone)
		}
	}
	for _, kept := range []string{"PO box", "Business profile address", "Alternate name", "Slogan", "Founding date", "Employees from", "Employees to", "VAT ID", "Print the logo with its width and height", "Count is", "Best rating", "Worst rating"} {
		if !strings.Contains(s, kept) {
			t.Errorf("site form lacks %q", kept)
		}
	}
	card, err := schemaform.Render(businessSchema(), nil, schemaform.Options{Prefix: "biz."})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Latitude", "Longitude", "Map address", "Country name", "VAT ID", "Description", `id="sf-biz-image"`} {
		if !strings.Contains(string(card), want) {
			t.Errorf("locale card form lacks %q", want)
		}
	}
	for _, gone := range []string{"Alternate name", "Slogan", "Founding date", "Employees from", "Print the logo with its width and height"} {
		if strings.Contains(string(card), gone) {
			t.Errorf("locale card form has the site-only %q", gone)
		}
	}
	if strings.Contains(s, "Business type") || strings.Contains(s, "No office") {
		t.Error("site form shows a locale-only field")
	}
	full, err := schemaform.Render(businessSchema(), nil, schemaform.Options{Prefix: "loc_x."})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Legal name", "Business type", "No office in this locale", `<option value="Restaurant">`} {
		if !strings.Contains(string(full), want) {
			t.Errorf("locale form lacks %q", want)
		}
	}
}

func TestBusinessFormRoundTripsNewFields(t *testing.T) {
	schema, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	prev := bizprofile.Profile{Name: "Example Co", EmployeesMin: 10, EmployeesMax: 50, FoundingDate: "2009", Slogan: "Hello", LogoObject: true,
		ContactPoints: []bizprofile.ContactPoint{{Type: "sales", Name: "Sales", AreaServed: "Europe", AvailableLanguage: "English"}},
		Rating:        bizprofile.Rating{Value: "4.8", Best: "5", Worst: "1", Count: "120", CountType: "reviews"}}
	html, err := schemaform.Render(schema, profileMap(t, prev), schemaform.Options{Prefix: "biz."})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`value="10"`, `value="50"`, `value="2009"`, `value="Europe"`, "Languages", `<option value="reviews" selected>`} {
		if !strings.Contains(string(html), want) {
			t.Errorf("form lacks %q", want)
		}
	}
	// Posting the rendered values back keeps the integers as integers.
	form := url.Values{"biz.name": {"Example Co"}, "biz.employees_min": {"10"}, "biz.employees_max": {"50"}, "biz.rating.count_type": {"reviews"}}
	vals, errs := schemaform.Decode(schema, form, profileMap(t, prev), schemaform.Options{Prefix: "biz."})
	if len(errs) != 0 {
		t.Fatalf("decode errors = %v", errs)
	}
	got, err := valuesProfile(vals)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmployeesMin != 10 || got.EmployeesMax != 50 || got.Rating.CountType != "reviews" {
		t.Errorf("profile = %+v", got)
	}
}

func TestBusinessFormRoundTripsRepeaterRows(t *testing.T) {
	prev := bizprofile.Profile{Name: "Example Co", GeoRegion: "zz"}
	pm := profileMap(t, prev)
	form := url.Values{
		"loc.name":                            {"Example Branch"},
		"loc.business_type":                   {"Restaurant"},
		"loc.address.street":                  {"1 Example Street"},
		"loc.address.city":                    {"Exampleton"},
		"_present.loc.opening_hours":          {"1"},
		"_present.loc.opening_hours.0.days":   {"1"},
		"loc.opening_hours.0.days":            {"Monday, Tuesday"},
		"loc.opening_hours.0.opens":           {"09:00"},
		"loc.opening_hours.0.closes":          {"17:30"},
		"_present.loc.special_hours":          {"1"},
		"loc.special_hours.0.from":            {"2030-01-01"},
		"_present.loc.special_hours.0.closed": {"1"},
		"loc.special_hours.0.closed":          {"true"},
		"_present.loc.sameas":                 {"1"},
		"loc.sameas.0":                        {"https://example.test/profile"},
		"loc.rating.value":                    {"4.5"},
	}
	out, errs := schemaform.Decode(businessSchema(), form, pm, schemaform.Options{Prefix: "loc."})
	if len(errs) > 0 {
		t.Fatalf("decode errors: %v", errs)
	}
	raw, _ := json.Marshal(out)
	got, err := bizprofile.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Example Branch" || got.BusinessType != "Restaurant" || got.Address.City != "Exampleton" || got.GeoRegion != "zz" {
		t.Errorf("profile = %+v", got)
	}
	if len(got.OpeningHours) != 1 || strings.Join(got.OpeningHours[0].Days, ",") != "Monday,Tuesday" || got.OpeningHours[0].Closes != "17:30" {
		t.Errorf("opening hours = %+v", got.OpeningHours)
	}
	if len(got.SpecialHours) != 1 || !got.SpecialHours[0].Closed || got.SpecialHours[0].From != "2030-01-01" {
		t.Errorf("special hours = %+v", got.SpecialHours)
	}
	if len(got.SameAs) != 1 || got.Rating.Value != "4.5" {
		t.Errorf("sameas/rating = %+v %+v", got.SameAs, got.Rating)
	}
	if msgs := bizprofile.Validate(got); len(msgs) != 0 {
		t.Errorf("a valid profile reports %v", msgs)
	}
}

func TestBusinessValidationMessagesLandOnFieldIDs(t *testing.T) {
	p := bizprofile.Profile{
		Address:      bizprofile.Address{Lat: "999", Lng: "10"},
		OpeningHours: []bizprofile.Hours{{Opens: "9am"}},
	}
	errs := profileErrors(bizprofile.Validate(p))
	if len(errs["address.lat"]) == 0 || len(errs["opening_hours.0.opens"]) == 0 {
		t.Fatalf("errors = %v", errs)
	}
	out, err := schemaform.Render(businessSchema(), profileMap(t, p), schemaform.Options{Prefix: "biz.", Errors: errs})
	if err != nil {
		t.Fatal(err)
	}
	summary := formErrors("biz.", errs)
	for _, e := range summary {
		if !strings.Contains(string(out), `id="`+e.ID+`"`) {
			t.Errorf("summary id %q has no field in the form", e.ID)
		}
	}
	if len(summary) != 2 || summary[0].Label != "Address Latitude" || summary[1].Label != "Opening hours 1 Opens" {
		t.Errorf("summary = %+v", summary)
	}
}

// Every path Validate can flag must be a field of the form it is reported on.
func TestBusinessValidationPathsOfNewFieldsLandOnFieldIDs(t *testing.T) {
	site, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	p := bizprofile.Profile{
		Description: strings.Repeat("x", 5000), AlternateName: strings.Repeat("x", 5000), Slogan: strings.Repeat("x", 5000),
		FoundingDate: "soon", EmployeesMin: 9, EmployeesMax: 2, VATID: strings.Repeat("x", 5000),
		ContactPoints: []bizprofile.ContactPoint{{Name: strings.Repeat("x", 5000), AreaServed: strings.Repeat("x", 5000), AvailableLanguage: strings.Repeat("x", 5000)}},
		Rating:        bizprofile.Rating{Value: "0x1p3", Best: "NaN", Worst: "Inf", CountType: "both"},
	}
	msgs := bizprofile.Validate(p)
	errs := profileErrors(msgs)
	out, err := schemaform.Render(site, profileMap(t, p), schemaform.Options{Prefix: "biz.", Errors: errs})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 10 {
		t.Fatalf("only %d errors: %v", len(msgs), msgs)
	}
	for _, e := range formErrors("biz.", errs) {
		if !strings.Contains(string(out), `id="`+e.ID+`"`) {
			t.Errorf("summary id %q (%s) has no field in the site form", e.ID, e.Label)
		}
	}
}

func TestLocaleDisplayName(t *testing.T) {
	for tag, want := range map[string]string{"fr": "French", "fr-CA": "Canadian French", "not a tag": "not a tag", "": ""} {
		got := localeDisplayName(tag)
		if tag == "fr-CA" {
			if got == "" || got == "fr-CA" {
				t.Errorf("localeDisplayName(%q) = %q, want a name", tag, got)
			}
			continue
		}
		if got != want {
			t.Errorf("localeDisplayName(%q) = %q, want %q", tag, got, want)
		}
	}
}

// profileMap is the form's view of a profile.
func profileMap(t *testing.T, p bizprofile.Profile) map[string]any {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	triToForm(m)
	return m
}

// A fully filled form posts into a Profile, and the Profile renders back with
// every new field present as a form id.
func TestBusinessFormRoundTripsExtendedFields(t *testing.T) {
	schema, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	want := bizprofile.Profile{
		Naics: "541511", Isic: "6201", Duns: "123456789", Lei: "5493001KJTIIGC8Y1R12", Gln: "1234567890123",
		Founders:  []bizprofile.Person{{Name: "Ada Example", URL: "https://example.test/ada"}},
		ParentOrg: bizprofile.Person{Name: "Example Holding", URL: "https://example.test"},
		Brands:    []string{"Example Brand"}, PaymentMethods: []string{"Cash"},
		AcceptsReservations: &yes, BookingURL: "https://example.test/book", OrderURL: "https://example.test/order", MenuURL: "https://example.test/menu",
		ServiceRadiusKm: "25",
		Services:        []bizprofile.Service{{Name: "Repair", Description: "Fixes things.", URL: "https://example.test/repair", Price: "49.90"}},
		KnowsAbout:      []string{"Plumbing"}, Awards: []string{"Best Example 2030"},
		Memberships:    []bizprofile.Person{{Name: "Example Guild", URL: "https://example.test/guild"}},
		AmenityPresets: bizprofile.AmenityPresets{Parking: &yes, Wifi: &no},
		Amenities:      []bizprofile.Amenity{{Name: "Pets welcome", Value: true}, {Name: "Smoking", Value: false}},
		PublicAccess:   &yes, FreeEntry: &no,
	}
	if msgs := bizprofile.Validate(want); len(msgs) != 0 {
		t.Fatalf("fixture invalid: %v", msgs)
	}
	html, err := schemaform.Render(schema, profileMap(t, want), schemaform.Options{Prefix: "biz."})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"naics", "isic", "duns", "lei", "gln", "founders-0-name", "founders-0-url", "parent_organisation-name", "parent_organisation-url",
		"brands", "payment_methods", "accepts_reservations", "booking_url", "order_url", "menu_url", "service_radius_km",
		"services-0-name", "services-0-description", "services-0-url", "services-0-price", "knows_about", "awards",
		"memberships-0-name", "memberships-0-url", "amenity_presets-parking", "amenity_presets-wheelchair_access", "amenity_presets-wifi",
		"amenity_presets-toilets", "amenity_presets-air_conditioning", "amenities-0-name", "amenities-0-value", "public_access", "free_entry"} {
		if !strings.Contains(string(html), `id="sf-biz-`+id+`"`) {
			t.Errorf("form lacks id sf-biz-%s", id)
		}
	}
	// Post the filled values back as a browser would: switches carry a
	// _present marker, and an unticked one posts nothing.
	form := url.Values{
		"biz.naics": {"541511"}, "biz.isic": {"6201"}, "biz.duns": {"123456789"}, "biz.lei": {"5493001KJTIIGC8Y1R12"}, "biz.gln": {"1234567890123"},
		"_present.biz.founders": {"1"}, "biz.founders.0.name": {"Ada Example"}, "biz.founders.0.url": {"https://example.test/ada"},
		"biz.parent_organisation.name": {"Example Holding"}, "biz.parent_organisation.url": {"https://example.test"},
		"_present.biz.brands":          {"1"},
		"biz.brands":                   {"Example Brand"},
		"_present.biz.payment_methods": {"1"},
		"biz.payment_methods":          {"Cash"},
		"biz.accepts_reservations":     {"yes"},
		"biz.booking_url":              {"https://example.test/book"}, "biz.order_url": {"https://example.test/order"}, "biz.menu_url": {"https://example.test/menu"},
		"biz.service_radius_km": {"25"},
		"_present.biz.services": {"1"}, "biz.services.0.name": {"Repair"}, "biz.services.0.description": {"Fixes things."},
		"biz.services.0.url": {"https://example.test/repair"}, "biz.services.0.price": {"49.90"},
		"_present.biz.knows_about": {"1"},
		"biz.knows_about":          {"Plumbing"},
		"_present.biz.awards":      {"1"},
		"biz.awards":               {"Best Example 2030"},
		"_present.biz.memberships": {"1"}, "biz.memberships.0.name": {"Example Guild"}, "biz.memberships.0.url": {"https://example.test/guild"},
		"biz.amenity_presets.parking": {"yes"}, "biz.amenity_presets.wifi": {"no"},
		"_present.biz.amenities": {"1"}, "biz.amenities.0.name": {"Pets welcome"}, "_present.biz.amenities.0.value": {"1"}, "biz.amenities.0.value": {"true"},
		"biz.amenities.1.name": {"Smoking"}, "_present.biz.amenities.1.value": {"1"},
		"biz.public_access": {"yes"}, "biz.free_entry": {"no"},
	}
	vals, errs := schemaform.Decode(schema, form, nil, schemaform.Options{Prefix: "biz."})
	if len(errs) != 0 {
		t.Fatalf("decode errors = %v", errs)
	}
	got, err := valuesProfile(vals)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Errorf("round trip\n got %s\nwant %s", g, w)
	}
}

// A switch nobody touched stays unset, so a locale card keeps inheriting it and
// an empty card is still deleted.
func TestBusinessFormUntouchedSwitchesStayUnset(t *testing.T) {
	vals, errs := schemaform.Decode(businessSchema(), url.Values{"loc.name": {"Example Branch"}}, nil, schemaform.Options{Prefix: "loc."})
	if len(errs) != 0 {
		t.Fatalf("decode errors = %v", errs)
	}
	got, err := valuesProfile(vals)
	if err != nil {
		t.Fatal(err)
	}
	got.Name = ""
	if !reflect.DeepEqual(got, bizprofile.Profile{}) {
		raw, _ := json.Marshal(got)
		t.Errorf("untouched form left %s", raw)
	}
}

// A repeater row with only a switch set is a nameless row; Validate flags it on
// an id the form has.
func TestBusinessNamelessAmenityRowLandsOnFieldID(t *testing.T) {
	p := bizprofile.Profile{Amenities: []bizprofile.Amenity{{Value: true}}}
	errs := profileErrors(bizprofile.Validate(p))
	if len(errs["amenities.0.name"]) == 0 {
		t.Fatalf("errors = %v", errs)
	}
	out, err := schemaform.Render(businessSchema(), profileMap(t, p), schemaform.Options{Prefix: "biz.", Errors: errs})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range formErrors("biz.", errs) {
		if !strings.Contains(string(out), `id="`+e.ID+`"`) {
			t.Errorf("summary id %q has no field in the form", e.ID)
		}
	}
}

// Every new Validate path is a field of the card it is reported on.
func TestBusinessExtendedValidationPathsLandOnFieldIDs(t *testing.T) {
	long := strings.Repeat("x", 5000)
	p := bizprofile.Profile{
		Naics: "x", Isic: "x", Duns: "x", Lei: "x", Gln: "x", BookingURL: "nope", OrderURL: "nope", MenuURL: "nope", ServiceRadiusKm: "far",
		Founders:    []bizprofile.Person{{URL: "nope"}},
		Memberships: []bizprofile.Person{{Name: long}},
		Brands:      []string{long}, PaymentMethods: []string{long}, KnowsAbout: []string{long}, Awards: []string{long},
		Services:  []bizprofile.Service{{Price: "cheap", URL: "nope"}},
		Amenities: []bizprofile.Amenity{{Value: true}},
	}
	msgs := bizprofile.Validate(p)
	errs := profileErrors(msgs)
	if len(msgs) < 10 {
		t.Fatalf("only %d errors: %v", len(msgs), msgs)
	}
	site, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	for name, schema := range map[string]json.RawMessage{"site": site, "locale": businessSchema()} {
		out, err := schemaform.Render(schema, profileMap(t, p), schemaform.Options{Prefix: "biz.", Errors: errs})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range formErrors("biz.", errs) {
			if !strings.Contains(string(out), `id="`+e.ID+`"`) {
				t.Errorf("%s form: summary id %q (%s) has no field", name, e.ID, e.Label)
			}
		}
	}
}

func TestBusinessSchemasLabelTheLegalName(t *testing.T) {
	site, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	for name, schema := range map[string]json.RawMessage{"site": site, "locale": businessSchema()} {
		out, err := schemaform.Render(schema, nil, schemaform.Options{Prefix: "biz."})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "Legal name") || strings.Contains(string(out), "Business name") {
			t.Errorf("%s form does not label the name Legal name", name)
		}
	}
}

var (
	hiddenInput = regexp.MustCompile(`<input[^>]*type="(?:hidden|text)"[^>]*>`)
	selectBlock = regexp.MustCompile(`(?s)<select[^>]*name="([^"]+)"[^>]*>(.*?)</select>`)
	attrValue   = func(tag, name string) string {
		m := regexp.MustCompile(`\s` + name + `="([^"]*)"`).FindStringSubmatch(tag)
		if m == nil {
			return ""
		}
		return html.UnescapeString(m[1])
	}
)

// browserPost is what a browser submits for the rendered form untouched: every
// hidden and text input and the selected option of every select.
func browserPost(rendered string) url.Values {
	form := url.Values{}
	for _, tag := range hiddenInput.FindAllString(rendered, -1) {
		form.Add(attrValue(tag, "name"), attrValue(tag, "value"))
	}
	for _, m := range selectBlock.FindAllStringSubmatch(rendered, -1) {
		val := ""
		for _, o := range regexp.MustCompile(`<option[^>]*>`).FindAllString(m[2], -1) {
			if strings.Contains(o, " selected") {
				val = attrValue(o, "value")
			}
		}
		form.Set(m[1], val)
	}
	return form
}

// A tri-state field set to "no" can be put back to "Not stated".
func TestBusinessFormTriStateReturnsToUnset(t *testing.T) {
	schema, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	no := false
	prev := bizprofile.Profile{AcceptsReservations: &no, PublicAccess: &no, AmenityPresets: bizprofile.AmenityPresets{Wifi: &no}}
	opt := schemaform.Options{Prefix: "biz.", UI: businessUI}
	out, err := schemaform.Render(schema, profileValues(prev), opt)
	if err != nil {
		t.Fatal(err)
	}
	form := browserPost(string(out))
	// Untouched, the stored "no" survives the round trip.
	vals, errs := schemaform.Decode(schema, form, profileValues(prev), opt)
	if len(errs) != 0 {
		t.Fatalf("decode errors = %v", errs)
	}
	got, err := valuesProfile(vals)
	if err != nil {
		t.Fatal(err)
	}
	if got.AcceptsReservations == nil || *got.AcceptsReservations || got.AmenityPresets.Wifi == nil || got.PublicAccess == nil {
		t.Fatalf("untouched post changed the profile: %+v", got)
	}
	// Choosing "Not stated" on each removes the key.
	form.Set("biz.accepts_reservations", "")
	form.Set("biz.public_access", "")
	form.Set("biz.amenity_presets.wifi", "")
	vals, errs = schemaform.Decode(schema, form, profileValues(prev), opt)
	if len(errs) != 0 {
		t.Fatalf("decode errors = %v", errs)
	}
	got, err = valuesProfile(vals)
	if err != nil {
		t.Fatal(err)
	}
	if got.AcceptsReservations != nil || got.PublicAccess != nil || got.AmenityPresets.Wifi != nil {
		raw, _ := json.Marshal(got)
		t.Errorf("Not stated left %s", raw)
	}
	if !strings.Contains(string(out), `<option value="">Not stated</option>`) {
		t.Error("the form does not offer Not stated")
	}
}

// The parent organisation prints on the Organization only, so a locale card
// has no field for it.
func TestParentOrganisationIsSiteOnly(t *testing.T) {
	card, err := schemaform.Render(businessSchema(), nil, schemaform.Options{Prefix: "loc."})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(card), "Parent organisation") {
		t.Error("the locale card offers Parent organisation")
	}
	site, _ := siteSchema()
	out, err := schemaform.Render(site, nil, schemaform.Options{Prefix: "biz."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Parent organisation") {
		t.Error("the site form lacks Parent organisation")
	}
}

// Awards and topics are one per line: a comma stays inside the title.
func TestAwardsAndTopicsKeepCommas(t *testing.T) {
	schema, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"_present.biz.awards": {"1"}, "biz.awards": {"Best Restaurant, Sydney 2030\r\nSecond Award"},
		"_present.biz.knows_about": {"1"}, "biz.knows_about": {"Pipes, drains\nBoilers"},
	}
	vals, errs := schemaform.Decode(schema, form, nil, schemaform.Options{Prefix: "biz."})
	if len(errs) != 0 {
		t.Fatalf("decode errors = %v", errs)
	}
	p, err := valuesProfile(vals)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Awards, []string{"Best Restaurant, Sydney 2030", "Second Award"}) ||
		!reflect.DeepEqual(p.KnowsAbout, []string{"Pipes, drains", "Boilers"}) {
		t.Errorf("awards = %q, knows_about = %q", p.Awards, p.KnowsAbout)
	}
}

// An "Other amenities" row that was added and never touched is dropped on
// save, like a blank row of any other repeater.
func TestUntouchedAddedAmenityRowIsDropped(t *testing.T) {
	schema, err := siteSchema()
	if err != nil {
		t.Fatal(err)
	}
	opt := schemaform.Options{Prefix: "biz.", UI: businessUI}
	vals, _ := schemaform.Decode(schema, url.Values{"_present.biz.amenities": {"1"}, "_add.biz.amenities": {"1"}}, nil, opt)
	if items, _ := vals["amenities"].([]any); len(items) != 1 {
		t.Fatalf("add button left %v", vals["amenities"])
	}
	out, err := schemaform.Render(schema, vals, opt)
	if err != nil {
		t.Fatal(err)
	}
	vals, errs := schemaform.Decode(schema, browserPost(string(out)), nil, opt)
	if len(errs) != 0 {
		t.Fatalf("decode errors = %v", errs)
	}
	p, err := valuesProfile(vals)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Amenities) != 0 {
		t.Errorf("untouched row kept: %+v", p.Amenities)
	}
}
