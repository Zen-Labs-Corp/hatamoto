package wpimport

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSEOExtrasSitemapExcludedTypes(t *testing.T) {
	rm := &SEOFile{Plugin: "rankmath", Values: map[string]any{
		"pt_post_sitemap": "on", "pt_page_sitemap": "off", "pt_product_sitemap": "off", "pt_page_title": "x",
	}}
	if got := sitemapExcludedTypes(rm); !reflect.DeepEqual(got, []string{"page", "product"}) {
		t.Errorf("rank math = %v", got)
	}
	y := &SEOFile{Plugin: "yoast", Values: map[string]any{"post_types-post-not_in_sitemap": true, "post_types-page-not_in_sitemap": false}}
	if got := sitemapExcludedTypes(y); !reflect.DeepEqual(got, []string{"post"}) {
		t.Errorf("yoast = %v", got)
	}
	if sitemapExcludedTypes(&SEOFile{Plugin: "tsf", Values: map[string]any{"pt_page_sitemap": "off"}}) != nil || sitemapExcludedTypes(nil) != nil {
		t.Error("tsf and nil have no per-type sitemap option")
	}
}

func TestSEOExtrasSchemaSitemapKeepsPriority(t *testing.T) {
	ex, prio, freq := schemaSitemap(json.RawMessage(`{"url_pattern":"/x","sitemap":{"priority":0.4,"changefreq":"weekly"}}`))
	if ex || prio == nil || *prio != 0.4 || freq != "weekly" {
		t.Errorf("got %v %v %q", ex, prio, freq)
	}
	if ex, _, _ := schemaSitemap(json.RawMessage(`{"sitemap":{"exclude":true}}`)); !ex {
		t.Error("exclude not read")
	}
	if ex, p, f := schemaSitemap(nil); ex || p != nil || f != "" {
		t.Error("empty settings")
	}
}

func TestSEOExtrasMergeSameAsUnionsAndKeepsProfile(t *testing.T) {
	profile := json.RawMessage(`{"name":"Acme","phone":"123","sameas":["https://social.example.com/acme"],"custom_future_key":{"a":1}}`)
	merged, before, added, err := mergeSameAs(profile, []string{
		"https://social.example.com/acme/", // same as an existing one
		"http://insecure.example.com/acme", // not https
		"https://other.example.com/acme",
		"not a url",
		"https://other.example.com/acme", // duplicate in the input
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || !reflect.DeepEqual(added, []string{"https://other.example.com/acme"}) {
		t.Errorf("before %v added %v", before, added)
	}
	var obj map[string]any
	if err := json.Unmarshal(merged, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["name"] != "Acme" || obj["phone"] != "123" || obj["custom_future_key"] == nil {
		t.Errorf("profile keys lost: %s", merged)
	}
	if got, _ := obj["sameas"].([]any); len(got) != 2 {
		t.Errorf("sameas = %v", obj["sameas"])
	}
}

func TestSEOExtrasMergeSameAsEmptyProfileAndNothingNew(t *testing.T) {
	merged, _, added, err := mergeSameAs(nil, []string{"https://a.example.com/x"})
	if err != nil || len(added) != 1 || string(merged) != `{"sameas":["https://a.example.com/x"]}` {
		t.Errorf("merged %s added %v err %v", merged, added, err)
	}
	same := json.RawMessage(`{"sameas":["https://a.example.com/x"]}`)
	if m, _, added, _ := mergeSameAs(same, []string{"https://a.example.com/x"}); len(added) != 0 || string(m) != string(same) {
		t.Errorf("nothing new must return the profile as is: %s %v", m, added)
	}
	if _, _, _, err := mergeSameAs(json.RawMessage(`[1]`), []string{"https://a.example.com/x"}); err == nil {
		t.Error("an unreadable profile must not be overwritten")
	}
}
