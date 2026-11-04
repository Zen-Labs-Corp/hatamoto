package httpx

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// Encoding is a response content coding Hatamoto can produce.
type Encoding string

const (
	// EncodingNone means the response goes out as is.
	EncodingNone Encoding = ""
	// EncodingZstd is Zstandard (Content-Encoding: zstd).
	EncodingZstd Encoding = "zstd"
	// EncodingGzip is gzip (Content-Encoding: gzip).
	EncodingGzip Encoding = "gzip"
)

// NegotiateEncoding picks the response coding from Accept-Encoding: zstd when
// the client accepts it, else gzip, else none. A coding with q=0 is refused;
// "*" stands for every coding not named. A missing header or "identity" yields
// no coding.
func NegotiateEncoding(r *http.Request) Encoding {
	var zstdQ, gzipQ, starQ float64 = -1, -1, -1
	for _, h := range r.Header.Values("Accept-Encoding") {
		for _, part := range strings.Split(h, ",") {
			name, params, _ := strings.Cut(part, ";")
			name = strings.ToLower(strings.TrimSpace(name))
			q := 1.0
			if p := strings.TrimSpace(params); p != "" {
				if v, ok := strings.CutPrefix(strings.ToLower(p), "q="); ok {
					f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
					if err != nil {
						continue
					}
					q = f
				}
			}
			switch name {
			case "zstd":
				zstdQ = q
			case "gzip", "x-gzip":
				gzipQ = q
			case "*":
				starQ = q
			}
		}
	}
	if zstdQ < 0 {
		zstdQ = starQ
	}
	if gzipQ < 0 {
		gzipQ = starQ
	}
	switch {
	case zstdQ > 0:
		return EncodingZstd
	case gzipQ > 0:
		return EncodingGzip
	}
	return EncodingNone
}

var (
	zstdFast   = mustZstd(zstd.SpeedDefault)
	zstdBetter = mustZstd(zstd.SpeedBetterCompression)
)

func mustZstd(level zstd.EncoderLevel) *zstd.Encoder {
	e, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1))
	if err != nil {
		panic("httpx: zstd encoder: " + err.Error())
	}
	return e
}

// CompressBytes compresses a whole body. cached selects the slower, smaller
// levels used for bodies that are compressed once and served many times.
func CompressBytes(enc Encoding, b []byte, cached bool) ([]byte, error) {
	switch enc {
	case EncodingZstd:
		e := zstdFast
		if cached {
			e = zstdBetter
		}
		return e.EncodeAll(b, make([]byte, 0, len(b)/3+64)), nil
	case EncodingGzip:
		level := 5
		if cached {
			level = gzip.BestCompression
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, level)
		if err != nil {
			return nil, err
		}
		if _, err := zw.Write(b); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return b, nil
}

var (
	gzipPool = sync.Pool{New: func() any {
		w, _ := gzip.NewWriterLevel(nil, 5)
		return w
	}}
	zstdPool = sync.Pool{New: func() any { return mustZstd(zstd.SpeedDefault) }}
)

// newStreamWriter returns a compressing writer over w for the streaming path
// and a release func that returns the encoder to its pool; call release after
// Close.
func newStreamWriter(enc Encoding, w io.Writer) (io.WriteCloser, func()) {
	switch enc {
	case EncodingZstd:
		e := zstdPool.Get().(*zstd.Encoder)
		e.Reset(w)
		return e, func() { e.Reset(nil); zstdPool.Put(e) }
	case EncodingGzip:
		g := gzipPool.Get().(*gzip.Writer)
		g.Reset(w)
		return g, func() { g.Reset(nil); gzipPool.Put(g) }
	}
	return nopWriteCloser{w}, func() {}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
