package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/web"
)

// The admin fonts are served from the admin's own origin as fingerprinted,
// immutable font/woff2 files, and the CSS bundle points at the hashed names.
func TestAdminFontsAreFingerprintedAndServed(t *testing.T) {
	t.Parallel()

	as, err := BuildAssets(web.Admin(), web.Vendor())
	if err != nil {
		t.Fatal(err)
	}
	css := string(as.files[strings.TrimPrefix(as.CSSURL, "/admin/_/")].body)
	if strings.Contains(css, "url(fonts/Inter-Variable.woff2)") || strings.Contains(css, "url(fonts/JetBrainsMono-Variable.woff2)") {
		t.Fatal("bundle still references an unhashed font name")
	}
	if strings.Contains(string(as.Tokens), "@font-face") || strings.Contains(string(as.Tokens), "url(") {
		t.Fatal("tokens.css is served cross-origin and must carry no @font-face or url()")
	}
	for _, family := range []string{"Inter", "JetBrainsMono"} {
		var name string
		for n := range as.files {
			if strings.HasPrefix(n, "fonts/"+family+"-Variable.") && strings.HasSuffix(n, ".woff2") {
				name = n
			}
		}
		if name == "" {
			t.Fatalf("%s font not registered", family)
		}
		if !strings.Contains(css, "url("+name+")") {
			t.Errorf("bundle does not reference %s", name)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/_/"+name, nil)
		req.SetPathValue("path", name)
		as.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "font/woff2" {
			t.Errorf("%s: status %d type %q", name, rec.Code, rec.Header().Get("Content-Type"))
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("%s: Cache-Control %q", name, cc)
		}
	}
}
