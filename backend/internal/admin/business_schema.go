package admin

import (
	"encoding/json"
	"fmt"
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
// into it. %[1]s and %[2]s are the business type and weekday enums.
const businessSchemaTpl = `{"type":"object","properties":{
"name":{"type":"string","title":"Business name","x-hm-group":"Organisation"},
"business_type":{"type":"string","title":"Business type","enum":%[1]s,"x-hm-group":"Organisation"},
"tax_id":{"type":"string","title":"Tax ID","x-hm-group":"Organisation"},
"no_office":{"type":"boolean","title":"No office in this locale","default":false,"x-hm-group":"Organisation"},
"address":{"type":"object","title":"Postal address","x-hm-group":"Address","properties":{
 "street":{"type":"string","title":"Street"},
 "city":{"type":"string","title":"City"},
 "state":{"type":"string","title":"State or region"},
 "postal_code":{"type":"string","title":"Postal code"},
 "country":{"type":"string","title":"Country"},
 "po_box":{"type":"string","title":"PO box"},
 "lat":{"type":"string","title":"Latitude"},
 "lng":{"type":"string","title":"Longitude"},
 "map_url":{"type":"string","title":"Map address","format":"uri"},
 "gbp_url":{"type":"string","title":"Business profile address","format":"uri"}}},
"phone":{"type":"string","title":"Phone","x-hm-group":"Reach"},
"fax":{"type":"string","title":"Fax","x-hm-group":"Reach"},
"email":{"type":"string","title":"Email","format":"email","x-hm-group":"Reach"},
"contact_points":{"type":"array","title":"Contact points","x-hm-group":"Reach","items":{"type":"object","properties":{
 "type":{"type":"string","title":"Type"},
 "phone":{"type":"string","title":"Phone"},
 "email":{"type":"string","title":"Email","format":"email"}}}},
"hours_note":{"type":"string","title":"Hours note","x-hm-group":"Opening hours"},
"opening_hours":{"type":"array","title":"Opening hours","x-hm-group":"Opening hours","items":{"type":"object","properties":{
 "days":{"type":"array","title":"Days","items":{"type":"string","enum":%[2]s}},
 "opens":{"type":"string","title":"Opens","format":"time"},
 "closes":{"type":"string","title":"Closes","format":"time"}}}},
"special_hours":{"type":"array","title":"Special opening hours","x-hm-group":"Opening hours","items":{"type":"object","properties":{
 "from":{"type":"string","title":"From","format":"date"},
 "to":{"type":"string","title":"To","format":"date"},
 "closed":{"type":"boolean","title":"Closed","default":false},
 "opens":{"type":"string","title":"Opens","format":"time"},
 "closes":{"type":"string","title":"Closes","format":"time"}}}},
"sameas":{"type":"array","title":"Profile addresses","x-hm-group":"Profiles & rating","items":{"type":"string","format":"uri"}},
"rating":{"type":"object","title":"Aggregate rating","x-hm-group":"Profiles & rating","properties":{
 "value":{"type":"string","title":"Rating value"},
 "count":{"type":"string","title":"Number of ratings"}}},
"currency":{"type":"string","title":"Currency","x-hm-group":"Commerce"},
"price_range":{"type":"string","title":"Price range","x-hm-group":"Commerce"},
"area_served":{"type":"array","title":"Area served","x-hm-group":"Commerce","items":{"type":"string"}},
"geo_region":{"type":"string","title":"Geo region","x-hm-group":"Commerce"}}}`

// locale-only property names, dropped from the site-wide form.
var localeOnlyProps = []string{"business_type", "no_office"}

// businessUI holds the help text of the fields that need one. It is a hint map
// rather than schema descriptions because a text field shows its description
// as the placeholder.
var businessUI = map[string]schemaform.UIHint{
	"tax_id":               {Help: "The tax or company number printed on invoices."},
	"hours_note":           {Help: "Free text, like Mon to Fri 9 to 5. Themes and the contact point show it as written."},
	"opening_hours.days":   {Help: "Day names separated by commas, like Monday, Tuesday."},
	"special_hours.to":     {Help: "Leave empty for a single day."},
	"special_hours.closed": {Help: "Closed all day; the times are ignored."},
	"rating.value":         {Help: "Between 0 and 5. Search engines may ignore a rating you state yourself."},
	"currency":             {Help: "A three letter code, like USD."},
	"geo_region":           {Help: "Printed as a geo.region tag on this locale's pages."},
	"area_served":          {Help: "Places this business serves, separated by commas."},
	"sameas":               {Help: "Social and directory profile addresses."},
	"address.lat":          {Help: "Between -90 and 90."},
	"address.lng":          {Help: "Between -180 and 180."},
}

func businessSchema() json.RawMessage {
	types, _ := json.Marshal(bizprofile.Types)
	days, _ := json.Marshal(bizprofile.Days)
	return json.RawMessage(fmt.Sprintf(businessSchemaTpl, types, days))
}

// siteSchema is the site-wide form: the locale-only properties are dropped and
// the name is the Organization's legal name.
func siteSchema() (json.RawMessage, error) {
	s, err := schemaform.WithoutProperties(businessSchema(), localeOnlyProps...)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(strings.Replace(string(s), `"title":"Business name"`, `"title":"Legal name"`, 1)), nil
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
			ID:      "sf-" + strings.ReplaceAll(prefix+p, ".", "-"),
			Label:   pathLabel(p),
			Message: strings.Join(errs[p], " "),
		})
	}
	return out
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
	"map_url": "Map address", "gbp_url": "Business profile address", "sameas": "Profile address",
	"hours_note": "Hours note", "geo_region": "Geo region", "state": "State or region",
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
