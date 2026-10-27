package supervisor

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppendEventLinesParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	now := time.Now().UTC()
	for _, e := range []Event{
		{From: "v1.0.0", To: "v1.0.1", Phase: PhaseSuccess, StartedAt: now, FinishedAt: now},
		{From: "bootstrap", To: "v1.0.2", Phase: PhaseRolledBack, Error: "smoke: HTTP 500\nsecond line", StartedAt: now, FinishedAt: now},
	} {
		if err := AppendEvent(path, e); err != nil {
			t.Fatal(err)
		}
	}
	lines := readEvents(t, path)
	if len(lines) != 2 || lines[1].Error != "smoke: HTTP 500\nsecond line" || lines[0].To != "v1.0.1" {
		t.Errorf("events = %+v", lines)
	}
	if !regexp.MustCompile(`^\d+-[0-9a-f]{8}$`).MatchString(lines[0].ID) || lines[0].ID == lines[1].ID {
		t.Errorf("ids = %q %q", lines[0].ID, lines[1].ID)
	}
}

func TestAppendEventKeepsGivenID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := AppendEvent(path, Event{ID: "fixed", Phase: PhaseFailed}); err != nil {
		t.Fatal(err)
	}
	if got := readEvents(t, path); got[0].ID != "fixed" {
		t.Errorf("id = %q", got[0].ID)
	}
}

func TestAppendEventConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	pad := strings.Repeat("x", 3000) // long lines would interleave if writes split
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if err := AppendEvent(path, Event{Phase: PhaseSuccess, Error: pad}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	ids := map[string]bool{}
	for _, e := range readEvents(t, path) {
		ids[e.ID] = true
	}
	if len(ids) != 200 {
		t.Errorf("%d unique events, want 200", len(ids))
	}
}

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("unparseable line %.80q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}
