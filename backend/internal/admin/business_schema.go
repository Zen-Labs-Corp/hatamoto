package admin

import (
	"encoding/json"
	"fmt"
	"html/template"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/bizprofile"
)

// businessSchemaTpl describes one business profile as a form. Property names
// are the JSON keys of bizprofile.Profile, so a decoded form parses straight
// into it. %[1]s and %[2]s are the business type and weekday enums, %[3]s the
// address properties only a locale card has.
const businessSchemaTpl = `{"type":"object","properties":{
"name":{"type":"string","title":"Legal name","x-hm-group":"Organisation"},
"alternate_name":{"type":"string","title":"Alternate name","x-hm-group":"Organisation"},
"business_type":{"type":"string","title":"Business type","enum":%[1]s,"x-hm-group":"Organisation"},
"description":{"type":"string","title":"Description","format":"textarea","x-hm-group":"Organisation"},
"slogan":{"type":"string","title":"Slogan","x-hm-group":"Organisation"},
"founding_date":{"type":"string","title":"Founding date","x-hm-group":"Organisation"},
"employees_min":{"type":"integer","title":"Employees from","minimum":0,"x-hm-group":"Organisation"},
"employees_max":{"type":"integer","title":"Employees to","minimum":0,"x-hm-group":"Organisation"},
"tax_id":{"type":"string","title":"Tax ID","x-hm-group":"Organisation"},
"vat_id":{"type":"string","title":"VAT ID","x-hm-group":"Organisation"},
"country_name":{"type":"string","title":"Country name","x-hm-group":"Organisation"},
"image":{"type":"string","title":"Image","format":"uri","x-hm-group":"Organisation"},
"logo_object":{"type":"boolean","title":"Print the logo with its width and height","default":false,"x-hm-group":"Organisation"},
"no_office":{"type":"boolean","title":"No office in this locale","default":false,"x-hm-group":"Organisation"},
"naics":{"type":"string","title":"NAICS code","x-hm-group":"Organisation"},
"isic":{"type":"string","title":"ISIC code","x-hm-group":"Organisation"},
"duns":{"type":"string","title":"D-U-N-S number","x-hm-group":"Organisation"},
"lei":{"type":"string","title":"LEI","x-hm-group":"Organisation"},
"gln":{"type":"string","title":"GLN","x-hm-group":"Organisation"},
"founders":{"type":"array","x-hm-item-label":"founder","title":"Founders","x-hm-group":"Organisation","items":{"type":"object","properties":{
 "name":{"type":"string","title":"Name"},
 "url":{"type":"string","title":"Web address","format":"uri"}}}},
"parent_organisation":{"type":"object","title":"Parent organisation","x-hm-group":"Organisation","properties":{
 "name":{"type":"string","title":"Name"},
 "url":{"type":"string","title":"Web address","format":"uri"}}},
"brands":{"type":"array","x-hm-item-label":"brand","title":"Brands","x-hm-group":"Organisation","items":{"type":"string"}},
"address":{"type":"object","title":"Postal address","x-hm-group":"Address","properties":{
 "street":{"type":"string","title":"Street"},
 "city":{"type":"string","title":"City"},
 "state":{"type":"string","title":"State or region"},
 "postal_code":{"type":"string","title":"Postal code"},
 "country":{"type":"string","title":"Country"},
 "po_box":{"type":"string","title":"PO box"},%[3]s
 "gbp_url":{"type":"string","title":"Business profile address","format":"uri"}}},
"phone":{"type":"string","title":"Phone","x-hm-group":"Reach"},
"fax":{"type":"string","title":"Fax","x-hm-group":"Reach"},
"email":{"type":"string","title":"Email","format":"email","x-hm-group":"Reach"},
"contact_points":{"type":"array","x-hm-item-label":"contact point","title":"Contact points","x-hm-group":"Reach","items":{"type":"object","properties":{
 "type":{"type":"string","title":"Type"},
 "name":{"type":"string","title":"Name"},
 "phone":{"type":"string","title":"Phone"},
 "email":{"type":"string","title":"Email","format":"email"},
 "area_served":{"type":"string","title":"Area served"},
 "available_language":{"type":"string","title":"Languages"}}}},
"hours_note":{"type":"string","title":"Hours note","x-hm-group":"Opening hours"},
"opening_hours":{"type":"array","x-hm-item-label":"opening hours","title":"Opening hours","x-hm-group":"Opening hours","items":{"type":"object","properties":{
 "days":{"type":"array","title":"Days","items":{"type":"string","enum":%[2]s}},
 "opens":{"type":"string","title":"Opens","format":"time"},
 "closes":{"type":"string","title":"Closes","format":"time"}}}},
"special_hours":{"type":"array","x-hm-item-label":"special opening hours","title":"Special opening hours","x-hm-group":"Opening hours","items":{"type":"object","properties":{
 "from":{"type":"string","title":"From","format":"date"},
 "to":{"type":"string","title":"To","format":"date"},
 "closed":{"type":"boolean","title":"Closed","default":false},
 "opens":{"type":"string","title":"Opens","format":"time"},
 "closes":{"type":"string","title":"Closes","format":"time"}}}},
"currency":{"type":"string","title":"Currency","x-hm-group":"Commerce"},
"price_range":{"type":"string","title":"Price range","x-hm-group":"Commerce"},
"area_served":{"type":"array","x-hm-item-label":"area","title":"Area served","x-hm-group":"Commerce","items":{"type":"string"}},
"geo_region":{"type":"string","title":"Geo region","x-hm-group":"Commerce"},
"service_radius_km":{"type":"string","title":"Service radius (km)","x-hm-group":"Commerce"},
"payment_methods":{"type":"array","x-hm-item-label":"payment method","title":"Payment methods","x-hm-group":"Commerce","items":{"type":"string"}},
"accepts_reservations":{"type":"string","enum":["yes","no"],"title":"Accepts reservations","x-hm-group":"Commerce"},
"booking_url":{"type":"string","title":"Booking address","format":"uri","x-hm-group":"Commerce"},
"order_url":{"type":"string","title":"Online ordering address","format":"uri","x-hm-group":"Commerce"},
"menu_url":{"type":"string","title":"Menu address","format":"uri","x-hm-group":"Commerce"},
"services":{"type":"array","x-hm-item-label":"service","title":"Services","x-hm-group":"Services","items":{"type":"object","properties":{
 "name":{"type":"string","title":"Name"},
 "description":{"type":"string","title":"Description","format":"textarea"},
 "url":{"type":"string","title":"Web address","format":"uri"},
 "price":{"type":"string","title":"Price"}}}},
"knows_about":{"type":"array","x-hm-item-label":"topic","title":"Knows about","x-hm-group":"Expertise & amenities","items":{"type":"string"}},
"awards":{"type":"array","x-hm-item-label":"award","title":"Awards","x-hm-group":"Expertise & amenities","items":{"type":"string"}},
"memberships":{"type":"array","x-hm-item-label":"membership","title":"Memberships","x-hm-group":"Expertise & amenities","items":{"type":"object","properties":{
 "name":{"type":"string","title":"Name"},
 "url":{"type":"string","title":"Web address","format":"uri"}}}},
"amenity_presets":{"type":"object","title":"Amenities","x-hm-group":"Expertise & amenities","properties":{
 "parking":{"type":"string","enum":["yes","no"],"title":"Parking"},
 "wheelchair_access":{"type":"string","enum":["yes","no"],"title":"Wheelchair access"},
 "wifi":{"type":"string","enum":["yes","no"],"title":"Wifi"},
 "toilets":{"type":"string","enum":["yes","no"],"title":"Toilets"},
 "air_conditioning":{"type":"string","enum":["yes","no"],"title":"Air conditioning"}}},
"amenities":{"type":"array","x-hm-item-label":"amenity","title":"Other amenities","x-hm-group":"Expertise & amenities","items":{"type":"object","properties":{
 "name":{"type":"string","title":"Name"},
 "value":{"type":"boolean","title":"Available","default":false}}}},
"public_access":{"type":"string","enum":["yes","no"],"title":"Open to the public","x-hm-group":"Expertise & amenities"},
"free_entry":{"type":"string","enum":["yes","no"],"title":"Free entry","x-hm-group":"Expertise & amenities"},
"sameas":{"type":"array","x-hm-item-label":"profile address","title":"Profile addresses","x-hm-group":"Profiles & rating","items":{"type":"string","format":"uri"}},
"rating":{"type":"object","title":"Aggregate rating","x-hm-group":"Profiles & rating","properties":{
 "value":{"type":"string","title":"Rating value"},
 "best":{"type":"string","title":"Best rating"},
 "worst":{"type":"string","title":"Worst rating"},
 "count":{"type":"string","title":"Number of ratings"},
 "count_type":{"type":"string","title":"Count is","enum":["ratings","reviews"]}}}}}`

// localeAddressProps are the address properties of a locale card. The site-wide
// form leaves them out: coordinates and the map address describe one place.
const localeAddressProps = `
 "lat":{"type":"string","title":"Latitude"},
 "lng":{"type":"string","title":"Longitude"},
 "map_url":{"type":"string","title":"Map address","format":"uri"},`

// locale-only property names, dropped from the site-wide form.
var localeOnlyProps = []string{"business_type", "no_office", "country_name", "image"}

// site-only property names, dropped from a locale card.
var siteOnlyProps = []string{"alternate_name", "slogan", "founding_date", "employees_min", "employees_max", "logo_object", "parent_organisation"}

// notStated labels the empty choice of a tri-state field: unset, so nothing is
// printed (and a locale card inherits the site-wide answer).
const notStated = "Not stated"

// triPaths are the tri-state fields (*bool in the profile). The form edits them
// as selects: empty is unset, "yes" is true, "no" is false.
var triPaths = [][]string{
	{"accepts_reservations"}, {"public_access"}, {"free_entry"},
	{"amenity_presets", "parking"}, {"amenity_presets", "wheelchair_access"}, {"amenity_presets", "wifi"},
	{"amenity_presets", "toilets"}, {"amenity_presets", "air_conditioning"},
}

// triToForm and triFromForm convert the tri-state values of a profile map
// between the stored booleans and the select choices.
func triToForm(vals map[string]any) {
	triConvert(vals, func(b bool) any { return map[bool]string{true: "yes", false: "no"}[b] })
}
func triFromForm(vals map[string]any) { triConvert(vals, nil) }

func triConvert(vals map[string]any, toForm func(bool) any) {
	for _, path := range triPaths {
		m := vals
		for _, k := range path[:len(path)-1] {
			m, _ = m[k].(map[string]any)
		}
		if m == nil {
			continue
		}
		last := path[len(path)-1]
		switch v := m[last].(type) {
		case bool:
			if toForm != nil {
				m[last] = toForm(v)
			}
		case string:
			if toForm == nil {
				m[last] = v == "yes"
			}
		}
	}
}

// businessUI holds the help text of the fields that need one. It is a hint map
// rather than schema descriptions because a text field shows its description
// as the placeholder.
var businessUI = map[string]schemaform.UIHint{
	"tax_id":                            {Help: "The tax or company number printed on invoices."},
	"description":                       {Help: "Empty uses the tagline."},
	"founding_date":                     {Help: "A year, like 2009, or a full date, like 2009-04-30"},
	"logo_object":                       {Help: "Only when the logo size is known. Otherwise the logo prints as an address."},
	"contact_points.available_language": {Help: "A language name, like English. Empty uses the locale's language."},
	"rating.best":                       {Help: "The top of your scale. Empty means 5."},
	"rating.worst":                      {Help: "The bottom of your scale. Empty means 0."},
	"hours_note":                        {Help: "Free text, like Mon to Fri 9 to 5. Themes and the contact point show it as written."},
	"opening_hours.days":                {Help: "Day names separated by commas, like Monday, Tuesday."},
	"special_hours.to":                  {Help: "Leave empty for a single day"},
	"special_hours.closed":              {Help: "Closed all day; the times are ignored."},
	"rating.value":                      {Help: "Within your best and worst rating, 0 to 5 when those are empty. Search engines may ignore a rating you state yourself."},
	"currency":                          {Help: "A three letter code, like USD"},
	"geo_region":                        {Help: "Printed as a geo.region tag on this locale's pages."},
	"area_served":                       {Help: "Places this business serves, separated by commas."},
	"sameas":                            {Help: "Social and directory profile addresses."},
	"naics":                             {Help: "Two to six digits"},
	"isic":                              {Help: "Four digits, ISIC revision 4"},
	"duns":                              {Help: "Nine digits"},
	"lei":                               {Help: "Twenty letters and digits"},
	"gln":                               {Help: "Thirteen digits"},
	"service_radius_km":                 {Help: "Kilometres around the coordinates of this business. Needs both coordinates."},
	"brands":                            {Help: "Brand names, separated by commas."},
	"payment_methods":                   {Help: "Like Cash or Visa, separated by commas."},
	"accepts_reservations":              {Help: "Choose Not stated to say nothing.", Placeholder: notStated},
	"amenity_presets":                   {Help: "Choose Not stated on a line to say nothing about it."},
	"amenity_presets.parking":           {Placeholder: notStated},
	"amenity_presets.wheelchair_access": {Placeholder: notStated},
	"amenity_presets.wifi":              {Placeholder: notStated},
	"amenity_presets.toilets":           {Placeholder: notStated},
	"amenity_presets.air_conditioning":  {Placeholder: notStated},
	"public_access":                     {Placeholder: notStated},
	"free_entry":                        {Placeholder: notStated},
	"amenities":                         {Help: "Features beyond the list above."},
	"knows_about":                       {Help: "Topics this business is expert in, separated by commas."},
	"address.lat":                       {Help: "Between -90 and 90"},
	"address.lng":                       {Help: "Between -180 and 180"},
}

// plusIcon is the glyph of the Add buttons in the business forms.
func plusIcon() template.HTML {
	svg, _ := iconSVG("plus") // a constant name; TestIconsAllNamesPresent covers it
	return svg
}

// businessSchema is the form of a locale card: the site-only properties are
// left out.
func businessSchema() json.RawMessage {
	s, err := schemaform.WithoutProperties(businessSchemaWith(localeAddressProps), siteOnlyProps...)
	if err != nil {
		return businessSchemaWith(localeAddressProps)
	}
	return s
}

func businessSchemaWith(addressProps string) json.RawMessage {
	types, _ := json.Marshal(bizprofile.Types)
	days, _ := json.Marshal(bizprofile.Days)
	return json.RawMessage(fmt.Sprintf(businessSchemaTpl, types, days, addressProps))
}

// siteSchema is the site-wide form: the locale-only properties are dropped.
func siteSchema() (json.RawMessage, error) {
	s, err := schemaform.WithoutProperties(businessSchemaWith(""), localeOnlyProps...)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// localeDisplayName is the English name of a BCP 47 tag ("French (Canada)"), or
// the raw tag when it cannot be parsed or has no name.
func localeDisplayName(tag string) string {
	t, err := language.Parse(tag)
	if err != nil {
		return tag
	}
	if name := display.English.Tags().Name(t); name != "" {
		return name
	}
	return tag
}

// formErrors turns schemaform errors into error summary entries whose IDs are
// the field ids of a form rendered with prefix. Entries are ordered by path.
func formErrors(prefix string, errs schemaform.Errors) []ui.FieldError {
	paths := make([]string, 0, len(errs))
	for p := range errs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]ui.FieldError, 0, len(paths))
	for _, p := range paths {
		out = append(out, ui.FieldError{
			ID:      "sf-" + strings.ReplaceAll(prefix+tagListField(p), ".", "-"),
			Label:   pathLabel(p),
			Message: strings.Join(errs[p], " "),
		})
	}
	return out
}

// tagLists are the string lists the form edits as one comma separated field.
var tagLists = []string{"area_served", "brands", "payment_methods", "knows_about", "awards"}

// tagListField maps an item path ("brands.2") to its field ("brands"); other
// paths stay as they are.
func tagListField(path string) string {
	name, idx, ok := strings.Cut(path, ".")
	if ok && slices.Contains(tagLists, name) {
		if _, err := strconv.Atoi(idx); err == nil {
			return name
		}
	}
	return path
}

// profileErrors adapts bizprofile.Validate messages to schemaform errors.
func profileErrors(msgs map[string]string) schemaform.Errors {
	if len(msgs) == 0 {
		return nil
	}
	out := make(schemaform.Errors, len(msgs))
	for p, m := range msgs {
		out[p] = []string{m}
	}
	return out
}

// segmentLabels name the keys whose humanized form reads badly.
var segmentLabels = map[string]string{
	"tax_id": "Tax ID", "po_box": "PO box", "lat": "Latitude", "lng": "Longitude",
	"vat_id": "VAT ID", "alternate_name": "Alternate name", "country_name": "Country name",
	"employees_min": "Employees from", "employees_max": "Employees to", "count_type": "Count is",
	"available_language": "Languages", "area_served": "Area served", "founding_date": "Founding date",
	"map_url": "Map address", "gbp_url": "Business profile address", "sameas": "Profile address",
	"hours_note": "Hours note", "geo_region": "Geo region", "state": "State or region",
	"naics": "NAICS code", "isic": "ISIC code", "duns": "D-U-N-S number", "lei": "LEI", "gln": "GLN",
	"parent_organisation": "Parent organisation", "service_radius_km": "Service radius (km)",
	"booking_url": "Booking address", "order_url": "Online ordering address", "menu_url": "Menu address",
	"knows_about": "Knows about", "url": "Web address", "wifi": "Wifi", "free_entry": "Free entry",
}

// pathLabel reads "opening_hours.0.opens" as "Opening hours 1 Opens".
func pathLabel(path string) string {
	var words []string
	for _, seg := range strings.Split(path, ".") {
		if n, err := strconv.Atoi(seg); err == nil {
			words = append(words, strconv.Itoa(n+1))
		} else if l, ok := segmentLabels[seg]; ok {
			words = append(words, l)
		} else {
			words = append(words, schemaform.Humanize(seg))
		}
	}
	return strings.Join(words, " ")
}
