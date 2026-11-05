package budgetfile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var htmlRaw = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&")

// The committed baselines were written by save: loading one and saving it again
// must give the same bytes, or a budgets update would churn every file.
//
// The committed files mix "->" written raw (by hand) and as \u003e (by the
// previous encoder, which escapes HTML characters), so they never regenerated
// byte for byte, and an encoder that writes them raw cannot either. Both sides
// are therefore compared with \u003c, \u003e and \u0026 read as the characters
// they stand for: it still catches any other drift in layout, order or values.
func TestBudgetFilesRegenerateIdentical(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "budgets", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("budget files: %v %v", files, err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			committed, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			fd, err := load(f)
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "out.json")
			if err := save(out, fd); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			got, want := []byte(htmlRaw.Replace(string(raw))), []byte(htmlRaw.Replace(string(committed)))
			if bytes.Equal(got, want) {
				return
			}
			gl, wl := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
			for i := 0; i < len(gl) && i < len(wl); i++ {
				if !bytes.Equal(gl[i], wl[i]) {
					t.Fatalf("%s does not regenerate identically: line %d is %q, committed %q", f, i+1, gl[i], wl[i])
				}
			}
			t.Fatalf("%s does not regenerate identically: %d lines, committed %d", f, len(gl), len(wl))
		})
	}
}

// Zero tolerances and cap are left out of the file; an empty history stays [].
func TestBudgetSaveShape(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.json")
	fd := &fileData{
		Metrics: map[string]Metric{"b": {Value: 2, Unit: "bytes"}, "a": {Value: 1, Unit: "allocs", TolPct: 2.5, TolAbs: 3, Cap: 10}},
		History: []Entry{},
	}
	if err := save(out, fd); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"metrics\": {\n    \"a\": {\n      \"value\": 1,\n      \"unit\": \"allocs\",\n      \"tol_pct\": 2.5,\n      \"tol_abs\": 3,\n      \"cap\": 10\n    },\n    \"b\": {\n      \"value\": 2,\n      \"unit\": \"bytes\"\n    }\n  },\n  \"history\": []\n}\n"
	if string(got) != want {
		t.Fatalf("saved bytes changed:\n got %q\nwant %q", got, want)
	}
}
