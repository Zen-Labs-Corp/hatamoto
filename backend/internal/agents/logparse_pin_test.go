package agents

import (
	"testing"
	"time"
)

// Access logs are third-party documents and are read tolerantly: a bad UTF-8
// byte in a user agent becomes U+FFFD and the hit still counts, member names
// of the typed fields match without case, and an unknown or repeated member is
// ignored.

var pinNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func TestParseLineInvalidUTF8(t *testing.T) {
	for _, c := range []struct {
		name string
		line string
		want RawHit
	}{
		{"logpush bad byte in the user agent",
			"{\"ClientIP\":\"203.0.113.9\",\"ClientRequestURI\":\"/media/a.png?x=1\",\"ClientRequestMethod\":\"GET\",\"ClientRequestUserAgent\":\"GPT\xffBot/1.2\",\"EdgeResponseStatus\":200,\"EdgeResponseBytes\":512}",
			RawHit{UA: "GPT\ufffdBot/1.2", IP: "203.0.113.9", Method: "GET", Path: "/media/a.png", Status: 200, Bytes: 512, Source: SourceLog}},
		{"caddy bad byte in the user agent",
			"{\"status\":200,\"size\":10,\"duration\":0.5,\"request\":{\"remote_ip\":\"203.0.113.9\",\"method\":\"GET\",\"uri\":\"/media/b.png\",\"headers\":{\"User-Agent\":[\"Claude\xe9Bot\"]}}}",
			RawHit{UA: "Claude\ufffdBot", IP: "203.0.113.9", Method: "GET", Path: "/media/b.png", Status: 200, Bytes: 10, DurationMS: 500, Source: SourceLog}},
		{"logpush case variants and a repeated member",
			`{"clientip":"203.0.113.9","ClientRequestURI":"/media/c.png","clientrequestmethod":"GET","CLIENTREQUESTUSERAGENT":"GPTBot/1.2","EdgeResponseStatus":404,"EdgeResponseStatus":200,"extra":{"k":1}}`,
			RawHit{UA: "GPTBot/1.2", IP: "203.0.113.9", Method: "GET", Path: "/media/c.png", Status: 200, Source: SourceLog}},
		{"caddy case variants and unknown members",
			`{"STATUS":200,"Size":7,"request":{"Remote_IP":"203.0.113.9","METHOD":"GET","URI":"/media/d.png","headers":{"User-Agent":["GPTBot/1.2"]},"tls":{"version":772}},"msg":"handled"}`,
			RawHit{UA: "GPTBot/1.2", IP: "203.0.113.9", Method: "GET", Path: "/media/d.png", Status: 200, Bytes: 7, Source: SourceLog}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseLogLineAt([]byte(c.line), pinNow)
			if !ok {
				t.Fatal("line was not parsed")
			}
			if got != c.want {
				t.Fatalf("hit = %+v\nwant  %+v", got, c.want)
			}
		})
	}
}
