package fonts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeSource map[string][]byte

func (f fakeSource) Data(_ context.Context, hash string) ([]byte, error) {
	if b, ok := f[hash]; ok {
		return b, nil
	}
	return nil, ErrNotFound
}

func serve(t *testing.T, src Source, method, file string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /fonts/{file}", Handler(src))
	req := httptest.NewRequest(method, "/fonts/"+file, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerHeaders(t *testing.T) {
	data := []byte("wOF2-bytes")
	hash := hashOf(data)
	rec := serve(t, fakeSource{hash: data}, http.MethodGet, hash+".woff2", nil)
	if rec.Code != 200 || rec.Body.String() != string(data) {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
	want := map[string]string{
		"Content-Type":                "font/woff2",
		"Cache-Control":               "public, max-age=31536000, immutable",
		"ETag":                        `"` + hash + `"`,
		"Access-Control-Allow-Origin": "*",
		"X-Content-Type-Options":      "nosniff",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	rec = serve(t, fakeSource{hash: data}, http.MethodGet, hash+".woff2", map[string]string{"If-None-Match": `"` + hash + `"`})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("conditional = %d %q", rec.Code, rec.Body)
	}
	if rec := serve(t, fakeSource{hash: data}, http.MethodHead, hash+".woff2", nil); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d %q", rec.Code, rec.Body)
	}
}

func TestHandlerRefusesOtherNames(t *testing.T) {
	data := []byte("wOF2-bytes")
	hash := hashOf(data)
	src := fakeSource{hash: data}
	for _, name := range []string{
		strings.Repeat("0", 64) + ".woff2", // unknown
		hash,                               // no extension
		hash + ".woff",
		strings.ToUpper(hash) + ".woff2",
		hash[:63] + ".woff2",
		"..%2f" + hash + ".woff2",
		hash + ".woff2.exe",
	} {
		if rec := serve(t, src, http.MethodGet, name, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%q = %d, want 404", name, rec.Code)
		}
	}
}
