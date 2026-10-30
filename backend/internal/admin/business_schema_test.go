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
		if p.Name == "business_type" || p.Name == "no_office" {
			t.Errorf("site-wide schema still has %q", p.Name)
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
		Address:      bizprofile.Address{Lat: "999"},
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
