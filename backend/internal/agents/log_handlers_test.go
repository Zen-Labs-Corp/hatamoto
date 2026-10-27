package agents

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	logMedia = `{"ClientIP":"1.2.3.4","ClientRequestURI":"/media/a.png","ClientRequestMethod":"GET","ClientRequestUserAgent":"GPTBot/1.2","EdgeResponseStatus":200,"EdgeResponseBytes":9}`
	logPage  = `{"ClientIP":"1.2.3.4","ClientRequestURI":"/pricing","ClientRequestMethod":"GET","ClientRequestUserAgent":"GPTBot/1.2","EdgeResponseStatus":200}`
)

func postLogs(h *LogHandlers, secret string, body []byte, gz bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/agent-logs", bytes.NewReader(body))
	if secret != "" {
		req.Header.Set("X-Agent-Log-Secret", secret)
	}
	if gz {
		req.Header.Set("Content-Encoding", "gzip")
	}
	rr := httptest.NewRecorder()
	h.HandleIngest(rr, req)
	return rr
}

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLogIngestAuth(t *testing.T) {
	rec := &fakeRecorder{}
	body := []byte(logMedia)
	if rr := postLogs(NewLogHandlers(rec, ""), "x", body, false); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no secret configured: %d, want 503", rr.Code)
	}
	h := NewLogHandlers(rec, "s3cret")
	if rr := postLogs(h, "", body, false); rr.Code != http.StatusUnauthorized {
		t.Errorf("missing secret: %d", rr.Code)
	}
	if rr := postLogs(h, "wrong", body, false); rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", rr.Code)
	}
	// The middleware secret header must not authorize the log endpoint.
	req := httptest.NewRequest(http.MethodPost, "/agent-logs", bytes.NewReader(body))
	req.Header.Set("X-Bot-Track-Secret", "s3cret")
	rr := httptest.NewRecorder()
	h.HandleIngest(rr, req)
	if rr.Code != http.StatusUnauthorized || len(rec.hits) != 0 {
		t.Errorf("wrong header must be 401 and record nothing: %d %d", rr.Code, len(rec.hits))
	}
}

func TestLogIngestCountsAndSource(t *testing.T) {
	rec := &fakeRecorder{}
	h := NewLogHandlers(rec, "s")
	body := logMedia + "\n\n" + logPage + "\ngarbage\n"
	rr := postLogs(h, "s", []byte(body), false)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code %d: %s", rr.Code, rr.Body)
	}
	var got map[string]int
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got["accepted"] != 1 || got["skipped"] != 2 {
		t.Fatalf("counts %v err %v", got, err)
	}
	if len(rec.hits) != 1 || rec.hits[0].Source != SourceLog || rec.hits[0].Path != "/media/a.png" || rec.hits[0].Status != 200 {
		t.Fatalf("hits %+v", rec.hits)
	}
}

func TestLogIngestGzip(t *testing.T) {
	rec := &fakeRecorder{}
	h := NewLogHandlers(rec, "s")
	rr := postLogs(h, "s", gzipBytes(t, []byte(logMedia+"\n"+logMedia+"\n")), true)
	if rr.Code != http.StatusAccepted || len(rec.hits) != 2 {
		t.Fatalf("gzip: %d hits=%d", rr.Code, len(rec.hits))
	}
	if rr := postLogs(h, "s", []byte("not gzip"), true); rr.Code != http.StatusBadRequest {
		t.Errorf("bad gzip: %d", rr.Code)
	}
}

func TestLogIngestLimits(t *testing.T) {
	rec := &fakeRecorder{}
	h := NewLogHandlers(rec, "s")

	long := `{"ClientRequestURI":"/media/` + strings.Repeat("a", maxLogLineBytes) + `"}`
	if rr := postLogs(h, "s", []byte(logMedia+"\n"+long), false); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("long line: %d", rr.Code)
	}
	many := strings.Repeat(logMedia+"\n", maxLogLines+1)
	if rr := postLogs(h, "s", []byte(many), false); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too many lines: %d", rr.Code)
	}
	atCap := strings.Repeat(logMedia+"\n", maxLogLines)
	if rr := postLogs(h, "s", []byte(atCap), false); rr.Code != http.StatusAccepted {
		t.Errorf("exactly max lines: %d", rr.Code)
	}
	rec.hits = nil
	// Oversize plain body: 8 MiB+ of blank lines (not counted) hits the byte cap.
	big := bytes.Repeat([]byte(" \n"), maxLogBodyBytes/2+10)
	if rr := postLogs(h, "s", big, false); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize body: %d", rr.Code)
	}
	// Gzip bomb: tiny on the wire, over the cap once decompressed.
	if rr := postLogs(h, "s", gzipBytes(t, big), true); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize gzip body: %d", rr.Code)
	}
	if len(rec.hits) != 0 {
		t.Errorf("rejected bodies must record nothing, got %d", len(rec.hits))
	}
}

func TestLogIngestReportsWriterRefusals(t *testing.T) {
	body := []byte(strings.Repeat(logMedia+"\n", 3))

	// Nothing enqueued: 503 so Logpush retries the whole batch.
	rec := &fakeRecorder{capped: true}
	rr := postLogs(NewLogHandlers(rec, "s"), "s", body, false)
	if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("all refused: %d retry-after=%q", rr.Code, rr.Header().Get("Retry-After"))
	}

	// Partially enqueued: 202 and the refused hits are reported, not hidden.
	rec = &fakeRecorder{capped: true, capacity: 2}
	rr = postLogs(NewLogHandlers(rec, "s"), "s", body, false)
	var got map[string]int
	if rr.Code != http.StatusAccepted || json.Unmarshal(rr.Body.Bytes(), &got) != nil ||
		got["accepted"] != 2 || got["dropped"] != 1 {
		t.Fatalf("partial: %d %s", rr.Code, rr.Body)
	}
}

// Human media fetches in a batch must not mask a full queue: they are skipped,
// so a batch whose agent hits were all refused still gets 503 (Logpush retries).
func TestLogIngestHumanLinesDoNotMaskRefusal(t *testing.T) {
	human := strings.Replace(logMedia, "GPTBot", "Mozilla/5.0 (Windows NT 10.0) Chrome/120", 1)
	if human == logMedia {
		t.Fatal("fixture logMedia has no GPTBot UA to replace")
	}
	body := []byte(logMedia + "\n" + human + "\n" + human + "\n")
	// Like the real Writer: unknown agents are ignored (true), known agents are
	// refused because the queue is full.
	rec := fullQueueRecorder{}
	rr := postLogs(NewLogHandlers(rec, "s"), "s", body, false)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("mixed batch, agent hit refused: %d %s", rr.Code, rr.Body)
	}
}

// fullQueueRecorder mimics Writer.Record with a full queue: unknown agents are
// ignored (true), known agents are refused (false).
type fullQueueRecorder struct{}

func (fullQueueRecorder) Record(h RawHit) bool { return Default().Match(h.UA) == nil }

// One request must never be able to overflow the writer queue on its own.
func TestLogLineCapFitsWriterQueue(t *testing.T) {
	if maxLogLines > queueSize {
		t.Fatalf("maxLogLines %d exceeds writer queueSize %d", maxLogLines, queueSize)
	}
}
