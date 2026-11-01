package bizprofile

import "testing"

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
		VATID: long, AlternateName: long, Description: long, Slogan: long, CountryName: long,
		ContactPoints: []ContactPoint{{Name: long, AreaServed: long, AvailableLanguage: long}},
	}
	errs := Validate(p)
	for _, path := range []string{"vat_id", "alternate_name", "description", "slogan", "country_name",
		"contact_points.0.name", "contact_points.0.area_served", "contact_points.0.available_language"} {
		if errs[path] == "" {
			t.Errorf("no error for %s", path)
		}
	}
}

func TestValidate_BadLatKeepsOwnError(t *testing.T) {
	errs := Validate(Profile{Address: Address{Lat: "Inf"}})
	if errs["address.lat"] != "Enter a number between -90 and 90." || errs["address.lng"] != "Enter both latitude and longitude." {
		t.Errorf("errors = %v", errs)
	}
}
