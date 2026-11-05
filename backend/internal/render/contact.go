package render

import (
	"html/template"
	"net/mail"
	"net/url"
	"strings"
)

// ContactView is the phone and email a contact link falls back to when its
// owner set none: the business profile of the page's locale.
type ContactView struct{ Phone, Email string }

// ContactFloat is the site-wide floating contact link as the owner saved it;
// the zero value is disabled.
type ContactFloat struct {
	Enabled                               bool
	Channel, Position, Label, To, Message string
}

// contactDefaults are the profile values a link uses when its own number or
// address is blank.
type contactDefaults struct{ Phone, Email string }

// contactDefaultsOf reads the page's locale contact, then the site-wide phone
// and organisation email.
func contactDefaultsOf(v *PageView) contactDefaults {
	if v == nil {
		return contactDefaults{}
	}
	d := contactDefaults{Phone: v.Contact.Phone, Email: v.Contact.Email}
	if d.Phone == "" {
		d.Phone = v.Site.Phone
	}
	if d.Email == "" {
		d.Email = v.Site.Org.Email
	}
	return d
}

// contactLink builds the href of a contact link. ok is false when there is no
// usable number or address for the channel. The composed string always goes
// through safeURL, the one place that decides which schemes may be linked.
func contactLink(channel, to, msg string, d contactDefaults) (template.URL, bool) {
	to = strings.TrimSpace(to)
	var raw string
	switch channel {
	case "email":
		if to == "" {
			to = strings.TrimSpace(d.Email)
		}
		if !validMailbox(to) {
			return "", false
		}
		raw = "mailto:" + to + queryBody(msg)
	case "whatsapp", "call", "sms":
		if to == "" {
			to = strings.TrimSpace(d.Phone)
		}
		digits := phoneDigits(to)
		if len(digits) < 6 || len(digits) > 15 {
			return "", false
		}
		switch channel {
		case "whatsapp":
			raw = "https://wa.me/" + digits
			if msg != "" {
				raw += "?text=" + queryEscape(msg)
			}
		case "call":
			raw = "tel:" + phoneWithPlus(to)
		default:
			raw = "sms:" + phoneWithPlus(to) + queryBody(msg)
		}
	default:
		return "", false
	}
	href := safeURL(raw)
	if href == "#" {
		return "", false
	}
	return template.URL(href), true // #nosec G203 -- scheme allowlist enforced by safeURL
}

// contactLabel is the owner's label, else a plain default for the channel.
func contactLabel(channel, label string) string {
	if l := strings.TrimSpace(label); l != "" {
		return l
	}
	switch channel {
	case "call":
		return "Call us"
	case "sms":
		return "Send a text"
	case "email":
		return "Send an email"
	}
	return "Chat on WhatsApp"
}

func queryBody(msg string) string {
	if msg == "" {
		return ""
	}
	return "?body=" + queryEscape(msg)
}

// queryEscape escapes for a query value with %20 for spaces, which every
// target app reads, unlike "+".
func queryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func phoneDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// phoneWithPlus keeps the digits and a leading "+".
func phoneWithPlus(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "+") {
		return "+" + phoneDigits(s)
	}
	return phoneDigits(s)
}

// validMailbox accepts a bare address; characters that would add headers or
// query parameters to a mailto: link are refused.
func validMailbox(s string) bool {
	if s == "" || strings.ContainsAny(s, "?&,;\r\n<> ") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// floatLink is what the contact-float partial prints.
type floatLink struct {
	Href            template.URL
	Label, Position string
}

// contactFloatLink is the floating contact link of a page, nil when there is
// none to print: disabled, no number or address to link to, or a render that
// is not a public page (editor preview, canvas). The 404, error and access
// renders clear the setting before they get here.
func contactFloatLink(d *pageData) *floatLink {
	if d == nil {
		return nil
	}
	v := &d.PageView
	if !v.Site.ContactFloat.Enabled || v.Preview || v.Canvas {
		return nil
	}
	f := v.Site.ContactFloat
	href, ok := contactLink(f.Channel, f.To, f.Message, contactDefaultsOf(v))
	if !ok {
		return nil
	}
	pos := "bottom-right"
	if f.Position == "bottom-left" {
		pos = "bottom-left"
	}
	return &floatLink{Href: href, Label: contactLabel(f.Channel, f.Label), Position: pos}
}
