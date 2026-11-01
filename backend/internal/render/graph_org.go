package render

import "net/url"

// orgNode is the Organization node of the site graph. base is the site's base
// URL without a trailing slash.
func orgNode(v *PageView, base string) map[string]any {
	org := map[string]any{
		"@type": "Organization",
		"@id":   base + "#organization",
		"name":  v.Site.Name,
		"url":   base,
	}
	if v.Site.LogoURL != "" {
		org["logo"] = absURL(base, v.Site.LogoURL)
	}
	if v.Site.Tagline != "" {
		org["description"] = v.Site.Tagline
	}
	if v.Site.Phone != "" {
		org["telephone"] = v.Site.Phone
		contact := map[string]any{"@type": "ContactPoint", "telephone": v.Site.Phone, "contactType": "customer service"}
		if v.Site.Language != "" {
			contact["availableLanguage"] = v.Site.Language
		}
		if v.Site.Hours != "" {
			contact["hoursAvailable"] = v.Site.Hours
		}
		org["contactPoint"] = contact
	}
	if a := v.Site.Address; a != (Address{}) || v.Site.Org.POBox != "" {
		addr := map[string]any{"@type": "PostalAddress"}
		for k, val := range map[string]string{
			"streetAddress": a.Street, "addressLocality": a.City, "addressRegion": a.State,
			"postalCode": a.PostalCode, "addressCountry": a.Country, "postOfficeBoxNumber": v.Site.Org.POBox,
		} {
			if val != "" {
				addr[k] = val
			}
		}
		org["address"] = addr
	}
	var same []string
	for _, l := range v.Site.SocialLinks {
		if u, err := url.Parse(l.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			same = append(same, l.URL)
		}
	}
	same = appendUnique(same, v.Site.Org.SameAs...)
	if len(same) > 0 {
		org["sameAs"] = same
	}
	setIf(org, "legalName", v.Site.Org.LegalName)
	setIf(org, "taxID", v.Site.Org.TaxID)
	setIf(org, "faxNumber", v.Site.Org.Fax)
	setIf(org, "email", v.Site.Org.Email)
	if r := ratingLD(v.Site.Org.Rating); r != nil {
		org["aggregateRating"] = r
	}
	return org
}
