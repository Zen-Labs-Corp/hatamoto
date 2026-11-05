package app

import (
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// TestResumeReadsWhatStoredOptionsWrote guards the --resume path: the options
// document a run stored must give back its url and user (v2 decoding is
// case-sensitive, so the key spelling has to match).
func TestResumeReadsWhatStoredOptionsWrote(t *testing.T) {
	f := &wpFlags{url: "https://blog.example.test", user: "editor", wxr: "/tmp/export.xml"}
	raw, err := jsonx.Marshal(storedOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	got := readStoredImport(raw)
	if got.URL != f.url || got.User != f.user {
		t.Errorf("readStoredImport(%s) = %+v, want url %q user %q", raw, got, f.url, f.user)
	}
}
