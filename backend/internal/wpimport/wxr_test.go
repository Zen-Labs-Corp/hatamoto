package wpimport

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

const sampleWXR = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/"
 xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
 <title>Old Site</title><link>https://old.example</link><description>Just a tagline</description><language>en-US</language>
 <wp:base_site_url>https://old.example</wp:base_site_url>
 <wp:author><wp:author_login><![CDATA[jane]]></wp:author_login><wp:author_display_name><![CDATA[Jane Guest]]></wp:author_display_name></wp:author>
 <wp:category><wp:term_id>2</wp:term_id><wp:category_nicename><![CDATA[news]]></wp:category_nicename><wp:category_parent><![CDATA[]]></wp:category_parent><wp:cat_name><![CDATA[News]]></wp:cat_name></wp:category>
 <wp:category><wp:term_id>3</wp:term_id><wp:category_nicename><![CDATA[updates]]></wp:category_nicename><wp:category_parent><![CDATA[news]]></wp:category_parent><wp:cat_name><![CDATA[Updates]]></wp:cat_name></wp:category>
 <wp:term><wp:term_id>9</wp:term_id><wp:term_taxonomy><![CDATA[nav_menu]]></wp:term_taxonomy><wp:term_slug><![CDATA[main]]></wp:term_slug><wp:term_name><![CDATA[Main]]></wp:term_name></wp:term>
 <item>
  <title>Hello World</title><link>https://old.example/2024/05/hello-world/</link><dc:creator><![CDATA[jane]]></dc:creator>
  <content:encoded><![CDATA[<!-- wp:paragraph --><p>Hi [gallery ids="1"]</p><!-- /wp:paragraph -->]]></content:encoded>
  <excerpt:encoded><![CDATA[]]></excerpt:encoded>
  <wp:post_id>10</wp:post_id><wp:post_date_gmt><![CDATA[2024-05-10 09:00:00]]></wp:post_date_gmt><wp:post_modified_gmt><![CDATA[2024-05-11 09:00:00]]></wp:post_modified_gmt>
  <wp:post_name><![CDATA[hello-world]]></wp:post_name><wp:status><![CDATA[publish]]></wp:status><wp:post_parent>0</wp:post_parent><wp:post_type><![CDATA[post]]></wp:post_type>
  <category domain="category" nicename="news"><![CDATA[News]]></category>
  <wp:postmeta><wp:meta_key><![CDATA[_yoast_wpseo_title]]></wp:meta_key><wp:meta_value><![CDATA[%%title%% %%sep%% %%sitename%% custom]]></wp:meta_value></wp:postmeta>
  <wp:postmeta><wp:meta_key><![CDATA[_yoast_wpseo_meta-robots-noindex]]></wp:meta_key><wp:meta_value><![CDATA[1]]></wp:meta_value></wp:postmeta>
  <wp:postmeta><wp:meta_key><![CDATA[_thumbnail_id]]></wp:meta_key><wp:meta_value><![CDATA[11]]></wp:meta_value></wp:postmeta>
 </item>
 <item><title>pic</title><wp:post_id>11</wp:post_id><wp:post_type><![CDATA[attachment]]></wp:post_type><wp:status><![CDATA[inherit]]></wp:status>
  <wp:attachment_url><![CDATA[https://old.example/wp-content/uploads/2024/05/pic.jpg]]></wp:attachment_url></item>
 <item><title>Services</title><link>https://old.example/services/</link><wp:post_id>12</wp:post_id><wp:post_name>services</wp:post_name><wp:status>publish</wp:status><wp:post_type>page</wp:post_type><content:encoded>Plain text page</content:encoded></item>
 <item><title>Auto</title><wp:post_id>13</wp:post_id><wp:status>auto-draft</wp:status><wp:post_type>post</wp:post_type></item>
 <item><title>Home</title><wp:post_id>20</wp:post_id><wp:post_name>home-link</wp:post_name><wp:status>publish</wp:status><wp:post_type>nav_menu_item</wp:post_type><wp:menu_order>1</wp:menu_order>
  <category domain="nav_menu" nicename="main"><![CDATA[Main]]></category>
  <wp:postmeta><wp:meta_key>_menu_item_type</wp:meta_key><wp:meta_value>post_type</wp:meta_value></wp:postmeta>
  <wp:postmeta><wp:meta_key>_menu_item_object_id</wp:meta_key><wp:meta_value>12</wp:meta_value></wp:postmeta>
  <wp:postmeta><wp:meta_key>_menu_item_object</wp:meta_key><wp:meta_value>page</wp:meta_value></wp:postmeta></item>
</channel></rss>`

func TestParseWXR(t *testing.T) {
	d, err := ParseWXR(strings.NewReader(sampleWXR), WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Site.Title != "Old Site" || d.Site.URL != "https://old.example" || d.Site.Language != "en-US" {
		t.Errorf("site %+v", d.Site)
	}
	if len(d.Items) != 2 || len(d.Media) != 1 || len(d.Authors) != 1 || len(d.Terms) != 2 || len(d.Menus) != 1 {
		t.Fatalf("items=%d media=%d authors=%d terms=%d menus=%d", len(d.Items), len(d.Media), len(d.Authors), len(d.Terms), len(d.Menus))
	}
	post := d.Items[0]
	if post.AuthorKey != "jane" || post.FeaturedMedia != "11" || len(post.Terms) != 1 || post.Terms[0].Slug != "news" {
		t.Errorf("post %+v", post)
	}
	if post.SEO.Title != "Hello World - Old Site custom" || !post.SEO.NoIndex || post.SEO.Source != "yoast" {
		t.Errorf("seo %+v", post.SEO)
	}
	if d.Detect.SEOPlugin != "yoast" || d.Terms[1].ParentSlug != "news" {
		t.Errorf("detect %+v terms %+v", d.Detect, d.Terms)
	}
	if m := d.Menus[0]; m.Name != "Main" || len(m.Items) != 1 || m.Items[0].ObjectID != "12" {
		t.Errorf("menu %+v", m)
	}
	if !d.RawBodies || len(d.OldURLs) != 2 || d.Site.Permalink != "/%year%/%monthnum%/%postname%/" {
		t.Errorf("raw=%v urls=%v permalink=%q", d.RawBodies, d.OldURLs, d.Site.Permalink)
	}
	rep := Scan(d, "wxr")
	if rep.Shortcodes["gallery"] != 1 || rep.Posts != 1 || rep.Pages != 1 {
		t.Errorf("report %+v", rep)
	}
}

func TestParseWXRLimits(t *testing.T) {
	_, err := ParseWXR(strings.NewReader(sampleWXR), WXROptions{MaxBytes: 300})
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("size cap: %v", err)
	}
	if _, err := ParseWXR(strings.NewReader(sampleWXR), WXROptions{MaxItems: 2}); err == nil || !strings.Contains(err.Error(), "more than 2 items") {
		t.Errorf("item cap: %v", err)
	}
	if _, err := ParseWXR(strings.NewReader("<html>no</html>"), WXROptions{}); err == nil {
		t.Error("a non-WXR file must fail")
	}
}

func TestSEOFromOtherPlugins(t *testing.T) {
	site := Site{Title: "S", Tagline: "T"}
	it := Item{Title: "Post"}
	rm := seoFromMeta(map[string]string{"rank_math_title": "%title% %sep% %sitename%", "rank_math_robots": `a:1:{i:0;s:7:"noindex";}`}, site, it)
	if rm.Title != "Post - S" || !rm.NoIndex || rm.Source != "rankmath" {
		t.Errorf("rank math %+v", rm)
	}
	sp := seoFromMeta(map[string]string{"_seopress_titles_title": "X %%sitetitle%%", "_seopress_robots_index": "yes"}, site, it)
	if sp.Title != "X S" || !sp.NoIndex {
		t.Errorf("seopress %+v", sp)
	}
}

func TestParseHeadAndSitemap(t *testing.T) {
	h := ParseHead(`<html><head><title>A &amp; B</title><meta name="description" content="d &quot;x&quot;"><meta name='robots' content='noindex, follow'/>
<link rel="canonical" href="https://x.test/p/"><meta property="og:image" content="https://x.test/i.png"></head><body><title>no</title></body></html>`, "html")
	if h.Title != "A & B" || h.Description != `d "x"` || !h.NoIndex || h.Canonical != "https://x.test/p/" || h.OGImage != "https://x.test/i.png" {
		t.Errorf("head %+v", h)
	}
	kind, locs, err := ParseSitemap([]byte(`<urlset xmlns:image="i"><url><loc>https://x.test/a/</loc><image:image><image:loc>https://x.test/a.jpg</image:loc></image:image></url><url><loc>https://x.test/b/</loc></url></urlset>`), 100)
	if err != nil || kind != "urlset" || len(locs) != 2 || locs[0] != "https://x.test/a/" {
		t.Errorf("sitemap %s %v %v", kind, locs, err)
	}
}

// repeatReader streams head, then n copies of unit, then tail, without
// holding the payload in memory.
type repeatReader struct {
	head, unit, tail string
	n                int
	cur              string
}

func (r *repeatReader) Read(p []byte) (int, error) {
	for r.cur == "" {
		switch {
		case r.head != "":
			r.cur, r.head = r.head, ""
		case r.n > 0:
			r.cur, r.n = r.unit, r.n-1
		case r.tail != "":
			r.cur, r.tail = r.tail, ""
		default:
			return 0, io.EOF
		}
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

func TestParseWXRDeepNestingFailsWithBoundedMemory(t *testing.T) {
	cases := map[string]string{
		"channel":        `<rss><channel>`,
		"inside an item": `<rss><channel><item>`,
		"inside a title": `<rss><channel><title>`,
	}
	for name, head := range cases {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			// 10 million nested elements: about 30 MB of input.
			_, err := ParseWXR(&repeatReader{head: head, unit: "<a>", n: 10_000_000}, WXROptions{})
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Fatal("deeply nested export parsed")
			}
			if !errors.Is(err, ErrTooComplex) {
				t.Fatalf("error = %v, want ErrTooComplex", err)
			}
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
				t.Fatalf("parsing allocated %d MB for a nesting attack", grew>>20)
			}
		})
	}
}

func TestParseWXRTooManyElements(t *testing.T) {
	_, err := ParseWXR(&repeatReader{head: `<rss><channel>`, unit: `<a/>`, n: 1000}, WXROptions{MaxElements: 100})
	if !errors.Is(err, ErrTooComplex) {
		t.Fatalf("error = %v, want ErrTooComplex", err)
	}
}
