package render

import (
	"net/url"
	"strings"
	"unicode"

	"hatamoto.dev/core/internal/bizprofile"
)

// localePage reports whether the page prints locale-only Organization details:
// a page of a locale on a site with two or more locales.
func localePage(v *PageView) bool {
	return len(v.Locales) >= 2 && !v.Shared
}

// orgContact is the address and phone the Organization prints. Locale pages
// follow the site-wide address setting; every other page uses the site-wide
// values. hasAddr is false when no address prints.
func orgContact(v *PageView) (addr Address, poBox, phone string, hasAddr bool) {
	site := v.Site
	addr, poBox, phone = site.Address, site.Org.POBox, site.Phone
	hasAddr = addr != (Address{}) || poBox != ""
	if !localePage(v) || site.OrgAddress == "" {
		return
	}
	if b := v.Business; b != nil {
		addr, poBox, phone = b.Address, b.POBox, b.Phone
		return addr, poBox, phone, addr != (Address{}) || poBox != ""
	}
	if site.OrgAddress == bizprofile.OrgAddressLocale {
		return Address{}, "", "", false
	}
	return
}

// orgNode is the Organization node of the site graph. base is the site's base
// URL without a trailing slash.
func orgNode(v *PageView, base string) map[string]any {
	addr, poBox, phone, hasAddr := orgContact(v)
	org := map[string]any{
		"@type": "Organization",
		"@id":   base + "#organization",
		"name":  v.Site.Name,
		"url":   base,
	}
	if v.Site.LogoURL != "" {
		logo := absURL(base, v.Site.LogoURL)
		if v.Site.Org.LogoObject && v.Site.LogoWidth > 0 && v.Site.LogoHeight > 0 {
			org["logo"] = map[string]any{"@type": "ImageObject", "url": logo, "width": v.Site.LogoWidth, "height": v.Site.LogoHeight}
		} else {
			org["logo"] = logo
		}
		if localePage(v) {
			org["image"] = logo
		}
	}
	if d := firstNonEmpty(v.Site.Org.Description, v.Site.Tagline); d != "" {
		org["description"] = d
	}
	setIf(org, "alternateName", v.Site.Org.AlternateName)
	setIf(org, "slogan", v.Site.Org.Slogan)
	setIf(org, "foundingDate", v.Site.Org.FoundingDate)
	if mn, mx := v.Site.Org.EmployeesMin, v.Site.Org.EmployeesMax; mn > 0 || mx > 0 {
		n := map[string]any{"@type": "QuantitativeValue"}
		if mn > 0 {
			n["minValue"] = mn
		}
		if mx > 0 {
			n["maxValue"] = mx
		}
		org["numberOfEmployees"] = n
	}
	vat := v.Site.Org.VATID
	if localePage(v) && v.Site.OrgAddress != "" && v.Business != nil {
		vat = v.Business.VATID
	}
	setIf(org, "vatID", vat)
	if phone != "" {
		org["telephone"] = phone
	}
	if points := orgContactPoints(v, phone); len(points) == 1 {
		org["contactPoint"] = points[0]
	} else if len(points) > 1 {
		org["contactPoint"] = points
	}
	if hasAddr {
		a := map[string]any{"@type": "PostalAddress"}
		for k, val := range map[string]string{
			"streetAddress": addr.Street, "addressLocality": addr.City, "addressRegion": addr.State,
			"postalCode": addr.PostalCode, "addressCountry": addr.Country, "postOfficeBoxNumber": poBox,
		} {
			if val != "" {
				a[k] = val
			}
		}
		org["address"] = a
	}
	if localePage(v) {
		if served := orgAreaServed(v.Locales); len(served) > 0 {
			org["areaServed"] = served
		}
		for _, l := range v.Locales {
			if l.Tag == v.Locale {
				setIf(org, "priceRange", l.PriceRange)
				setIf(org, "currenciesAccepted", l.Currency)
			}
		}
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
	setDetailsLD(org, v.Site.Org.BusinessDetails, v.Site.Org.Currency)
	return org
}

// orgContactPoints lists the Organization's contact points: on a locale page
// one per locale that has a phone of its own (named by the card's country and
// language only), then a site-wide point for the effective phone and, on locale pages, the
// site phone, each unless an earlier point already carries that number. Single-locale and shared pages print the site-wide point
// alone, as before.
func orgContactPoints(v *PageView, phone string) []any {
	var points []any
	seen := map[string]bool{}
	if localePage(v) {
		for _, l := range v.Locales {
			if l.Phone == "" {
				continue
			}
			cp := map[string]any{"@type": "ContactPoint", "telephone": l.Phone, "contactType": "customer service"}
			setIf(cp, "areaServed", l.CountryName)
			setIf(cp, "availableLanguage", l.Language)
			points = append(points, cp)
			seen[l.Phone] = true
		}
	}
	phones := []string{phone}
	if localePage(v) {
		phones = append(phones, v.Site.Phone)
	}
	for _, p := range phones {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		contact := map[string]any{"@type": "ContactPoint", "telephone": p, "contactType": "customer service"}
		if v.Site.Language != "" {
			contact["availableLanguage"] = v.Site.Language
		}
		if v.Site.Hours != "" {
			contact["hoursAvailable"] = v.Site.Hours
		}
		points = append(points, contact)
	}
	return points
}

// orgAreaServed lists one Country per locale that names its country. The
// identifier is the region subtag of the locale tag, when it has one.
func orgAreaServed(locales []LocaleSummary) []any {
	var out []any
	for _, l := range locales {
		if l.CountryName == "" {
			continue
		}
		out = append(out, countryLD(l.CountryName, l.Tag))
	}
	return out
}

// countryLD is a schema.org Country; the identifier is the region subtag of tag
// ("XX" for "en-XX") and is left out when the tag has none.
func countryLD(name, tag string) map[string]any {
	c := map[string]any{"@type": "Country", "name": name}
	setIf(c, "identifier", regionSubtag(tag))
	return c
}

func regionSubtag(tag string) string {
	parts := strings.Split(tag, "-")
	for _, p := range parts[1:] {
		if len(p) == 2 && strings.IndexFunc(p, func(r rune) bool { return !unicode.IsLetter(r) }) < 0 {
			return strings.ToUpper(p)
		}
	}
	return ""
}
