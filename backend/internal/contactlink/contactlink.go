// Package contactlink holds the number and address rules shared by the
// contact button settings (validation) and the renderer (link building), so
// both accept exactly the same input.
package contactlink

import (
	"net/mail"
	"strings"
)

// Phone normalises a typed phone number: a "(0)" trunk group is dropped and a
// leading "00" becomes "+". digits holds only the digits, international says
// whether the number carries a country code ("+" or "00" prefix). ok is false
// when it has fewer than 6 or more than 15 digits.
func Phone(s string) (digits string, international, ok bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "(0)", ""))
	if strings.HasPrefix(s, "00") {
		s = "+" + s[2:]
	}
	international = strings.HasPrefix(s, "+")
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits = b.String()
	return digits, international, len(digits) >= 6 && len(digits) <= 15
}

// Tel is the number as a tel: or sms: target: digits with a leading "+" when
// the number is international.
func Tel(digits string, international bool) string {
	if international {
		return "+" + digits
	}
	return digits
}

// ValidMailbox accepts a bare address. Characters that would add recipients,
// headers or query parameters to a mailto: link, or that mail clients decode
// inside the address part ("%", "#"), are refused.
func ValidMailbox(s string) bool {
	if s == "" || strings.ContainsAny(s, "?&,;\r\n<> ") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}
