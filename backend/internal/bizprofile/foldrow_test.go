package bizprofile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFoldRow(t *testing.T) {
	t.Parallel()
	row := Row{
		Default:         tagDef,
		BusinessProfile: []byte(`{"tax_id":"S","email":"s@example.test"}`),
		LocaleProfiles:  []byte(`{"en-XX":{"phone":"222","email":"d@example.test","address":{"street":"2 Card St","city":"Cardtown","lat":"5"}},"fr-CA":{"phone":"333"}}`),
		Phone:           "111",
		BusinessHours:   "Mon-Fri",
		PostalAddress:   []byte(`{"street":"1 Site St","city":"Siteville"}`),
	}
	out, changed, err := FoldRow(row)
	if err != nil || !changed {
		t.Fatalf("FoldRow = %v, %v", changed, err)
	}
	if out.Phone != "222" || out.BusinessHours != "Mon-Fri" || out.Default != tagDef {
		t.Errorf("columns = %q %q", out.Phone, out.BusinessHours)
	}
	var addr map[string]string
	if err := json.Unmarshal(out.PostalAddress, &addr); err != nil || addr["street"] != "2 Card St" || addr["city"] != "Cardtown" {
		t.Errorf("PostalAddress = %s, %v", out.PostalAddress, err)
	}
	p, err := Parse(out.BusinessProfile)
	if err != nil || p.Email != "d@example.test" || p.TaxID != "S" || !p.Office || p.Address.Lat != "5" || p.Phone != "" || p.Address.Street != "" {
		t.Errorf("BusinessProfile = %s, %v", out.BusinessProfile, err)
	}
	cards, err := ParseLocales(out.LocaleProfiles)
	if err != nil || len(cards) != 1 || cards[tagOther].Phone != "333" || cards[tagOther].Email != "s@example.test" {
		t.Errorf("LocaleProfiles = %s, %v", out.LocaleProfiles, err)
	}

	again, changed, err := FoldRow(out)
	if err != nil || changed || string(again.BusinessProfile) != string(out.BusinessProfile) || string(again.LocaleProfiles) != string(out.LocaleProfiles) || again.Phone != out.Phone {
		t.Errorf("second FoldRow = %v, %v, %+v", changed, err, again)
	}
}

func TestFoldRow_Unchanged(t *testing.T) {
	t.Parallel()
	row := Row{Default: tagDef, BusinessProfile: []byte(`{"tax_id":"S"}`), LocaleProfiles: []byte(`{"fr-CA":{"phone":"1"}}`), Phone: "9"}
	for _, r := range []Row{row, {Default: ""}, {Default: tagDef}, {Default: tagDef, LocaleProfiles: []byte("null")}} {
		out, changed, err := FoldRow(r)
		if err != nil || changed || string(out.BusinessProfile) != string(r.BusinessProfile) || out.Phone != r.Phone {
			t.Errorf("FoldRow(%+v) = %+v, %v, %v", r, out, changed, err)
		}
	}
}

func TestFoldRow_BadJSON(t *testing.T) {
	t.Parallel()
	for _, r := range []Row{
		{Default: tagDef, BusinessProfile: []byte("{bad")},
		{Default: tagDef, LocaleProfiles: []byte("[1]")},
	} {
		if _, _, err := FoldRow(r); err == nil || !strings.Contains(err.Error(), "parse") {
			t.Errorf("FoldRow(%+v) err = %v", r, err)
		}
	}
}

// A previous default swaps the old business into its card even when the new
// default has no card, and the column values follow the stripped business.
func TestFoldRow_PreviousDefault(t *testing.T) {
	t.Parallel()
	row := Row{
		Default: tagOther, Previous: tagDef,
		BusinessProfile: []byte(`{"office":true}`), LocaleProfiles: []byte(`{}`),
		Phone: "111", PostalAddress: []byte(`{"street":"1 Site St","city":"Siteville"}`),
	}
	out, changed, err := FoldRow(row)
	if err != nil || !changed {
		t.Fatalf("FoldRow = %v, %v", changed, err)
	}
	cards, err := ParseLocales(out.LocaleProfiles)
	if err != nil || cards[tagDef].Address.Street != "1 Site St" || cards[tagDef].NoOffice {
		t.Errorf("LocaleProfiles = %s, %v", out.LocaleProfiles, err)
	}
	p, _ := Parse(out.BusinessProfile)
	if p.Office || strings.Contains(string(out.PostalAddress), "Site St") || out.Phone != "111" {
		t.Errorf("business = %s, address %s, phone %q", out.BusinessProfile, out.PostalAddress, out.Phone)
	}
}
