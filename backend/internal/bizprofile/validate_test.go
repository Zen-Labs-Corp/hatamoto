package bizprofile

import (
	"strings"
	"testing"
)

func TestParseDecimal(t *testing.T) {
	for _, v := range []string{"0", "5", "-33.5", "151.25", "0.0", "+51.5", ".5", "5.", "1e1", "-1.5E-2", "+.5e+1"} {
		if _, ok := ParseDecimal(v); !ok {
			t.Errorf("ParseDecimal(%q) rejected", v)
		}
	}
	for _, v := range []string{"", "0x1p3", "Inf", "+Inf", "-Inf", "NaN", "1_0", "e3", ".", "+", "1e", "0x10", " 5", "1,5"} {
		if _, ok := ParseDecimal(v); ok {
			t.Errorf("ParseDecimal(%q) accepted", v)
		}
	}
}

func TestValidate_NumbersAndPairs(t *testing.T) {
	tests := []struct {
		name string
		p    Profile
		path string // "" means no errors
		msg  string
	}{
		{"hex lat", Profile{Address: Address{Lat: "0x1p3", Lng: "1"}}, "address.lat", "Enter a number between -90 and 90."},
		{"Inf lng", Profile{Address: Address{Lat: "1", Lng: "Inf"}}, "address.lng", "Enter a number between -180 and 180."},
		{"NaN lat", Profile{Address: Address{Lat: "NaN", Lng: "1"}}, "address.lat", "Enter a number between -90 and 90."},
		{"exponent lng", Profile{Address: Address{Lat: "1", Lng: "1e3"}}, "address.lng", "Enter a number between -180 and 180."},
		{"lat only", Profile{Address: Address{Lat: "10"}}, "address.lng", "Enter both latitude and longitude."},
		{"lng only", Profile{Address: Address{Lng: "10"}}, "address.lat", "Enter both latitude and longitude."},
		{"pair ok", Profile{Address: Address{Lat: "-33.5", Lng: "151.2"}}, "", ""},
		{"rating hex", Profile{Rating: Rating{Value: "0x1p2"}}, "rating.value", "Enter a number between 0 and 5."},
		{"rating Inf", Profile{Rating: Rating{Value: "Inf"}}, "rating.value", "Enter a number between 0 and 5."},
		{"rating NaN", Profile{Rating: Rating{Value: "NaN"}}, "rating.value", "Enter a number between 0 and 5."},
		{"rating exponent out of range", Profile{Rating: Rating{Value: "1e1"}}, "rating.value", "Enter a number between 0 and 5."},
		{"rating above default", Profile{Rating: Rating{Value: "5.1"}}, "rating.value", "Enter a number between 0 and 5."},
		{"rating within bounds", Profile{Rating: Rating{Value: "8.5", Best: "10", Worst: "1"}}, "", ""},
		{"rating below worst", Profile{Rating: Rating{Value: "0.5", Best: "10", Worst: "1"}}, "rating.value", "Enter a number between 1 and 10."},
		{"rating above best", Profile{Rating: Rating{Value: "4", Best: "3"}}, "rating.value", "Enter a number between 0 and 3."},
		{"best hex", Profile{Rating: Rating{Best: "0x10"}}, "rating.best", "Enter a number such as 5."},
		{"worst NaN", Profile{Rating: Rating{Worst: "NaN"}}, "rating.worst", "Enter a number such as 5."},
		{"best not above worst", Profile{Rating: Rating{Best: "2", Worst: "2"}}, "rating.best", "Enter a best rating higher than the worst."},
		{"count type ok", Profile{Rating: Rating{CountType: "reviews"}}, "", ""},
		{"count type bad", Profile{Rating: Rating{CountType: "stars"}}, "rating.count_type", "Choose ratings or reviews."},
		{"special row without times", Profile{SpecialHours: []SpecialHours{{From: "2030-12-24"}}}, "special_hours.0.opens", "Enter the times, or tick Closed."},
		{"special row closed", Profile{SpecialHours: []SpecialHours{{From: "2030-12-24", Closed: true}}}, "", ""},
		{"special row with times", Profile{SpecialHours: []SpecialHours{{From: "2030-12-24", Opens: "10:00", Closes: "12:00"}}}, "", ""},
		{"founding year", Profile{FoundingDate: "2009"}, "", ""},
		{"founding month", Profile{FoundingDate: "2009-05"}, "", ""},
		{"founding day", Profile{FoundingDate: "2009-05-17"}, "", ""},
		{"founding month 13", Profile{FoundingDate: "2009-13"}, "founding_date", "Use a year or a date such as 2009, 2009-05 or 2009-05-17."},
		{"founding day 31 of June", Profile{FoundingDate: "2009-06-31"}, "founding_date", "Use a year or a date such as 2009, 2009-05 or 2009-05-17."},
		{"founding text", Profile{FoundingDate: "last year"}, "founding_date", "Use a year or a date such as 2009, 2009-05 or 2009-05-17."},
		{"founding short year", Profile{FoundingDate: "99"}, "founding_date", "Use a year or a date such as 2009, 2009-05 or 2009-05-17."},
		{"employees ok", Profile{EmployeesMin: 10, EmployeesMax: 50}, "", ""},
		{"employees only min", Profile{EmployeesMin: 10}, "", ""},
		{"employees negative", Profile{EmployeesMin: -1}, "employees_min", "Enter a whole number of 0 or more."},
		{"employees reversed", Profile{EmployeesMin: 60, EmployeesMax: 50}, "employees_max", "Enter a number that is not lower than the minimum."},
		{"org address locale", Profile{OrgAddress: OrgAddressLocale}, "", ""},
		{"org address unknown", Profile{OrgAddress: "everywhere"}, "org_address", "Choose one of the listed address options."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := Validate(tc.p)
			if tc.path == "" {
				if len(errs) != 0 {
					t.Fatalf("errors = %v, want none", errs)
				}
				return
			}
			if len(errs) != 1 || errs[tc.path] != tc.msg {
				t.Fatalf("errors = %v, want only %s: %q", errs, tc.path, tc.msg)
			}
		})
	}
}

func TestValidate_NewTextLimits(t *testing.T) {
	long := ""
	for i := 0; i <= maxTextLen; i++ {
		long += "a"
	}
	p := Profile{
		VATID: long, AlternateName: long, Slogan: long, CountryName: long,
		ContactPoints: []ContactPoint{{Name: long, AreaServed: long, AvailableLanguage: long}},
	}
	errs := Validate(p)
	for _, path := range []string{"vat_id", "alternate_name", "slogan", "country_name",
		"contact_points.0.name", "contact_points.0.area_served", "contact_points.0.available_language"} {
		if errs[path] == "" {
			t.Errorf("no error for %s", path)
		}
	}
}

func TestValidate_DescriptionLimit(t *testing.T) {
	if errs := Validate(Profile{Description: strings.Repeat("a", maxDescLen)}); errs["description"] != "" {
		t.Errorf("a %d character description was refused: %v", maxDescLen, errs)
	}
	if errs := Validate(Profile{Description: strings.Repeat("a", maxDescLen+1)}); errs["description"] == "" {
		t.Error("an over-long description was accepted")
	}
}

func TestValidate_BadLatKeepsOwnError(t *testing.T) {
	errs := Validate(Profile{Address: Address{Lat: "Inf"}})
	if errs["address.lat"] != "Enter a number between -90 and 90." || errs["address.lng"] != "Enter both latitude and longitude." {
		t.Errorf("errors = %v", errs)
	}
}

func TestValidate_ExtendedFields(t *testing.T) {
	if errs := Validate(filledExtended()); len(errs) != 0 {
		t.Fatalf("filled profile: %v", errs)
	}
	tests := []struct {
		name string
		p    Profile
		path string
		msg  string
	}{
		{"naics short", Profile{Naics: "5"}, "naics", "Enter 2 to 6 digits."},
		{"naics letters", Profile{Naics: "54a1"}, "naics", "Enter 2 to 6 digits."},
		{"isic", Profile{Isic: "620"}, "isic", "Enter 4 digits."},
		{"duns", Profile{Duns: "12345678"}, "duns", "Enter 9 digits."},
		{"lei", Profile{Lei: "short"}, "lei", "Enter 20 letters or digits."},
		{"gln", Profile{Gln: "123"}, "gln", "Enter 13 digits."},
		{"booking url", Profile{BookingURL: "book now"}, "booking_url", "Enter a full web address starting with http:// or https://."},
		{"order url", Profile{OrderURL: "ftp://x.test"}, "order_url", "Enter a full web address starting with http:// or https://."},
		{"menu url", Profile{MenuURL: "x"}, "menu_url", "Enter a full web address starting with http:// or https://."},
		{"radius text", Profile{ServiceRadiusKm: "far"}, "service_radius_km", "Enter a distance in kilometres."},
		{"radius huge", Profile{ServiceRadiusKm: "1e306"}, "service_radius_km", "Enter a distance in kilometres."},
		{"radius above the cap", Profile{ServiceRadiusKm: "20001"}, "service_radius_km", "Enter a distance in kilometres."},
		{"radius zero", Profile{ServiceRadiusKm: "0"}, "service_radius_km", "Enter a distance in kilometres."},
		{"founder nameless", Profile{Founders: []Person{{Name: "A"}, {URL: "https://example.test"}}}, "founders.1.name", "Enter a name."},
		{"founder url", Profile{Founders: []Person{{Name: "A", URL: "x"}}}, "founders.0.url", "Enter a full web address starting with http:// or https://."},
		{"membership nameless", Profile{Memberships: []Person{{}}}, "memberships.0.name", "Enter a name."},
		{"parent nameless", Profile{ParentOrg: Person{URL: "https://example.test"}}, "parent_organisation.name", "Enter a name."},
		{"parent url", Profile{ParentOrg: Person{Name: "P", URL: "x"}}, "parent_organisation.url", "Enter a full web address starting with http:// or https://."},
		{"service nameless", Profile{Services: []Service{{Price: "5"}}}, "services.0.name", "Enter a name."},
		{"service price text", Profile{Services: []Service{{Name: "S", Price: "cheap"}}}, "services.0.price", "Enter a price of 0 or more, such as 49.90."},
		{"service price negative", Profile{Services: []Service{{Name: "S", Price: "-1"}}}, "services.0.price", "Enter a price of 0 or more, such as 49.90."},
		{"service url", Profile{Services: []Service{{Name: "S", URL: "x"}}}, "services.0.url", "Enter a full web address starting with http:// or https://."},
		{"amenity nameless", Profile{Amenities: []Amenity{{Value: true}}}, "amenities.0.name", "Enter a name."},
		{"brand long", Profile{Brands: []string{"ok", strings.Repeat("x", 201)}}, "brands.1", "Use at most 200 characters."},
		{"award long", Profile{Awards: []string{strings.Repeat("x", 201)}}, "awards.0", "Use at most 200 characters."},
	}
	for _, tt := range tests {
		errs := Validate(tt.p)
		if errs[tt.path] != tt.msg || len(errs) != 1 {
			t.Errorf("%s: errs = %v, want only %s: %q", tt.name, errs, tt.path, tt.msg)
		}
	}
	ok := Profile{Lei: "5493001kjtiigc8y1r12", ServiceRadiusKm: "12.5", Services: []Service{{Name: "S", Price: "0"}}}
	if errs := Validate(ok); len(errs) != 0 {
		t.Errorf("lowercase LEI, zero price: %v", errs)
	}
}

// Each new list stays under the 64-element array cap of the site graph filter.
func TestValidate_ListsAreCapped(t *testing.T) {
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "x"
		}
		return out
	}
	p51 := make([]Person, 51)
	for i := range p51 {
		p51[i].Name = "x"
	}
	s51 := make([]Service, 51)
	a51 := make([]Amenity, 51)
	for i := range s51 {
		s51[i].Name, a51[i].Name = "x", "x"
	}
	for key, p := range map[string]Profile{
		"founders": {Founders: p51}, "memberships": {Memberships: p51}, "brands": {Brands: many(51)},
		"payment_methods": {PaymentMethods: many(51)}, "knows_about": {KnowsAbout: many(51)}, "awards": {Awards: many(51)},
		"services": {Services: s51}, "amenities": {Amenities: a51},
	} {
		if msg := Validate(p)[key]; msg != "Add at most 50 items." {
			t.Errorf("%s: %q", key, msg)
		}
	}
	if msgs := Validate(Profile{Brands: many(50)}); len(msgs) != 0 {
		t.Errorf("50 brands: %v", msgs)
	}
}
