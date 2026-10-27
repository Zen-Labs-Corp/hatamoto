package admin

import (
	"os"
	"strings"
	"testing"
)

// Client-side dates share one day-month format (dates.js) so they match the server's.
func TestDatesJS(t *testing.T) {
	t.Parallel()

	read := func(name string) string {
		b, err := os.ReadFile("../../web/admin/js/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if src := read("dates.js"); !strings.Contains(src, `Intl.DateTimeFormat("en-GB"`) || !strings.Contains(src, "Sept") {
		t.Error(`dates.js must define the date helpers with Intl.DateTimeFormat("en-GB") and normalise "Sept" to "Sep"`)
	}
	for _, name := range []string{"blogeditor.js", "builder/screen.js"} {
		src := read(name)
		if strings.Contains(src, "toLoc"+"ale") {
			t.Errorf("%s must use the @hm/dates helpers, not the browser-default formatters", name)
		}
		if strings.Contains(src, "window.hm") || !strings.Contains(src, `from "@hm/dates"`) {
			t.Errorf("%s must import its date helpers from @hm/dates, not read window globals", name)
		}
	}
	for _, name := range []string{"daterange.js"} {
		if strings.Contains(read(name), `DateTimeFormat("en-US"`) {
			t.Errorf("%s date labels must be day-month (en-GB)", name)
		}
	}
}
