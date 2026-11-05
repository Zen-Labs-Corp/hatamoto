package bizprofile

import (
	"encoding/json/jsontext"
	"reflect"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
)

// fieldKind says how Fold treats one Profile field.
type fieldKind int

const (
	// kindInherit: a card falls back to the default business for it. The
	// default card's value wins in the folded business and is copied down into
	// the other cards that left it empty.
	kindInherit fieldKind = iota + 1
	// kindLegacy: like kindInherit; stored in a legacy column (phone, hours).
	kindLegacy
	// kindLocaleOnly: never inherited. The default card's value is laid over the
	// site-wide one and nothing is copied down.
	kindLocaleOnly
	// kindOrgOnly: only the site-wide profile holds it; the default card is
	// ignored.
	kindOrgOnly
	// kindAddress: the address block, see foldAddress.
	kindAddress
	// kindOffice: NoOffice and Office, recomputed from the default card.
	kindOffice
	// kindMarker: NoInherit, a card-only marker that Fold never moves.
	kindMarker
)

// classification lists every Profile field. A test requires an entry for each
// field, so a new field cannot be added without a fold decision.
var classification = map[string]fieldKind{
	"Name":                 kindLocaleOnly,
	"TaxID":                kindInherit,
	"BusinessType":         kindLocaleOnly,
	"NoOffice":             kindOffice,
	"Office":               kindOffice,
	"Address":              kindAddress,
	"Phone":                kindLegacy,
	"Fax":                  kindInherit,
	"Email":                kindInherit,
	"HoursNote":            kindLegacy,
	"ContactPoints":        kindInherit,
	"OpeningHours":         kindInherit,
	"SpecialHours":         kindInherit,
	"AreaServed":           kindInherit,
	"Currency":             kindInherit,
	"PriceRange":           kindInherit,
	"GeoRegion":            kindInherit,
	"SameAs":               kindInherit,
	"Rating":               kindInherit,
	"NoInherit":            kindMarker,
	"Description":          kindInherit,
	"AlternateName":        kindOrgOnly,
	"Slogan":               kindOrgOnly,
	"FoundingDate":         kindOrgOnly,
	"EmployeesMin":         kindOrgOnly,
	"EmployeesMax":         kindOrgOnly,
	"VATID":                kindInherit,
	"CountryName":          kindLocaleOnly,
	"Image":                kindLocaleOnly,
	"LogoObject":           kindOrgOnly,
	"OrgAddress":           kindOrgOnly,
	"Naics":                kindInherit,
	"Isic":                 kindInherit,
	"Duns":                 kindInherit,
	"Lei":                  kindInherit,
	"Gln":                  kindInherit,
	"Founders":             kindInherit,
	"ParentOrg":            kindInherit,
	"Brands":               kindInherit,
	"Memberships":          kindInherit,
	"PaymentMethods":       kindInherit,
	"AcceptsReservations":  kindInherit,
	"BookingURL":           kindInherit,
	"OrderURL":             kindInherit,
	"MenuURL":              kindInherit,
	"ServiceRadiusKm":      kindInherit,
	"CatalogName":          kindInherit,
	"ServiceProvider":      kindInherit,
	"Services":             kindInherit,
	"AdditionalProperties": kindInherit,
	"KnowsAbout":           kindInherit,
	"Awards":               kindInherit,
	"AmenityPresets":       kindInherit,
	"Amenities":            kindInherit,
	"PublicAccess":         kindInherit,
	"FreeEntry":            kindInherit,
}

// FoldIn is the data of Fold. Site is the site-wide profile after FromLegacy;
// Cards are the per-locale cards keyed by normalised tag; Default is the
// normalised default tag.
type FoldIn struct {
	Default string
	Site    Profile
	Cards   map[string]Profile
}

// FoldOut is the folded data. Business is the default business (legacy values
// still inside; SplitLegacy takes them out); Cards holds the other locales'
// cards. Changed is true when a card under the default tag was folded.
// OrgChanges lists the JSON keys of the Organization fields the fold moved.
type FoldOut struct {
	Business   Profile
	Cards      map[string]Profile
	Changed    bool
	OrgChanges []string
}

// Fold folds the card under the default tag into the site-wide profile:
//
//   - The business is the site-wide profile with the default card's non-empty
//     fields on top (a set *bool, even false, wins; lists when non-empty;
//     Rating as a whole when it has a Value; ParentOrg as a whole when it has
//     a Name). Name falls back to the site-wide Name.
//   - Address: the card's block replaces the business's whole block when it has
//     a street or a city, otherwise only its non-empty po_box, lat, lng,
//     map_url and gbp_url are laid over.
//   - Office is on iff the card had an office (not NoOffice, a street or a
//     city); NoOffice is cleared. With no default card the site-wide profile is
//     returned as it is, Office untouched.
//   - Copy-down keeps the other cards' output: for every inheritable field the
//     default card overrode (non-empty and different from the site-wide value),
//     a card that left it empty gets the site-wide value (Rating and ParentOrg
//     as a whole struct, *bool only into nil). Name, CountryName, Image,
//     BusinessType, NoOffice, the address block, lat, lng and map_url are never
//     copied. Orphan cards are treated like the others.
//   - The default card is dropped, so folding again changes nothing.
//
// A site-wide value that is empty cannot be copied down: a card that inherited
// "nothing" would now inherit the default card's value, so the card gets the
// field's key in NoInherit instead and keeps printing nothing for it.
func Fold(in FoldIn) FoldOut {
	others := make(map[string]*Profile, len(in.Cards))
	for tag, c := range in.Cards {
		if tag != in.Default {
			others[tag] = &c
		}
	}
	d, has := in.Cards[in.Default]
	out := FoldOut{Business: in.Site}
	if has {
		out.Changed = true
		foldInto(&out, in.Site, d, others)
	}
	out.Cards = make(map[string]Profile, len(others))
	for tag, c := range others {
		out.Cards[tag] = *c
	}
	return out
}

func foldInto(out *FoldOut, s, d Profile, others map[string]*Profile) {
	f := &out.Business
	fv := reflect.ValueOf(f).Elem()
	sv, dv := reflect.ValueOf(s), reflect.ValueOf(d)
	typ := fv.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		switch classification[name] {
		case kindInherit, kindLegacy:
			var os []target
			for _, c := range others {
				os = append(os, target{c, reflect.ValueOf(c).Elem().Field(i)})
			}
			out.foldInherit(name, jsonKey(typ.Field(i)), fv.Field(i), sv.Field(i), dv.Field(i), os, d.NoInherit)
		case kindLocaleOnly:
			if isSet(name, dv.Field(i)) {
				fv.Field(i).Set(dv.Field(i))
			}
		}
	}
	f.Address = foldAddress(s.Address, d.Address)
	if f.Address != s.Address {
		out.OrgChanges = append(out.OrgChanges, "address")
	}
	if f.Name != s.Name {
		out.OrgChanges = append(out.OrgChanges, "name")
	}
	f.Office = hasOffice(&d)
	f.NoOffice = false
}

// target is a card that may receive a copied-down field: the card, for its
// NoInherit marker, and the field's value.
type target struct {
	card *Profile
	v    reflect.Value
}

// foldInherit lays d over f when d overrides s and copies s down into the
// cards os that left the field empty. AmenityPresets recurses per preset. A key
// in dMarks (the default card's NoInherit, which SwapDefault writes) means the
// default card leaves the field empty on purpose: the business loses the
// site-wide value, like any other override.
func (out *FoldOut) foldInherit(name, key string, f, s, d reflect.Value, os []target, dMarks []string) {
	if f.Type() == reflect.TypeFor[AmenityPresets]() {
		for j := 0; j < f.NumField(); j++ {
			var sub []target
			for _, o := range os {
				sub = append(sub, target{o.card, o.v.Field(j)})
			}
			out.foldInherit("", key+"."+jsonKey(f.Type().Field(j)), f.Field(j), s.Field(j), d.Field(j), sub, dMarks)
		}
		return
	}
	if cleared := !isSet(name, d) && slices.Contains(dMarks, key); cleared {
		if !isSet(name, s) {
			return
		}
		f.SetZero()
	} else if !isSet(name, d) || reflect.DeepEqual(d.Interface(), s.Interface()) {
		return
	} else {
		f.Set(d)
	}
	out.OrgChanges = append(out.OrgChanges, key)
	for _, o := range os {
		switch {
		case isSet(name, o.v):
		case slices.Contains(o.card.NoInherit, key):
			// Already marked: stays empty on purpose.
		case isSet(name, s):
			o.v.Set(s)
		case !slices.Contains(o.card.NoInherit, key):
			o.card.NoInherit = append(o.card.NoInherit, key)
		}
	}
}

// inheritPaths maps the JSON key of every inheritable field to its field index
// path in Profile (AmenityPresets keys are "amenity_presets.<key>").
var inheritPaths = func() map[string][]int {
	paths := map[string][]int{}
	typ := reflect.TypeFor[Profile]()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if k := classification[f.Name]; k != kindInherit && k != kindLegacy {
			continue
		}
		if f.Type != reflect.TypeFor[AmenityPresets]() {
			paths[jsonKey(f)] = []int{i}
			continue
		}
		for j := 0; j < f.Type.NumField(); j++ {
			paths[jsonKey(f)+"."+jsonKey(f.Type.Field(j))] = []int{i, j}
		}
	}
	return paths
}()

// Inherits reports whether the card falls back to the default business for the
// field with the given JSON key; false when the key is in NoInherit.
func (p Profile) Inherits(key string) bool {
	return !slices.Contains(p.NoInherit, key)
}

// keepOwn sets out's fields named in loc.NoInherit to loc's own values.
func keepOwn(out *Profile, loc Profile) {
	ov, lv := reflect.ValueOf(out).Elem(), reflect.ValueOf(loc)
	for _, key := range loc.NoInherit {
		if path, ok := inheritPaths[key]; ok {
			ov.FieldByIndex(path).Set(lv.FieldByIndex(path))
		}
	}
}

func foldAddress(site, card Address) Address {
	if card.Street != "" || card.City != "" {
		return card
	}
	out := site
	for _, p := range []struct {
		dst *string
		src string
	}{
		{&out.POBox, card.POBox}, {&out.Lat, card.Lat}, {&out.Lng, card.Lng},
		{&out.MapURL, card.MapURL}, {&out.GBPURL, card.GBPURL},
	} {
		if p.src != "" {
			*p.dst = p.src
		}
	}
	return out
}

// isSet reports whether a field holds a value of its own. Rating counts from
// its Value and ParentOrg from its Name, the keys Resolve overrides on.
func isSet(name string, v reflect.Value) bool {
	switch name {
	case "Rating":
		return v.FieldByName("Value").String() != ""
	case "ParentOrg":
		return v.FieldByName("Name").String() != ""
	}
	switch v.Kind() {
	case reflect.String:
		return v.String() != ""
	case reflect.Bool:
		return v.Bool()
	case reflect.Int:
		return v.Int() != 0
	case reflect.Slice:
		return v.Len() > 0
	case reflect.Pointer:
		return !v.IsNil()
	}
	return !v.IsZero()
}

func jsonKey(f reflect.StructField) string {
	k, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return k
}

// SplitLegacy is the inverse of FromLegacy: it returns p without the values
// the legacy columns hold (phone, hours note and the five address keys) and
// those values as the column contents. addr is the postal_address JSON.
func SplitLegacy(p Profile) (stored Profile, phone, hours string, addr jsontext.Value) {
	addr, _ = jsonx.Marshal(struct {
		Street     string `json:"street"`
		City       string `json:"city"`
		State      string `json:"state"`
		PostalCode string `json:"postal_code"`
		Country    string `json:"country"`
	}{p.Address.Street, p.Address.City, p.Address.State, p.Address.PostalCode, p.Address.Country})
	stored = p
	stored.Phone, stored.HoursNote = "", ""
	stored.Address.Street, stored.Address.City, stored.Address.State, stored.Address.PostalCode, stored.Address.Country = "", "", "", "", ""
	return stored, p.Phone, p.HoursNote, addr
}
