package bizprofile

import (
	"reflect"
	"slices"
	"testing"
)

// swapFold is what a default-locale change does: swap the old default out,
// then fold the new default's card in.
func swapFold(site Profile, cards map[string]Profile, from, to string) FoldOut {
	return Fold(SwapDefault(FoldIn{Default: to, Site: site, Cards: cards}, from))
}

func TestSwapDefault_Table(t *testing.T) {
	t.Parallel()
	office := Address{Street: "1 Example St", City: "Sample City", Lat: "1.5", Lng: "2.5", MapURL: "https://example.test/map"}
	tests := []struct {
		name  string
		site  Profile
		cards map[string]Profile
		check func(t *testing.T, o FoldOut)
	}{
		{
			name: "old business moves to its card, the new default has no card",
			site: Profile{Name: "Example Legal", CountryName: "Exampleland", Image: "i.png", BusinessType: "Restaurant", Address: office, Office: true, Email: "a@x.test"},
			check: func(t *testing.T, o FoldOut) {
				c := o.Cards[tagDef]
				if c.Name != "Example Legal" || c.CountryName != "Exampleland" || c.Image != "i.png" || c.BusinessType != "Restaurant" || c.Address != office || c.NoOffice {
					t.Errorf("old default card = %+v", c)
				}
				b := o.Business
				if b.Office || b.Address != (Address{}) || b.CountryName != "" || b.Image != "" || b.BusinessType != "" {
					t.Errorf("new default business keeps locale-only values: %+v", b)
				}
				if b.Name != "Example Legal" || b.Email != "a@x.test" {
					t.Errorf("business lost its name or inheritable values: %+v", b)
				}
				if !hasOffice(&c) || ResolveDefault(b).Office {
					t.Error("old default must keep its office, the new default none")
				}
			},
		},
		{
			name: "old business without an office gets no_office",
			site: Profile{Address: Address{Street: "1 Example St"}},
			check: func(t *testing.T, o FoldOut) {
				if c := o.Cards[tagDef]; !c.NoOffice || c.Address.Street != "1 Example St" {
					t.Errorf("card = %+v", c)
				}
			},
		},
		{
			name:  "new default card is folded; old values copied down, markers for what the old left empty",
			site:  Profile{Phone: "100", Email: "a@x.test", Office: true, Address: office},
			cards: map[string]Profile{tagOther: {Phone: "200", Currency: "CAD", Address: Address{City: "Other City"}}},
			check: func(t *testing.T, o FoldOut) {
				b, c := o.Business, o.Cards[tagDef]
				if b.Phone != "200" || b.Currency != "CAD" || !b.Office || b.Address.City != "Other City" || b.Address.Street != "" {
					t.Errorf("business = %+v", b)
				}
				if c.Phone != "100" || c.Address != office || c.NoOffice {
					t.Errorf("old default card = %+v, want its phone and address", c)
				}
				if !slices.Equal(c.NoInherit, []string{"currency"}) {
					t.Errorf("NoInherit = %v, want currency (the old default had none)", c.NoInherit)
				}
				if _, ok := o.Cards[tagOther]; ok {
					t.Error("new default card not dropped")
				}
			},
		},
		{
			name: "a leftover card under the old tag loses to the business",
			site: Profile{Name: "Legal", CountryName: "Exampleland", Address: office, Office: true},
			cards: map[string]Profile{tagDef: {
				Name: "Own Name", Address: Address{Street: "9 Own St"}, NoOffice: true, Email: "own@x.test",
			}},
			check: func(t *testing.T, o FoldOut) {
				c := o.Cards[tagDef]
				if c.Name != "Legal" || c.Address.Street != "1 Example St" || c.NoOffice || c.Email != "" || c.CountryName != "Exampleland" {
					t.Errorf("leftover won over the business: %+v", c)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.check(t, swapFold(tt.site, tt.cards, tagDef, tagOther))
		})
	}
}

// Changing the default from A to B and back leaves every locale's resolved
// business as it was.
func TestSwapDefault_RoundTrip(t *testing.T) {
	t.Parallel()
	siteA := Profile{
		Name: "Legal A", Email: "a@x.test", TaxID: "T1", CountryName: "Landa", Phone: "100", Office: true,
		Address: Address{Street: "1 A St", City: "A City"}, SameAs: []string{"https://example.test/a"},
	}
	cards := map[string]Profile{
		tagOther: {Name: "Name B", Email: "b@x.test", Currency: "CAD", Phone: "200", CountryName: "Landb", Address: Address{City: "B City"}},
		tagThird: {NoOffice: true, CountryName: "Landc"},
	}
	views := func(site Profile, cards map[string]Profile, def string) map[string]Profile {
		out := map[string]Profile{}
		for tag, c := range cards {
			out[tag] = ResolveContact(site, &c).Profile
		}
		d := ResolveDefault(site)
		if d.Office {
			out[def] = d.Profile
		}
		return out
	}
	want := views(siteA, cards, tagDef)

	toB := swapFold(siteA, cards, tagDef, tagOther)
	if _, ok := toB.Cards[tagDef]; !ok {
		t.Fatal("old default has no card after A to B")
	}
	back := swapFold(toB.Business, toB.Cards, tagOther, tagDef)
	if _, ok := back.Cards[tagDef]; ok {
		t.Error("default card left after B to A")
	}
	if got := views(back.Business, back.Cards, tagDef); !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the resolved businesses:\ngot  %+v\nwant %+v", got, want)
	}
	// B is a normal locale again and prints what it printed.
	if want[tagOther].Currency != "CAD" || back.Cards[tagOther].Address.City != "B City" {
		t.Errorf("B after the round trip = %+v", back.Cards[tagOther])
	}
}

func TestFold_DefaultCardMarkerClearsBusinessField(t *testing.T) {
	t.Parallel()
	o := Fold(FoldIn{Default: tagDef, Site: Profile{Email: "a@x.test", Currency: "EUR"}, Cards: map[string]Profile{
		tagDef: {NoInherit: []string{"email"}, Currency: "CAD"}, tagOther: {},
	}})
	if o.Business.Email != "" || o.Business.Currency != "CAD" {
		t.Errorf("business = %+v, want email cleared", o.Business)
	}
	if o.Cards[tagOther].Email != "a@x.test" {
		t.Errorf("other card email = %q, want the old value copied", o.Cards[tagOther].Email)
	}
}
