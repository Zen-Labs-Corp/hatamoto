package admin

import (
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/spam"
)

// spamForm is what a Spam protection POST carried, kept to redisplay after a
// failed save. The raw numbers and word list are echoed as typed.
type spamForm struct {
	Posted     bool
	Settings   spam.Settings
	Secret     string // new secret typed this time; blank keeps the saved one
	MinSeconds string
	MaxLinks   string
	Words      string
}

// orInt returns typed when the form was posted, else the stored number as text.
func (f spamForm) orInt(stored int, typed string) string {
	if f.Posted {
		return typed
	}
	return strconv.Itoa(stored)
}

// validateSpam reads the Spam protection form. The secret is write-only: a blank
// field keeps the saved one (secretReadable says whether there is one to keep).
// The returned Settings keeps the stored sealed secret; the handler seals a new
// one into it.
func validateSpam(form url.Values, stored spam.Settings, secretReadable bool) (spam.Settings, spamForm, []ui.FieldError) {
	var errs []ui.FieldError
	bad := func(id, label, msg string) { errs = append(errs, ui.FieldError{ID: id, Label: label, Message: msg}) }
	out := stored
	in := spamForm{
		Posted:     true,
		Secret:     strings.TrimSpace(form.Get("secret_key")),
		MinSeconds: strings.TrimSpace(form.Get("min_seconds")),
		MaxLinks:   strings.TrimSpace(form.Get("max_links")),
		Words:      form.Get("blocked_words"),
	}

	out.Provider = form.Get("provider")
	if !contains(spam.Providers, out.Provider) {
		bad("provider", "Captcha provider", "Choose one of the listed providers.")
	}
	out.SiteKey = strings.TrimSpace(form.Get("site_key"))
	if len(out.SiteKey) > spam.MaxSiteKeyLen {
		bad("site-key", "Site key", "The site key is too long.")
	}
	if out.Provider != spam.ProviderNone && contains(spam.Providers, out.Provider) {
		if out.SiteKey == "" {
			bad("site-key", "Site key", "Enter the site key from your captcha provider.")
		}
		switch {
		case in.Secret != "":
		case !secretReadable:
			bad("secret-key", "Secret key", "Enter the secret key from your captcha provider.")
		case out.Provider != stored.Provider:
			bad("secret-key", "Secret key", "Enter the secret key for the new provider.")
		}
	}

	if n, err := strconv.Atoi(in.MinSeconds); err != nil || n < 0 || n > spam.MaxMinSeconds {
		bad("min-seconds", "Minimum time to submit", "Enter a whole number of seconds from 0 to "+strconv.Itoa(spam.MaxMinSeconds)+". 0 turns the check off.")
	} else {
		out.MinSeconds = n
	}
	if n, err := strconv.Atoi(in.MaxLinks); err != nil || n < 0 || n > spam.MaxMaxLinks {
		bad("max-links", "Max links per message", "Enter a whole number from 0 to "+strconv.Itoa(spam.MaxMaxLinks)+". 0 blocks every link.")
	} else {
		out.MaxLinks = n
	}

	out.BlockedWords = splitWords(in.Words)
	if len(out.BlockedWords) > spam.MaxBlockedWords {
		bad("blocked-words", "Blocked words", "Use at most "+strconv.Itoa(spam.MaxBlockedWords)+" words.")
	}
	for _, w := range out.BlockedWords {
		if len(w) > spam.MaxWordLen {
			bad("blocked-words", "Blocked words", "Each word can be at most "+strconv.Itoa(spam.MaxWordLen)+" characters.")
			break
		}
	}

	out.BlockedScripts = nil
	for _, sc := range form["scripts"] {
		if !contains(spam.Scripts, sc) {
			bad("scripts", "Block scripts", "Choose from the listed scripts.")
			break
		}
		if !contains(out.BlockedScripts, sc) {
			out.BlockedScripts = append(out.BlockedScripts, sc)
		}
	}

	out.Action = form.Get("action")
	if out.Action != spam.ActionSpam && out.Action != spam.ActionRefuse {
		bad("action", "When a rule trips", "Choose Save as Spam or Refuse submission.")
	}
	in.Settings = out
	return out, in, errs
}

// splitWords reads the blocked words as typed: separated by commas or new lines.
func splitWords(v string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}
