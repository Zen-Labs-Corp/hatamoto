package bizprofile

import (
	"fmt"
	"math"
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
	maxDescLen  = 1000 // the description is prose in a textarea
	maxPhoneLen = 50
	maxURLLen   = 2000
	// maxRadiusKm keeps the radius in metres a finite JSON number.
	maxRadiusKm = 20000
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

// IsFoodType reports whether a business type is a schema.org FoodEstablishment
// (the only type that defines acceptsReservations and hasMenu).
func IsFoodType(t string) bool {
	switch t {
	case "Restaurant", "BarOrPub", "Bakery", "CafeOrCoffeeShop":
		return true
	}
	return false
}

// Days are the day names an opening hours block may list.
var Days = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

var (
	timeRE     = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	currencyRE = regexp.MustCompile(`^[A-Za-z]{3}$`)
	decimalRE  = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)
	naicsRE    = regexp.MustCompile(`^\d{2,6}$`)
	isicRE     = regexp.MustCompile(`^\d{4}$`)
	dunsRE     = regexp.MustCompile(`^\d{9}$`)
	leiRE      = regexp.MustCompile(`^[A-Z0-9]{20}$`)
	glnRE      = regexp.MustCompile(`^\d{13}$`)
)

// ParseDecimal parses a decimal number: digits with an optional sign, fraction
// (a leading or trailing dot is fine) and exponent. Unlike strconv.ParseFloat it
// rejects hex floats, Inf, NaN and underscores, so a stored value always prints
// as a JSON number.
func ParseDecimal(v string) (float64, bool) {
	if !decimalRE.MatchString(v) {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

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
		if f, ok := ParseDecimal(v); !ok || f < -limit || f > limit {
			errs[path] = fmt.Sprintf("Enter a number between -%g and %g.", limit, limit)
		}
	}

	ident := func(path, v string, re *regexp.Regexp, msg string) {
		if v != "" && !re.MatchString(strings.ToUpper(v)) {
			errs[path] = msg
		}
	}
	// named checks a row that needs a name; the repeater cannot drop an
	// otherwise empty row whose switch always counts as set.
	named := func(path, name string) {
		if strings.TrimSpace(name) == "" {
			errs[path+".name"] = "Enter a name."
			return
		}
		text(path+".name", name)
	}
	money := func(path, v string) {
		if v == "" {
			return
		}
		if f, ok := ParseDecimal(v); !ok || f < 0 {
			errs[path] = "Enter a price of 0 or more, such as 49.90."
		}
	}
	texts := func(key string, vs []string) {
		for i, v := range vs {
			text(key+"."+strconv.Itoa(i), v)
		}
	}
	people := func(key string, ps []Person) {
		for i, v := range ps {
			at := key + "." + strconv.Itoa(i)
			named(at, v.Name)
			link(at+".url", v.URL)
		}
	}

	text("name", p.Name)
	text("tax_id", p.TaxID)
	text("vat_id", p.VATID)
	text("alternate_name", p.AlternateName)
	if utf8.RuneCountInString(p.Description) > maxDescLen {
		errs["description"] = fmt.Sprintf("Use at most %d characters.", maxDescLen)
	}
	text("slogan", p.Slogan)
	text("country_name", p.CountryName)
	if utf8.RuneCountInString(p.Image) > maxURLLen {
		errs["image"] = fmt.Sprintf("Use at most %d characters.", maxURLLen)
	}
	if v := p.FoundingDate; v != "" && !isFoundingDate(v) {
		errs["founding_date"] = "Use a year or a date such as 2009, 2009-05 or 2009-05-17."
	}
	if p.EmployeesMin < 0 {
		errs["employees_min"] = "Enter a whole number of 0 or more."
	}
	if p.EmployeesMax < 0 {
		errs["employees_max"] = "Enter a whole number of 0 or more."
	} else if p.EmployeesMax > 0 && p.EmployeesMin > p.EmployeesMax {
		errs["employees_max"] = "Enter a number that is not lower than the minimum."
	}
	switch p.OrgAddress {
	case "", OrgAddressLocale, OrgAddressLocaleOrSite:
	default:
		errs["org_address"] = "Choose one of the listed address options."
	}
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
	if (a.Lat == "") != (a.Lng == "") {
		missing := "address.lng"
		if a.Lng != "" {
			missing = "address.lat"
		}
		if _, bad := errs[missing]; !bad {
			errs[missing] = "Enter both latitude and longitude."
		}
	}
	link("address.map_url", a.MapURL)
	link("address.gbp_url", a.GBPURL)
	phone("phone", p.Phone)
	phone("fax", p.Fax)
	email("email", p.Email)
	text("hours_note", p.HoursNote)
	for i, c := range p.ContactPoints {
		at := func(k string) string { return "contact_points." + strconv.Itoa(i) + "." + k }
		text(at("type"), c.Type)
		text(at("name"), c.Name)
		text(at("area_served"), c.AreaServed)
		text(at("available_language"), c.AvailableLanguage)
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
		if !h.Closed && h.Opens == "" && h.Closes == "" {
			errs[at("opens")] = "Enter the times, or tick Closed."
		}
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
	validateRating(errs, p.Rating)
	ident("naics", p.Naics, naicsRE, "Enter 2 to 6 digits.")
	ident("isic", p.Isic, isicRE, "Enter 4 digits.")
	ident("duns", p.Duns, dunsRE, "Enter 9 digits.")
	ident("lei", p.Lei, leiRE, "Enter 20 letters or digits.")
	ident("gln", p.Gln, glnRE, "Enter 13 digits.")
	people("founders", p.Founders)
	people("memberships", p.Memberships)
	if p.ParentOrg != (Person{}) {
		named("parent_organisation", p.ParentOrg.Name)
		link("parent_organisation.url", p.ParentOrg.URL)
	}
	texts("brands", p.Brands)
	texts("payment_methods", p.PaymentMethods)
	texts("knows_about", p.KnowsAbout)
	texts("awards", p.Awards)
	link("booking_url", p.BookingURL)
	link("order_url", p.OrderURL)
	link("menu_url", p.MenuURL)
	if v := p.ServiceRadiusKm; v != "" {
		if f, ok := ParseDecimal(v); !ok || f <= 0 || f > maxRadiusKm {
			errs["service_radius_km"] = "Enter a distance in kilometres."
		}
	}
	for i, v := range p.Services {
		at := "services." + strconv.Itoa(i)
		named(at, v.Name)
		if utf8.RuneCountInString(v.Description) > maxDescLen {
			errs[at+".description"] = fmt.Sprintf("Use at most %d characters.", maxDescLen)
		}
		link(at+".url", v.URL)
		money(at+".price", v.Price)
	}
	for i, v := range p.Amenities {
		named("amenities."+strconv.Itoa(i), v.Name)
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

// validateRating checks the rating bounds and that the value lies within them
// (0 to 5 when a bound is not set).
func validateRating(errs map[string]string, r Rating) {
	lo, hi := 0.0, 5.0
	bound := func(path, v string, def *float64) bool {
		if v == "" {
			return true
		}
		f, ok := ParseDecimal(v)
		if !ok {
			errs[path] = "Enter a number such as 5."
			return false
		}
		*def = f
		return true
	}
	okBest := bound("rating.best", r.Best, &hi)
	okWorst := bound("rating.worst", r.Worst, &lo)
	if okBest && okWorst && lo >= hi {
		errs["rating.best"] = "Enter a best rating higher than the worst."
		return
	}
	switch r.CountType {
	case "", "ratings", "reviews":
	default:
		errs["rating.count_type"] = "Choose ratings or reviews."
	}
	if r.Value == "" || !okBest || !okWorst {
		return
	}
	if f, ok := ParseDecimal(r.Value); !ok || f < lo || f > hi {
		errs["rating.value"] = fmt.Sprintf("Enter a number between %g and %g.", lo, hi)
	}
}

func isFoundingDate(v string) bool {
	for _, layout := range []string{"2006", "2006-01", time.DateOnly} {
		if len(v) == len(layout) {
			if _, err := time.Parse(layout, v); err == nil {
				return true
			}
		}
	}
	return false
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
