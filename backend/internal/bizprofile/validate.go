package bizprofile

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Field limits, equal to the site settings contact limits.
const (
	maxTextLen  = 200
	maxPhoneLen = 50
	maxURLLen   = 2000
)

// Types are the business types a profile may name: LocalBusiness and a curated
// set of its schema.org subtypes.
var Types = []string{
	"LocalBusiness",
	"AccountingService",
	"AutoRepair",
	"BarOrPub",
	"Bakery",
	"BeautySalon",
	"BookStore",
	"CafeOrCoffeeShop",
	"ChildCare",
	"ClothingStore",
	"DaySpa",
	"Dentist",
	"Electrician",
	"EmploymentAgency",
	"FinancialService",
	"FitnessCenter",
	"Florist",
	"GeneralContractor",
	"HairSalon",
	"HardwareStore",
	"HealthClub",
	"HomeAndConstructionBusiness",
	"HVACBusiness",
	"InsuranceAgency",
	"LegalService",
	"Library",
	"LodgingBusiness",
	"MedicalBusiness",
	"MovingCompany",
	"Notary",
	"Optician",
	"Physician",
	"Plumber",
	"ProfessionalService",
	"RealEstateAgent",
	"Restaurant",
	"RoofingContractor",
	"SelfStorage",
	"ShoppingCenter",
	"SportsActivityLocation",
	"Store",
	"TravelAgency",
	"VeterinaryCare",
}

// Days are the day names an opening hours block may list.
var Days = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

var (
	timeRE     = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	currencyRE = regexp.MustCompile(`^[A-Za-z]{3}$`)
)

// Validate checks a profile and returns a message per offending field, keyed by
// the dotted JSON path (list items carry their index: "opening_hours.0.opens").
// An empty map means the profile is valid.
func Validate(p Profile) map[string]string {
	errs := map[string]string{}
	text := func(path, v string) {
		if utf8.RuneCountInString(v) > maxTextLen {
			errs[path] = fmt.Sprintf("Use at most %d characters.", maxTextLen)
		}
	}
	phone := func(path, v string) {
		if utf8.RuneCountInString(v) > maxPhoneLen {
			errs[path] = fmt.Sprintf("Use at most %d characters.", maxPhoneLen)
		}
	}
	email := func(path, v string) {
		if v == "" {
			return
		}
		if utf8.RuneCountInString(v) > maxTextLen {
			errs[path] = fmt.Sprintf("Use at most %d characters.", maxTextLen)
		} else if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			errs[path] = "Enter a valid email address."
		}
	}
	link := func(path, v string) {
		if v == "" {
			return
		}
		if utf8.RuneCountInString(v) > maxURLLen {
			errs[path] = fmt.Sprintf("Use at most %d characters.", maxURLLen)
		} else if !isHTTPURL(v) {
			errs[path] = "Enter a full web address starting with http:// or https://."
		}
	}
	clock := func(path, v string) {
		if v != "" && !timeRE.MatchString(v) {
			errs[path] = "Use a 24 hour time such as 09:00."
		}
	}
	date := func(path, v string) {
		if v == "" {
			return
		}
		if _, err := time.Parse(time.DateOnly, v); err != nil {
			errs[path] = "Use a date such as 2030-01-31."
		}
	}
	coord := func(path, v string, limit float64) {
		if v == "" {
			return
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < -limit || f > limit {
			errs[path] = fmt.Sprintf("Enter a number between -%g and %g.", limit, limit)
		}
	}

	text("name", p.Name)
	text("tax_id", p.TaxID)
	if p.BusinessType != "" && !isType(p.BusinessType) {
		errs["business_type"] = "Choose a business type from the list."
	}
	a := p.Address
	text("address.street", a.Street)
	text("address.city", a.City)
	text("address.state", a.State)
	text("address.postal_code", a.PostalCode)
	text("address.country", a.Country)
	text("address.po_box", a.POBox)
	coord("address.lat", a.Lat, 90)
	coord("address.lng", a.Lng, 180)
	link("address.map_url", a.MapURL)
	link("address.gbp_url", a.GBPURL)
	phone("phone", p.Phone)
	phone("fax", p.Fax)
	email("email", p.Email)
	text("hours_note", p.HoursNote)
	for i, c := range p.ContactPoints {
		at := func(k string) string { return "contact_points." + strconv.Itoa(i) + "." + k }
		text(at("type"), c.Type)
		phone(at("phone"), c.Phone)
		email(at("email"), c.Email)
	}
	for i, h := range p.OpeningHours {
		at := func(k string) string { return "opening_hours." + strconv.Itoa(i) + "." + k }
		for _, d := range h.Days {
			if !isDay(d) {
				errs[at("days")] = "Choose days of the week."
				break
			}
		}
		clock(at("opens"), h.Opens)
		clock(at("closes"), h.Closes)
	}
	for i, h := range p.SpecialHours {
		at := func(k string) string { return "special_hours." + strconv.Itoa(i) + "." + k }
		date(at("from"), h.From)
		date(at("to"), h.To)
		clock(at("opens"), h.Opens)
		clock(at("closes"), h.Closes)
	}
	for i, v := range p.AreaServed {
		text("area_served."+strconv.Itoa(i), v)
	}
	if p.Currency != "" && !currencyRE.MatchString(p.Currency) {
		errs["currency"] = "Use a three letter currency code."
	}
	text("price_range", p.PriceRange)
	text("geo_region", p.GeoRegion)
	for i, v := range p.SameAs {
		link("sameas."+strconv.Itoa(i), v)
	}
	if v := p.Rating.Value; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 || f > 5 {
			errs["rating.value"] = "Enter a number between 0 and 5."
		}
	}
	if c := p.Rating.Count; c != "" {
		if n, err := strconv.Atoi(c); err != nil || n < 1 {
			errs["rating.count"] = "Enter a whole number of 1 or more."
		} else if p.Rating.Value == "" {
			errs["rating.value"] = "Enter the rating value."
		}
	}
	return errs
}

func isType(v string) bool {
	for _, t := range Types {
		if t == v {
			return true
		}
	}
	return false
}

func isDay(v string) bool {
	for _, d := range Days {
		if d == v {
			return true
		}
	}
	return false
}

func isHTTPURL(v string) bool {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return false
	}
	s := strings.ToLower(u.Scheme)
	return s == "http" || s == "https"
}
