package importjob

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSealRoundTripAndBinding(t *testing.T) {
	key := DeriveKey("jwt-secret-of-the-server-0123456789")
	run := uuid.New()
	plain := []byte(`{"password":"abcd efgh"}`)
	sealed, err := seal(key, run, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("abcd")) {
		t.Fatal("the password is readable in the sealed value")
	}
	got, err := open(key, run, sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := open(key, uuid.New(), sealed); err == nil {
		t.Error("a sealed credential opened under another run")
	}
	if _, err := open(DeriveKey("another secret"), run, sealed); err == nil {
		t.Error("a sealed credential opened under another key")
	}
	again, _ := seal(key, run, plain)
	if bytes.Equal(again, sealed) {
		t.Error("sealing twice gave the same bytes (nonce reused)")
	}
	if _, err := seal([]byte("short"), run, plain); err == nil {
		t.Error("a short key was accepted")
	}
}

func TestCleanSiteURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://old.example.com/":        "https://old.example.com",
		"old.example.com":                 "https://old.example.com",
		" http://old.example.com/blog/ ":  "http://old.example.com/blog",
		"https://old.example.com?x=1#top": "https://old.example.com",
	} {
		got, err := cleanSiteURL(in)
		if err != nil || got != want {
			t.Errorf("cleanSiteURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "ftp://old.example.com", "https://user:secret@old.example.com", "http://"} {
		_, err := cleanSiteURL(in)
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Field != "url" {
			t.Errorf("cleanSiteURL(%q) = %v, want a url validation error", in, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("the error echoes the credential: %v", err)
		}
	}
}

func TestEffectiveStatus(t *testing.T) {
	for _, c := range []struct {
		status string
		active bool
		want   string
	}{
		{StatusRunning, true, StatusRunning}, {StatusRunning, false, StatusInterrupted}, {StatusScanning, false, StatusInterrupted},
		{StatusScanned, false, StatusScanned}, {StatusCompleted, false, StatusCompleted}, {StatusFailed, false, StatusFailed},
	} {
		if got := (&record{status: c.status}).effective(c.active); got != c.want {
			t.Errorf("%s active=%v: %s, want %s", c.status, c.active, got, c.want)
		}
	}
}

// A job removes only its own registry entry: a finished job must not take the
// slot of the one that replaced it.
func TestFinishRemovesOnlyItsOwnEntry(t *testing.T) {
	m := New(Config{})
	t.Cleanup(m.Close)
	id := uuid.New()
	first, err := m.reserve(id, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.reserve(id, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("second reserve: %v", err)
	}
	// The registry now holds another job under the id (as an overwrite would).
	other := &job{m: m, ctx: context.Background(), cancel: func() {}}
	m.mu.Lock()
	m.jobs[id] = other
	m.mu.Unlock()
	first.finish(id)
	first.finish(id) // twice is harmless
	if m.job(id) != other {
		t.Fatal("a finished job removed the entry of another job")
	}
	m.mu.Lock()
	delete(m.jobs, id)
	m.mu.Unlock()
}
