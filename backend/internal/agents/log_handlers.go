package agents

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	maxLogLineBytes = 1 << 20 // per line
	maxLogBodyBytes = 8 << 20 // whole (decompressed) body
	maxLogLines     = 1000    // well under the writer queue (queueSize)

	logRetryAfter = "30"
)

// LogHandlers serves the access-log ingest endpoint (Cloudflare Logpush /
// Caddy JSON, NDJSON). It is only mounted when log ingest is enabled.
type LogHandlers struct {
	writer Recorder
	secret string
	reg    *Registry
}

// NewLogHandlers creates the log ingest handlers. secret guards the endpoint
// (header X-Agent-Log-Secret) and is independent from the middleware secret.
func NewLogHandlers(writer Recorder, secret string) *LogHandlers {
	return &LogHandlers{writer: writer, secret: secret, reg: Default()}
}

// HandleIngest reads an NDJSON body (optionally gzip), keeps the lines that
// decision 19 assigns to the log source, and records them. Limits are
// all-or-nothing: a body over the byte, line-length or line-count cap is
// rejected with 413 before anything is recorded, so a sender notices instead
// of silently losing a tail. If the writer refuses every kept hit the answer
// is 503 + Retry-After (the sender retries the batch); if it refuses only some,
// the 202 body reports them as "dropped".
func (h *LogHandlers) HandleIngest(w http.ResponseWriter, r *http.Request) {
	if !authorizeHeaderSecret(w, r, "X-Agent-Log-Secret", h.secret) {
		return
	}

	var src io.Reader = http.MaxBytesReader(w, r.Body, maxLogBodyBytes)
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip") {
		zr, err := gzip.NewReader(src)
		if err != nil {
			http.Error(w, "invalid gzip body", http.StatusBadRequest)
			return
		}
		defer zr.Close()
		// The cap applies to the decompressed stream too (gzip bombs).
		src = &capReader{r: zr, left: maxLogBodyBytes}
	}

	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64<<10), maxLogLineBytes)
	var hits []RawHit
	lines, skipped := 0, 0
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		lines++
		if lines > maxLogLines {
			http.Error(w, "too many lines", http.StatusRequestEntityTooLarge)
			return
		}
		// Unknown agents (mostly humans fetching media) are skipped here, so
		// accepted/dropped and the all-refused 503 only count real agent hits.
		if hit, ok := ParseLogLine(sc.Bytes()); ok && h.reg.Match(hit.UA) != nil {
			hits = append(hits, hit)
		} else {
			skipped++
		}
	}
	if err := sc.Err(); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.Is(err, bufio.ErrTooLong), errors.Is(err, errLogBodyTooLarge), errors.As(err, &tooBig):
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		default:
			http.Error(w, "invalid body", http.StatusBadRequest)
		}
		return
	}

	dropped := 0
	for _, hit := range hits {
		if !h.writer.Record(hit) {
			dropped++
		}
	}
	if dropped > 0 && dropped == len(hits) {
		// Nothing was stored: ask Logpush to retry the whole batch.
		w.Header().Set("Retry-After", logRetryAfter)
		http.Error(w, "writer queue full", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]int{"accepted": len(hits) - dropped, "skipped": skipped, "dropped": dropped})
}

var errLogBodyTooLarge = errors.New("log body too large")

// capReader fails with errLogBodyTooLarge once more than left bytes are read.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		// Probe for one more byte so an exactly-at-cap body still succeeds.
		var b [1]byte
		if n, _ := c.r.Read(b[:]); n > 0 {
			return 0, errLogBodyTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}
