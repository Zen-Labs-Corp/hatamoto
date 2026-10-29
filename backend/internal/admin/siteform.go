package admin

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

// siteForm is the site identity the wizard and Settings > Site both edit.
type siteForm struct {
	Name, URL, Language, Timezone string
	// Locales is the "tag segment" per line text of the Locales field; empty
	// means a single-locale site. PrefixAll and XDefault go with it.
	Locales   string
	PrefixAll bool
	XDefault  string
}

func siteFormFrom(c *siteconfig.Config) siteForm {
	f := siteForm{Name: c.CompanyName, URL: c.PublicBaseURL, Language: c.Language, Timezone: c.Timezone}
	for _, e := range c.Locales {
		f.Locales += strings.TrimSpace(e.Tag+" "+e.Segment) + "\n"
	}
	f.PrefixAll, f.XDefault = c.PrefixAll, c.XDefaultLocale
	if f.Language == "" {
		f.Language = "en"
	}
	if f.Timezone == "" {
		f.Timezone = "UTC"
	}
	return f
}

// validate returns field errors keyed by field name; f is normalised in place.
func (f *siteForm) validate() map[string]string {
	errs := map[string]string{}
	f.Name = strings.TrimSpace(f.Name)
	switch n := utf8.RuneCountInString(f.Name); {
	case n == 0:
		errs["name"] = "Enter a site name"
	case n > 120:
		errs["name"] = "Use at most 120 characters"
	}
	f.URL = strings.TrimRight(strings.TrimSpace(f.URL), "/")
	if f.URL != "" {
		u, err := url.Parse(f.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
			errs["url"] = "Enter the address as https://example.com (no path)"
		}
	}
	if tag, err := language.Parse(strings.TrimSpace(f.Language)); err != nil || tag == language.Und {
		errs["language"] = "Choose a language"
	} else {
		f.Language = tag.String()
	}
	if _, err := time.LoadLocation(strings.TrimSpace(f.Timezone)); err != nil || strings.TrimSpace(f.Timezone) == "" {
		errs["timezone"] = "Choose a time zone"
	} else {
		f.Timezone = strings.TrimSpace(f.Timezone)
	}
	return errs
}

// parseLocales reads the Locales field: one "tag segment" pair per line, the
// segment optional (the default locale may sit at the root).
func parseLocales(text string) ([]siteconfig.LocaleEntry, string) {
	var out []siteconfig.LocaleEntry
	for _, line := range strings.Split(text, "\n") {
		parts := strings.Fields(line)
		switch len(parts) {
		case 0:
		case 1:
			out = append(out, siteconfig.LocaleEntry{Tag: parts[0]})
		case 2:
			out = append(out, siteconfig.LocaleEntry{Tag: parts[0], Segment: parts[1]})
		default:
			return nil, "Write each locale as a language code and a URL segment, like: fr fr"
		}
	}
	return out, ""
}

// xDefaultOptions offers "first live copy" plus every saved locale.
func xDefaultOptions(f siteForm) []ui.Option {
	entries, _ := parseLocales(f.Locales)
	out := []ui.Option{{Value: "", Label: "First available translation", Selected: f.XDefault == ""}}
	for _, e := range entries {
		out = append(out, ui.Option{Value: e.Tag, Label: e.Tag, Selected: e.Tag == f.XDefault})
	}
	return out
}

func (a *Admin) saveSite(ctx context.Context, f siteForm) error {
	_, err := a.deps.Site.Update(ctx, siteconfig.UpdateInput{
		CompanyName: &f.Name, PublicBaseURL: &f.URL, Language: &f.Language, Timezone: &f.Timezone,
	})
	return err
}

func validBrandColor(v string) bool { return v != "" && render.ValidColor(v) }

// languageTags are the offered site languages, written as one list so the
// options are generated rather than enumerated; labels come from the CLDR names
// (the language in itself, then in English).
const languageTags = "en es fr de it pt nl sv da nb fi pl cs hu ro el tr ru uk ar he hi id vi th ja ko zh"

var languages = func() []ui.Option {
	var out []ui.Option
	for _, t := range strings.Fields(languageTags) {
		tag := language.MustParse(t)
		self, english := display.Self.Name(tag), display.English.Languages().Name(tag)
		label := self
		if self != english {
			label = self + " (" + english + ")"
		}
		out = append(out, ui.Option{Value: tag.String(), Label: label})
	}
	return out
}()

var timezones = []string{
	"UTC", "Europe/London", "Europe/Dublin", "Europe/Lisbon", "Europe/Paris", "Europe/Berlin", "Europe/Madrid", "Europe/Rome",
	"Europe/Amsterdam", "Europe/Stockholm", "Europe/Warsaw", "Europe/Athens", "Europe/Helsinki", "Europe/Istanbul", "Europe/Moscow",
	"Africa/Cairo", "Africa/Lagos", "Africa/Johannesburg", "Africa/Nairobi", "Asia/Dubai", "Asia/Karachi", "Asia/Kolkata",
	"Asia/Dhaka", "Asia/Bangkok", "Asia/Jakarta", "Asia/Singapore", "Asia/Hong_Kong", "Asia/Shanghai", "Asia/Manila", "Asia/Seoul",
	"Asia/Tokyo", "Australia/Perth", "Australia/Adelaide", "Australia/Sydney", "Pacific/Auckland", "America/St_Johns",
	"America/Halifax", "America/New_York", "America/Chicago", "America/Denver", "America/Phoenix", "America/Los_Angeles",
	"America/Anchorage", "Pacific/Honolulu", "America/Mexico_City", "America/Bogota", "America/Lima", "America/Santiago",
	"America/Sao_Paulo", "America/Argentina/Buenos_Aires",
}

// languageOptions marks current selected and keeps an unlisted current value.
func languageOptions(current string) []ui.Option {
	out := make([]ui.Option, 0, len(languages)+1)
	found := false
	for _, o := range languages {
		o.Selected = o.Value == current
		found = found || o.Selected
		out = append(out, o)
	}
	if !found && current != "" {
		out = append([]ui.Option{{Value: current, Label: current, Selected: true}}, out...)
	}
	return out
}

func timezoneOptions(current string) []ui.Option {
	out := make([]ui.Option, 0, len(timezones)+1)
	found := false
	for _, z := range timezones {
		o := ui.Option{Value: z, Label: strings.ReplaceAll(z, "_", " "), Selected: z == current}
		found = found || o.Selected
		out = append(out, o)
	}
	if !found && current != "" {
		out = append([]ui.Option{{Value: current, Label: current, Selected: true}}, out...)
	}
	return out
}
