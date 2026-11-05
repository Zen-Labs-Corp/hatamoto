package indexnow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The IndexNow request body as the previous encoder wrote it: member order of
// the struct, HTML characters in URLs escaped.
func TestIndexNowBodyBytes(t *testing.T) {
	var got string
	var ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, ctype = string(b), r.Header.Get("Content-Type")
	}))
	defer srv.Close()
	urls := []string{"https://example.org/a?x=1&y=2", "https://example.org/é", "https://example.org/<p>"}
	status, err := submit(context.Background(), srv.Client(), srv.URL, "example.org", "k3y", "https://example.org/k3y.txt?a=1&b=2", urls)
	if err != nil || status != http.StatusOK {
		t.Fatalf("submit = %d %v", status, err)
	}
	want := `{"host":"example.org","key":"k3y","keyLocation":"https://example.org/k3y.txt?a=1\u0026b=2","urlList":["https://example.org/a?x=1\u0026y=2","https://example.org/é","https://example.org/\u003cp\u003e"]}`
	if got != want {
		t.Fatalf("body changed:\n got %s\nwant %s", got, want)
	}
	if ctype != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", ctype)
	}
}
