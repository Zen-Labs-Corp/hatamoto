package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidID(t *testing.T) {
	cases := map[string]bool{
		"20261019T091200Z-manual":     true,
		"20261019T091200Z-pre_update": true,
		"20261019T091200Z-scheduled":  true,
		"20261019T091200Z-other":      false,
		"../x":                        false,
		"a/b":                         false,
		"":                            false,
		"20261019T091200Z-manual/..":  false,
		"20261019T091200Z-manual\n":   false,
	}
	for id, want := range cases {
		if got := ValidID(id); got != want {
			t.Errorf("ValidID(%q) = %v, want %v", id, got, want)
		}
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func put(t *testing.T, s *Store, at time.Time, kind Kind, st State) Info {
	t.Helper()
	in := Info{ID: NewID(at, kind), Kind: kind, State: st, Created: at}
	if err := s.Write(in); err != nil {
		t.Fatal(err)
	}
	if st == StateOK {
		if err := os.WriteFile(s.archive(in.ID), []byte("tar"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return in
}

func TestNewStorePerms(t *testing.T) {
	s := newStore(t)
	fi, err := os.Stat(s.Dir())
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, %v", fi.Mode().Perm(), err)
	}
	in := put(t, s, time.Now(), KindManual, StateOK)
	fi, _ = os.Stat(s.sidecar(in.ID))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("sidecar mode = %v", fi.Mode().Perm())
	}
}

func TestWriteListGetOrder(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	a := put(t, s, base, KindManual, StateOK)
	b := put(t, s, base.Add(time.Hour), KindPreUpdate, StateOK)
	c := put(t, s, base.Add(2*time.Hour), KindManual, StateFailed)
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != c.ID || got[1].ID != b.ID || got[2].ID != a.ID {
		t.Fatalf("order = %+v", got)
	}
	one, err := s.Get(b.ID)
	if err != nil || one.Kind != KindPreUpdate {
		t.Fatalf("Get = %+v, %v", one, err)
	}
	if _, err := s.Get("20261019T000000Z-manual"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id err = %v", err)
	}
	if _, err := s.Get("../x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad id err = %v", err)
	}
}

func TestPruneKeepsNewestOKOnly(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	var oks []Info
	for i := range 4 {
		oks = append(oks, put(t, s, base.Add(time.Duration(i)*time.Hour), KindManual, StateOK))
	}
	failed := put(t, s, base.Add(-time.Hour), KindManual, StateFailed)
	pre := put(t, s, base, KindPreUpdate, StateOK)
	legacy := filepath.Join(s.Dir(), "pre-v1.dump")
	if err := os.WriteFile(legacy, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(KindManual, 2); err != nil {
		t.Fatal(err)
	}
	for i, in := range oks {
		_, err := s.Get(in.ID)
		if want := i >= 2; (err == nil) != want {
			t.Errorf("backup %d kept = %v, want %v", i, err == nil, want)
		}
	}
	for _, in := range []Info{failed, pre} {
		if _, err := s.Get(in.ID); err != nil {
			t.Errorf("%s was pruned: %v", in.ID, err)
		}
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("legacy dump touched: %v", err)
	}
	if _, err := os.Stat(s.archive(oks[0].ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("pruned archive still there: %v", err)
	}
	all, _ := s.List()
	for _, in := range all {
		if in.ID == "pre-v1" {
			t.Error("legacy dump listed")
		}
	}
}

func TestDeleteRunningRefused(t *testing.T) {
	s := newStore(t)
	in := put(t, s, time.Now(), KindManual, StateRunning)
	release, err := TryLock(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := s.Delete(in.ID); !errors.Is(err, ErrRunning) {
		t.Fatalf("Delete running = %v, want ErrRunning", err)
	}
}

func TestStaleRunningBecomesInterrupted(t *testing.T) {
	s := newStore(t)
	in := put(t, s, time.Now(), KindManual, StateRunning)
	got, err := s.Get(in.ID)
	if err != nil || got.State != StateFailed || got.Error != "interrupted" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	// The rewrite is persistent, and the record can now be deleted.
	again, _ := s.Get(in.ID)
	if again.State != StateFailed {
		t.Fatalf("not rewritten: %+v", again)
	}
	if err := s.Delete(in.ID); err != nil {
		t.Fatalf("Delete interrupted: %v", err)
	}
	if _, err := s.Get(in.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
}

func TestOpenOnlyOK(t *testing.T) {
	s := newStore(t)
	ok := put(t, s, time.Now(), KindManual, StateOK)
	bad := put(t, s, time.Now().Add(time.Second), KindManual, StateFailed)
	f, _, err := s.Open(ok.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := s.Open(bad.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open failed backup = %v", err)
	}
}
