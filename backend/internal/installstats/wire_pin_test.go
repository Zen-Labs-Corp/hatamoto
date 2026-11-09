package installstats

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Basic request body is sent to the stats endpoint; its bytes are pinned
// as the previous encoder wrote them, HTML characters escaped.

var wireCases = []struct {
	name string
	p    Payload
	body string
}{
	{"plain", Payload{Install: "x", Version: "v", Channel: "c"},
		`{"install":"x","version":"v","channel":"c"}`},
	{"html characters and non-ASCII", Payload{Install: "i-é", Version: "v1.2.3-rc.1+b&c", Channel: "a&b<c>d"},
		`{"install":"i-é","version":"v1.2.3-rc.1+b\u0026c","channel":"a\u0026b\u003cc\u003ed"}`},
	{"empty channel", Payload{Install: "x", Version: "v"},
		`{"install":"x","version":"v","channel":""}`},
}

func TestWireBodyBytes(t *testing.T) {
	for _, c := range wireCases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				got = string(b)
			}))
			defer srv.Close()
			s := &Service{http: srv.Client(), url: srv.URL}
			body, err := encodeBasic(c.p)
			if err != nil {
				t.Fatal(err)
			}
			if status := s.post(context.Background(), body); status != "ok" {
				t.Fatalf("status = %q", status)
			}
			if got != c.body {
				t.Fatalf("wire body changed:\n got %s\nwant %s", got, c.body)
			}
		})
	}
}
