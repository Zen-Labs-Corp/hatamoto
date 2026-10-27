package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/adminpath"
)

// fakeAdminStore is an in-memory adminStore.
type fakeAdminStore struct {
	cur          adminpath.Stored
	sets         []string
	acks         []string
	pendingCalls int
}

func (f *fakeAdminStore) Get(context.Context) (adminpath.Stored, error) { return f.cur, nil }
func (f *fakeAdminStore) Set(_ context.Context, p, by string) error {
	f.sets = append(f.sets, p)
	f.cur = adminpath.Stored{Path: &p, SetBy: &by}
	return nil
}
func (f *fakeAdminStore) SetAck(_ context.Context, p, by string) error {
	f.acks = append(f.acks, p)
	now := time.Now()
	f.cur = adminpath.Stored{Path: &p, AckBy: &by, AckAt: &now, SetBy: &by}
	return nil
}
func (f *fakeAdminStore) SetIfPending(_ context.Context, p string) (bool, error) {
	f.pendingCalls++
	if f.cur.Path != nil {
		return false, nil
	}
	f.cur.Path = &p
	return true, nil
}

func owner(v bool) func(context.Context) (bool, error) {
	return func(context.Context) (bool, error) { return v, nil }
}

func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewTextHandler(&b, nil)), &b
}

func strp(s string) *string { return &s }

func TestPinWinsOverStored(t *testing.T) {
	st := &fakeAdminStore{cur: adminpath.Stored{Path: strp("/k7qm-x2rf-9tdc")}}
	lg, _ := bufLogger()
	got, err := resolveAdminPath(context.Background(), st, staticSite{}, owner(true), "/pinned-place-x7q2", lg)
	if err != nil || got != "/pinned-place-x7q2" {
		t.Fatalf("got %q, %v", got, err)
	}
	if st.pendingCalls != 0 || len(st.sets) != 0 {
		t.Error("the pin must not write the database")
	}
}

func TestPinWeakWarns(t *testing.T) {
	lg, buf := bufLogger()
	got, err := resolveAdminPath(context.Background(), &fakeAdminStore{}, staticSite{}, owner(true), "/admin", lg)
	if err != nil || got != "/admin" {
		t.Fatalf("got %q, %v", got, err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 {
		t.Errorf("want one log line, got %d: %s", n, buf)
	}
	if !strings.Contains(buf.String(), "admin address /admin from HATAMOTO_ADMIN_PATH is weak: on the scanner list") || !strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("log = %s", buf)
	}
}

func TestPinMalformedRefuses(t *testing.T) {
	lg, _ := bufLogger()
	_, err := resolveAdminPath(context.Background(), &fakeAdminStore{}, staticSite{}, owner(true), "/a/b", lg)
	if err == nil || !strings.Contains(err.Error(), "HATAMOTO_ADMIN_PATH") {
		t.Fatalf("err = %v", err)
	}
}

func TestPinReservedRefuses(t *testing.T) {
	lg, _ := bufLogger()
	_, err := resolveAdminPath(context.Background(), &fakeAdminStore{}, staticSite{}, owner(true), "/api", lg)
	if err == nil || !strings.Contains(err.Error(), "HATAMOTO_ADMIN_PATH") || !strings.Contains(err.Error(), "reserved") && !strings.Contains(err.Error(), "serves") {
		t.Fatalf("err = %v", err)
	}
}

func TestStoredWinsWithoutPin(t *testing.T) {
	st := &fakeAdminStore{cur: adminpath.Stored{Path: strp("/k7qm-x2rf-9tdc")}}
	lg, _ := bufLogger()
	got, err := resolveAdminPath(context.Background(), st, staticSite{}, owner(false), "", lg)
	if err != nil || got != "/k7qm-x2rf-9tdc" || st.pendingCalls != 0 {
		t.Fatalf("got %q, %v, pending calls %d", got, err, st.pendingCalls)
	}
}

func TestPendingNoOwnerGenerates(t *testing.T) {
	st := &fakeAdminStore{}
	lg, _ := bufLogger()
	got, err := resolveAdminPath(context.Background(), st, staticSite{}, owner(false), "", lg)
	if err != nil {
		t.Fatal(err)
	}
	if res := adminpath.Assess(context.Background(), got, nil); res.Level != adminpath.LevelStrong {
		t.Errorf("%q is %v, want strong", got, res.Level)
	}
	if st.cur.Path == nil || *st.cur.Path != got {
		t.Error("generated address was not stored")
	}
}

func TestPendingWithOwnerGetsAdmin(t *testing.T) {
	st := &fakeAdminStore{}
	lg, _ := bufLogger()
	got, err := resolveAdminPath(context.Background(), st, staticSite{}, owner(true), "", lg)
	if err != nil || got != "/admin" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestStoredMalformedRefusesBoot(t *testing.T) {
	for _, bad := range []string{"", "admin", "/a/b", "/Admin-Zone", "/ab", "/ab cd"} {
		st := &fakeAdminStore{cur: adminpath.Stored{Path: strp(bad)}}
		lg, _ := bufLogger()
		_, err := resolveAdminPath(context.Background(), st, staticSite{}, owner(true), "", lg)
		if err == nil || !strings.Contains(err.Error(), "hatamoto admin-url set") {
			t.Errorf("%q: err = %v, want one naming hatamoto admin-url set", bad, err)
		}
	}
}

func TestStoredFixedReservedRefusesBoot(t *testing.T) {
	for _, bad := range []string{"/api", "/media", "/p", "/-"} {
		st := &fakeAdminStore{cur: adminpath.Stored{Path: strp(bad)}}
		lg, _ := bufLogger()
		_, err := resolveAdminPath(context.Background(), st, staticSite{}, owner(true), "", lg)
		if err == nil || !strings.Contains(err.Error(), "hatamoto admin-url set") {
			t.Errorf("%q: err = %v, want one naming hatamoto admin-url set", bad, err)
		}
	}
}

// A live-content collision (a page now uses the address) keeps booting, so the
// owner is never locked out.
func TestStoredServedWarnsAndBoots(t *testing.T) {
	st := &fakeAdminStore{cur: adminpath.Stored{Path: strp("/k7qm-x2rf-9tdc")}}
	lg, buf := bufLogger()
	got, err := resolveAdminPath(context.Background(), st, servingSite{"/k7qm-x2rf-9tdc"}, owner(true), "", lg)
	if err != nil || got != "/k7qm-x2rf-9tdc" {
		t.Fatalf("got %q, %v", got, err)
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "reserved") {
		t.Errorf("no warning: %s", buf.String())
	}
}

// servingSite serves one path as a live page.
type servingSite struct{ path string }

func (s servingSite) Serves(_ context.Context, p string) (bool, string) {
	return p == s.path, "a page uses it"
}
func (servingSite) Name() string   { return "" }
func (servingSite) Domain() string { return "" }
