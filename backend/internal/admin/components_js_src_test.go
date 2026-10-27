package admin

import (
	"os"
	"strings"
	"testing"
)

func readComponentsJS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../web/admin/js/components.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The behaviour (form button confirm, carried email, dismissed banners) is tested with real
// inputs in scripts/admin-qa/unit/shelllogic.test.mjs; this only checks the glue still calls it.
func TestComponentsJSUsesShellLogic(t *testing.T) {
	t.Parallel()

	src := readComponentsJS(t)
	for _, want := range []string{`from "@hm/shelllogic"`, "confirmPost(form, link)", "carryHref(", `isDismissed(safeStorage("session")`, `markDismissed(safeStorage("session")`} {
		if !strings.Contains(src, want) {
			t.Errorf("components.js must call %s", want)
		}
	}
}

// The danger card's submit gate lives in components.js and finds its parts by the
// attribute names in the partial. Only phraseMatches has a unit test, so this guards
// the contract between the two: rename one side and a JS-enabled browser gets a
// submit that never enables, with every other test still green.
func TestComponentsJSMatchesDangerConfirmAttributes(t *testing.T) {
	t.Parallel()

	js := readComponentsJS(t)
	b, err := os.ReadFile("../../web/admin/partials/danger-confirm.html")
	if err != nil {
		t.Fatal(err)
	}
	partial := string(b)
	for _, name := range []string{"data-hm-danger-confirm", "data-hm-danger-phrase", "data-hm-danger-input", "data-hm-danger-submit"} {
		if !strings.Contains(partial, name) {
			t.Errorf("danger-confirm.html lost %s", name)
		}
		if name != "data-hm-danger-phrase" && !strings.Contains(js, "["+name+"]") {
			t.Errorf("components.js must select [%s]", name)
		}
	}
	if !strings.Contains(js, "dataset.hmDangerPhrase") {
		t.Error("components.js must read the phrase from dataset.hmDangerPhrase (data-hm-danger-phrase)")
	}
}

// pageshow fires on window and never reaches a document listener.
func TestComponentsJSListensForPageshowOnWindow(t *testing.T) {
	t.Parallel()

	js := readComponentsJS(t)
	if strings.Contains(js, `document.addEventListener("pageshow"`) {
		t.Error("pageshow is dispatched on window: a document listener never fires")
	}
	if !strings.Contains(js, `window.addEventListener("pageshow", () => syncDangerAll(document))`) {
		t.Error("the danger card must re-sync on window pageshow")
	}
}
