// Package budgetfile compares measured sizes against a committed baseline.
//
// Baselines live in testdata/budgets/<file>.json (one file per package, so
// parallel changes do not collide). A plain `go test` only reads them; they are
// rewritten solely when HM_BUDGETS_UPDATE=1 (see `make budgets-update`). The
// package is a leaf: standard library only.
package budgetfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Metric is one baseline entry.
type Metric struct {
	Value  int64   `json:"value"`
	Unit   string  `json:"unit"` // bytes | allocs
	TolPct float64 `json:"tol_pct,omitempty"`
	TolAbs int64   `json:"tol_abs,omitempty"`
	Cap    int64   `json:"cap,omitempty"` // absolute hard limit, 0 = none
}

// Entry is one line of a file's update history.
type Entry struct {
	Reason string `json:"reason"`
	Date   string `json:"date"`
}

type fileData struct {
	Metrics map[string]Metric `json:"metrics"`
	History []Entry           `json:"history"`
}

const historyKeep = 20

var (
	mu      sync.Mutex
	stamped = map[string]bool{} // files that already got a history line in this process
)

// Check fails t when got exceeds baseline+tolerance or the cap, or when key is
// missing from the file. It always logs
//
//	BUDGET <file>/<key> got=N baseline=N limit=N unit=U status=ok|FAIL|under
//
// With HM_BUDGETS_UPDATE=1 it instead rewrites the metric's value (keeping
// Tol*/Cap; a new key is added with def), requires HM_BUDGETS_REASON, appends
// a history line and never fails on size.
func Check(t testing.TB, file, key string, got int64, def Metric) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "budgets", file+".json")
	check(t, path, file, key, got, def, os.Getenv("HM_BUDGETS_UPDATE") == "1", os.Getenv("HM_BUDGETS_REASON"))
}

func check(t testing.TB, path, file, key string, got int64, def Metric, update bool, reason string) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()

	fd, err := load(path)
	if err != nil {
		t.Errorf("budget %s/%s: %v", file, key, err)
		return
	}
	if update {
		if reason == "" {
			t.Errorf("budget %s/%s: HM_BUDGETS_REASON is required to update baselines; nothing written", file, key)
			return
		}
		m, ok := fd.Metrics[key]
		if !ok {
			m = def
		}
		m.Value = got
		fd.Metrics[key] = m
		if !stamped[path] {
			stamped[path] = true
			fd.History = append(fd.History, Entry{Reason: reason, Date: time.Now().UTC().Format("2006-01-02")})
			if n := len(fd.History); n > historyKeep {
				fd.History = fd.History[n-historyKeep:]
			}
		}
		if err := save(path, fd); err != nil {
			t.Errorf("budget %s/%s: %v", file, key, err)
			return
		}
		t.Logf("BUDGET %s/%s got=%d baseline=%d limit=%d unit=%s status=updated", file, key, got, got, limit(m), m.Unit)
		return
	}

	m, ok := fd.Metrics[key]
	if !ok {
		t.Errorf("BUDGET %s/%s got=%d status=FAIL: no baseline; add it with make budgets-update REASON=\"...\"", file, key, got)
		return
	}
	lim := limit(m)
	status := "ok"
	switch {
	case got > lim:
		status = "FAIL"
	case m.Cap > 0 && got > m.Cap:
		status = "FAIL"
	case got < m.Value-tolerance(m):
		status = "under"
	}
	line := fmt.Sprintf("BUDGET %s/%s got=%d baseline=%d limit=%d unit=%s status=%s", file, key, got, m.Value, lim, m.Unit, status)
	if status == "FAIL" {
		t.Errorf("%s (cap=%d); if the growth is intended run make budgets-update REASON=\"...\"", line, m.Cap)
		return
	}
	t.Log(line)
}

func tolerance(m Metric) int64 {
	tol := int64(float64(m.Value) * m.TolPct / 100)
	if m.TolAbs > tol {
		tol = m.TolAbs
	}
	return tol
}

// limit is the highest accepted value: baseline plus tolerance, never above Cap.
func limit(m Metric) int64 {
	l := m.Value + tolerance(m)
	if m.Cap > 0 && l > m.Cap {
		l = m.Cap
	}
	return l
}

func load(path string) (*fileData, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fd fileData
	if err := json.Unmarshal(b, &fd); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if fd.Metrics == nil {
		fd.Metrics = map[string]Metric{}
	}
	if fd.History == nil {
		fd.History = []Entry{}
	}
	return &fd, nil
}

// save writes sorted keys (encoding/json sorts map keys), 2-space indent and a
// trailing newline.
func save(path string, fd *fileData) error {
	b, err := json.MarshalIndent(fd, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
