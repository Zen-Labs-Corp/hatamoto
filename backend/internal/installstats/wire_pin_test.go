package installstats

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The request body and the indented preview are shown to the owner and sent
// to the stats endpoint; their bytes are pinned as the previous encoder wrote
// them, HTML characters escaped.

var wireCases = []struct {
	name       string
	p          Payload
	body       string
	prettyBody string
}{
	{"plain", Payload{Install: "x", Version: "v", Channel: "c"},
		`{"install":"x","version":"v","channel":"c"}`,
		"{\n  \"install\": \"x\",\n  \"version\": \"v\",\n  \"channel\": \"c\"\n}"},
	{"html characters and non-ASCII", Payload{Install: "i-é", Version: "v1.2.3-rc.1+b&c", Channel: "a&b<c>d"},
		`{"install":"i-é","version":"v1.2.3-rc.1+b\u0026c","channel":"a\u0026b\u003cc\u003ed"}`,
		"{\n  \"install\": \"i-é\",\n  \"version\": \"v1.2.3-rc.1+b\\u0026c\",\n  \"channel\": \"a\\u0026b\\u003cc\\u003ed\"\n}"},
	{"empty channel", Payload{Install: "x", Version: "v"},
		`{"install":"x","version":"v","channel":""}`,
		"{\n  \"install\": \"x\",\n  \"version\": \"v\",\n  \"channel\": \"\"\n}"},
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
			if status := s.send(context.Background(), c.p); status != "ok" {
				t.Fatalf("status = %q", status)
			}
			if got != c.body {
				t.Fatalf("wire body changed:\n got %s\nwant %s", got, c.body)
			}
		})
	}
}

func TestPrettyBytes(t *testing.T) {
	for _, c := range wireCases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.Pretty(); got != c.prettyBody {
				t.Fatalf("Pretty changed:\n got %q\nwant %q", got, c.prettyBody)
			}
		})
	}
}
