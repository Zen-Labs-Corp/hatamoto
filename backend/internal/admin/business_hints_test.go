package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/schemaform"
)

// Full-sentence hints end with a period; only these fragments go without.
func TestBusinessHintsKeepSentencePeriods(t *testing.T) {
	fragments := map[string]bool{
		"currency": true, "address.lat": true, "address.lng": true,
		"special_hours.to": true, "founding_date": true,
	}
	for _, ui := range []map[string]schemaform.UIHint{businessUI, localeUI()} {
		for k, hint := range ui {
			h := hint.Help
			if fragments[k] {
				if strings.HasSuffix(h, ".") {
					t.Errorf("%s: fragment hint %q should not end with a period", k, h)
				}
				continue
			}
			if !strings.HasSuffix(h, ".") {
				t.Errorf("%s: sentence hint %q needs a final period", k, h)
			}
		}
	}
}
