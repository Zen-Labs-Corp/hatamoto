package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/s3/s3test"
	"hatamoto.dev/core/internal/worker"
)

// memOffsiteStore keeps the settings row in memory with the same atomic claim
// the database gives.
type memOffsiteStore struct {
	mu       sync.Mutex
	set      offsite.Settings
	deferred []time.Time
	results  []offsite.Result
}

func (m *memOffsiteStore) Get(context.Context) (offsite.Settings, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.set, true, nil
}

func (m *memOffsiteStore) Claim(_ context.Context, now, next time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.set.Enabled || m.set.NextDueAt.IsZero() || m.set.NextDueAt.After(now) {
		return false, nil
	}
	m.set.NextDueAt = next
	return true, nil
}

func (m *memOffsiteStore) Defer(_ context.Context, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set.NextDueAt = until
	m.deferred = append(m.deferred, until)
	return nil
}

func (m *memOffsiteStore) SaveResult(_ context.Context, r offsite.Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = append(m.results, r)
	return nil
}

// fakeOffsiteEngine writes a small tar per backup and runs OnDone like the
// real engine does after the sidecar is written.
type fakeOffsiteEngine struct {
	dir     string
	clock   func() time.Time
	busy    atomic.Bool
	begun   atomic.Int32
	mu      sync.Mutex
	records map[string]backup.Info
}

func (f *fakeOffsiteEngine) Begin(_ context.Context, req backup.Request) (backup.Info, func(context.Context) (backup.Info, error), error) {
	if f.busy.Load() {
		return backup.Info{}, nil, backup.ErrBusy
	}
	n := f.begun.Add(1)
	info := backup.Info{ID: backup.NewID(f.clock().Add(time.Duration(n)*time.Second), req.Kind), Kind: req.Kind, State: backup.StateOK}
	return info, func(ctx context.Context) (backup.Info, error) {
		if err := ctx.Err(); err != nil {
			return info, err
		}
		path := filepath.Join(f.dir, info.ID+".tar")
		if err := os.WriteFile(path, bytes.Repeat([]byte("tar"), 500), 0o600); err != nil {
			return info, err
		}
		f.mu.Lock()
		if f.records == nil {
			f.records = map[string]backup.Info{}
		}
		f.records[info.ID] = info
		f.mu.Unlock()
		if req.OnDone != nil {
			req.OnDone(ctx, info, path)
		}
		return info, nil
	}, nil
}

func (f *fakeOffsiteEngine) Update(id string, fn func(*backup.Info)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := f.records[id]
	fn(&in)
	f.records[id] = in
	return nil
}

type offsiteRig struct {
	svc    *offsiteService
	store  *memOffsiteStore
	eng    *fakeOffsiteEngine
	srv    *s3test.Server
	now    *time.Time
	logs   *bytes.Buffer
	events []admin.AuditEvent
	mu     sync.Mutex
}

func newOffsiteRig(t *testing.T, start time.Time) *offsiteRig {
	t.Helper()
	r := &offsiteRig{srv: s3test.NewServer(t), now: &start, logs: &bytes.Buffer{}}
	key := offsite.SecretKey("server-secret")
	sealed, err := offsite.SealSecret(key, s3test.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	r.store = &memOffsiteStore{set: offsite.Settings{
		Enabled: true, Service: offsite.ServiceCustom, Endpoint: r.srv.URL, Region: s3test.Region,
		Bucket: s3test.Bucket, Prefix: "site/", AccessKey: s3test.AccessKey, SecretSealed: sealed,
		Schedule: offsite.Schedule{Kind: offsite.ScheduleDaily, Hour: 3}, Keep: 2,
	}}
	r.eng = &fakeOffsiteEngine{dir: t.TempDir(), clock: func() time.Time { return *r.now }}
	r.svc = &offsiteService{
		store: r.store, eng: r.eng, key: key, now: func() time.Time { return *r.now },
		audit: func(_ context.Context, e admin.AuditEvent) {
			r.mu.Lock()
			r.events = append(r.events, e)
			r.mu.Unlock()
		},
		logger: slog.New(slog.NewTextHandler(r.logs, nil)), wg: &sync.WaitGroup{},
	}
	return r
}

func at(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, time.UTC) }

func TestOffsiteTickDailyTakesOneBackupPerDay(t *testing.T) {
	r := newOffsiteRig(t, at(1, 2, 0))
	tick := func(now time.Time) {
		*r.now = now
		if err := r.svc.tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	tick(at(1, 2, 0)) // first tick only sets the slot
	if r.eng.begun.Load() != 0 || !r.store.set.NextDueAt.Equal(at(1, 3, 0)) {
		t.Fatalf("begun %d, due %v", r.eng.begun.Load(), r.store.set.NextDueAt)
	}
	tick(at(1, 2, 59))
	tick(at(1, 3, 1))
	tick(at(1, 3, 2))
	tick(at(1, 20, 0))
	if r.eng.begun.Load() != 1 || len(r.srv.Objects()) != 1 {
		t.Fatalf("day 1: %d backups, %d objects", r.eng.begun.Load(), len(r.srv.Objects()))
	}
	tick(at(2, 3, 0))
	tick(at(2, 3, 1))
	if r.eng.begun.Load() != 2 || len(r.srv.Objects()) != 2 {
		t.Fatalf("day 2: %d backups, %d objects", r.eng.begun.Load(), len(r.srv.Objects()))
	}
	for id, in := range r.eng.records {
		if in.Kind != backup.KindScheduled || in.Offsite != "uploaded" || in.SHA256 == "" {
			t.Fatalf("record %s = %+v", id, in)
		}
	}
	if n := len(r.store.results); n != 2 || !r.store.results[1].OK {
		t.Fatalf("results = %+v", r.store.results)
	}
}

// The scheduler caps a job at 10 minutes; the backup and upload must not be
// cut off by that cap.
func TestOffsiteTickRunsPastTheJobCap(t *testing.T) {
	r := newOffsiteRig(t, at(1, 3, 5))
	r.store.set.NextDueAt = at(1, 3, 0)
	life := t.Context()
	capped, cancel := context.WithTimeout(worker.WithJobParent(life, life), time.Nanosecond)
	defer cancel()
	<-capped.Done()
	if err := r.svc.tick(capped); err != nil {
		t.Fatal(err)
	}
	if len(r.srv.Objects()) != 1 || len(r.store.results) != 1 || !r.store.results[0].OK {
		t.Fatalf("objects %d, results %+v", len(r.srv.Objects()), r.store.results)
	}
}

func TestOffsiteTwoClaimersRunOneSlot(t *testing.T) {
	r := newOffsiteRig(t, at(1, 3, 5))
	r.store.set.NextDueAt = at(1, 3, 0)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.svc.tick(t.Context())
		}()
	}
	wg.Wait()
	if n := r.eng.begun.Load(); n != 1 {
		t.Fatalf("%d backups for one slot", n)
	}
}

func TestOffsiteBusyDefersWithoutAFailure(t *testing.T) {
	r := newOffsiteRig(t, at(1, 3, 5))
	r.store.set.NextDueAt = at(1, 3, 0)
	r.eng.busy.Store(true)
	if err := r.svc.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.store.deferred) != 1 || !r.store.deferred[0].Equal(at(1, 3, 15)) {
		t.Fatalf("deferred = %v", r.store.deferred)
	}
	if len(r.store.results) != 0 || len(r.events) != 0 || len(r.srv.Objects()) != 0 {
		t.Fatalf("a busy lock must not record a result: %+v", r.store.results)
	}
	r.eng.busy.Store(false)
	*r.now = at(1, 3, 16)
	_ = r.svc.tick(t.Context())
	if r.eng.begun.Load() != 1 {
		t.Fatal("the deferred slot did not run after the lock freed")
	}
}

func TestOffsiteOffAndDisabledDoNothing(t *testing.T) {
	r := newOffsiteRig(t, at(1, 4, 0))
	r.store.set.NextDueAt = at(1, 3, 0)
	r.store.set.Enabled = false
	_ = r.svc.tick(t.Context())
	r.store.set.Enabled, r.store.set.Schedule.Kind = true, offsite.ScheduleOff
	_ = r.svc.tick(t.Context())
	if r.eng.begun.Load() != 0 {
		t.Fatal("a backup ran")
	}
}

func TestOffsiteFailureNeverLeaksTheSecretKey(t *testing.T) {
	r := newOffsiteRig(t, at(1, 3, 5))
	r.store.set.NextDueAt = at(1, 3, 0)
	r.srv.Tamper = func(_ string, o *s3test.Object) { o.Data = o.Data[:10] }
	if err := r.svc.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.store.results) != 1 || r.store.results[0].OK || r.store.results[0].Error == "" {
		t.Fatalf("results = %+v", r.store.results)
	}
	for _, in := range r.eng.records {
		if in.Offsite != "failed" || in.OffsiteError == "" {
			t.Fatalf("record = %+v", in)
		}
	}
	all := r.logs.String()
	for _, e := range r.events {
		all += e.Action + e.Actor + e.Target
		for k, v := range e.Detail {
			all += k + "=" + toString(v)
		}
	}
	if !strings.Contains(all, "backups.offsite.upload") || strings.Contains(all, s3test.SecretKey) {
		t.Fatalf("audit/log text wrong:\n%s", all)
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestOffsiteScrubRemovesTheKey(t *testing.T) {
	o := &offsiteService{}
	got := o.scrubText("sekret", "denied for sekret: sekret")
	if strings.Contains(got, "sekret") {
		t.Fatalf("scrub = %q", got)
	}
}

func TestOffsiteRunNowUploadsOnTheLifecycleContext(t *testing.T) {
	r := newOffsiteRig(t, at(1, 12, 0))
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.svc.base = func() context.Context { return base }
	reqCtx, reqCancel := context.WithCancel(context.Background())
	id, err := r.svc.RunNow(reqCtx, "owner@example.com")
	reqCancel()
	if err != nil || id == "" {
		t.Fatalf("RunNow = %q, %v", id, err)
	}
	r.svc.wg.Wait()
	if len(r.srv.Objects()) != 1 || len(r.events) != 1 || r.events[0].Actor != "owner@example.com" {
		t.Fatalf("objects %d, events %+v", len(r.srv.Objects()), r.events)
	}
	r.eng.busy.Store(true)
	if _, err := r.svc.RunNow(context.Background(), "owner@example.com"); err == nil {
		t.Fatal("RunNow succeeded while the lock was held")
	}
}

func (m *memOffsiteStore) Save(_ context.Context, set offsite.Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := set.Validate(); err != nil {
		return err
	}
	m.set = set
	return nil
}

func newOffsiteAdmin(t *testing.T) (*offsiteAdmin, *memOffsiteStore, *s3test.Server) {
	t.Helper()
	srv := s3test.NewServer(t)
	store := &memOffsiteStore{}
	now := at(1, 12, 0)
	key := offsite.SecretKey("server-secret")
	return &offsiteAdmin{store: store, key: key, now: func() time.Time { return now },
		svc: &offsiteService{store: store, key: key}}, store, srv
}

func offsiteInput(srv *s3test.Server) admin.OffsiteInput {
	return admin.OffsiteInput{
		Enabled: true, Service: offsite.ServiceCustom, Endpoint: srv.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey,
		Schedule: offsite.ScheduleDaily, Hour: 3, Keep: 5,
	}
}

func TestOffsiteAdminSavesTheKeySealedAndNeverShowsIt(t *testing.T) {
	o, store, srv := newOffsiteAdmin(t)
	if errs, err := o.Save(t.Context(), offsiteInput(srv)); err != nil || len(errs) != 0 {
		t.Fatalf("Save = %v, %v", errs, err)
	}
	if bytes.Contains(store.set.SecretSealed, []byte(s3test.SecretKey)) || len(store.set.SecretSealed) == 0 {
		t.Fatal("the key is stored in the clear or not at all")
	}
	if !store.set.NextDueAt.Equal(at(2, 3, 0)) {
		t.Fatalf("next due = %v, want tomorrow 03:00 (saved after today's slot)", store.set.NextDueAt)
	}
	v, err := o.View(t.Context())
	if err != nil || !v.SecretSaved || v.SecretLost || !v.NextRun.Equal(at(2, 3, 0)) {
		t.Fatalf("View = %+v, %v", v, err)
	}
	if strings.Contains(fmt.Sprintf("%+v", v), s3test.SecretKey) {
		t.Fatal("the view carries the key")
	}
	// A blank key keeps the saved one; an unchanged schedule keeps its slot.
	store.set.NextDueAt = at(1, 20, 0)
	in := offsiteInput(srv)
	in.SecretKey, in.Keep = "", 9
	if errs, err := o.Save(t.Context(), in); err != nil || len(errs) != 0 {
		t.Fatalf("Save blank key = %v, %v", errs, err)
	}
	if _, ok := store.set.Secret(o.key); !ok || store.set.Keep != 9 || !store.set.NextDueAt.Equal(at(1, 20, 0)) {
		t.Fatalf("saved = %+v", store.set)
	}
}

func TestOffsiteAdminSaveReportsFieldErrors(t *testing.T) {
	o, _, srv := newOffsiteAdmin(t)
	for name, tc := range map[string]struct {
		mut func(*admin.OffsiteInput)
		id  string
	}{
		"bucket":     {func(in *admin.OffsiteInput) { in.Bucket = "X" }, "offsite-bucket"},
		"endpoint":   {func(in *admin.OffsiteInput) { in.Endpoint = "ftp://x" }, "offsite-endpoint"},
		"prefix":     {func(in *admin.OffsiteInput) { in.Prefix = "../x" }, "offsite-prefix"},
		"secret key": {func(in *admin.OffsiteInput) { in.SecretKey = "" }, "offsite-secret-key"},
	} {
		in := offsiteInput(srv)
		tc.mut(&in)
		errs, err := o.Save(t.Context(), in)
		if err != nil || len(errs) != 1 || errs[0].ID != tc.id || errs[0].Message == "" {
			t.Errorf("%s: %v, %v", name, errs, err)
		}
	}
}

func TestOffsiteAdminTestUsesTheSavedKeyAndShowsNothingSecret(t *testing.T) {
	o, _, srv := newOffsiteAdmin(t)
	if err := o.Test(t.Context(), offsiteInput(srv)); err != nil {
		t.Fatalf("Test with a typed key: %v", err)
	}
	if _, err := o.Save(t.Context(), offsiteInput(srv)); err != nil {
		t.Fatal(err)
	}
	in := offsiteInput(srv)
	in.SecretKey = ""
	if err := o.Test(t.Context(), in); err != nil {
		t.Fatalf("Test with the saved key: %v", err)
	}
	in.SecretKey = "wrong-" + s3test.SecretKey
	err := o.Test(t.Context(), in)
	if err == nil || strings.Contains(err.Error(), in.SecretKey) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestOffsiteAdminRunNowMapsBusy(t *testing.T) {
	r := newOffsiteRig(t, at(1, 12, 0))
	o := &offsiteAdmin{store: r.store, svc: r.svc, key: r.svc.key}
	r.eng.busy.Store(true)
	if _, err := o.RunNow(t.Context(), "owner@example.com"); !errors.Is(err, admin.ErrBackupRunning) {
		t.Fatalf("err = %v", err)
	}
}
