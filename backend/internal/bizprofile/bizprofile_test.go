package bizprofile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParse_EmptyAndObject(t *testing.T) {
	for _, raw := range []string{"", "  ", "null", "{}"} {
		p, err := Parse([]byte(raw))
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		if p.Phone != "" || p.Address != (Address{}) || len(p.SameAs) != 0 {
			t.Errorf("Parse(%q) = %+v, want zero", raw, p)
		}
	}
	if _, err := Parse([]byte("{bad")); err == nil {
		t.Error("Parse of invalid JSON: want error")
	}
	p, err := Parse([]byte(`{"tax_id":"X1","unknown":1,"rating":{"value":"4.5"}}`))
	if err != nil || p.TaxID != "X1" || p.Rating.Value != "4.5" {
		t.Errorf("Parse = %+v, %v", p, err)
	}
}

func TestParseLocales(t *testing.T) {
	m, err := ParseLocales([]byte(`{"xx-a":{"phone":"1"},"xx-b":{"no_office":true}}`))
	if err != nil || m["xx-a"].Phone != "1" || !m["xx-b"].NoOffice {
		t.Errorf("ParseLocales = %+v, %v", m, err)
	}
	if m, err := ParseLocales([]byte("{}")); err != nil || len(m) != 0 {
		t.Errorf("ParseLocales({}) = %v, %v", m, err)
	}
	if m, err := ParseLocales(nil); err != nil || m != nil {
		t.Errorf("ParseLocales(nil) = %v, %v", m, err)
	}
	if _, err := ParseLocales([]byte("[1]")); err == nil {
		t.Error("ParseLocales of an array: want error")
	}
}

func TestFromLegacy_LegacyWins(t *testing.T) {
	stored := Profile{
		Phone:     "stale",
		HoursNote: "stale",
		Address:   Address{Street: "stale", POBox: "Box 1", Lat: "1.5"},
		TaxID:     "T1",
	}
	addr := json.RawMessage(`{"street":"Example Street 1","city":"Sample City","state":"S","postal_code":"1000","country":"EX"}`)
	got := FromLegacy(stored, "555 0100", "Weekdays", addr)
	want := Address{Street: "Example Street 1", City: "Sample City", State: "S", PostalCode: "1000", Country: "EX", POBox: "Box 1", Lat: "1.5"}
	if got.Phone != "555 0100" || got.HoursNote != "Weekdays" || got.Address != want || got.TaxID != "T1" {
		t.Errorf("FromLegacy = %+v", got)
	}
	empty := FromLegacy(stored, "", "", nil)
	if empty.Phone != "" || empty.HoursNote != "" || empty.Address.Street != "" || empty.Address.POBox != "Box 1" {
		t.Errorf("FromLegacy with empty legacy = %+v", empty)
	}
}

func TestResolve_Office(t *testing.T) {
	site := Profile{Phone: "site-phone", Address: Address{Street: "Site Street", City: "Site City"}}
	tests := []struct {
		name   string
		loc    *Profile
		office bool
	}{
		{"nil card", nil, false},
		{"empty card", &Profile{}, false},
		{"street only", &Profile{Address: Address{Street: "Example Street"}}, true},
		{"city only", &Profile{Address: Address{City: "Sample City"}}, true},
		{"state and postal only", &Profile{Address: Address{State: "S", PostalCode: "1"}}, false},
		{"no office wins", &Profile{NoOffice: true, Address: Address{Street: "Example Street"}}, false},
	}
	for _, tt := range tests {
		got := Resolve(site, tt.loc)
		if got.Office != tt.office {
			t.Errorf("%s: Office = %v, want %v", tt.name, got.Office, tt.office)
		}
		if !tt.office && got.Profile.Address != (Address{}) {
			t.Errorf("%s: address leaked: %+v", tt.name, got.Profile.Address)
		}
	}
}

func TestResolve_AddressIsABlock(t *testing.T) {
	site := Profile{Address: Address{Street: "Site Street", City: "Site City", Country: "SC", Lat: "9"}}
	got := Resolve(site, &Profile{Address: Address{City: "Sample City"}})
	if got.Profile.Address != (Address{City: "Sample City"}) {
		t.Errorf("address = %+v, want the card's block only", got.Profile.Address)
	}
}

func TestResolve_FieldFallback(t *testing.T) {
	site := Profile{
		Name: "Legal Name", TaxID: "T-site", Phone: "site-phone", Fax: "site-fax", Email: "site@example.test",
		HoursNote: "site hours", Currency: "USD", PriceRange: "$$", GeoRegion: "XX-S",
		AreaServed:   []string{"Site Area"},
		SameAs:       []string{"https://example.test/site"},
		OpeningHours: []Hours{{Days: []string{"Monday"}, Opens: "09:00", Closes: "17:00"}},
		Rating:       Rating{Value: "4", Count: "10", Best: "5", Worst: "1", CountType: "reviews"},
		VATID:        "V-site", Description: "Site description",
	}
	t.Run("empty card inherits", func(t *testing.T) {
		got := Resolve(site, &Profile{Address: Address{Street: "Example Street"}}).Profile
		if got.Name != "" {
			t.Errorf("Name = %q, want the card's own override only", got.Name)
		}
		if got.BusinessType != DefaultType {
			t.Errorf("BusinessType = %q", got.BusinessType)
		}
		if got.TaxID != "T-site" || got.Phone != "site-phone" || got.Fax != "site-fax" || got.Email != "site@example.test" ||
			got.HoursNote != "site hours" || got.Currency != "USD" || got.PriceRange != "$$" || got.GeoRegion != "XX-S" {
			t.Errorf("text fields did not fall back: %+v", got)
		}
		if len(got.AreaServed) != 1 || len(got.SameAs) != 1 || len(got.OpeningHours) != 1 || got.Rating != site.Rating {
			t.Errorf("list or rating did not fall back: %+v", got)
		}
		if got.VATID != "V-site" || got.Description != "Site description" {
			t.Errorf("vat_id or description did not fall back: %+v", got)
		}
		if got.CountryName != "" || got.Image != "" {
			t.Errorf("country_name and image are card-only: %+v", got)
		}
	})
	t.Run("card overrides field by field", func(t *testing.T) {
		loc := &Profile{
			Name: "Branch", BusinessType: "Restaurant", Phone: "own-phone",
			Address:    Address{Street: "Example Street"},
			AreaServed: []string{"Own Area"},
			Rating:     Rating{Value: "5", Count: "2", Best: "10", Worst: "0", CountType: "ratings"},
			VATID:      "V-own", Description: "Own description", CountryName: "Exampleland", Image: "/media/x.png",
		}
		got := Resolve(site, loc).Profile
		if got.Name != "Branch" || got.BusinessType != "Restaurant" || got.Phone != "own-phone" {
			t.Errorf("overrides lost: %+v", got)
		}
		if got.Fax != "site-fax" || got.TaxID != "T-site" {
			t.Errorf("other fields should still fall back: %+v", got)
		}
		if len(got.AreaServed) != 1 || got.AreaServed[0] != "Own Area" || got.Rating.Value != "5" {
			t.Errorf("list or rating override lost: %+v", got)
		}
		if got.Rating.Best != "10" || got.Rating.Worst != "0" || got.Rating.CountType != "ratings" {
			t.Errorf("rating bounds travel with the card rating: %+v", got.Rating)
		}
		if got.VATID != "V-own" || got.Description != "Own description" || got.CountryName != "Exampleland" || got.Image != "/media/x.png" {
			t.Errorf("new field overrides lost: %+v", got)
		}
	})
}

func TestValidate_Valid(t *testing.T) {
	p := Profile{
		Name: "Example", BusinessType: "Restaurant", Phone: "555 0100", Email: "hello@example.test",
		Address:       Address{Street: "Example Street", Lat: "-33.5", Lng: "151.2", MapURL: "https://example.test/map"},
		OpeningHours:  []Hours{{Days: []string{"Monday", "Friday"}, Opens: "09:00", Closes: "17:30"}},
		SpecialHours:  []SpecialHours{{From: "2030-12-24", To: "2030-12-26", Closed: true}},
		ContactPoints: []ContactPoint{{Type: "sales", Phone: "555", Email: "sales@example.test"}},
		SameAs:        []string{"http://example.test/a"},
		Currency:      "usd",
		Rating:        Rating{Value: "4.5", Count: "12"},
	}
	if errs := Validate(p); len(errs) != 0 {
		t.Errorf("Validate = %v, want none", errs)
	}
	if errs := Validate(Profile{}); len(errs) != 0 {
		t.Errorf("Validate(zero) = %v", errs)
	}
}

func TestValidate_Errors(t *testing.T) {
	long := strings.Repeat("a", 201)
	tests := []struct {
		name string
		p    Profile
		path string
	}{
		{"name too long", Profile{Name: long}, "name"},
		{"tax id too long", Profile{TaxID: long}, "tax_id"},
		{"street too long", Profile{Address: Address{Street: long}}, "address.street"},
		{"phone over 50", Profile{Phone: strings.Repeat("1", 51)}, "phone"},
		{"fax over 50", Profile{Fax: strings.Repeat("1", 51)}, "fax"},
		{"hours note", Profile{HoursNote: long}, "hours_note"},
		{"unknown type", Profile{BusinessType: "Spaceship"}, "business_type"},
		{"bad email", Profile{Email: "not-an-email"}, "email"},
		{"map url scheme", Profile{Address: Address{MapURL: "ftp://example.test/m"}}, "address.map_url"},
		{"gbp url no scheme", Profile{Address: Address{GBPURL: "example.test"}}, "address.gbp_url"},
		{"javascript url", Profile{SameAs: []string{"https://example.test/ok", "javascript:alert(1)"}}, "sameas.1"},
		{"lat high", Profile{Address: Address{Lat: "90.1"}}, "address.lat"},
		{"lat NaN", Profile{Address: Address{Lat: "NaN"}}, "address.lat"},
		{"lng Inf", Profile{Address: Address{Lng: "+Inf"}}, "address.lng"},
		{"rating NaN", Profile{Rating: Rating{Value: "NaN"}}, "rating.value"},
		{"rating Inf", Profile{Rating: Rating{Value: "Inf"}}, "rating.value"},
		{"lat text", Profile{Address: Address{Lat: "north"}}, "address.lat"},
		{"lng low", Profile{Address: Address{Lng: "-180.5"}}, "address.lng"},
		{"time", Profile{OpeningHours: []Hours{{}, {Opens: "9:00"}}}, "opening_hours.1.opens"},
		{"time 24", Profile{OpeningHours: []Hours{{Closes: "24:00"}}}, "opening_hours.0.closes"},
		{"day", Profile{OpeningHours: []Hours{{Days: []string{"Funday"}}}}, "opening_hours.0.days"},
		{"special date", Profile{SpecialHours: []SpecialHours{{From: "31-01-2030"}}}, "special_hours.0.from"},
		{"special time", Profile{SpecialHours: []SpecialHours{{To: "2030-01-01", Opens: "25:00"}}}, "special_hours.0.opens"},
		{"contact phone", Profile{ContactPoints: []ContactPoint{{Phone: strings.Repeat("1", 51)}}}, "contact_points.0.phone"},
		{"contact email", Profile{ContactPoints: []ContactPoint{{Email: "x"}}}, "contact_points.0.email"},
		{"area long", Profile{AreaServed: []string{"ok", long}}, "area_served.1"},
		{"currency", Profile{Currency: "dollars"}, "currency"},
		{"rating high", Profile{Rating: Rating{Value: "5.1"}}, "rating.value"},
		{"rating count", Profile{Rating: Rating{Value: "4", Count: "0"}}, "rating.count"},
		{"count without value", Profile{Rating: Rating{Count: "3"}}, "rating.value"},
	}
	for _, tt := range tests {
		errs := Validate(tt.p)
		if errs[tt.path] == "" {
			t.Errorf("%s: no error at %q, got %v", tt.name, tt.path, errs)
		}
	}
}

func TestValidate_Boundaries(t *testing.T) {
	ok := Profile{
		Name:    strings.Repeat("a", 200),
		Phone:   strings.Repeat("1", 50),
		Address: Address{Lat: "90", Lng: "-180"},
	}
	if errs := Validate(ok); len(errs) != 0 {
		t.Errorf("Validate at the limits = %v, want none", errs)
	}
}

func TestResolveContact_NoOfficeStillResolves(t *testing.T) {
	site := Profile{Phone: "site-phone", ContactPoints: []ContactPoint{{Type: "sales"}}, Currency: "USD"}
	loc := &Profile{NoOffice: true, Phone: "own-phone", CountryName: "Exampleland"}
	if r := Resolve(site, loc); r.Office || r.Profile.Phone != "" {
		t.Fatalf("Resolve must stay zero without an office: %+v", r)
	}
	r := ResolveContact(site, loc)
	if r.Office {
		t.Error("Office = true for a NoOffice card")
	}
	if r.Profile.Phone != "own-phone" || r.Profile.CountryName != "Exampleland" || r.Profile.Currency != "USD" || len(r.Profile.ContactPoints) != 1 {
		t.Errorf("ResolveContact = %+v", r.Profile)
	}
	if r := ResolveContact(site, nil); r.Office || r.Profile.Phone != "site-phone" {
		t.Errorf("nil card = %+v", r)
	}
	if r := ResolveContact(site, &Profile{Address: Address{City: "Example City"}}); !r.Office {
		t.Error("a card with a city has an office")
	}
}

func TestProfile_EmptyMarshalsUnchanged(t *testing.T) {
	b, err := json.Marshal(Profile{})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"address":{},"rating":{}}`; string(b) != want {
		t.Errorf("empty profile = %s, want %s", b, want)
	}
}

func TestProfile_NewFieldsRoundTrip(t *testing.T) {
	in := Profile{
		Description: "d", AlternateName: "alt", Slogan: "s", FoundingDate: "2009-05",
		EmployeesMin: 10, EmployeesMax: 50, VATID: "V1", CountryName: "Exampleland", Image: "/media/i.png",
		LogoObject: true, OrgAddress: OrgAddressLocaleOrSite,
		ContactPoints: []ContactPoint{{Type: "sales", Name: "Sales", AreaServed: "XX", AvailableLanguage: "English"}},
		Rating:        Rating{Value: "4", Best: "5", Worst: "1", Count: "9", CountType: "reviews"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.ContactPoints[0] != in.ContactPoints[0] || out.Rating != in.Rating {
		t.Errorf("nested round trip: %+v", out)
	}
	out.ContactPoints, in.ContactPoints = nil, nil
	if out.Description != in.Description || out.EmployeesMin != 10 || out.EmployeesMax != 50 || out.FoundingDate != "2009-05" ||
		!out.LogoObject || out.OrgAddress != OrgAddressLocaleOrSite || out.VATID != "V1" || out.CountryName != "Exampleland" ||
		out.Image != "/media/i.png" || out.AlternateName != "alt" || out.Slogan != "s" {
		t.Errorf("round trip: %+v", out)
	}
	// The admin form decodes numbers as float64: 10 marshals as 10 and parses back as an int.
	var m map[string]any
	if err := json.Unmarshal([]byte(`{"employees_min":10,"employees_max":50}`), &m); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	p, err := Parse(raw)
	if err != nil || p.EmployeesMin != 10 || p.EmployeesMax != 50 {
		t.Errorf("float64 round trip: %+v, %v", p, err)
	}
}
