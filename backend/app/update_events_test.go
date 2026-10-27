package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/supervisor"
)

func eventsDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "releases"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "releases", "events.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func evLine(id string) string {
	return fmt.Sprintf(`{"id":%q,"from":"v1.0.0","to":"v1.0.1","phase":"success","error":"","started_at":"2026-10-22T10:00:00Z","finished_at":"2026-10-22T10:00:05Z"}`, id) + "\n"
}

func warnLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestReadUpdateEvents(t *testing.T) {
	log, buf := warnLog()
	dir := eventsDir(t, evLine("a-1")+"not json\n\n"+`{"id":"","phase":"success"}`+"\n"+`{"id":"x","phase":""}`+"\n"+evLine("a-2"))
	got, err := readUpdateEvents(supervisor.NewLayout(dir).EventsPath(), log)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a-1" || got[1].ID != "a-2" || got[0].From != "v1.0.0" || got[0].To != "v1.0.1" {
		t.Fatalf("got %+v", got)
	}
	if n := strings.Count(buf.String(), "malformed"); n != 3 {
		t.Fatalf("%d warnings, want 3:\n%s", n, buf.String())
	}
}

func TestReadUpdateEventsMissingFileIsNoop(t *testing.T) {
	log, _ := warnLog()
	got, err := readUpdateEvents(filepath.Join(t.TempDir(), "releases", "events.jsonl"), log)
	if err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestReadUpdateEventsTailCap(t *testing.T) {
	log, buf := warnLog()
	var b strings.Builder
	b.WriteString(evLine("old-first"))
	for b.Len() < maxEventsRead+4096 { // pushes the first lines out of the window
		b.WriteString(evLine(fmt.Sprintf("filler-%d", b.Len())))
	}
	b.WriteString(evLine("newest"))
	dir := eventsDir(t, b.String())
	got, err := readUpdateEvents(supervisor.NewLayout(dir).EventsPath(), log)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1].ID != "newest" {
		t.Fatalf("newest line missing, %d events", len(got))
	}
	for _, e := range got {
		if e.ID == "old-first" {
			t.Fatal("line outside the 1 MiB tail was read")
		}
	}
	if strings.Contains(buf.String(), "malformed") {
		t.Fatalf("the cut first line was parsed instead of dropped:\n%s", buf.String())
	}
}

type fakeExec struct {
	rows map[string][]any
	fail error
}

func (f *fakeExec) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if f.fail != nil {
		return pgconn.CommandTag{}, f.fail
	}
	if !strings.Contains(sql, "ON CONFLICT (event_id) DO NOTHING") {
		panic("insert must be idempotent")
	}
	id := args[0].(string)
	if _, dup := f.rows[id]; !dup {
		f.rows[id] = args
	}
	return pgconn.CommandTag{}, nil
}

func TestImportUpdateEventsMapsFieldsAndIsIdempotent(t *testing.T) {
	log, _ := warnLog()
	dir := eventsDir(t, evLine("e-1")+evLine("e-2")+
		`{"id":"e-3","from":"bootstrap","to":"v1.0.2","phase":"rolled_back","error":"smoke: / returned 500","started_at":"0001-01-01T00:00:00Z","finished_at":"0001-01-01T00:00:00Z"}`+"\n")
	fx := &fakeExec{rows: map[string][]any{}}
	for range 2 {
		if err := importUpdateEvents(context.Background(), fx, dir, log); err != nil {
			t.Fatal(err)
		}
	}
	if len(fx.rows) != 3 {
		t.Fatalf("rows: %d", len(fx.rows))
	}
	r := fx.rows["e-1"]
	if r[1] != "v1.0.0" || r[2] != "v1.0.1" || r[3] != "success" || r[4] != "" {
		t.Fatalf("args %v", r)
	}
	if ts := r[5].(time.Time); ts.Year() != 2026 || r[6].(*time.Time) == nil {
		t.Fatalf("times %v %v", r[5], r[6])
	}
	r = fx.rows["e-3"]
	if r[3] != "rolled_back" || r[4] != "smoke: / returned 500" || r[6].(*time.Time) != nil || r[5].(time.Time).IsZero() {
		t.Fatalf("zero times not handled: %v", r)
	}
}

func TestImportUpdateEventsMissingFileAndDBError(t *testing.T) {
	log, _ := warnLog()
	fx := &fakeExec{rows: map[string][]any{}}
	if err := importUpdateEvents(context.Background(), fx, t.TempDir(), log); err != nil || len(fx.rows) != 0 {
		t.Fatalf("missing file: %v %v", err, fx.rows)
	}
	bad := &fakeExec{fail: fmt.Errorf("relation does not exist")}
	if err := importUpdateEvents(context.Background(), bad, eventsDir(t, evLine("z")), log); err == nil {
		t.Fatal("db error swallowed")
	}
}

// A success the supervisor writes after the child booted reaches the table
// without a restart.
func TestWatchUpdateEventsImportsLaterEvents(t *testing.T) {
	log, _ := warnLog()
	dir := eventsDir(t, evLine("boot-time"))
	fx := &syncExec{fakeExec{rows: map[string][]any{}}}
	if err := importUpdateEvents(context.Background(), fx, dir, log); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { watchUpdateEvents(ctx, fx, dir, log, 10*time.Millisecond); close(done) }()
	f, err := os.OpenFile(filepath.Join(dir, "releases", "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(evLine("after-gate"))
	f.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !fx.has("after-gate") {
		if time.Now().After(deadline) {
			t.Fatal("event written after boot was never imported")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

type syncExec struct{ fakeExec }

var execMu sync.Mutex

func (s *syncExec) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	execMu.Lock()
	defer execMu.Unlock()
	return s.fakeExec.Exec(ctx, sql, args...)
}

func (s *syncExec) has(id string) bool {
	execMu.Lock()
	defer execMu.Unlock()
	_, ok := s.rows[id]
	return ok
}
