package wpimport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/safefetch"
)

const liveHome = `<!doctype html><html><head>
<!-- This site is optimized with Rank Math - https://rankmath.example.com -->
<title>Example Blog</title>
<meta property="og:site_name" content="Example Blog" />
<meta name="twitter:site" content="@example" />
<meta name="google-site-verification" content="g-code" />
<meta name="msvalidate.01" content="b-code" />
<script type="application/ld+json">{"@graph":[{"@type":"WebSite"},{"@type":"Organization","name":"Example Org","logo":{"@type":"ImageObject","url":"https://example.com/logo.png"},"sameAs":["https://example.com/p"]}]}</script>
<script type="application/ld+json">{not json</script>
</head><body></body></html>`

func TestSEOLiveReadsPublicPages(t *testing.T) {
	var offHost atomic.Int32
	off := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { offHost.Add(1) }))
	defer off.Close()
	var sitemap atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(liveHome)) })
	mux.HandleFunc("/hello/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>Hello – Example Blog</title></head></html>`))
	})
	mux.HandleFunc("/broken/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) })
	mux.HandleFunc("/category/x/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><meta name="robots" content="noindex, follow"></head></html>`))
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("User-agent: *\nDisallow: /wp-admin/\nAllow: /wp-admin/admin-ajax.php\nDisallow: /private/\nSitemap: " + strings.Replace(off.URL, "127.0.0.1", "localhost", 1) + "/sm.xml\n"))
	})
	mux.HandleFunc("/sm.xml", func(w http.ResponseWriter, r *http.Request) { sitemap.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, err := NewClient(ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000, Sleep: noSleep, MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	live := readSEOLive(context.Background(), c, srv.URL+"/", srv.URL+"/hello/", srv.URL+"/category/x/")
	if live.Plugin != "Rank Math" || live.SiteName != "Example Blog" || live.TwitterSite != "@example" {
		t.Errorf("%+v", live)
	}
	if live.Verification["google"] != "g-code" || live.Verification["bing"] != "b-code" {
		t.Errorf("verification %v", live.Verification)
	}
	if live.OrgName != "Example Org" || live.OrgLogo != "https://example.com/logo.png" || len(live.SameAs) != 1 {
		t.Errorf("org %q %q %v", live.OrgName, live.OrgLogo, live.SameAs)
	}
	if live.Separator != "–" || !live.Noindex["archive"] {
		t.Errorf("sep %q noindex %v", live.Separator, live.Noindex)
	}
	if got := strings.Join(live.RobotsExtra, "|"); got != "User-agent: *|Disallow: /private/" {
		t.Errorf("robots %q", got)
	}
	if offHost.Load() != 0 || sitemap.Load() != 0 {
		t.Errorf("off-host sitemap fetched: %d %d", offHost.Load(), sitemap.Load())
	}

	live = readSEOLive(context.Background(), c, srv.URL+"/", srv.URL+"/broken/", "")
	if len(live.Warnings) != 1 || !strings.Contains(live.Warnings[0], "SEO live read: "+srv.URL+"/broken/") {
		t.Errorf("warnings %v", live.Warnings)
	}
}

func TestSEOLiveBlockedWithoutAllowPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(liveHome)) }))
	defer srv.Close()
	c, err := NewClient(ClientOptions{URL: srv.URL, RatePerSec: 1000, Sleep: noSleep, Timeout: 5 * time.Second})
	if err != nil {
		// construction itself may refuse private hosts
		if !errors.Is(err, safefetch.ErrBlocked) {
			t.Fatal(err)
		}
		return
	}
	live := readSEOLive(context.Background(), c, srv.URL+"/", "", "")
	if len(live.Warnings) == 0 || !strings.Contains(live.Warnings[0], safefetch.ErrBlocked.Error()) {
		t.Errorf("warnings %v", live.Warnings)
	}
}

func TestDetectTSF(t *testing.T) {
	d := &Dump{Items: []Item{{Meta: map[string]string{"_genesis_title": "x"}}}}
	detectFromMeta(d)
	if d.Detect.SEOPlugin != "tsf" {
		t.Errorf("plugin %q", d.Detect.SEOPlugin)
	}
}

func TestSEOLiveDiscouragedSourceSkipsRobotsAndArchiveNoindex(t *testing.T) {
	for name, tc := range map[string]struct{ home, robots string }{
		"robots":     {liveHome, "User-agent: *\nDisallow: /\n"},
		"noindex-hp": {strings.Replace(liveHome, "<title>", `<meta name="robots" content="noindex, nofollow"><title>`, 1), "User-agent: *\nDisallow: /private/\n"},
	} {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.home)) })
			mux.HandleFunc("/category/x/", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`<html><head><meta name="robots" content="noindex"></head></html>`))
			})
			mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.robots)) })
			srv := httptest.NewServer(mux)
			defer srv.Close()
			c, err := NewClient(ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000, Sleep: noSleep, MaxRetries: 0})
			if err != nil {
				t.Fatal(err)
			}
			live := readSEOLive(context.Background(), c, srv.URL+"/", "", srv.URL+"/category/x/")
			if !live.Discouraged || len(live.Warnings) != 1 {
				t.Fatalf("discouraged=%v warnings=%v", live.Discouraged, live.Warnings)
			}
			p := buildSEOPlan(nil, live, testSchemaOf, "")
			s := applyPlan(t, p)
			if len(s.Robots.ExtraLines) != 0 || s.Index.NoIndexArchive {
				t.Errorf("imported robots %v / archive noindex %v", s.Robots.ExtraLines, s.Index.NoIndexArchive)
			}
			if !anyHas(p.NotMapped, "discourages search engines") {
				t.Errorf("not mapped = %v", p.NotMapped)
			}
		})
	}
}

func TestSEOLiveStaysOnTheSourceOrigin(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><meta name="google-site-verification" content="other"></head></html>`))
	}))
	defer other.Close()
	var robotsPath atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/blog/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(liveHome)) })
	mux.HandleFunc("/jump/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL+"/x/", http.StatusFound) })
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) { robotsPath.Store(r.URL.Path) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, err := NewClient(ClientOptions{URL: srv.URL + "/blog", AllowPrivate: true, RatePerSec: 1000, Sleep: noSleep, MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	// Another port of the same host is not the source.
	wrongPort := strings.Replace(srv.URL, "127.0.0.1:", "127.0.0.1:1", 1) + "/blog/"
	live := readSEOLive(context.Background(), c, srv.URL+"/blog/", srv.URL+"/jump/", wrongPort)
	if live.Plugin != "Rank Math" || live.Noindex["archive"] || live.Verification["google"] != "g-code" {
		t.Errorf("plugin %q, noindex %v, verification %v", live.Plugin, live.Noindex, live.Verification)
	}
	if len(live.Warnings) != 1 || !strings.Contains(live.Warnings[0], "redirected to another site") {
		t.Errorf("warnings %v", live.Warnings)
	}
	if got, _ := robotsPath.Load().(string); got != "/robots.txt" {
		t.Errorf("robots.txt requested at %q, want the host root", got)
	}
}

func TestRobotsDisallowsAllIsGroupAware(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"star":            {"User-agent: *\nDisallow: /\n", true},
		"named only":      {"User-agent: GPTBot\nDisallow: /\n", false},
		"named then star": {"User-agent: GPTBot\nDisallow: /\nUser-agent: *\nDisallow: /private/\n", false},
		"grouped agents":  {"User-agent: GPTBot\nUser-agent: *\nDisallow: /\n", true},
		"star then named": {"User-agent: *\nDisallow: /x/\n\nUser-agent: CCBot\nDisallow: /\n", false},
	} {
		if got := robotsDisallowsAll(tc.body); got != tc.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestSEOLiveNamedCrawlerBlockStillMapsRobotsLines(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(liveHome)) })
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("User-agent: GPTBot\nDisallow: /\nUser-agent: *\nDisallow: /private/\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, err := NewClient(ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000, Sleep: noSleep, MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	live := readSEOLive(context.Background(), c, srv.URL+"/", "", "")
	if live.Discouraged || len(live.Warnings) != 0 {
		t.Fatalf("discouraged=%v warnings=%v", live.Discouraged, live.Warnings)
	}
	if got := strings.Join(live.RobotsExtra, "|"); got != "User-agent: GPTBot|Disallow: /|User-agent: *|Disallow: /private/" {
		t.Errorf("robots = %q", got)
	}
}
