package entity

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeLocale(t *testing.T) {
	for in, want := range map[string]string{
		"en":      "en",
		"fr":      "fr",
		"EN":      "en",
		" fr ":    "fr",
		"en_au":   "en-AU",
		"EN-au":   "en-AU",
		"fr-CA":   "fr-CA",
		"zh-hans": "zh-Hans",
		"iw":      "he",
		"tl":      "fil",
		"es-419":  "es-419",
		"pt_BR":   "pt-BR",
	} {
		got, err := NormalizeLocale(in)
		if err != nil || got != want {
			t.Errorf("NormalizeLocale(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestNormalizeLocale_Invalid(t *testing.T) {
	for _, in := range []string{"", "x", "en us", "12", "en-", "-en", "english", "en-a", "en--US", "en-" + strings.Repeat("a", 40)} {
		if got, err := NormalizeLocale(in); !errors.Is(err, ErrValidationFailed) || got != "" {
			t.Errorf("NormalizeLocale(%q) = %q, %v; want ErrValidationFailed", in, got, err)
		}
	}
}
