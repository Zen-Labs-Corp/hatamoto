package bizprofile

// DefaultType is the business type used when a locale card names none.
const DefaultType = "LocalBusiness"

// Resolved is the local business of one locale after fallback. When Office is
// false nothing is printed and Profile is the zero value.
type Resolved struct {
	Office  bool
	Profile Profile
}

// Resolve merges a locale card over the site-wide profile, field by field. The
// address is the exception: it is a block, taken whole from the card when the
// card has a street or a city, and otherwise the locale has no office. The
// site-wide address is never copied into a locale.
//
// Name is the card's own override only; the caller falls back to the site name.
// Rating, lists and text fall back to the site-wide value when the card's is
// empty.
func Resolve(site Profile, loc *Profile) Resolved {
	if loc == nil || loc.NoOffice || (loc.Address.Street == "" && loc.Address.City == "") {
		return Resolved{}
	}
	out := Profile{
		Name:          loc.Name,
		TaxID:         pick(loc.TaxID, site.TaxID),
		BusinessType:  pick(loc.BusinessType, DefaultType),
		Address:       loc.Address,
		Phone:         pick(loc.Phone, site.Phone),
		Fax:           pick(loc.Fax, site.Fax),
		Email:         pick(loc.Email, site.Email),
		HoursNote:     pick(loc.HoursNote, site.HoursNote),
		ContactPoints: pickList(loc.ContactPoints, site.ContactPoints),
		OpeningHours:  pickList(loc.OpeningHours, site.OpeningHours),
		SpecialHours:  pickList(loc.SpecialHours, site.SpecialHours),
		AreaServed:    pickList(loc.AreaServed, site.AreaServed),
		Currency:      pick(loc.Currency, site.Currency),
		PriceRange:    pick(loc.PriceRange, site.PriceRange),
		GeoRegion:     pick(loc.GeoRegion, site.GeoRegion),
		SameAs:        pickList(loc.SameAs, site.SameAs),
		Rating:        site.Rating,
	}
	if loc.Rating.Value != "" {
		out.Rating = loc.Rating
	}
	return Resolved{Office: true, Profile: out}
}

func pick(own, fallback string) string {
	if own != "" {
		return own
	}
	return fallback
}

func pickList[T any](own, fallback []T) []T {
	if len(own) > 0 {
		return own
	}
	return fallback
}
