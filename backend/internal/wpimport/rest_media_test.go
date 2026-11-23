package wpimport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// A site where a plugin makes WordPress fail on one attachment's media_details
// (a 500 for every page that holds it), and where the pages are short because
// WordPress counts attachments it then hides. Every attachment still comes over;
// the broken one without its size variants, the others with theirs.
func TestRESTMediaSurvivesABrokenItemAndShortPages(t *testing.T) {
	media := map[int]string{
		1: `{"id":1,"source_url":"http://x/a.jpg","mime_type":"image/jpeg","media_details":{"sizes":{"thumbnail":{"source_url":"http://x/a-150x150.jpg"}}}}`,
		2: `{"id":2,"source_url":"http://x/b.svg","mime_type":"image/svg+xml"}`,
		3: `{"id":3,"source_url":"http://x/c.jpg","mime_type":"image/jpeg"}`,
	}
	pages := map[string][]int{"1": {1, 2}, "2": {}, "3": {3}} // three pages, page 2 empty
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		route := r.URL.Query().Get("rest_route")
		if route == "" {
			route = strings.TrimPrefix(r.URL.Path, "/wp-json")
		}
		fields := r.URL.Query().Get("_fields")
		switch {
		case route == "/" || route == "":
			fmt.Fprint(w, `{"name":"Site","url":"http://x","namespaces":["wp/v2"]}`)
		case route == "/wp/v2/types":
			fmt.Fprint(w, `{}`)
		case route == "/wp/v2/media":
			ids := pages[r.URL.Query().Get("page")]
			w.Header().Set("X-WP-TotalPages", "3")
			if fields != "id" && strings.Contains(fields, "media_details") && containsInt(ids, 2) {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"code":"internal_server_error"}`)
				return
			}
			var out []string
			for _, id := range ids {
				if fields == "id" {
					out = append(out, fmt.Sprintf(`{"id":%d}`, id))
				} else {
					out = append(out, media[id])
				}
			}
			fmt.Fprint(w, "["+strings.Join(out, ",")+"]")
		case strings.HasPrefix(route, "/wp/v2/media/"):
			var id int
			fmt.Sscanf(strings.TrimPrefix(route, "/wp/v2/media/"), "%d", &id)
			if id == 2 && strings.Contains(fields, "media_details") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, media[id])
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
	var got []string
	sizes := map[string]int{}
	for _, m := range d.Media {
		got = append(got, m.ID)
		sizes[m.ID] = len(m.Sizes)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "1,2,3" {
		t.Fatalf("media %v, warnings %q", got, d.Warnings)
	}
	if sizes["1"] != 1 || sizes["2"] != 0 {
		t.Errorf("sizes %v", sizes)
	}
	warned := false
	for _, w := range d.Warnings {
		if strings.Contains(w, "could not read the media library") {
			t.Errorf("library lost: %s", w)
		}
		if strings.Contains(w, "media 2:") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning for media 2: %q", d.Warnings)
	}
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
