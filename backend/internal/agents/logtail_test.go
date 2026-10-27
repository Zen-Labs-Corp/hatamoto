package agents

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

type tailRecorder struct {
	mu   sync.Mutex
	hits []RawHit
}

func (r *tailRecorder) Record(h RawHit) bool {
	r.mu.Lock()
	r.hits = append(r.hits, h)
	r.mu.Unlock()
	return true
}

func (r *tailRecorder) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, h := range r.hits {
		out = append(out, h.Path)
	}
	return out
}

func caddyLineFor(path string, status int) string {
	return `{"ts":` + strconv.FormatInt(time.Now().Unix(), 10) + `,"request":{"client_ip":"1.2.3.4","method":"GET","uri":"` + path +
		`","headers":{"User-Agent":["GPTBot/1.2"]}},"status":` + itoa(status) + `,"size":1,"duration":0.01}` + "\n"
}

func itoa(n int) string {
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func startTestTailer(t *testing.T, path string) (*tailRecorder, context.CancelFunc, *Tailer) {
	t.Helper()
	rec := &tailRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	tl := &Tailer{path: path, rec: rec, logger: discard, poll: 10 * time.Millisecond}
	tl.start(ctx)
	t.Cleanup(func() { cancel(); tl.Wait() })
	return rec, cancel, tl
}

func waitPaths(t *testing.T, rec *tailRecorder, want ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := rec.paths()
		if len(got) == len(want) {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("got %v want %v", got, want)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout: got %v want %v", rec.paths(), want)
}

func TestTailerAppendStartsAtEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, caddyLineFor("/media/old.png", 200)) // before start: skipped
	rec, _, _ := startTestTailer(t, path)
	time.Sleep(60 * time.Millisecond)
	appendTo(t, path, caddyLineFor("/media/a.png", 200))
	appendTo(t, path, caddyLineFor("/page", 200)) // filtered by the parser
	appendTo(t, path, caddyLineFor("/page", 502)) // 5xx pages belong to other sources
	appendTo(t, path, caddyLineFor("/media/b.png", 200))
	waitPaths(t, rec, "/media/a.png", "/media/b.png")
}

func TestTailerPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	rec, _, _ := startTestTailer(t, path)
	time.Sleep(60 * time.Millisecond)
	line := caddyLineFor("/media/p.png", 200)
	appendTo(t, path, line[:30])
	time.Sleep(60 * time.Millisecond)
	if len(rec.paths()) != 0 {
		t.Fatal("partial line must wait for its newline")
	}
	appendTo(t, path, line[30:])
	waitPaths(t, rec, "/media/p.png")
}

func TestTailerRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	appendTo(t, path, "")
	rec, _, _ := startTestTailer(t, path)
	time.Sleep(60 * time.Millisecond)
	appendTo(t, path, caddyLineFor("/media/1.png", 200))
	waitPaths(t, rec, "/media/1.png")

	if err := os.Rename(path, filepath.Join(dir, "access.log.1")); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, caddyLineFor("/media/2.png", 200)) // new file, read from its start
	waitPaths(t, rec, "/media/1.png", "/media/2.png")
	appendTo(t, path, caddyLineFor("/media/3.png", 200))
	waitPaths(t, rec, "/media/1.png", "/media/2.png", "/media/3.png")
}

func TestTailerTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	rec, _, _ := startTestTailer(t, path)
	time.Sleep(60 * time.Millisecond)
	appendTo(t, path, caddyLineFor("/media/long-name-aaaaaaaaaaaaaaaaaaaa.png", 200))
	waitPaths(t, rec, "/media/long-name-aaaaaaaaaaaaaaaaaaaa.png")
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, caddyLineFor("/media/b.png", 200))
	waitPaths(t, rec, "/media/long-name-aaaaaaaaaaaaaaaaaaaa.png", "/media/b.png")
}

func TestTailerMissingFileRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "later", "access.log")
	rec, _, _ := startTestTailer(t, path)
	time.Sleep(60 * time.Millisecond) // missing dir: must not panic or exit
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, caddyLineFor("/media/late.png", 200))
	waitPaths(t, rec, "/media/late.png")
}

func TestTailerStopsOnCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	_, cancel, tl := startTestTailer(t, path)
	cancel()
	done := make(chan struct{})
	go func() { tl.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tailer did not stop after cancel")
	}
}
