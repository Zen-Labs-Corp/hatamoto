package siteconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/contactlink"
)

// contactButtonKey is the theme settings key that holds the floating contact
// button; every other key of the document is left as stored.
const contactButtonKey = "contactButton"

// Channels and positions of the floating contact button.
const (
	ContactWhatsApp = "whatsapp"
	ContactCall     = "call"
	ContactSMS      = "sms"
	ContactEmail    = "email"

	ContactBottomRight = "bottom-right"
	ContactBottomLeft  = "bottom-left"
)

// Limits of the free-text fields, in runes.
const (
	contactLabelMax   = 60
	contactMessageMax = 500
	contactToMax      = 120
)

// ContactButton is the site-wide floating contact link.
type ContactButton struct {
	Enabled  bool   `json:"enabled"`
	Channel  string `json:"channel"`  // whatsapp|call|sms|email
	Position string `json:"position"` // bottom-right|bottom-left
	Label    string `json:"label"`    // <=60 runes
	To       string `json:"to"`       // blank = business profile
	Message  string `json:"message"`  // <=500 runes
}

// FieldError names a form field and what is wrong with it.
type FieldError struct {
	Field   string
	Message string
}

// ParseContactButton reads the contactButton key of the theme settings JSON.
// Missing or malformed data is the zero value (disabled).
func ParseContactButton(themeExtra []byte) ContactButton {
	var top map[string]json.RawMessage
	if json.Unmarshal(themeExtra, &top) != nil || len(top[contactButtonKey]) == 0 {
		return ContactButton{}
	}
	var c ContactButton
	if json.Unmarshal(top[contactButtonKey], &c) != nil {
		return ContactButton{}
	}
	return c
}

// Validate reports every problem of c; no errors means it can be saved.
func (c ContactButton) Validate() []FieldError {
	var errs []FieldError
	add := func(field, msg string) { errs = append(errs, FieldError{field, msg}) }
	switch c.Channel {
	case ContactWhatsApp, ContactCall, ContactSMS, ContactEmail:
	default:
		add("channel", "Choose WhatsApp, call, text message or email.")
	}
	switch c.Position {
	case ContactBottomRight, ContactBottomLeft:
	default:
		add("position", "Choose bottom right or bottom left.")
	}
	if utf8.RuneCountInString(c.Label) > contactLabelMax {
		add("label", fmt.Sprintf("Use at most %d characters.", contactLabelMax))
	}
	if utf8.RuneCountInString(c.Message) > contactMessageMax {
		add("message", fmt.Sprintf("Use at most %d characters.", contactMessageMax))
	}
	if to := strings.TrimSpace(c.To); to != "" {
		switch {
		case utf8.RuneCountInString(to) > contactToMax:
			add("to", fmt.Sprintf("Use at most %d characters.", contactToMax))
		case c.Channel == ContactEmail:
			if !contactlink.ValidMailbox(to) {
				add("to", "Enter a valid email address.")
			}
		case c.Channel == ContactWhatsApp || c.Channel == ContactCall || c.Channel == ContactSMS:
			_, intl, ok := contactlink.Phone(to)
			switch {
			case !ok || !validPhoneChars(to):
				add("to", "Enter a phone number with 6 to 15 digits.")
			case c.Channel == ContactWhatsApp && !intl:
				add("to", "Include the country code, for example +41 79 123 45 67.")
			}
		}
	}
	return errs
}

// MergeContactButton sets the contactButton key and keeps every other key.
func MergeContactButton(themeExtra []byte, c ContactButton) ([]byte, error) {
	top := map[string]json.RawMessage{}
	if len(themeExtra) > 0 && string(themeExtra) != "null" {
		if err := json.Unmarshal(themeExtra, &top); err != nil {
			return nil, fmt.Errorf("theme settings: %w", err)
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	top[contactButtonKey] = raw
	return json.Marshal(top)
}

// validPhoneChars allows digits plus the separators people type.
func validPhoneChars(s string) bool {
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9', r == ' ', r == '-', r == '(', r == ')':
		case r == '+' && i == 0:
		default:
			return false
		}
	}
	return true
}
