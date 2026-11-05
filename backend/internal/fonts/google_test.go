package fonts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

const woff = "wOF2-test-bytes"

// stub serves a stylesheet built by sheet(baseURL) at /css2 and font files at
// /f/<name>.woff2, over TLS on 127.0.0.1. Its host counts as both hosts.
type stub struct {
	srv   *httptest.Server
	mu    sync.Mutex
	query string
	files map[string]string
}

func newStub(t *testing.T, sheet func(base string) string) *stub {
	t.Helper()
	st := &stub{files: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/css2", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.query = r.URL.RawQuery
		st.mu.Unlock()
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no UA", 400)
			return
		}
		fmt.Fprint(w, sheet(st.srv.URL))
	})
	mux.HandleFunc("/f/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/f/")
		body, ok := st.files[name]
		if !ok {
			body = woff + name
		}
		fmt.Fprint(w, body)
	})
	st.srv = httptest.NewTLSServer(mux)
	t.Cleanup(st.srv.Close)
	return st
}

func (st *stub) google() *Google {
	return &Google{
		Client:    st.srv.Client(),
		CSSURL:    st.srv.URL + "/css2",
		AllowHost: func(host string, _ bool) bool { return host == "127.0.0.1" },
	}
}

func face(base, subset, file string, weight int, style string) string {
	return fmt.Sprintf(`/* %s */
@font-face {
  font-family: 'Inter';
  font-style: %s;
  font-weight: %d;
  font-display: swap;
  src: url(%s/f/%s.woff2) format('woff2');
  unicode-range: U+0000-00FF, U+0131;
}
`, subset, style, weight, base, file)
}

func TestDownloadLatinOnly(t *testing.T) {
	st := newStub(t, func(b string) string {
		return face(b, "cyrillic", "cyr", 400, "normal") + face(b, "latin-ext", "ext", 400, "normal") + face(b, "latin", "lat", 400, "normal")
	})
	fonts, err := st.google().Download(context.Background(), "Inter", []int{400}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(fonts) != 2 || fonts[0].Subset != "latin-ext" || fonts[1].Subset != "latin" {
		t.Fatalf("fonts = %+v", fonts)
	}
	f := fonts[1]
	if f.Source != "google" || f.Family != "Inter" || f.UnicodeRange != "U+0000-00FF, U+0131" || string(f.Data) != woff+"lat.woff2" || f.Hash != hashOf(f.Data) {
		t.Errorf("face = %+v", f)
	}
	all, err := st.google().Download(context.Background(), "Inter", []int{400}, false, false)
	if err != nil || len(all) != 3 {
		t.Errorf("all subsets = %d, %v", len(all), err)
	}
}

func TestDownloadQuery(t *testing.T) {
	st := newStub(t, func(b string) string {
		return strings.Replace(face(b, "latin", "a", 400, "normal"), "'Inter'", "'Open Sans'", 1)
	})
	g := st.google()
	if _, err := g.Download(context.Background(), "Open Sans", []int{700, 400, 400}, true, true); err != nil {
		t.Fatal(err)
	}
	want := "family=Open+Sans:ital,wght@0,400;0,700;1,400;1,700&display=swap"
	if st.query != want {
		t.Errorf("query = %q\nwant    %q", st.query, want)
	}
	if _, err := g.Download(context.Background(), "Open Sans", []int{500, 300}, false, true); err != nil {
		t.Fatal(err)
	}
	if want := "family=Open+Sans:wght@300;500&display=swap"; st.query != want {
		t.Errorf("query = %q, want %q", st.query, want)
	}
	if _, err := g.Download(context.Background(), "Inter", nil, false, true); err == nil {
		t.Error("no weights accepted")
	}
	if _, err := g.Download(context.Background(), "Inter", []int{450}, false, true); err != ErrBadWeight {
		t.Errorf("weight 450 = %v", err)
	}
	if _, err := g.Download(context.Background(), `In"ter`, []int{400}, false, true); err != ErrBadFamily {
		t.Errorf("bad family = %v", err)
	}
}

func TestDownloadRefusals(t *testing.T) {
	tests := []struct {
		name  string
		sheet func(base string) string
		files map[string]string
		want  string
	}{
		{"foreign file host", func(string) string {
			return face("https://evil.example.com", "latin", "a", 400, "normal")
		}, nil, "not allowed"},
		{"not woff2", func(b string) string { return face(b, "latin", "a", 400, "normal") },
			map[string]string{"a.woff2": "<html>nope"}, "not a .woff2"},
		{"oversize", func(b string) string { return face(b, "latin", "a", 400, "normal") },
			map[string]string{"a.woff2": "wOF2" + strings.Repeat("x", MaxFileBytes)}, "at most 2 MB"},
		{"unknown declaration", func(b string) string {
			return strings.Replace(face(b, "latin", "a", 400, "normal"), "font-display: swap;", "behavior: url(x);", 1)
		}, nil, "does not understand"},
		{"other family", func(b string) string {
			return strings.Replace(face(b, "latin", "a", 400, "normal"), "'Inter'", "'Other'", 1)
		}, nil, "family differs"},
		{"junk outside a face", func(b string) string { return "@import url(x);" + face(b, "latin", "a", 400, "normal") }, nil, "does not understand"},
		{"file url on http", func(b string) string {
			return strings.Replace(face(b, "latin", "a", 400, "normal"), "https://", "http://", 1)
		}, nil, "does not understand"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newStub(t, tc.sheet)
			st.files = tc.files
			_, err := st.google().Download(context.Background(), "Inter", []int{400}, false, true)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestDownloadRefusesRedirectOffHost(t *testing.T) {
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the foreign host was contacted")
	}))
	defer other.Close()
	st := newStub(t, func(string) string { return "" })
	mux := http.NewServeMux()
	mux.HandleFunc("/css2", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1)+"/css2", http.StatusFound)
	})
	st.srv.Config.Handler = mux
	_, err := st.google().Download(context.Background(), "Inter", []int{400}, false, true)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseFacesFontStretch(t *testing.T) {
	const tail = `src:url(https://fonts.gstatic.com/s/a.woff2) format('woff2');unicode-range:U+0000-00FF}`
	for _, ok := range []string{"normal", "100%", "75% 100%", "87.5%", "semi-condensed", "ultra-expanded"} {
		css := "@font-face{font-family:'Open Sans';font-style:normal;font-weight:400;font-stretch:" + ok + ";font-display:swap;" + tail
		if _, err := ParseFaces(css); err != nil {
			t.Errorf("font-stretch %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"url(x)", "100", "100%;x", "expression(1)", "1000%", "100% 100% 100%", ""} {
		css := "@font-face{font-family:'Open Sans';font-style:normal;font-weight:400;font-stretch:" + bad + ";" + tail
		if _, err := ParseFaces(css); err == nil {
			t.Errorf("font-stretch %q accepted", bad)
		}
	}
	// Two declarations of it are still refused.
	if _, err := ParseFaces("@font-face{font-family:Inter;font-style:normal;font-weight:400;font-stretch:100%;font-stretch:100%;" + tail); err == nil {
		t.Error("repeated font-stretch accepted")
	}
}

func TestParseFacesWeightRangeAndSubset(t *testing.T) {
	faces, err := ParseFaces(`/* latin */ @font-face{font-family:'Inter';font-style:italic;font-weight:100 900;src:url(https://fonts.gstatic.com/s/a.woff2) format('woff2');unicode-range:U+0000-00FF}
@font-face{font-family:Inter;font-style:normal;font-weight:400;src:url(https://fonts.gstatic.com/s/b.woff2) format("woff2")}`)
	if err != nil || len(faces) != 2 {
		t.Fatalf("faces %+v err %v", faces, err)
	}
	if f := faces[0]; f.Weight != 100 || !f.Variable || f.Style != "italic" || f.Subset != "latin" {
		t.Errorf("first = %+v", f)
	}
	if f := faces[1]; f.Subset != "" || f.Family != "Inter" {
		t.Errorf("second (comment must not carry over) = %+v", f)
	}
	for _, bad := range []string{
		"", "body{}", "@font-face{font-family:Inter}",
		"@font-face{font-family:'In;ter';src:url(https://a/b.woff2) format('woff2')}",
		"@font-face{font-family:Inter;font-style:normal;font-weight:400;src:url(https://a/b.woff2) format('woff2'), url(https://a/c.woff2) format('woff2')}",
		"@font-face{font-family:Inter;font-style:normal;font-weight:400;src:url(https://a/b.ttf) format('truetype')}",
		"@font-face{font-family:Inter;font-style:normal;font-weight:400;src:url(https://a/b.woff2) format('woff2');unicode-range:U+0000;}x",
		"@font-face{font-family:Inter;font-style:normal;font-weight:400;src:url(https://a/b.woff2) format('woff2');unicode-range:url(x)}",
	} {
		if _, err := ParseFaces(bad); err == nil {
			t.Errorf("ParseFaces(%q) accepted", bad)
		}
	}
}

// The saved stylesheets are what Google really answers for the downloader's
// request (Chrome user agent, css2, display=swap). Their unicode-range lists
// run past 1500 bytes, which an earlier length cap refused.
func TestRealStylesheets(t *testing.T) {
	for _, tc := range []struct {
		file, family string
		italic       bool
	}{
		{"opensans.css", "Open Sans", false},
		{"opensans-italic.css", "Open Sans", true},
		{"roboto.css", "Roboto", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			faces, err := ParseFaces(string(raw))
			if err != nil {
				t.Fatalf("ParseFaces: %v", err)
			}
			if len(faces) < 10 {
				t.Fatalf("got %d faces", len(faces))
			}
			st := newStub(t, func(base string) string {
				return strings.ReplaceAll(string(raw), "https://fonts.gstatic.com", base+"/f")
			})
			// Font URLs become <base>/f/s/...; the stub keys files by the rest of the path.
			got, err := st.google().Download(context.Background(), tc.family, []int{400, 700}, tc.italic, false)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			if len(got) != len(faces) {
				t.Fatalf("downloaded %d faces, parsed %d", len(got), len(faces))
			}
		})
	}
}

func TestParseFacesRangeLength(t *testing.T) {
	block := func(r string) string {
		return "@font-face{font-family:Inter;font-style:normal;font-weight:400;src:url(https://a/b.woff2) format('woff2');unicode-range:" + r + "}"
	}
	if _, err := ParseFaces(block(strings.Repeat("U+0041, ", 190) + "U+0042")); err != nil {
		t.Fatalf("long list refused: %v", err)
	}
	if _, err := ParseFaces(block(strings.Repeat("U+0041, ", 600) + "U+0042")); err == nil {
		t.Fatal("oversized list accepted")
	}
}

// Open Sans italic is 40 blocks per two weights; with Latin only ticked the
// face cap must count what is downloaded, not what Google lists.
func TestFaceCapAfterLatinFilter(t *testing.T) {
	raw, err := os.ReadFile("testdata/opensans-italic.css")
	if err != nil {
		t.Fatal(err)
	}
	sheet := string(raw)
	big := sheet + sheet + sheet // 120 blocks, 12 of them Latin
	serve := func(base string) string {
		return strings.ReplaceAll(big, "https://fonts.gstatic.com", base+"/f")
	}
	st := newStub(t, serve)
	got, err := st.google().Download(context.Background(), "Open Sans", []int{400, 600, 700}, true, true)
	if err != nil {
		t.Fatalf("latin only: %v", err)
	}
	if len(got) == 0 || len(got) > maxFaces {
		t.Fatalf("got %d faces", len(got))
	}
	_, err = st.google().Download(context.Background(), "Open Sans", []int{400, 600, 700}, true, false)
	if err == nil || !strings.Contains(err.Error(), "fewer weights or styles, or tick Latin only") {
		t.Fatalf("err = %v, want an actionable cap message", err)
	}
}
