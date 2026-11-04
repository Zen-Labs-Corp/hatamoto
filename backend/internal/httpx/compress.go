package httpx

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// CompressMinSize is the body size below which compressing costs more than it
// saves. Smaller bodies, and streams flushed before they reach it, go out plain.
const CompressMinSize = 1024

// Compress compresses text responses with zstd or gzip, whichever the client
// prefers (see NegotiateEncoding). enabled is read per request, so the switch
// can change while the server runs.
//
// The decision is made at the first body write, when the handler's headers are
// final: only compressible content types are touched, never a response that
// already carries a Content-Encoding, a Range request, a partial or bodiless
// status, or a HEAD request. The body is held until it reaches CompressMinSize;
// a handler that flushes earlier (a stream) is passed through plain. Vary:
// Accept-Encoding is added to every compressible response, compressed or not.
func Compress(enabled func() bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled() || r.Header.Get("Range") != "" {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressWriter{ResponseWriter: w, enc: NegotiateEncoding(r), head: r.Method == http.MethodHead}
			defer cw.finish()
			next.ServeHTTP(cw, r)
		})
	}
}

type compressWriter struct {
	http.ResponseWriter
	enc    Encoding
	head   bool
	status int  // 0 until the handler calls WriteHeader
	state  int  // cwPending, cwPlain or cwCompress
	typed  bool // eligibility (content type etc.) has been evaluated
	ok     bool // eligible for compression
	buf    []byte
	zw     io.WriteCloser
	free   func()
}

const (
	cwPending = iota
	cwPlain
	cwCompress
)

func (w *compressWriter) WriteHeader(code int) {
	if w.state != cwPending || w.status != 0 {
		return
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(code) // informational, the final status follows
		return
	}
	w.status = code
	if bodiless(code) || w.head {
		w.decideNoBody()
	}
}

// decideNoBody settles the response when no body will be written (204, 304,
// HEAD): the headers of the matching GET are kept where they are knowable.
func (w *compressWriter) decideNoBody() {
	h := w.Header()
	if !bodiless(w.status) && w.eligible(h.Get("Content-Type")) {
		n, err := strconv.Atoi(h.Get("Content-Length"))
		if err == nil && n >= CompressMinSize && w.enc != EncodingNone {
			w.markCompressed()
		}
	}
	w.state = cwPlain
	w.ResponseWriter.WriteHeader(w.statusOrOK())
}

func (w *compressWriter) statusOrOK() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func bodiless(code int) bool {
	return code == http.StatusNoContent || code == http.StatusNotModified || code == http.StatusPartialContent || code == http.StatusSwitchingProtocols
}

// eligible reports whether the response may be compressed, and adds Vary when
// the type is compressible. Called once, with the handler's headers final.
func (w *compressWriter) eligible(contentType string) bool {
	if w.typed {
		return w.ok
	}
	w.typed = true
	h := w.Header()
	if h.Get("Content-Range") != "" || !compressibleType(contentType) {
		return false
	}
	addVary(h, "Accept-Encoding")
	w.ok = h.Get("Content-Encoding") == "" && w.enc != EncodingNone
	return w.ok
}

func (w *compressWriter) markCompressed() {
	h := w.Header()
	h.Del("Content-Length")
	h.Del("Accept-Ranges")
	h.Set("Content-Encoding", string(w.enc))
	if et := h.Get("ETag"); strings.HasPrefix(et, `"`) {
		h.Set("ETag", "W/"+et)
	}
}

func (w *compressWriter) Write(p []byte) (int, error) {
	switch w.state {
	case cwPlain:
		return w.ResponseWriter.Write(p)
	case cwCompress:
		return w.zw.Write(p)
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.typed {
		h := w.Header()
		ct := h.Get("Content-Type")
		if ct == "" {
			ct = http.DetectContentType(p)
			h.Set("Content-Type", ct)
		}
		w.eligible(ct)
	}
	if !w.ok {
		w.state = cwPlain
		w.ResponseWriter.WriteHeader(w.status)
		return w.ResponseWriter.Write(p)
	}
	if n, err := strconv.Atoi(w.Header().Get("Content-Length")); err == nil && n < CompressMinSize {
		w.state = cwPlain
		w.ResponseWriter.WriteHeader(w.status)
		return w.ResponseWriter.Write(p)
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) >= CompressMinSize {
		if err := w.startCompress(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// startCompress commits to compression and feeds the held bytes through it.
func (w *compressWriter) startCompress() error {
	w.markCompressed()
	w.state = cwCompress
	w.ResponseWriter.WriteHeader(w.status)
	w.zw, w.free = newStreamWriter(w.enc, w.ResponseWriter)
	b := w.buf
	w.buf = nil
	_, err := w.zw.Write(b)
	return err
}

// passThrough releases a held body uncompressed.
func (w *compressWriter) passThrough() {
	w.state = cwPlain
	w.ResponseWriter.WriteHeader(w.statusOrOK())
	if len(w.buf) > 0 {
		_, _ = w.ResponseWriter.Write(w.buf)
		w.buf = nil
	}
}

// Flush sends what is buffered. A handler that flushes before the threshold is
// streaming, so the response stays plain and unbuffered from here on.
func (w *compressWriter) Flush() {
	switch w.state {
	case cwPending:
		if !w.typed && w.status == 0 && len(w.buf) == 0 {
			// Flush before any header or body: nothing to decide on yet.
			w.state = cwPlain
			w.ResponseWriter.WriteHeader(http.StatusOK)
		} else {
			w.passThrough()
		}
	case cwCompress:
		if f, ok := w.zw.(interface{ Flush() error }); ok {
			_ = f.Flush()
		}
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finish runs after the handler returns: it drains the held body or closes the
// encoder.
func (w *compressWriter) finish() {
	switch w.state {
	case cwCompress:
		_ = w.zw.Close()
		w.free()
	case cwPending:
		if w.status == 0 && len(w.buf) == 0 {
			return // the handler wrote nothing; net/http sends the implicit 200
		}
		w.passThrough()
	}
}

// Unwrap lets http.ResponseController reach the real writer (deadlines, Hijack).
func (w *compressWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// compressibleType reports whether a Content-Type is text-like. Images (except
// SVG), fonts, audio, video and archives are already compressed or do not
// shrink; text/event-stream is a stream.
func compressibleType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	switch {
	case mt == "text/event-stream":
		return false
	case strings.HasPrefix(mt, "text/"):
		return true
	case strings.HasSuffix(mt, "+json"), strings.HasSuffix(mt, "+xml"):
		return true
	}
	switch mt {
	case "application/json", "application/xml", "application/javascript", "application/x-javascript",
		"application/ecmascript", "application/manifest+json", "image/svg+xml":
		return true
	}
	return false
}

// addVary adds a token to Vary unless it is already listed (or Vary is "*").
func addVary(h http.Header, token string) {
	for _, v := range h.Values("Vary") {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t == "*" || strings.EqualFold(t, token) {
				return
			}
		}
	}
	h.Add("Vary", token)
}
