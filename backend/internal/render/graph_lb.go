package render

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
	addr := map[string]any{"@type": "PostalAddress"}
	for k, val := range map[string]string{
		"streetAddress": b.Address.Street, "addressLocality": b.Address.City, "addressRegion": b.Address.State,
		"postalCode": b.Address.PostalCode, "addressCountry": b.Address.Country, "postOfficeBoxNumber": b.POBox,
	} {
		setIf(addr, k, val)
	}
	lb["address"] = addr
	if b.Lat != "" && b.Lng != "" {
		lb["geo"] = map[string]any{"@type": "GeoCoordinates", "latitude": b.Lat, "longitude": b.Lng}
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
	for _, h := range b.SpecialHours {
		if h.From == "" {
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
		lb["openingHoursSpecification"] = specs
	}
	if len(b.AreaServed) > 0 {
		lb["areaServed"] = b.AreaServed
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
