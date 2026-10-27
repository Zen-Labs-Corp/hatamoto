package agents

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

const tailPollInterval = time.Second

// Tailer follows a Caddy JSON access log and records the lines ParseLogLine
// keeps. It is strictly read-only on the file and never touches Caddy config.
// There is no offset persistence: a restart resumes at the end of the file, so
// lines written while the backend was down are not ingested.
type Tailer struct {
	path   string
	rec    Recorder
	logger *slog.Logger
	poll   time.Duration

	wg sync.WaitGroup
}

// StartTailer starts following path until ctx is cancelled. The file does not
// need to exist yet (it is retried every poll). Call Wait after cancelling ctx
// and before closing the writer.
func StartTailer(ctx context.Context, path string, rec Recorder, logger *slog.Logger) *Tailer {
	t := &Tailer{path: path, rec: rec, logger: logger, poll: tailPollInterval}
	t.start(ctx)
	return t
}

func (t *Tailer) start(ctx context.Context) {
	t.wg.Go(func() {
		t.run(ctx)
	})
}

// Wait blocks until the tail goroutine has exited.
func (t *Tailer) Wait() { t.wg.Wait() }

func (t *Tailer) run(ctx context.Context) {
	var (
		f       *os.File
		rd      *bufio.Reader
		partial []byte
		offset  int64
		warned  bool
		first   = true // only the very first open attempt starts at EOF
	)
	closeFile := func() {
		if f != nil {
			f.Close()
			f, rd, partial = nil, nil, nil
		}
	}
	defer closeFile()

	ticker := time.NewTicker(t.poll)
	defer ticker.Stop()
	for {
		if f == nil {
			nf, err := os.Open(t.path)
			if err != nil {
				if !warned {
					t.logger.Warn("agent log tail: cannot open file, will retry",
						slog.String("path", t.path), slog.String("error", err.Error()))
					warned = true
				}
			} else {
				warned = false
				f, rd, offset = nf, bufio.NewReaderSize(nf, 64<<10), 0
				if first {
					if end, err := f.Seek(0, io.SeekEnd); err == nil {
						offset = end
					}
				}
				t.logger.Info("agent log tail: following", slog.String("path", t.path))
			}
		}
		first = false
		if f != nil {
			offset, partial = t.drain(f, rd, offset, partial)
			f, rd, partial = t.rotated(f, rd, partial, &offset)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// drain reads every complete line currently available and returns the new
// offset and any trailing partial line.
func (t *Tailer) drain(f *os.File, rd *bufio.Reader, offset int64, partial []byte) (int64, []byte) {
	for {
		chunk, err := rd.ReadBytes('\n')
		offset += int64(len(chunk))
		partial = append(partial, chunk...)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.logger.Warn("agent log tail: read error", slog.String("error", err.Error()))
			}
			if len(partial) > maxLogLineBytes {
				partial = nil // runaway line without newline: drop it
			}
			return offset, partial
		}
		if hit, ok := ParseLogLine(partial); ok {
			t.rec.Record(hit)
		}
		partial = partial[:0]
	}
}

// rotated handles truncation (size below our offset: restart at the top) and
// replacement (path now points at a different file: the old one is already
// drained, so close it and let the loop reopen from the start).
func (t *Tailer) rotated(f *os.File, rd *bufio.Reader, partial []byte, offset *int64) (*os.File, *bufio.Reader, []byte) {
	cur, err := os.Stat(t.path)
	if err != nil {
		return f, rd, partial // path vanished mid-rotation: keep draining the old handle until it reappears
	}
	old, err := f.Stat()
	if err != nil {
		return f, rd, partial
	}
	if !os.SameFile(cur, old) {
		f.Close()
		return nil, nil, nil
	}
	if old.Size() < *offset {
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			rd.Reset(f)
			*offset = 0
			return f, rd, nil
		}
	}
	return f, rd, partial
}
