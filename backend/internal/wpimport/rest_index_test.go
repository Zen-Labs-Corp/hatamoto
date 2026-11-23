package wpimport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// WordPress sends gmt_offset as the raw option: a string on a site with a manual UTC
// offset. Every shape loads; whole-hour offsets become an Etc/GMT zone.
func TestRESTIndexGMTOffsetShapes(t *testing.T) {
	cases := []struct {
		name, field, want string
	}{
		{"string zero", `"gmt_offset":"0",`, ""},
		{"string hours", `"gmt_offset":"10",`, "Etc/GMT-10"},
		{"string negative", `"gmt_offset":"-5",`, "Etc/GMT+5"},
		{"number hours", `"gmt_offset":10,`, "Etc/GMT-10"},
		{"string fraction", `"gmt_offset":"5.5",`, ""},
		{"number fraction", `"gmt_offset":9.5,`, ""},
		{"empty string", `"gmt_offset":"",`, ""},
		{"null", `"gmt_offset":null,`, ""},
		{"junk", `"gmt_offset":"UTC+10",`, ""},
		{"out of range", `"gmt_offset":"1e9",`, ""},
		{"missing", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-WP-TotalPages", "1")
				route := r.URL.Query().Get("rest_route")
				if route == "" {
					route = strings.TrimPrefix(r.URL.Path, "/wp-json")
				}
				switch route {
				case "/", "":
					fmt.Fprintf(w, `{"name":"Site","url":"http://x",%s"timezone_string":"","namespaces":["wp/v2"]}`, tc.field)
				case "/wp/v2/types":
					fmt.Fprint(w, `{}`)
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
				t.Fatalf("load: %v", err)
			}
			if d.Site.Title != "Site" || d.Site.Timezone != tc.want {
				t.Errorf("site %q timezone %q, want %q", d.Site.Title, d.Site.Timezone, tc.want)
			}
		})
	}
}
