package budgetfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeT captures Errorf/Logf; everything else comes from the embedded TB.
type fakeT struct {
	testing.TB
	errs, logs []string
}

func (f *fakeT) Helper() {}
func (f *fakeT) Errorf(format string, a ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, a...))
}
func (f *fakeT) Logf(format string, a ...any) { f.logs = append(f.logs, fmt.Sprintf(format, a...)) }
func (f *fakeT) Log(a ...any)                 { f.logs = append(f.logs, fmt.Sprint(a...)) }

const seed = `{
  "metrics": {
    "js":    {"value": 116736, "unit": "bytes", "cap": 122880, "tol_abs": 1024},
    "css":   {"value": 2662, "unit": "bytes", "tol_abs": 1024},
    "pct":   {"value": 1000, "unit": "bytes", "tol_pct": 5},
    "capped": {"value": 1000, "unit": "bytes", "tol_abs": 5000, "cap": 2000}
  },
  "history": []
}
`

func setup(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.json")
	if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func run(path, key string, got int64, update bool, reason string) *fakeT {
	f := &fakeT{}
	check(f, path, "x", key, got, Metric{Unit: "bytes", TolAbs: 7}, update, reason)
	return f
}

func TestCheckStatuses(t *testing.T) {
	p := setup(t)
	cases := []struct {
		key    string
		got    int64
		fail   bool
		status string
	}{
		{"pct", 900, false, "under"},
		{"pct", 1000, false, "ok"},
		{"pct", 1050, false, "ok"},
		{"pct", 1051, true, "FAIL"},
		{"css", 2662 + 1024, false, "ok"},
		{"css", 2662 + 1025, true, "FAIL"},
		{"capped", 2000, false, "ok"},
		{"capped", 2001, true, "FAIL"},         // within tolerance, over the cap
		{"js", 116736 + 10*1024, true, "FAIL"}, // ISC-3
		{"css", 2662 + 2048 + 1, true, "FAIL"}, // ISC-4
	}
	for _, c := range cases {
		f := run(p, c.key, c.got, false, "")
		if (len(f.errs) > 0) != c.fail {
			t.Errorf("%s got=%d: errs=%v, want fail=%v", c.key, c.got, f.errs, c.fail)
		}
		out := strings.Join(append(f.errs, f.logs...), "\n")
		if !strings.Contains(out, "status="+c.status) || !strings.Contains(out, "BUDGET x/"+c.key) {
			t.Errorf("%s got=%d: output %q lacks status=%s", c.key, c.got, out, c.status)
		}
	}
}

func TestCheckMissingKeyFails(t *testing.T) {
	f := run(setup(t), "nope", 1, false, "")
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "make budgets-update") {
		t.Fatalf("errs = %v", f.errs)
	}
}

func TestPlainModeLeavesFileUntouched(t *testing.T) {
	p := setup(t)
	run(p, "css", 1, false, "")
	run(p, "css", 99999, false, "")
	run(p, "nope", 1, false, "")
	b, _ := os.ReadFile(p)
	if string(b) != seed {
		t.Fatalf("file changed in plain mode:\n%s", b)
	}
}

func TestUpdateRequiresReason(t *testing.T) {
	p := setup(t)
	f := run(p, "css", 1, true, "")
	if len(f.errs) != 1 {
		t.Fatalf("errs = %v", f.errs)
	}
	if b, _ := os.ReadFile(p); string(b) != seed {
		t.Fatal("file written without a reason")
	}
}

func TestUpdateRewritesValueAndHistory(t *testing.T) {
	p := setup(t)
	stamped = map[string]bool{}
	if f := run(p, "css", 3000, true, "B10 adds X"); len(f.errs) != 0 {
		t.Fatal(f.errs)
	}
	run(p, "fresh", 42, true, "B10 adds X") // new key takes the defaults
	fd, err := load(p)
	if err != nil {
		t.Fatal(err)
	}
	if m := fd.Metrics["css"]; m.Value != 3000 || m.TolAbs != 1024 || m.Unit != "bytes" {
		t.Errorf("css = %+v", m)
	}
	if m := fd.Metrics["fresh"]; m.Value != 42 || m.TolAbs != 7 {
		t.Errorf("fresh = %+v", m)
	}
	if fd.Metrics["js"].Cap != 122880 {
		t.Errorf("cap lost: %+v", fd.Metrics["js"])
	}
	if len(fd.History) != 1 || fd.History[0].Reason != "B10 adds X" || fd.History[0].Date == "" {
		t.Errorf("history = %+v", fd.History)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasSuffix(string(b), "}\n") || !strings.Contains(string(b), "\n  \"metrics\"") {
		t.Errorf("format:\n%s", b)
	}
}

func TestHistoryKeepsLast20(t *testing.T) {
	p := setup(t)
	for i := 0; i < 25; i++ {
		stamped = map[string]bool{}
		run(p, "css", int64(i), true, fmt.Sprintf("r%d", i))
	}
	fd, _ := load(p)
	if len(fd.History) != 20 || fd.History[0].Reason != "r5" || fd.History[19].Reason != "r24" {
		t.Fatalf("history = %+v", fd.History)
	}
}
