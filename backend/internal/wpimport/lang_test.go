package wpimport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// wpmlWXR carries the language of each item in WPML style postmeta.
const wpmlWXR = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
 <title>Multi</title><link>https://multi.example</link><language>en</language>
 <wp:base_site_url>https://multi.example</wp:base_site_url>
 <item><title>Hello</title><link>https://multi.example/hello/</link><wp:post_id>1</wp:post_id><wp:post_name>hello</wp:post_name><wp:status>publish</wp:status><wp:post_type>post</wp:post_type>
  <wp:postmeta><wp:meta_key><![CDATA[_icl_lang_code]]></wp:meta_key><wp:meta_value><![CDATA[en]]></wp:meta_value></wp:postmeta></item>
 <item><title>Bonjour</title><link>https://multi.example/fr/hello/</link><wp:post_id>2</wp:post_id><wp:post_name>hello</wp:post_name><wp:status>publish</wp:status><wp:post_type>post</wp:post_type>
  <wp:postmeta><wp:meta_key><![CDATA[wpml_language]]></wp:meta_key><wp:meta_value><![CDATA[fr]]></wp:meta_value></wp:postmeta></item>
 <item><title>Salut</title><wp:post_id>3</wp:post_id><wp:post_name>salut</wp:post_name><wp:status>publish</wp:status><wp:post_type>page</wp:post_type>
  <wp:postmeta><wp:meta_key><![CDATA[_wpml_language]]></wp:meta_key><wp:meta_value><![CDATA[fr]]></wp:meta_value></wp:postmeta></item>
 <item><title>Plain</title><wp:post_id>4</wp:post_id><wp:post_name>plain</wp:post_name><wp:status>publish</wp:status><wp:post_type>page</wp:post_type></item>
</channel></rss>`

// polylangWXR carries the language as a language term on each item.
const polylangWXR = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
 <title>Multi</title><link>https://multi.example</link><language>en</language>
 <wp:base_site_url>https://multi.example</wp:base_site_url>
 <wp:term><wp:term_id>5</wp:term_id><wp:term_taxonomy><![CDATA[language]]></wp:term_taxonomy><wp:term_slug><![CDATA[en]]></wp:term_slug><wp:term_name><![CDATA[English]]></wp:term_name></wp:term>
 <wp:term><wp:term_id>6</wp:term_id><wp:term_taxonomy><![CDATA[language]]></wp:term_taxonomy><wp:term_slug><![CDATA[fr]]></wp:term_slug><wp:term_name><![CDATA[Francais]]></wp:term_name></wp:term>
 <item><title>Hello</title><wp:post_id>1</wp:post_id><wp:post_name>hello</wp:post_name><wp:status>publish</wp:status><wp:post_type>post</wp:post_type>
  <category domain="language" nicename="en"><![CDATA[English]]></category></item>
 <item><title>Bonjour</title><wp:post_id>2</wp:post_id><wp:post_name>hello</wp:post_name><wp:status>publish</wp:status><wp:post_type>post</wp:post_type>
  <category domain="language" nicename="fr"><![CDATA[Francais]]></category></item>
</channel></rss>`

func langsByID(d *Dump) map[string]string {
	out := map[string]string{}
	for _, it := range d.Items {
		out[it.ID] = it.Lang
	}
	return out
}

func TestParseWXR_WPMLLanguage(t *testing.T) {
	d, err := ParseWXR(strings.NewReader(wpmlWXR), WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := langsByID(d)
	want := map[string]string{"1": "en", "2": "fr", "3": "fr", "4": ""}
	if len(got) != len(want) {
		t.Fatalf("items %v, want %v", got, want)
	}
	for id, lang := range want {
		if got[id] != lang {
			t.Errorf("item %s lang %q, want %q", id, got[id], lang)
		}
	}
	if len(d.Warnings) != 0 {
		t.Errorf("warnings: %v", d.Warnings)
	}
	rep := Scan(d, "wxr")
	if rep.Languages["fr"] != 2 || rep.Languages["en"] != 2 {
		t.Errorf("languages %v", rep.Languages)
	}
}

func TestParseWXR_PolylangLanguage(t *testing.T) {
	d, err := ParseWXR(strings.NewReader(polylangWXR), WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := langsByID(d)
	if len(got) != 2 || got["1"] != "en" || got["2"] != "fr" {
		t.Errorf("langs %v: every language is imported and tagged", got)
	}
	if d.Detect.DefaultLang != "en" {
		t.Errorf("default language = %q, want the site language en in the scan", d.Detect.DefaultLang)
	}
	for _, it := range d.Items {
		for _, tr := range it.Terms {
			if tr.Taxonomy == "language" {
				t.Errorf("language term leaked into the item terms: %+v", it)
			}
		}
	}
}

func TestItemLangPrefersPostmeta(t *testing.T) {
	if got := itemLang(map[string]string{"wpml_language": " fr "}, "en"); got != "fr" {
		t.Errorf("got %q", got)
	}
	if got := itemLang(map[string]string{}, "en"); got != "en" {
		t.Errorf("got %q", got)
	}
}

func TestLocaleFor(t *testing.T) {
	cases := []struct{ raw, site, loc, warn string }{
		{"", "en", "en", ""},
		{"en", "en-AU", "en-AU", ""},
		{"fr", "en-AU", "fr", ""},
		{"fr_ca", "en", "fr-CA", ""},
		{"en-GB", "en-AU", "en-GB", ""},
		{"x", "en", "en", "not a valid"},
		{"iw", "he", "he", ""},
		{"iw", "en", "he", ""},
		{"tl", "fil", "fil", ""},
		{"tl", "en", "fil", ""},
		{"zh-hans", "zh-CN", "zh-CN", ""},
		{"zh-Hans", "zh-Hans", "zh-Hans", ""},
		{"zh-hant", "zh-CN", "zh-Hant", ""},
		{"zh-hant", "zh-TW", "zh-TW", ""},
		{"zh-hans", "en", "zh-Hans", ""},
		{"zh-SG", "zh-CN", "zh-SG", ""},
		{"en-" + strings.Repeat("a", 40), "en", "en", "not a valid"},
	}
	for _, c := range cases {
		loc, warn := localeFor(c.raw, c.site)
		if loc != c.loc || (c.warn == "") != (warn == "") || !strings.Contains(warn, c.warn) {
			t.Errorf("localeFor(%q, %q) = %q, %q; want %q, %q", c.raw, c.site, loc, warn, c.loc, c.warn)
		}
	}
}

// Polylang over REST: every language is fetched with its own lang parameter
// and each item is tagged with it.
func TestRESTFetchesEveryPolylangLanguage(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-WP-TotalPages", "1")
		route := r.URL.Query().Get("rest_route")
		if route == "" {
			route = strings.TrimPrefix(r.URL.Path, "/wp-json")
		}
		switch route {
		case "/", "":
			fmt.Fprint(w, `{"name":"Multi","url":"http://x","namespaces":["wp/v2","pll/v1"]}`)
		case "/pll/v1/languages":
			fmt.Fprint(w, `[{"slug":"en","is_default":true},{"slug":"fr","is_default":false}]`)
		case "/wp/v2/types":
			fmt.Fprint(w, `{"post":{"slug":"post","name":"Posts","rest_base":"posts"}}`)
		case "/wp/v2/posts":
			lang := r.URL.Query().Get("lang")
			asked = append(asked, lang)
			id := map[string]int{"en": 1, "fr": 2}[lang]
			fmt.Fprintf(w, `[{"id":%d,"lang":%q,"slug":"hello","status":"publish","link":"http://x/hello/","title":{"rendered":"Hello %s"},"content":{"rendered":"<p>x</p>"}}]`, id, lang, lang)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	src, err := NewRESTSource(RESTOptions{ClientOptions: ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000, Sleep: noSleep}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := src.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := langsByID(d)
	if len(got) != 2 || got["1"] != "en" || got["2"] != "fr" {
		t.Errorf("langs %v, asked %v", got, asked)
	}
}

// A post type Polylang does not translate ignores the lang parameter and comes
// back whole on every pass: it is imported once, in the site language, and
// counts once against MaxItems.
func TestRESTUntranslatedTypeIsNotDuplicatedPerLanguage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-WP-TotalPages", "1")
		route := r.URL.Query().Get("rest_route")
		if route == "" {
			route = strings.TrimPrefix(r.URL.Path, "/wp-json")
		}
		switch route {
		case "/", "":
			fmt.Fprint(w, `{"name":"Multi","url":"http://x","namespaces":["wp/v2","pll/v1"]}`)
		case "/pll/v1/languages":
			fmt.Fprint(w, `[{"slug":"en","is_default":true},{"slug":"fr","is_default":false}]`)
		case "/wp/v2/types":
			fmt.Fprint(w, `{"product":{"slug":"product","name":"Products","rest_base":"products"}}`)
		case "/wp/v2/products": // no lang field, same answer for every pass
			fmt.Fprint(w, `[{"id":7,"slug":"mug","status":"publish","link":"http://x/mug/","title":{"rendered":"Mug"},"content":{"rendered":"<p>x</p>"}},`+
				`{"id":8,"slug":"cup","status":"publish","link":"http://x/cup/","title":{"rendered":"Cup"},"content":{"rendered":"<p>x</p>"}}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	src, err := NewRESTSource(RESTOptions{ClientOptions: ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000, Sleep: noSleep}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := src.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 2 {
		t.Fatalf("%d items, want 2 (one per product)", len(d.Items))
	}
	for _, it := range d.Items {
		if it.Lang != "" {
			t.Errorf("item %s tagged %q, want the site default (empty)", it.ID, it.Lang)
		}
	}
}

func TestLanguageCountsKeyedByNormalizedTag(t *testing.T) {
	items := []Item{{Lang: "FR_ca"}, {Lang: "fr-CA"}, {Lang: "iw"}, {}}
	got := languageCounts(items, "en")
	if got["fr-CA"] != 2 || got["he"] != 1 || got["en"] != 1 || len(got) != 3 {
		t.Fatalf("counts = %v", got)
	}
}

// A link to an item in another language must not be rewritten to that entity's
// path: the path is the site-language twin's, and the other-language entity is
// not public.
func TestEntityForSkipsOtherLocale(t *testing.T) {
	en := &entInfo{ent: &entity.Entity{ID: uuid.New(), Locale: "en"}, newPath: "/hello/"}
	fr := &entInfo{ent: &entity.Entity{ID: uuid.New(), Locale: "fr"}, newPath: "/hello/"}
	r := &Runner{
		infos:    map[string]*entInfo{"1": en, "2": fr},
		oldPaths: map[string]*entInfo{normPath("/hello/"): en, normPath("/fr/bonjour/"): fr},
	}
	if ref, ok := r.entityFor("en", "/hello/", nil); !ok || ref.ID != en.ent.ID {
		t.Errorf("site-language path = %v, %v", ref, ok)
	}
	if ref, ok := r.entityFor("en", "/fr/bonjour/", nil); ok {
		t.Errorf("other-language path resolved to %v", ref)
	}
	if ref, ok := r.entityFor("en", "", url.Values{"p": {"1"}}); !ok || ref.ID != en.ent.ID {
		t.Errorf("site-language ?p= = %v, %v", ref, ok)
	}
	if ref, ok := r.entityFor("en", "", url.Values{"p": {"2"}}); ok {
		t.Errorf("other-language ?p= resolved to %v", ref)
	}
}
