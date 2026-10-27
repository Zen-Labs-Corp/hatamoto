package admin

import (
	"os"
	"strings"
	"testing"
)

// select.js remembers the focused trigger across an htmx swap. A response that is not swapped
// never reaches afterSettle, so the id must be dropped there or a later unrelated swap steals focus.
func TestSelectJSDropsRefocusWhenNothingSwaps(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/js/select.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{"shouldSwap", `"htmx:responseError"`} {
		if !strings.Contains(src, want) {
			t.Errorf("select.js must clear its remembered focus id on %s", want)
		}
	}
}

// A select without an id gets a new generated id each time init rebuilds it, so the remembered
// trigger is re-resolved by (form, name, index) rather than restored by id.
func TestSelectJSRefocusWithoutID(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/js/select.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{"describeTrigger", "resolveTrigger", "[data-hm-select]", "selectsNamed(d.form, d.name)"} {
		if !strings.Contains(src, want) {
			t.Errorf("select.js must re-resolve the focused trigger by form, name and index: missing %s", want)
		}
	}
	if strings.Contains(src, "getElementById(id)?.focus()") {
		t.Error("select.js must not restore focus by the generated id alone")
	}
}
