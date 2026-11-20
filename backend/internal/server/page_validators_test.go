package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/httpx"
)

type validatorClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *validatorClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *validatorClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClockedValidators() (*pageValidators, *validatorClock) {
	c := &validatorClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	return newPageValidators(c.now), c
}

func TestPageValidatorsStampKeepsSinceForSameBytes(t *testing.T) {
	v, c := newClockedValidators()
	c.advance(5 * time.Second)
	first := v.stamp("k", 1)
	c.advance(time.Hour)
	if got := v.stamp("k", 1); !got.Equal(first) {
		t.Fatalf("same bytes moved Last-Modified: %v -> %v", first, got)
	}
}

func TestPageValidatorsStampNewBytes(t *testing.T) {
	v, c := newClockedValidators()
	c.advance(5 * time.Second)
	first := v.stamp("k", 1)
	if second := v.stamp("k", 2); !second.Equal(first.Add(time.Second)) {
		t.Fatalf("same-second change = %v, want %v", second, first.Add(time.Second))
	}
	c.advance(10 * time.Second)
	want := c.now().Truncate(time.Second)
	if third := v.stamp("k", 3); !third.Equal(want) {
		t.Fatalf("later change = %v, want %v", third, want)
	}
}

func TestPageValidatorsFirstStampIsAfterStart(t *testing.T) {
	v, c := newClockedValidators()
	start := c.now()
	since := v.stamp("k", 1)
	if !since.Equal(start.Add(time.Second)) {
		t.Fatalf("since = %v, want start+1s", since)
	}
	hd := http.Header{}
	setValidators(hd, 1, since, v.clock())
	if hd.Get("ETag") == "" || hd.Get("Last-Modified") != "" {
		t.Fatalf("future since must send the ETag only: %v", hd)
	}
	c.advance(time.Second)
	setValidators(hd, 1, since, v.clock())
	if hd.Get("Last-Modified") == "" {
		t.Fatal("Last-Modified missing once the clock caught up")
	}
}

func TestPageValidatorsResetNeverRepeatsASecond(t *testing.T) {
	v, c := newClockedValidators()
	v.max = 2
	c.advance(5 * time.Second)
	latest := v.stamp("a", 1)
	if s := v.stamp("b", 1); s.After(latest) {
		latest = s
	}
	v.stamp("c", 1) // map full: starts over
	if got := v.stamp("a", 2); !got.After(latest) {
		t.Fatalf("after reset %v is not after %v", got, latest)
	}
}

func TestPageETagFormat(t *testing.T) {
	re := regexp.MustCompile(`^W/"[0-9a-f]{16}"$`)
	for _, fp := range []uint64{0, 1, 0xdeadbeefcafef00d, ^uint64(0)} {
		if got := pageETag(fp); !re.MatchString(got) {
			t.Errorf("pageETag(%x) = %q", fp, got)
		}
	}
	if got := pageETag(0xdeadbeefcafef00d); got != `W/"deadbeefcafef00d"` {
		t.Errorf("pageETag = %q", got)
	}
	if got := bareETag(1); got != `"0000000000000001"` {
		t.Errorf("bareETag = %q", got)
	}
}

func TestNotModified(t *testing.T) {
	const fp = 0xabc
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	since := now.Add(-time.Minute)
	tag := pageETag(fp)
	date := func(d time.Duration) string { return since.Add(d).Format(http.TimeFormat) }
	cases := []struct {
		name   string
		method string
		hdr    map[string]string
		since  time.Time
		want   bool
	}{
		{"weak exact", "GET", map[string]string{"If-None-Match": tag}, since, true},
		{"bare", "GET", map[string]string{"If-None-Match": bareETag(fp)}, since, true},
		{"list", "GET", map[string]string{"If-None-Match": `"x", ` + tag}, since, true},
		{"star", "GET", map[string]string{"If-None-Match": "*"}, since, true},
		{"head", "HEAD", map[string]string{"If-None-Match": tag}, since, true},
		{"mismatch", "GET", map[string]string{"If-None-Match": `W/"0000000000000000"`}, since, false},
		{"mismatch beats ims", "GET", map[string]string{"If-None-Match": `"x"`, "If-Modified-Since": date(time.Hour)}, since, false},
		{"ims equal", "GET", map[string]string{"If-Modified-Since": date(0)}, since, true},
		{"ims newer", "GET", map[string]string{"If-Modified-Since": date(time.Hour)}, since, true},
		{"ims older", "GET", map[string]string{"If-Modified-Since": date(-time.Second)}, since, false},
		{"ims garbage", "GET", map[string]string{"If-Modified-Since": "yesterday"}, since, false},
		{"post inm", "POST", map[string]string{"If-None-Match": tag}, since, false},
		{"post ims", "POST", map[string]string{"If-Modified-Since": date(0)}, since, false},
		{"future since", "GET", map[string]string{"If-Modified-Since": date(time.Hour)}, now.Add(time.Second), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/", nil)
			for k, val := range tc.hdr {
				r.Header.Set(k, val)
			}
			if got := notModified(r, fp, tc.since, now); got != tc.want {
				t.Fatalf("notModified = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPageValidatorsStampKnownKeyAllocatesNothing(t *testing.T) {
	v, _ := newClockedValidators()
	v.stamp("k", 7)
	if n := testing.AllocsPerRun(100, func() { v.stamp("k", 7) }); n != 0 {
		t.Fatalf("stamp allocates %v per call", n)
	}
}

func TestPageValidatorsConcurrentStamps(t *testing.T) {
	v, _ := newClockedValidators()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				v.stamp([]string{"a", "b", "c"}[i%3], uint64(i%4+g%2))
			}
		}(g)
	}
	wg.Wait()
}

func TestCachedVariantFPUsesGivenFingerprint(t *testing.T) {
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	key := cache.RenderedPageKey("v", "/p")
	plain := []byte(strings.Repeat("page body ", 300))
	fp := fingerprint(plain)
	r := httptest.NewRequest(http.MethodGet, "/p", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	warn := func(string, ...any) {}
	// A variant is only stored while its plain page is cached.
	_ = c.Set(ctx, key, plain, time.Hour)
	gen := c.Generation()

	enc, first := cachedVariantFP(r, c, key, plain, fp, time.Hour, gen, warn)
	if enc != httpx.EncodingGzip || len(first) == 0 {
		t.Fatalf("first call: enc=%q len=%d", enc, len(first))
	}
	stored, _ := c.Get(ctx, cache.EncodedKey(key, "gzip"))
	if len(stored) != fingerprintLen+len(first) || string(stored[:fingerprintLen]) != string(fp) {
		t.Fatal("stored variant is not fingerprint+packed")
	}
	// A second call must serve the stored bytes, not recompress.
	_, second := cachedVariantFP(r, c, key, plain, fp, time.Hour, gen, warn)
	if string(second) != string(first) {
		t.Fatal("second call did not serve the stored variant")
	}
}
