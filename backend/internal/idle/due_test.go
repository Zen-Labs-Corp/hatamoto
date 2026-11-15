package idle

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDueLookupErrorIsDueNowAndLoggedOnce(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	var d DueLookup
	now := time.Unix(100, 0)
	later := now.Add(time.Hour)

	for range 3 {
		got, ok := d.Result(nil, errors.New("db down"), now, log, "x")
		if !ok || !got.Equal(now) {
			t.Fatalf("error = %v, %v; want due now", got, ok)
		}
	}
	if n := strings.Count(buf.String(), "could not read"); n != 1 {
		t.Fatalf("logged %d times, want once per run of failures", n)
	}
	if got, ok := d.Result(&later, nil, now, log, "x"); !ok || !got.Equal(later) {
		t.Fatalf("success = %v, %v", got, ok)
	}
	if _, ok := d.Result(nil, nil, now, log, "x"); ok {
		t.Fatal("nothing due must report false")
	}
	d.Result(nil, errors.New("again"), now, log, "x")
	if n := strings.Count(buf.String(), "could not read"); n != 2 {
		t.Fatalf("a new failure after a success must log again, got %d", n)
	}
}
