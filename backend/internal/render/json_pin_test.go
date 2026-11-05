package render

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// These tests pin the JSON the render package hashes and embeds, byte for
// byte, as the v1 encoder wrote it. The hashes are cache-busting queries and
// ETags, so a different encoding would bust every cached page and asset.

func bodyDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func tagsPinConfigs() map[string]TagsConfig {
	return map[string]TagsConfig{
		"nil vendors": {},
		"vendors escaped": {
			ExcludeSignedIn: true, Member: "hm_member", Consent: true,
			Vendors: []TagVendor{
				{ID: "ga4", Boot: "ga4", Category: "analytics", Src: "https://example.com/gtag/js?id=G-1&l=x", Value: "G-1"},
				{ID: "custom", Boot: "<b>&", Category: "marketing", Value: "a\u2028b"},
			},
			Noscript: `<iframe src="https://example.com/ns.html" hidden></iframe>`,
		},
	}
}

func TestTagsConfigHashGolden(t *testing.T) {
	want := map[string]string{
		"nil vendors":     "be84cf44142f",
		"vendors escaped": "1a87b4151301",
	}
	for name, cfg := range tagsPinConfigs() {
		if got := cfg.hash(); got != want[name] {
			t.Errorf("%s: hash = %q, want %q", name, got, want[name])
		}
	}
}

func TestTagsScriptBodyGolden(t *testing.T) {
	wantJSON := map[string]string{
		"nil vendors":     `{"excludeSignedIn":false,"member":"","consent":false,"vendors":null}`,
		"vendors escaped": `{"excludeSignedIn":true,"member":"hm_member","consent":true,"vendors":[{"id":"ga4","boot":"ga4","category":"analytics","src":"https://example.com/gtag/js?id=G-1\u0026l=x","aux":"","value":"G-1"},{"id":"custom","boot":"\u003cb\u003e\u0026","category":"marketing","src":"","aux":"","value":"a\u2028b"}]}`,
	}
	wantSum := map[string]string{
		"nil vendors":     "3092901a2f54d367fd47fc7e608062e6051d926e35f97b4dc361d011017aff9a",
		"vendors escaped": "5c72de2c4572b835bc86cacd6a6692226f95511998dd3a34b7b73e30353a23ed",
	}
	for name, cfg := range tagsPinConfigs() {
		body := tagsScript(cfg)
		if !strings.Contains(string(body), wantJSON[name]) || wantJSON[name] == "" {
			t.Errorf("%s: body lacks the v1 JSON %q", name, wantJSON[name])
		}
		if got := bodyDigest(body); got != wantSum[name] {
			t.Errorf("%s: body sha256 = %q, want %q", name, got, wantSum[name])
		}
	}
}

func consentPinConfigs() map[string]ConsentConfig {
	return map[string]ConsentConfig{
		"nil categories": {Version: "1", Region: "eu", Title: "T", Text: "x"},
		"strings escaped": {
			Version:    "7",
			Categories: []string{"analytics", "marketing"},
			Region:     "all",
			Title:      "<Cookies> & you",
			Text:       "line\u2028sep \u2029 and <b>bold</b>",
			Strings:    map[string]string{"zeta": "<>&", "alpha": "a\u2028b", "mid": "ok", "accept": "Accept & close"},
		},
	}
}

func TestConsentConfigHashGolden(t *testing.T) {
	want := map[string]string{
		"nil categories":  "6dcb02baa11a",
		"strings escaped": "5ddd17f838c3",
	}
	for name, cfg := range consentPinConfigs() {
		if got := cfg.hash(); got != want[name] {
			t.Errorf("%s: hash = %q, want %q", name, got, want[name])
		}
	}
}

func TestConsentScriptBodyGolden(t *testing.T) {
	wantJSON := map[string]string{
		"nil categories":  `{"version":"1","categories":null,"region":"eu","title":"T","text":"x","strings":null}`,
		"strings escaped": `{"version":"7","categories":["analytics","marketing"],"region":"all","title":"\u003cCookies\u003e \u0026 you","text":"line\u2028sep \u2029 and \u003cb\u003ebold\u003c/b\u003e","strings":{"accept":"Accept \u0026 close","alpha":"a\u2028b","mid":"ok","zeta":"\u003c\u003e\u0026"}}`,
	}
	wantSum := map[string]string{
		"nil categories":  "6e633dbea11c0cd6c7cf6780b1ff69d64db08054e930dd6b7e746426beaa6123",
		"strings escaped": "743a164c53e990bb9da648a7b8ba32ba7bb2e8191abfe9fb094c93ae64c0f227",
	}
	for name, cfg := range consentPinConfigs() {
		body := consentScript(cfg)
		if !strings.Contains(string(body), wantJSON[name]) || wantJSON[name] == "" {
			t.Errorf("%s: body lacks the v1 JSON %q", name, wantJSON[name])
		}
		if got := bodyDigest(body); got != wantSum[name] {
			t.Errorf("%s: body sha256 = %q, want %q", name, got, wantSum[name])
		}
	}
}

// A2: a hostile "</script>" in any JSON-LD source must never close the block.
func TestJSONLDEscapesScriptClose(t *testing.T) {
	const evil = "</script><b>x"
	check := func(t *testing.T, out string) {
		t.Helper()
		if strings.Contains(out, evil) {
			t.Errorf("raw %q in output:\n%s", evil, out)
		}
		if !strings.Contains(out, `\u003c/script\u003e`) && !strings.Contains(out, `<\/script>`) {
			t.Errorf("escaped script close missing:\n%s", out)
		}
		blocks := strings.Split(out, `<script type="application/ld+json">`)[1:]
		for _, b := range blocks {
			if n := strings.Count(b[:strings.Index(b, "</script>")+len("</script>")], "</script>"); n != 1 {
				t.Errorf("block has %d closing tags: %s", n, b)
			}
			rest := b[strings.Index(b, "</script>")+len("</script>"):]
			if i := strings.Index(rest, "<script type=\"application/ld+json\">"); i >= 0 {
				rest = rest[:i]
			}
			if strings.Contains(rest, "<b>x") {
				t.Errorf("payload leaked after a block: %s", rest)
			}
		}
	}
	base := func() *PageView {
		return &PageView{BaseURL: "https://x.io", Path: "/", Page: PageMeta{Title: "Home"}}
	}
	t.Run("breadcrumb title", func(t *testing.T) {
		v := base()
		v.Breadcrumb = []Crumb{{Name: "Home", URL: "/"}, {Name: evil}}
		check(t, renderHead(t, buildHeadView(context.Background(), v, nil)))
	})
	t.Run("collection page title", func(t *testing.T) {
		v := base()
		v.Kind = KindArchive
		v.Page = PageMeta{Title: evil, Description: evil}
		check(t, renderHead(t, buildHeadView(context.Background(), v, nil)))
	})
	t.Run("raw view document", func(t *testing.T) {
		v := base()
		v.JSONLD = append(v.JSONLD, []byte(`{"@type":"Thing","name":"`+evil+`"}`))
		check(t, renderHead(t, buildHeadView(context.Background(), v, nil)))
	})
	t.Run("plugin head tag", func(t *testing.T) {
		f := hostileFilter(HeadTag{Kind: "jsonld", JSONLD: []byte(`{"@type":"Thing","name":"` + evil + `"}`)})
		check(t, renderHead(t, buildHeadView(context.Background(), base(), f)))
	})
}
