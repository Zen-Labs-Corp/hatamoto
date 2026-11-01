package admin

import (
	"encoding/json"
	"net/url"
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
	for _, want := range []string{"Business name", "Business type", "No office in this locale", `<option value="Restaurant">`} {
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
	return m
}
