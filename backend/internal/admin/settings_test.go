package admin

import (
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

// fieldErr is a validation error that names the setting at fault, like the
// server's locale error.
type fieldErr struct{ field, msg string }

func (e fieldErr) Error() string       { return e.msg }
func (e fieldErr) LocaleField() string { return e.field }

func TestLocaleSetOfKeepsHiddenAndName(t *testing.T) {
	set := localeSetOf(&siteconfig.Config{Language: "en", Locales: []siteconfig.LocaleEntry{
		{Tag: "en"}, {Tag: "fr", Segment: "fr", Name: "Francais", Hidden: true},
	}})
	if !set.IsHidden("fr") || set.Name("fr") != "Francais" || set.IsHidden("en") {
		t.Fatalf("hidden/name lost: %+v", set.Entries())
	}
}
