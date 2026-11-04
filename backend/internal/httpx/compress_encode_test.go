package httpx

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

func TestNegotiateEncoding(t *testing.T) {
	cases := []struct {
		header string
		want   Encoding
	}{
		{"", EncodingNone},
		{"identity", EncodingNone},
		{"gzip", EncodingGzip},
		{"gzip, deflate, br", EncodingGzip},
		{"zstd, gzip", EncodingZstd},
		{"gzip, zstd", EncodingZstd},
		{"zstd;q=0, gzip", EncodingGzip},
		{"zstd;q=0, gzip;q=0", EncodingNone},
		{"gzip;q=0.5, zstd;q=0.1", EncodingZstd},
		{"*", EncodingZstd},
		{"*;q=0", EncodingNone},
		{"gzip, *;q=0", EncodingGzip},
		{"zstd;q=0, *", EncodingGzip},
		{"GZIP", EncodingGzip},
		{"gzip;q=bogus", EncodingNone},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if c.header != "" {
			r.Header.Set("Accept-Encoding", c.header)
		}
		if got := NegotiateEncoding(r); got != c.want {
			t.Errorf("Accept-Encoding %q: got %q, want %q", c.header, got, c.want)
		}
	}
}

func TestCompressBytesRoundTrip(t *testing.T) {
	body := []byte(strings.Repeat("<p>hello compression</p>\n", 200))
	for _, cached := range []bool{false, true} {
		z, err := CompressBytes(EncodingZstd, body, cached)
		if err != nil || len(z) >= len(body) {
			t.Fatalf("zstd cached=%v: err=%v len=%d", cached, err, len(z))
		}
		zr, _ := zstd.NewReader(nil)
		got, err := zr.DecodeAll(z, nil)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("zstd round trip cached=%v: %v", cached, err)
		}
		g, err := CompressBytes(EncodingGzip, body, cached)
		if err != nil || len(g) >= len(body) {
			t.Fatalf("gzip cached=%v: err=%v len=%d", cached, err, len(g))
		}
		gr, err := gzip.NewReader(bytes.NewReader(g))
		if err != nil {
			t.Fatal(err)
		}
		got, _ = io.ReadAll(gr)
		if !bytes.Equal(got, body) {
			t.Fatalf("gzip round trip cached=%v", cached)
		}
	}
	if out, _ := CompressBytes(EncodingNone, body, false); !bytes.Equal(out, body) {
		t.Fatal("none must pass through")
	}
}

func TestStreamWriterRoundTrip(t *testing.T) {
	body := []byte(strings.Repeat("stream me ", 500))
	for _, enc := range []Encoding{EncodingZstd, EncodingGzip} {
		// Twice: the second pass reuses a pooled encoder.
		for range 2 {
			var buf bytes.Buffer
			w, release := newStreamWriter(enc, &buf)
			_, _ = w.Write(body)
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			release()
			var got []byte
			if enc == EncodingZstd {
				zr, _ := zstd.NewReader(&buf)
				got, _ = io.ReadAll(zr)
			} else {
				gr, err := gzip.NewReader(&buf)
				if err != nil {
					t.Fatal(err)
				}
				got, _ = io.ReadAll(gr)
			}
			if !bytes.Equal(got, body) {
				t.Fatalf("%s stream round trip mismatch", enc)
			}
		}
	}
}
