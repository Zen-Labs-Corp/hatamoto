//go:build integration

package plugrun

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func newStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return &Store{DB: db}, ctx
}

func plugin() string { return "plugrun-" + uuid.NewString()[:8] }

func TestRunLifecycle(t *testing.T) {
	s, ctx := newStore(t)
	p := plugin()
	up, err := s.AddUpload(ctx, Upload{Plugin: p, Filename: "a.csv", Size: 3, BlobKey: "k/a"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Create(ctx, Run{Plugin: p, Name: "import", Params: jsontext.Value(`{"a":1}`), Hosts: []string{"example.com"}, UploadID: &up})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, r.ID) })
	if r.State != StateRunning || r.Phase != PhaseFetch || r.Name != "import" || len(r.Hosts) != 1 || r.UploadID == nil || *r.UploadID != up {
		t.Fatalf("created run: %+v", r)
	}
	if string(r.Params) != `{"a": 1}` && string(r.Params) != `{"a":1}` {
		t.Fatalf("params: %s", r.Params)
	}
	if got, err := s.Active(ctx, p); err != nil || got.ID != r.ID {
		t.Fatalf("Active: %v %v", got.ID, err)
	}
	if _, err := s.Create(ctx, Run{Plugin: p}); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("second Create = %v, want ErrActiveRun", err)
	}
	if err := s.SetChangeset(ctx, r.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(ctx, r.ID, "page-2"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.BumpSteps(ctx, r.ID); err != nil || n != 1 {
		t.Fatalf("BumpSteps = %d %v", n, err)
	}
	if err := s.Progress(ctx, r.ID, jsontext.Value(`{"done":5}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePhase(ctx, r.ID, PhaseTransform); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, r.ID)
	if got.Phase != PhaseTransform || got.Cursor != "" || got.ChangesetID == nil || *got.ChangesetID != 42 || got.Steps != 1 {
		t.Fatalf("after advance: %+v", got)
	}
	if err := s.AdvancePhase(ctx, r.ID, PhaseTransform); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("second advance = %v", err)
	}
	if err := s.Finish(ctx, r.ID, jsontext.Value(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, r.ID)
	if got.State != StateFinished || got.Summary == nil {
		t.Fatalf("after finish: %+v", got)
	}
	if err := s.Fail(ctx, r.ID, "late"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Fail after finish = %v", err)
	}
	if _, err := s.Active(ctx, p); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Active after finish = %v", err)
	}
	// The slot is free again.
	r2, err := s.Create(ctx, Run{Plugin: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, r2.ID) })
	if err := s.Fail(ctx, r2.ID, strings.Repeat("x", MaxReasonLen+10)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, r2.ID); got.State != StateFailed || len(got.Reason) != MaxReasonLen {
		t.Fatalf("after fail: %+v", got)
	}
	u, err := s.Upload(ctx, up)
	if err != nil || u.Filename != "a.csv" || u.BlobKey != "k/a" {
		t.Fatalf("Upload: %+v %v", u, err)
	}
	if _, err := s.Upload(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing upload = %v", err)
	}
}

func TestRunConcurrentCreateOneWins(t *testing.T) {
	s, ctx := newStore(t)
	p := plugin()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, active int
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Create(ctx, Run{Plugin: p})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
				t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, r.ID) })
			case errors.Is(err, ErrActiveRun):
				active++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || active != 5 {
		t.Fatalf("ok=%d active=%d, want 1 and 5", ok, active)
	}
}

func TestRunStale(t *testing.T) {
	s, ctx := newStore(t)
	p := plugin()
	r, err := s.Create(ctx, Run{Plugin: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, r.ID) })
	has := func(d time.Duration) bool {
		runs, err := s.StaleRunning(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range runs {
			if x.ID == r.ID {
				return true
			}
		}
		return false
	}
	if has(time.Hour) {
		t.Fatal("fresh run reported stale")
	}
	s.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if !has(time.Hour) {
		t.Fatal("old run not reported stale")
	}
}

func TestBufferQuotaAndCascade(t *testing.T) {
	s, ctx := newStore(t)
	r, err := s.Create(ctx, Run{Plugin: plugin()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, r.ID) })

	for _, k := range []string{"a/1", "a/2", "a/3", "b/1"} {
		if err := s.BufferPut(ctx, r.ID, k, []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.BufferPut(ctx, r.ID, "a/1", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if v, err := s.BufferGet(ctx, r.ID, "a/1"); err != nil || string(v) != "new" {
		t.Fatalf("Get = %q %v", v, err)
	}
	if _, err := s.BufferGet(ctx, r.ID, "zz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key = %v", err)
	}
	keys, _ := s.BufferList(ctx, r.ID, "a/", "", 2)
	if strings.Join(keys, ",") != "a/1,a/2" {
		t.Fatalf("page 1 = %v", keys)
	}
	keys, _ = s.BufferList(ctx, r.ID, "a/", "a/2", 2)
	if strings.Join(keys, ",") != "a/3" {
		t.Fatalf("page 2 = %v", keys)
	}

	if err := s.BufferPut(ctx, r.ID, "big", make([]byte, MaxBufferValue+1)); !errors.Is(err, ErrQuota) {
		t.Fatalf("oversize value = %v", err)
	}
	// Fill to the byte quota with max-size values; the next new key must fail.
	chunk := make([]byte, MaxBufferValue)
	var err2 error
	n := 0
	for ; n < 100 && err2 == nil; n++ {
		err2 = s.BufferPut(ctx, r.ID, "fill/"+strings.Repeat("0", 3)+string(rune('a'+n%26))+string(rune('a'+n/26)), chunk)
	}
	if !errors.Is(err2, ErrQuota) {
		t.Fatalf("filling never hit the quota: %v after %d", err2, n)
	}
	var total int
	s.DB.Pool().QueryRow(ctx, `SELECT COALESCE(sum(length(value)),0) FROM hatamoto_run_buffer WHERE run_id = $1`, r.ID).Scan(&total)
	if total > MaxBufferBytes {
		t.Fatalf("buffer holds %d bytes, over the quota", total)
	}

	// Proposals: stored, ordered, capped.
	if err := s.AddProposal(ctx, r.ID, "site_settings", jsontext.Value(`{"title":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddProposal(ctx, r.ID, "x", jsontext.Value(`not json`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad payload = %v", err)
	}
	for i := 1; i < MaxProposals; i++ {
		if err := s.AddProposal(ctx, r.ID, "home_page", jsontext.Value(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddProposal(ctx, r.ID, "home_page", jsontext.Value(`{}`)); !errors.Is(err, ErrQuota) {
		t.Fatalf("proposal cap = %v", err)
	}
	props, err := s.Proposals(ctx, r.ID)
	if err != nil || len(props) != MaxProposals || props[0].Kind != "site_settings" {
		t.Fatalf("Proposals: %d %v", len(props), err)
	}

	// A finished run takes no more buffer writes or proposals.
	if err := s.Finish(ctx, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.BufferPut(ctx, r.ID, "late", []byte("x")); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("put after finish = %v", err)
	}

	// Deleting the run removes buffer and proposals.
	if _, err := s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	s.DB.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM hatamoto_run_buffer WHERE run_id=$1) + (SELECT count(*) FROM hatamoto_run_proposals WHERE run_id=$1)`, r.ID).Scan(&left)
	if left != 0 {
		t.Fatalf("%d rows survive the run", left)
	}
}
