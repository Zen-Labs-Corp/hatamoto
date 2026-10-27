package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/supervisor"
)

// maxEventsRead caps how much of events.jsonl is read: the newest lines. Older
// ones were imported at an earlier boot or are not worth a slow start.
const maxEventsRead = 1 << 20

// execer is the one pool method the import needs.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// importUpdateEvents copies the supervisor's switch events into
// hatamoto_updates. It is idempotent (event_id is unique), a missing file is
// not an error and malformed lines are skipped with a warning.
func importUpdateEvents(ctx context.Context, db execer, dataDir string, log *slog.Logger) error {
	events, err := readUpdateEvents(supervisor.NewLayout(dataDir).EventsPath(), log)
	if err != nil {
		return err
	}
	for _, e := range events {
		started := e.StartedAt
		if started.IsZero() {
			started = e.FinishedAt
		}
		if started.IsZero() {
			started = time.Now().UTC()
		}
		var finished *time.Time
		if !e.FinishedAt.IsZero() {
			finished = &e.FinishedAt
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO hatamoto_updates (event_id, from_version, to_version, phase, error, started_at, finished_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (event_id) DO NOTHING`,
			e.ID, e.From, e.To, e.Phase, e.Error, started, finished); err != nil {
			return fmt.Errorf("import update event %s: %w", e.ID, err)
		}
	}
	return nil
}

// updateEventsEvery is how often a running server looks for new events. A
// switch is recorded by the supervisor after its gate passes, which is after
// the new child booted and did its boot-time import, so without this the
// success would wait for the next restart.
const updateEventsEvery = time.Minute

// watchUpdateEvents imports new events every interval until ctx ends. The
// import is idempotent; a failure is logged and retried on the next tick.
func watchUpdateEvents(ctx context.Context, db execer, dataDir string, log *slog.Logger, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := importUpdateEvents(ctx, db, dataDir, log); err != nil && ctx.Err() == nil {
				log.Warn("cannot import update events", "error", err)
			}
		}
	}
}

// readUpdateEvents parses the last maxEventsRead bytes of the file.
func readUpdateEvents(path string, log *slog.Logger) ([]supervisor.Event, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	partial := false
	if fi.Size() > maxEventsRead {
		if _, err := f.Seek(fi.Size()-maxEventsRead, io.SeekStart); err != nil {
			return nil, err
		}
		partial = true // the first line is probably cut in half
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if partial {
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			raw = raw[i+1:]
		} else {
			raw = nil
		}
	}
	var out []supervisor.Event
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), maxEventsRead)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e supervisor.Event
		if err := json.Unmarshal(line, &e); err != nil || e.ID == "" || e.Phase == "" {
			log.Warn("skipping malformed update event line", "path", path)
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
