package admin

import (
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newSudoStore() (*sudoStore, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	return &sudoStore{now: c.now}, c
}

func TestSudoStoreWindowIsTenMinutes(t *testing.T) {
	t.Parallel()

	s, c := newSudoStore()
	id := uuid.New()
	if s.Active(id) {
		t.Fatal("active before any Grant")
	}
	s.Grant(id)
	c.t = c.t.Add(9*time.Minute + 59*time.Second)
	if !s.Active(id) {
		t.Fatal("inactive at 9m59s")
	}
	c.t = c.t.Add(time.Second)
	if s.Active(id) {
		t.Fatal("still active at exactly 10m")
	}
}

func TestSudoStoreUseDoesNotExtendAndGrantResets(t *testing.T) {
	t.Parallel()

	s, c := newSudoStore()
	id := uuid.New()
	s.Grant(id)
	c.t = c.t.Add(9 * time.Minute)
	if !s.Active(id) {
		t.Fatal("inactive at 9m")
	}
	c.t = c.t.Add(time.Minute)
	if s.Active(id) {
		t.Fatal("a check at 9m extended the window")
	}
	s.Grant(id)
	c.t = c.t.Add(9*time.Minute + 59*time.Second)
	if !s.Active(id) {
		t.Fatal("a second Grant did not reset the window")
	}
}

func TestSudoStoreKeysBySession(t *testing.T) {
	t.Parallel()

	s, _ := newSudoStore()
	a, b := uuid.New(), uuid.New()
	s.Grant(a)
	if s.Active(b) {
		t.Fatal("another session is elevated")
	}
}

func TestSudoStoreSweepsExpiredEntries(t *testing.T) {
	t.Parallel()

	s, c := newSudoStore()
	s.Grant(uuid.New())
	s.Grant(uuid.New())
	c.t = c.t.Add(sudoTTL)
	s.Grant(uuid.New())
	if n := len(s.until); n != 1 {
		t.Fatalf("%d entries after the sweep, want 1", n)
	}
}

func TestSudoStoreZeroValueUsesTheRealClock(t *testing.T) {
	t.Parallel()

	var s sudoStore
	id := uuid.New()
	s.Grant(id)
	if !s.Active(id) {
		t.Fatal("a fresh grant on the zero value is not active")
	}
}

func TestSudoURLAndNext(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	people := g.a.url("people")
	if got := g.a.sudoURL(people+"?a=1", true); got != g.a.sudoPath()+"?next="+url.QueryEscape(people+"?a=1")+"&resubmit=1" {
		t.Fatalf("sudoURL = %q", got)
	}
	if got := g.a.sudoURL("", false); got != g.a.sudoPath() {
		t.Fatalf("sudoURL empty = %q", got)
	}
	for in, want := range map[string]string{
		people:                      people,
		g.a.sudoPath():              g.a.url(),
		g.a.sudoPath() + "?next=/x": g.a.url(),
		"https://evil.example/x":    g.a.url(),
		"":                          g.a.url(),
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.URL.RawQuery = "next=" + url.QueryEscape(in)
		if got := g.a.sudoNext(r); got != want {
			t.Errorf("sudoNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// codeCapReached reports whether no further code attempt is admitted right now.
func (s *sudoStore) codeCapReached(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fails[id]
	return f != nil && f.n >= sudoMaxCodeFails
}

func TestSudoStoreBeginCodeAdmitsFiveAndLocksThatSessionOnly(t *testing.T) {
	t.Parallel()

	s, c := newSudoStore()
	id := uuid.New()
	for i := range sudoMaxCodeFails {
		if !s.BeginCode(id) {
			t.Fatalf("attempt %d refused", i)
		}
	}
	if s.BeginCode(id) {
		t.Fatal("a sixth attempt was admitted")
	}
	if !s.BeginCode(uuid.New()) {
		t.Fatal("another session was locked")
	}
	c.t = c.t.Add(sudoFailWindow - time.Second)
	if s.BeginCode(id) {
		t.Fatal("unlocked early")
	}
	c.t = c.t.Add(time.Second)
	if !s.BeginCode(id) {
		t.Fatal("still locked after the window")
	}
}

func TestSudoStoreReleaseGivesTheAttemptBackAndUnlocks(t *testing.T) {
	t.Parallel()

	s, _ := newSudoStore()
	id := uuid.New()
	for range sudoMaxCodeFails {
		s.BeginCode(id)
	}
	s.ReleaseCode(id)
	if !s.BeginCode(id) {
		t.Fatal("a released attempt was not given back")
	}
	if s.BeginCode(id) {
		t.Fatal("more than the cap admitted")
	}
}

func TestSudoStoreGrantClearsReservations(t *testing.T) {
	t.Parallel()

	s, _ := newSudoStore()
	id := uuid.New()
	for range sudoMaxCodeFails {
		s.BeginCode(id)
	}
	s.Grant(id)
	if !s.BeginCode(id) {
		t.Fatal("a success did not reset the count")
	}
}

func TestSudoStoreOldCodeFailsDoNotAccumulate(t *testing.T) {
	t.Parallel()

	s, c := newSudoStore()
	id := uuid.New()
	for range sudoMaxCodeFails - 1 {
		s.BeginCode(id)
	}
	c.t = c.t.Add(sudoFailWindow)
	for i := range sudoMaxCodeFails {
		if !s.BeginCode(id) {
			t.Fatalf("attempt %d of a new run refused: old failures counted", i)
		}
	}
}

// Two Admins built from one SudoStore share grants: the address change swaps
// in a new Admin and the owner must not be asked for the password again.
func TestSharedSudoStoreSurvivesANewAdmin(t *testing.T) {
	t.Parallel()

	shared := NewSudoStore()
	first, err := New(Deps{AdminPath: "/first-test", Sudo: shared})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Deps{AdminPath: "/second-test", Sudo: shared})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	first.sudo.store.Grant(id)
	if !second.sudo.store.Active(id) {
		t.Error("a grant on the first Admin is not active on the second")
	}

	own, err := New(Deps{AdminPath: "/own-test"})
	if err != nil {
		t.Fatal(err)
	}
	if own.sudo.store.Active(id) {
		t.Error("an Admin without a shared store saw another store's grant")
	}
}
