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
}

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
