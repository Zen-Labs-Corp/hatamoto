package agents

import (
	"bufio"
	"os"
	"testing"
	"time"
)

// fixtureNow is just after the fixtures' timestamps (2025-10-09).
var fixtureNow = time.Date(2025, 10, 10, 0, 0, 0, 0, time.UTC)

func parseLogFixture(t *testing.T, name string) []RawHit {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []RawHit
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if h, ok := parseLogLineAt(sc.Bytes(), fixtureNow); ok {
			out = append(out, h)
		}
	}
	return out
}

func TestParseLogpushFixture(t *testing.T) {
	hits := parseLogFixture(t, "logpush.ndjson")
	// media 200 kept, page 200 skipped, page 503 skipped (the middleware owns
	// pages, 5xx included), media 404 IPv6 kept, garbage skipped
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d: %+v", len(hits), hits)
	}
	m := hits[0]
	if m.Path != "/media/abc/hero.png" || m.Status != 200 || m.Bytes != 5120 || m.Source != SourceLog ||
		m.IP != "20.171.206.1" || m.Method != "GET" {
		t.Fatalf("media hit: %+v", m)
	}
	if !m.At.Equal(time.Unix(0, 1760000000000000000)) {
		t.Fatalf("ns timestamp: %v", m.At)
	}
	if hits[1].IP != "2001:db8::1" || hits[1].Status != 404 {
		t.Fatalf("ipv6 hit: %+v", hits[1])
	}
}

func TestParseCaddyFixture(t *testing.T) {
	hits := parseLogFixture(t, "caddy.ndjson")
	if len(hits) != 2 { // the 200 /blog line is not media
		t.Fatalf("want 2 hits, got %d: %+v", len(hits), hits)
	}
	m := hits[0]
	if m.IP != "20.171.206.1" {
		t.Fatalf("client_ip must win over remote_ip: %q", m.IP)
	}
	if m.Path != "/media/abc/hero.png" || m.DurationMS != 12 || m.Bytes != 5120 || m.Source != SourceLog || m.Status != 200 {
		t.Fatalf("caddy media hit: %+v", m)
	}
	if m.UA == "" || m.At.Unix() != 1760000000 {
		t.Fatalf("ua/ts: %+v", m)
	}
	e := hits[1]
	if e.IP != "2001:db8::7" || e.Status != 502 || e.DurationMS != 1500 {
		t.Fatalf("media 5xx hit with remote_ip fallback: %+v", e)
	}
}

// Pages, sitemaps and robots are recorded by other sources even when they
// fail, so a log 5xx on them must not be stored a second time.
func TestParseLogLineSkipsNonMedia5xx(t *testing.T) {
	for _, uri := range []string{"/pricing", "/sitemap.xml", "/llms.txt", "/robots.txt", "/blog/x"} {
		line := `{"ClientRequestURI":"` + uri + `","ClientRequestUserAgent":"GPTBot/1.2","EdgeResponseStatus":503}`
		if h, ok := ParseLogLine([]byte(line)); ok {
			t.Errorf("5xx on %s must be skipped, got %+v", uri, h)
		}
	}
	media := `{"ClientRequestURI":"/media/a.png","ClientRequestUserAgent":"GPTBot/1.2","EdgeResponseStatus":503}`
	if _, ok := ParseLogLine([]byte(media)); !ok {
		t.Error("media 5xx must be kept")
	}
}

func TestParseLogLineGarbage(t *testing.T) {
	for _, in := range []string{"", "   ", "{", "[]", "null", `{"a":1}`, `{"request":"x"}`,
		`{"ClientRequestURI":5}`, `{"request":{"uri":"/media/x"},"status":200}`,
		`{"ClientRequestURI":"/media/x","ClientRequestUserAgent":"GPTBot","EdgeResponseStatus":0}`} {
		if h, ok := ParseLogLine([]byte(in)); ok {
			t.Errorf("%q should be skipped, got %+v", in, h)
		}
	}
}

func TestStripPort(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4": "1.2.3.4", "1.2.3.4:80": "1.2.3.4", "[::1]:80": "::1", "2001:db8::1": "2001:db8::1", "": "",
	} {
		if got := stripPort(in); got != want {
			t.Errorf("stripPort(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParseLogLineTimestampBounds(t *testing.T) {
	now := time.Date(2025, 10, 10, 12, 0, 0, 0, time.UTC)
	logpush := func(ts time.Time) string {
		return `{"ClientRequestURI":"/media/a.png","ClientRequestUserAgent":"GPTBot/1.2","EdgeResponseStatus":200,"EdgeStartTimestamp":"` +
			ts.Format(time.RFC3339) + `"}`
	}
	cases := []struct {
		name string
		ts   time.Time
		keep bool
	}{
		{"fresh", now.Add(-time.Hour), true},
		{"6d23h old", now.Add(-7*24*time.Hour + time.Hour), true},
		{"8d old", now.Add(-8 * 24 * time.Hour), false},
		{"year old", now.AddDate(-1, 0, 0), false},
		{"4m future", now.Add(4 * time.Minute), true},
		{"10m future", now.Add(10 * time.Minute), false},
		{"far future", now.AddDate(5, 0, 0), false},
	}
	for _, c := range cases {
		if _, ok := parseLogLineAt([]byte(logpush(c.ts)), now); ok != c.keep {
			t.Errorf("%s: kept=%v want %v", c.name, ok, c.keep)
		}
	}
	// No timestamp at all means "now" and is kept.
	if _, ok := parseLogLineAt([]byte(`{"ClientRequestURI":"/media/a.png","ClientRequestUserAgent":"GPTBot/1.2","EdgeResponseStatus":200}`), now); !ok {
		t.Error("missing timestamp must be kept")
	}
}

func TestParseCaddyStringTimestamp(t *testing.T) {
	now := time.Date(2025, 10, 9, 9, 0, 0, 0, time.UTC)
	want := time.Date(2025, 10, 9, 8, 53, 20, 0, time.UTC)
	for _, ts := range []string{`"2025-10-09T08:53:20Z"`, `"1760000000"`, `1760000000`, `"1760000000.0"`} {
		line := `{"ts":` + ts + `,"request":{"client_ip":"1.2.3.4","method":"GET","uri":"/media/a.png","headers":{"User-Agent":["GPTBot/1.2"]}},"status":200}`
		h, ok := parseLogLineAt([]byte(line), now)
		if !ok || !h.At.Equal(want) {
			t.Errorf("ts %s: ok=%v at=%v want %v", ts, ok, h.At, want)
		}
	}
}
