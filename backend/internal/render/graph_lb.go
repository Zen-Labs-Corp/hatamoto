package render

import "hatamoto.dev/core/internal/bizprofile"

// localBusinessLD is the LocalBusiness node of the page's locale, linked to the
// shared Organization by parentOrganization. It reads only the BusinessView:
// the site-wide address is never a fallback.
func localBusinessLD(v *PageView, base string) map[string]any {
	b := v.Business
	typ := b.Type
	if typ == "" {
		typ = "LocalBusiness"
	}
	lb := map[string]any{
		"@type":              typ,
		"@id":                base + "#localbusiness-" + b.ID,
		"name":               firstNonEmpty(b.Name, v.Site.Name),
		"parentOrganization": map[string]any{"@id": base + "#organization"},
	}
	setIf(lb, "telephone", b.Phone)
	setIf(lb, "faxNumber", b.Fax)
	setIf(lb, "email", b.Email)
	setIf(lb, "taxID", b.TaxID)
	setIf(lb, "priceRange", b.PriceRange)
	setIf(lb, "currenciesAccepted", b.Currency)
	setIf(lb, "hasMap", b.MapURL)
	setIf(lb, "vatID", b.VATID)
	setIf(lb, "legalName", b.Name)
	setIf(lb, "url", localeHome(v, base))
	if img := firstNonEmpty(b.Image, v.Site.LogoURL); img != "" {
		lb["image"] = absURL(base, img)
	}
	setIf(lb, "description", firstNonEmpty(b.Description, v.Site.Org.Description, v.Site.Tagline))
	addr := map[string]any{"@type": "PostalAddress"}
	for k, val := range map[string]string{
		"streetAddress": b.Address.Street, "addressLocality": b.Address.City, "addressRegion": b.Address.State,
		"postalCode": b.Address.PostalCode, "addressCountry": b.Address.Country, "postOfficeBoxNumber": b.POBox,
	} {
		setIf(addr, k, val)
	}
	lb["address"] = addr
	if lat, ok := bizprofile.ParseDecimal(b.Lat); ok {
		if lng, ok := bizprofile.ParseDecimal(b.Lng); ok {
			lb["geo"] = map[string]any{"@type": "GeoCoordinates", "latitude": lat, "longitude": lng}
		}
	}
	var specs []any
	for _, h := range b.OpeningHours {
		if len(h.Days) == 0 {
			continue
		}
		spec := map[string]any{"@type": "OpeningHoursSpecification", "dayOfWeek": h.Days}
		setIf(spec, "opens", h.Opens)
		setIf(spec, "closes", h.Closes)
		specs = append(specs, spec)
	}
	if len(specs) > 0 {
		lb["openingHoursSpecification"] = oneOrList(specs)
	}
	specs = nil
	for _, h := range b.SpecialHours {
		// A row without a date, or with neither times nor a closed flag, says
		// nothing: skip it rather than print a bare date range.
		if h.From == "" || (!h.Closed && h.Opens == "" && h.Closes == "") {
			continue
		}
		spec := map[string]any{
			"@type": "OpeningHoursSpecification", "validFrom": h.From,
			"validThrough": firstNonEmpty(h.To, h.From),
		}
		setIf(spec, "opens", h.Opens)
		setIf(spec, "closes", h.Closes)
		if h.Closed {
			spec["opens"], spec["closes"] = "00:00", "00:00"
		}
		specs = append(specs, spec)
	}
	if len(specs) > 0 {
		lb["specialOpeningHoursSpecification"] = oneOrList(specs)
	}
	if len(b.AreaServed) > 0 {
		lb["areaServed"] = b.AreaServed
	} else if b.CountryName != "" {
		lb["areaServed"] = countryLD(b.CountryName, v.Locale)
	}
	if circle := geoCircleLD(b); circle != nil {
		switch served := lb["areaServed"].(type) {
		case nil:
			lb["areaServed"] = circle
		case []string:
			list := make([]any, 0, len(served)+1)
			for _, s := range served {
				list = append(list, s)
			}
			lb["areaServed"] = append(list, circle)
		default:
			lb["areaServed"] = []any{served, circle}
		}
	}
	parent := lb["parentOrganization"]
	setDetailsLD(lb, b.BusinessDetails, b.ServicesCurrency)
	lb["parentOrganization"] = parent // the external parent prints on the Organization only
	if b.AcceptsReservations != nil {
		lb["acceptsReservations"] = *b.AcceptsReservations
	}
	setIf(lb, "hasMenu", b.MenuURL)
	if b.PublicAccess != nil {
		lb["publicAccess"] = *b.PublicAccess
	}
	if b.FreeEntry != nil {
		lb["isAccessibleForFree"] = *b.FreeEntry
	}
	if features := amenitiesLD(b.Amenities); len(features) > 0 {
		lb["amenityFeature"] = oneOrList(features)
	}
	language := ""
	for _, l := range v.Locales {
		if l.Tag == v.Locale {
			language = l.Language
		}
	}
	var points []any
	for _, c := range b.ContactPoints {
		if c.Phone == "" && c.Email == "" {
			continue
		}
		cp := map[string]any{"@type": "ContactPoint"}
		setIf(cp, "contactType", c.Type)
		setIf(cp, "telephone", c.Phone)
		setIf(cp, "email", c.Email)
		setIf(cp, "name", c.Name)
		setIf(cp, "areaServed", c.AreaServed)
		setIf(cp, "availableLanguage", firstNonEmpty(c.AvailableLanguage, language))
		points = append(points, cp)
	}
	if len(points) > 0 {
		lb["contactPoint"] = points
	}
	if same := appendUnique(appendUnique(nil, b.SameAs...), b.GBPURL); len(same) > 0 {
		lb["sameAs"] = same
	}
	if r := ratingLD(b.Rating); r != nil {
		lb["aggregateRating"] = r
	}
	return lb
}

// geoCircleLD is the service radius around the business position, nil unless
// the radius and both coordinates parse.
func geoCircleLD(b *BusinessView) map[string]any {
	km, ok := bizprofile.ParseDecimal(b.ServiceRadiusKm)
	if !ok || km <= 0 {
		return nil
	}
	lat, ok := bizprofile.ParseDecimal(b.Lat)
	if !ok {
		return nil
	}
	lng, ok := bizprofile.ParseDecimal(b.Lng)
	if !ok {
		return nil
	}
	return map[string]any{
		"@type":       "GeoCircle",
		"geoMidpoint": map[string]any{"@type": "GeoCoordinates", "latitude": lat, "longitude": lng},
		"geoRadius":   km * 1000,
	}
}

// oneOrList is the single node itself, or the list when there are several.
func oneOrList(nodes []any) any {
	if len(nodes) == 1 {
		return nodes[0]
	}
	return nodes
}

// localeHome is the absolute home URL of the page's locale: the locale's own
// entry in the locale list, else the base URL plus the locale's segment.
func localeHome(v *PageView, base string) string {
	for _, l := range v.Locales {
		if l.Tag == v.Locale && l.Home != "" {
			return l.Home
		}
	}
	return base + "/" + v.Segment
}
