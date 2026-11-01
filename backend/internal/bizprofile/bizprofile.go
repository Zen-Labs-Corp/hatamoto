// Package bizprofile holds the business details of a site: one site-wide
// Profile and one Profile per public locale. It is pure data (parse, validate,
// resolve) and imports neither the database nor the renderer.
//
// The site settings keep phone, business hours and the postal address in their
// own columns; FromLegacy applies them to a stored Profile so those three
// values have a single source.
package bizprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Address is a postal address plus the map references of one place.
type Address struct {
	Street     string `json:"street,omitempty"`
	City       string `json:"city,omitempty"`
	State      string `json:"state,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"`
	POBox      string `json:"po_box,omitempty"`
	Lat        string `json:"lat,omitempty"`
	Lng        string `json:"lng,omitempty"`
	MapURL     string `json:"map_url,omitempty"`
	GBPURL     string `json:"gbp_url,omitempty"`
}

// ContactPoint is one department or channel (sales, support, ...).
type ContactPoint struct {
	Type  string `json:"type,omitempty"`
	Phone string `json:"phone,omitempty"`
	Email string `json:"email,omitempty"`
	// Name labels the point (for example "Sales Europe"); AreaServed is a region
	// or country; AvailableLanguage is a language name such as "English".
	Name              string `json:"name,omitempty"`
	AreaServed        string `json:"area_served,omitempty"`
	AvailableLanguage string `json:"available_language,omitempty"`
}

// Hours is one block of regular opening hours; Opens and Closes are HH:MM.
type Hours struct {
	Days   []string `json:"days,omitempty"`
	Opens  string   `json:"opens,omitempty"`
	Closes string   `json:"closes,omitempty"`
}

// SpecialHours is a dated exception: a closed span or different hours. From and
// To are YYYY-MM-DD (To may be empty for a single day).
type SpecialHours struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Closed bool   `json:"closed,omitempty"`
	Opens  string `json:"opens,omitempty"`
	Closes string `json:"closes,omitempty"`
}

// Rating is an aggregate rating as the owner states it.
type Rating struct {
	Value string `json:"value,omitempty"`
	Count string `json:"count,omitempty"`
	// Best and Worst bound Value (default 5 and 0 when empty). CountType is
	// "ratings" (default) or "reviews": what Count counts.
	Best      string `json:"best,omitempty"`
	Worst     string `json:"worst,omitempty"`
	CountType string `json:"count_type,omitempty"`
}

// Values of Profile.OrgAddress. The empty value keeps the site-wide address on
// every page.
const (
	// OrgAddressLocale prints the locale's office address and phone on the
	// Organization, and none for a locale without an office.
	OrgAddressLocale = "locale"
	// OrgAddressLocaleOrSite is like OrgAddressLocale but falls back to the
	// site-wide address for a locale without an office.
	OrgAddressLocaleOrSite = "locale_or_site"
)

// Profile is the set of business fields. The site-wide profile uses every field
// except BusinessType and NoOffice; a locale profile may use all of them.
type Profile struct {
	Name         string `json:"name,omitempty"`
	TaxID        string `json:"tax_id,omitempty"`
	BusinessType string `json:"business_type,omitempty"`
	// NoOffice marks a locale without an office: it prints no local business.
	NoOffice      bool           `json:"no_office,omitempty"`
	Address       Address        `json:"address"`
	Phone         string         `json:"phone,omitempty"`
	Fax           string         `json:"fax,omitempty"`
	Email         string         `json:"email,omitempty"`
	HoursNote     string         `json:"hours_note,omitempty"`
	ContactPoints []ContactPoint `json:"contact_points,omitempty"`
	OpeningHours  []Hours        `json:"opening_hours,omitempty"`
	SpecialHours  []SpecialHours `json:"special_hours,omitempty"`
	AreaServed    []string       `json:"area_served,omitempty"`
	Currency      string         `json:"currency,omitempty"`
	PriceRange    string         `json:"price_range,omitempty"`
	GeoRegion     string         `json:"geo_region,omitempty"`
	SameAs        []string       `json:"sameas,omitempty"`
	Rating        Rating         `json:"rating"`

	// Organisation details. Description, VATID, CountryName and Image also
	// apply to a locale card; the rest is site-wide only.
	Description   string `json:"description,omitempty"`
	AlternateName string `json:"alternate_name,omitempty"`
	Slogan        string `json:"slogan,omitempty"`
	// FoundingDate is YYYY, YYYY-MM or YYYY-MM-DD.
	FoundingDate string `json:"founding_date,omitempty"`
	EmployeesMin int    `json:"employees_min,omitempty"`
	EmployeesMax int    `json:"employees_max,omitempty"`
	VATID        string `json:"vat_id,omitempty"`
	// CountryName is the country as a locale card names it.
	CountryName string `json:"country_name,omitempty"`
	// Image is the media URL of a locale's local business image.
	Image string `json:"image,omitempty"`
	// LogoObject prints the logo as an ImageObject with its width and height.
	LogoObject bool `json:"logo_object,omitempty"`
	// OrgAddress is "", OrgAddressLocale or OrgAddressLocaleOrSite.
	OrgAddress string `json:"org_address,omitempty"`
}

// Parse decodes a stored profile. Empty input, null and {} give the zero
// Profile; unknown keys are ignored.
func Parse(raw []byte) (Profile, error) {
	var p Profile
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, fmt.Errorf("parse business profile: %w", err)
	}
	return p, nil
}

// ParseLocales decodes the per-locale map, keyed by BCP 47 tag.
func ParseLocales(raw []byte) (map[string]Profile, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]Profile
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse locale profiles: %w", err)
	}
	return m, nil
}

// FromLegacy applies the three values kept in their own columns (phone, the
// free-text hours and the five address keys) to p. The legacy values always
// win, empty or not, so the columns stay the single source.
func FromLegacy(p Profile, phone, hours string, addr json.RawMessage) Profile {
	var a struct {
		Street     string `json:"street"`
		City       string `json:"city"`
		State      string `json:"state"`
		PostalCode string `json:"postal_code"`
		Country    string `json:"country"`
	}
	if len(bytes.TrimSpace(addr)) > 0 {
		_ = json.Unmarshal(addr, &a)
	}
	p.Phone = phone
	p.HoursNote = hours
	p.Address.Street = a.Street
	p.Address.City = a.City
	p.Address.State = a.State
	p.Address.PostalCode = a.PostalCode
	p.Address.Country = a.Country
	return p
}
