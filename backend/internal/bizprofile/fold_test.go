package bizprofile

import (
	"reflect"
	"slices"
	"testing"
)

func bp(b bool) *bool { return &b }

const (
	tagDef   = "en-XX"
	tagOther = "fr-CA"
	tagThird = "de-XX"
)

func TestFold_ClassificationCoversEveryField(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[Profile]()
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		n := typ.Field(i).Name
		seen[n] = true
		if classification[n] == 0 {
			t.Errorf("Profile.%s has no fold classification", n)
		}
	}
	for n := range classification {
		if !seen[n] {
			t.Errorf("classification lists %s, which is not a Profile field", n)
		}
	}
}

func TestFold_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		site  Profile
		def   *Profile // nil: no default card
		card  Profile  // the other card (tagOther)
		check func(t *testing.T, o FoldOut)
	}{
		{
			name: "no default card keeps the site and the office switch",
			site: Profile{TaxID: "S", Office: true},
			card: Profile{TaxID: "C"},
			check: func(t *testing.T, o FoldOut) {
				if o.Changed || !o.Business.Office || o.Business.TaxID != "S" || o.Cards[tagOther].TaxID != "C" {
					t.Errorf("out = %+v", o)
				}
			},
		},
		{
			name: "empty default card is dropped",
			site: Profile{TaxID: "S"},
			def:  &Profile{},
			check: func(t *testing.T, o FoldOut) {
				if !o.Changed || o.Business.TaxID != "S" || o.Business.Office || len(o.Cards) != 2 || len(o.OrgChanges) != 0 {
					t.Errorf("out = %+v", o)
				}
			},
		},
		{
			name: "default card equal to the site copies nothing down",
			site: Profile{TaxID: "S", Email: "a@example.test", Rating: Rating{Value: "4"}},
			def:  &Profile{TaxID: "S", Email: "a@example.test", Rating: Rating{Value: "4"}},
			check: func(t *testing.T, o FoldOut) {
				if !reflect.DeepEqual(o.Cards[tagOther], Profile{}) || len(o.OrgChanges) != 0 {
					t.Errorf("out = %+v", o)
				}
			},
		},
		{
			name: "name falls back to site name and is never copied down",
			site: Profile{Name: "Site Ltd"},
			def:  &Profile{},
			check: func(t *testing.T, o FoldOut) {
				if o.Business.Name != "Site Ltd" {
					t.Errorf("Name = %q", o.Business.Name)
				}
			},
		},
		{
			name: "default name wins, other card keeps no name",
			site: Profile{Name: "Site Ltd"},
			def:  &Profile{Name: "Default Ltd"},
			check: func(t *testing.T, o FoldOut) {
				if o.Business.Name != "Default Ltd" || o.Cards[tagOther].Name != "" || !slices.Contains(o.OrgChanges, "name") {
					t.Errorf("out = %+v", o)
				}
			},
		},
		{
			name: "rating is copied down as a whole struct, also over a count-only card",
			site: Profile{Rating: Rating{Value: "4", Count: "10", Best: "5", CountType: "reviews"}},
			def:  &Profile{Rating: Rating{Value: "3", Count: "2"}},
			card: Profile{Rating: Rating{Count: "99"}},
			check: func(t *testing.T, o FoldOut) {
				if o.Business.Rating != (Rating{Value: "3", Count: "2"}) {
					t.Errorf("Business.Rating = %+v", o.Business.Rating)
				}
				if o.Cards[tagOther].Rating != (Rating{Value: "4", Count: "10", Best: "5", CountType: "reviews"}) {
					t.Errorf("card Rating = %+v", o.Cards[tagOther].Rating)
				}
			},
		},
		{
			name: "card with its own rating is untouched",
			site: Profile{Rating: Rating{Value: "4"}},
			def:  &Profile{Rating: Rating{Value: "3"}},
			card: Profile{Rating: Rating{Value: "2"}},
			check: func(t *testing.T, o FoldOut) {
				if o.Cards[tagOther].Rating.Value != "2" {
					t.Errorf("Rating = %+v", o.Cards[tagOther].Rating)
				}
			},
		},
		{
			name: "parent organisation as a whole struct",
			site: Profile{ParentOrg: Person{Name: "Parent", URL: "https://parent.example.test"}},
			def:  &Profile{ParentOrg: Person{Name: "Other"}},
			check: func(t *testing.T, o FoldOut) {
				if o.Business.ParentOrg.Name != "Other" || o.Cards[tagOther].ParentOrg != (Person{Name: "Parent", URL: "https://parent.example.test"}) {
					t.Errorf("out = %+v", o)
				}
			},
		},
		{
			name: "address block replaced when the card has a street",
			site: Profile{Address: Address{Street: "1 Site St", City: "Siteville", POBox: "PO1", GBPURL: "https://g.example.test"}},
			def:  &Profile{Address: Address{Street: "2 Card St"}},
			card: Profile{TaxID: "x"},
			check: func(t *testing.T, o FoldOut) {
				if o.Business.Address != (Address{Street: "2 Card St"}) || !o.Business.Office {
					t.Errorf("Business = %+v", o.Business)
				}
				if o.Cards[tagOther].Address != (Address{}) {
					t.Errorf("address leaked into the card: %+v", o.Cards[tagOther].Address)
				}
			},
		},
		{
			name: "city only is an office",
			site: Profile{},
			def:  &Profile{Address: Address{City: "Cardtown"}},
			check: func(t *testing.T, o FoldOut) {
				if !o.Business.Office {
					t.Error("city only: Office = false")
				}
			},
		},
		{
			name: "no address means no office, extras laid over the site block",
			site: Profile{Address: Address{Street: "1 Site St", POBox: "PO1", Lat: "1", Lng: "2"}},
			def:  &Profile{Address: Address{POBox: "PO2", MapURL: "https://m.example.test"}},
			check: func(t *testing.T, o FoldOut) {
				want := Address{Street: "1 Site St", POBox: "PO2", Lat: "1", Lng: "2", MapURL: "https://m.example.test"}
				if o.Business.Office || o.Business.Address != want {
					t.Errorf("Business = %+v", o.Business)
				}
			},
		},
		{
			name: "NoOffice on the default card turns the office off and is cleared",
			site: Profile{Office: true},
			def:  &Profile{NoOffice: true, Address: Address{Street: "2 Card St"}},
			check: func(t *testing.T, o FoldOut) {
				if o.Business.Office || o.Business.NoOffice {
					t.Errorf("Office=%v NoOffice=%v", o.Business.Office, o.Business.NoOffice)
				}
			},
		},
		{
			name: "locale-only fields come from the default card and are not copied",
			site: Profile{},
			def:  &Profile{CountryName: "Xland", Image: "/m/a.jpg", BusinessType: "Store", Address: Address{Lat: "3", Lng: "4", MapURL: "https://m.example.test"}},
			check: func(t *testing.T, o FoldOut) {
				b, c := o.Business, o.Cards[tagOther]
				if b.CountryName != "Xland" || b.Image != "/m/a.jpg" || b.BusinessType != "Store" || b.Address.Lat != "3" {
					t.Errorf("Business = %+v", b)
				}
				if c.CountryName != "" || c.Image != "" || c.BusinessType != "" || c.Address != (Address{}) || c.NoOffice {
					t.Errorf("card = %+v", c)
				}
			},
		},
		{
			name: "phone and hours: default wins, empty cards get the site value",
			site: Profile{Phone: "111", HoursNote: "Mon-Fri"},
			def:  &Profile{Phone: "222", HoursNote: "Daily"},
			card: Profile{Phone: "333"},
			check: func(t *testing.T, o FoldOut) {
				if o.Business.Phone != "222" || o.Business.HoursNote != "Daily" {
					t.Errorf("Business = %+v", o.Business)
				}
				if c := o.Cards[tagOther]; c.Phone != "333" || c.HoursNote != "Mon-Fri" {
					t.Errorf("card = %+v", c)
				}
			},
		},
		{
			name: "text fields",
			site: Profile{Email: "s@example.test", Fax: "1", TaxID: "T", VATID: "V", Description: "site", GeoRegion: "XX-A", Currency: "EUR", Naics: "1", Lei: "L"},
			def:  &Profile{Email: "d@example.test", Fax: "2", TaxID: "T2", VATID: "V2", Description: "def", GeoRegion: "XX-B", Currency: "USD", Naics: "2", Lei: "L2"},
			card: Profile{Email: "c@example.test", Currency: "CAD"},
			check: func(t *testing.T, o FoldOut) {
				b, c := o.Business, o.Cards[tagOther]
				if b.Email != "d@example.test" || b.Fax != "2" || b.TaxID != "T2" || b.VATID != "V2" || b.Description != "def" || b.GeoRegion != "XX-B" || b.Currency != "USD" || b.Naics != "2" || b.Lei != "L2" {
					t.Errorf("Business = %+v", b)
				}
				if c.Email != "c@example.test" || c.Currency != "CAD" || c.Fax != "1" || c.TaxID != "T" || c.VATID != "V" || c.Description != "site" || c.GeoRegion != "XX-A" || c.Naics != "1" || c.Lei != "L" {
					t.Errorf("card = %+v", c)
				}
			},
		},
		{
			name: "lists: default non-empty wins, empty card gets the site list",
			site: Profile{SameAs: []string{"https://a.example.test"}, Services: []Service{{Name: "S"}}, Founders: []Person{{Name: "F"}}, Brands: []string{"B"}, KnowsAbout: []string{"K"}},
			def:  &Profile{SameAs: []string{"https://d.example.test"}, Services: []Service{{Name: "D"}}},
			card: Profile{Brands: []string{"own"}},
			check: func(t *testing.T, o FoldOut) {
				b, c := o.Business, o.Cards[tagOther]
				if b.SameAs[0] != "https://d.example.test" || b.Services[0].Name != "D" || b.Founders[0].Name != "F" {
					t.Errorf("Business = %+v", b)
				}
				if c.SameAs[0] != "https://a.example.test" || c.Services[0].Name != "S" || c.Brands[0] != "own" || len(c.Founders) != 0 {
					t.Errorf("card = %+v", c)
				}
			},
		},
		{
			name: "tri-state: explicit false in the default card over true, copied down only into nil",
			site: Profile{ServiceProvider: bp(true), PublicAccess: bp(true), AmenityPresets: AmenityPresets{Wifi: bp(true), Parking: bp(true)}},
			def:  &Profile{ServiceProvider: bp(false), PublicAccess: bp(false), AmenityPresets: AmenityPresets{Wifi: bp(false)}},
			card: Profile{PublicAccess: bp(false), AmenityPresets: AmenityPresets{Wifi: bp(false)}},
			check: func(t *testing.T, o FoldOut) {
				b, c := o.Business, o.Cards[tagOther]
				if *b.ServiceProvider || *b.PublicAccess || *b.AmenityPresets.Wifi || !*b.AmenityPresets.Parking {
					t.Errorf("Business = %+v", b)
				}
				if c.ServiceProvider == nil || !*c.ServiceProvider || c.PublicAccess == nil || *c.PublicAccess || *c.AmenityPresets.Wifi || c.AmenityPresets.Parking != nil {
					t.Errorf("card = %+v", c)
				}
			},
		},
		{
			name: "org-only fields stay as the site has them",
			site: Profile{Slogan: "site", LogoObject: true, OrgAddress: OrgAddressLocale, EmployeesMin: 1},
			def:  &Profile{Slogan: "card", LogoObject: false, EmployeesMin: 9},
			check: func(t *testing.T, o FoldOut) {
				b := o.Business
				if b.Slogan != "site" || !b.LogoObject || b.OrgAddress != OrgAddressLocale || b.EmployeesMin != 1 {
					t.Errorf("Business = %+v", b)
				}
			},
		},
		{
			name: "orphan card gets the copy-down too",
			site: Profile{TaxID: "S"},
			def:  &Profile{TaxID: "D"},
			check: func(t *testing.T, o FoldOut) {
				if o.Cards[tagThird].TaxID != "S" {
					t.Errorf("orphan = %+v", o.Cards[tagThird])
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cards := map[string]Profile{tagOther: tc.card, tagThird: {}}
			if tc.def != nil {
				cards[tagDef] = *tc.def
			}
			in := FoldIn{Default: tagDef, Site: tc.site, Cards: cards}
			o := Fold(in)
			if _, ok := o.Cards[tagDef]; ok {
				t.Error("default card kept")
			}
			if _, ok := in.Cards[tagDef]; tc.def != nil && !ok {
				t.Error("Fold mutated its input")
			}
			tc.check(t, o)
			again := Fold(FoldIn{Default: tagDef, Site: o.Business, Cards: o.Cards})
			if again.Changed || !reflect.DeepEqual(again.Business, o.Business) || !reflect.DeepEqual(again.Cards, o.Cards) {
				t.Errorf("second fold changed the result: %+v", again)
			}
		})
	}
}

func TestFold_OrgChanges(t *testing.T) {
	t.Parallel()
	o := Fold(FoldIn{
		Default: tagDef,
		Site:    Profile{Email: "s@example.test", Address: Address{Street: "1 Site St"}, AmenityPresets: AmenityPresets{Wifi: bp(true)}},
		Cards: map[string]Profile{tagDef: {
			Email: "d@example.test", Address: Address{City: "Town"}, Rating: Rating{Value: "4"},
			AmenityPresets: AmenityPresets{Wifi: bp(false)},
		}},
	})
	want := []string{"email", "rating", "amenity_presets.wifi", "address"}
	if !slices.Equal(o.OrgChanges, want) {
		t.Errorf("OrgChanges = %v, want %v", o.OrgChanges, want)
	}
}

func TestResolveDefault(t *testing.T) {
	t.Parallel()
	addr := Address{Street: "1 Main St"}
	if r := ResolveDefault(Profile{Office: true, Address: addr, Phone: "1"}); !r.Office || r.Profile.Phone != "1" || r.Profile.Address != addr {
		t.Errorf("office on: %+v", r)
	}
	if r := ResolveDefault(Profile{Address: addr}); r.Office {
		t.Errorf("office off: %+v", r)
	}
	if r := ResolveDefault(Profile{Office: true}); r.Office {
		t.Errorf("office on, no address: %+v", r)
	}
}

func TestSplitLegacy_InverseOfFromLegacy(t *testing.T) {
	t.Parallel()
	p := Profile{TaxID: "T", Phone: "1", HoursNote: "h", Address: Address{Street: "s", City: "c", State: "st", PostalCode: "p", Country: "XX", Lat: "1", POBox: "b"}}
	stored, phone, hours, addr := SplitLegacy(p)
	if stored.Phone != "" || stored.HoursNote != "" || stored.Address != (Address{Lat: "1", POBox: "b"}) || stored.TaxID != "T" {
		t.Errorf("stored = %+v", stored)
	}
	if got := FromLegacy(stored, phone, hours, addr); !reflect.DeepEqual(got, p) {
		t.Errorf("FromLegacy(SplitLegacy(p)) = %+v, want %+v", got, p)
	}
}

func TestFold_NoInheritMarker(t *testing.T) {
	t.Parallel()
	def := Profile{
		OpeningHours:   []Hours{{Days: []string{"Monday"}, Opens: "09:00", Closes: "17:00"}},
		SameAs:         []string{"https://example.test/d"},
		ContactPoints:  []ContactPoint{{Type: "sales", Phone: "1"}},
		Currency:       "EUR",
		FreeEntry:      bp(true),
		Rating:         Rating{Value: "4"},
		AmenityPresets: AmenityPresets{Wifi: bp(false)},
	}
	o := Fold(FoldIn{Default: tagDef, Site: Profile{}, Cards: map[string]Profile{
		tagDef: def, tagOther: {Currency: "CAD"}, tagThird: {},
	}})
	want := []string{"contact_points", "currency", "opening_hours", "sameas", "rating", "free_entry", "amenity_presets.wifi"}
	got := o.Cards[tagThird].NoInherit
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("NoInherit = %v, want %v", got, want)
	}
	if slices.Contains(o.Cards[tagOther].NoInherit, "currency") || o.Cards[tagOther].Inherits("opening_hours") || !o.Cards[tagOther].Inherits("currency") {
		t.Errorf("other card = %+v", o.Cards[tagOther])
	}
	if len(o.Business.NoInherit) != 0 {
		t.Errorf("business carries a marker: %v", o.Business.NoInherit)
	}
	again := Fold(FoldIn{Default: tagDef, Site: o.Business, Cards: o.Cards})
	if !reflect.DeepEqual(again.Cards, o.Cards) {
		t.Errorf("second fold changed the cards: %+v", again.Cards)
	}
	// A marker is not added when the site-wide value is set (copied down) or
	// when the card has its own value.
	o = Fold(FoldIn{Default: tagDef, Site: Profile{Currency: "USD"}, Cards: map[string]Profile{
		tagDef: {Currency: "EUR"}, tagOther: {}, tagThird: {Currency: "CAD"},
	}})
	if len(o.Cards[tagOther].NoInherit) != 0 || o.Cards[tagOther].Currency != "USD" || len(o.Cards[tagThird].NoInherit) != 0 {
		t.Errorf("cards = %+v", o.Cards)
	}
}

func TestResolve_NoInherit(t *testing.T) {
	t.Parallel()
	site := Profile{
		Currency: "EUR", SameAs: []string{"https://example.test"}, Rating: Rating{Value: "4"},
		FreeEntry: bp(true), AmenityPresets: AmenityPresets{Wifi: bp(true), Parking: bp(true)},
		ParentOrg: Person{Name: "Parent"},
	}
	loc := Profile{
		Address: Address{City: "Town"}, Currency: "CAD",
		NoInherit: []string{"currency", "sameas", "rating", "free_entry", "amenity_presets.wifi", "parent_organisation", "bogus"},
	}
	p := Resolve(site, &loc).Profile
	if p.Currency != "CAD" || p.SameAs != nil || p.Rating.Value != "" || p.FreeEntry != nil || p.ParentOrg.Name != "" {
		t.Errorf("marked fields fell back: %+v", p)
	}
	if p.AmenityPresets.Wifi != nil || p.AmenityPresets.Parking == nil {
		t.Errorf("presets = %+v", p.AmenityPresets)
	}
}

// A card that already lists a field in no_inherit keeps it empty: the fold does
// not copy the site-wide value into it.
func TestFold_KeepsExistingMarker(t *testing.T) {
	t.Parallel()
	o := Fold(FoldIn{Default: tagDef, Site: Profile{Email: "a@x.test"}, Cards: map[string]Profile{
		tagDef:   {Email: "b@x.test"},
		tagOther: {NoInherit: []string{"email"}},
		tagThird: {},
	}})
	if got := o.Cards[tagOther]; got.Email != "" || !slices.Equal(got.NoInherit, []string{"email"}) {
		t.Errorf("marked card = %+v, want email empty and the marker kept", got)
	}
	if got := o.Cards[tagThird]; got.Email != "a@x.test" {
		t.Errorf("unmarked card email = %q, want the copied site value", got.Email)
	}
}
