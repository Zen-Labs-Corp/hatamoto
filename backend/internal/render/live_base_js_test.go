package render

import (
	"os"
	"strings"
	"testing"
)

// Every script that talks to the public API reads the live origin from the
// meta tag a static page carries, so served pages (no meta) are unchanged and
// no absolute URL is baked into a script.
func TestLiveBaseInScripts(t *testing.T) {
	t.Parallel()
	const meta = `meta[name="hm-live-origin"]`
	cases := []struct {
		path  string
		wants []string
	}{
		{"themes/default/assets/js/hm.js", []string{"new URL(", "/api/v1/public/"}},
		{"widgets/form/island.js", []string{"new URL(", "/api/v1/public/"}},
		{"hm_track.js", []string{"/api/v1/track"}},
		{"hm_consent.js", []string{"/api/v1/public/consent"}},
	}
	for _, c := range cases {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatal(err)
		}
		js := string(b)
		if !strings.Contains(js, meta) {
			t.Errorf("%s does not read %s", c.path, meta)
		}
		for _, want := range c.wants {
			// The literals stay so rewriteAPIPrefix can still replace them.
			if !strings.Contains(js, want) {
				t.Errorf("%s lacks %q", c.path, want)
			}
		}
		if strings.Contains(js, "exec(form.getAttribute('action')") || strings.Contains(js, `exec(form.getAttribute("action")`) {
			t.Errorf("%s still matches the raw action attribute", c.path)
		}
	}
}

func TestLiveBaseHmJSPrefixRewrite(t *testing.T) {
	SetPublicAPIPrefix("/x/api")
	t.Cleanup(func() { SetPublicAPIPrefix("") })
	b, err := os.ReadFile("themes/default/assets/js/hm.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(rewriteAPIPrefix(b))
	if strings.Contains(js, "/api/v1/public/") || !strings.Contains(js, "liveBase() + '/x/api/forms/'") {
		t.Error("API prefix not applied to the live-base requests")
	}
}
