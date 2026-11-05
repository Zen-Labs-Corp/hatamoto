//go:build integration

package pluginhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/sdk"
)

type fakeUploads struct {
	plugin string
	id     uuid.UUID
	data   []byte
}

func (f *fakeUploads) Open(_ context.Context, plugin string, id uuid.UUID) (io.ReaderAt, int64, error) {
	if plugin != f.plugin || id != f.id {
		return nil, 0, errors.New("not found")
	}
	return bytes.NewReader(f.data), int64(len(f.data)), nil
}

// runRig is an API over a real run store and one running run of plugin "p".
func runRig(t *testing.T, mutate func(*plugrun.Run, *Services)) (*API, RunCtx, *plugrun.Store) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	store := &plugrun.Store{DB: db}
	run := plugrun.Run{Plugin: "p" + uuid.NewString()[:8], Name: "import", Params: jsontext.Value(`{"site":"x"}`), Hosts: []string{"example.com"}}
	svc := Services{Runs: store}
	if mutate != nil {
		mutate(&run, &svc)
	}
	created, err := store.Create(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, created.ID) })
	g := NewGrantStore(nil)
	a := NewAPI(svc, g, func(string) (sdk.Manifest, bool) { return sdk.Manifest{Name: created.Plugin}, true }, nil)
	return a, RunCtx{RunID: created.ID, Plugin: created.Plugin, Name: "import", Phase: sdk.RunPhaseFetch, Hosts: created.Hosts, Signal: &RunSignal{}}, store
}

func callAs(t *testing.T, a *API, rc RunCtx, fn string, args any) (jsontext.Value, *sdk.HostError) {
	t.Helper()
	raw, _ := jsonx.MarshalPinned(args)
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: fn, Args: raw})
	var resp sdk.CallResponse
	if err := jsonx.UnmarshalPinned(a.Call(WithRun(context.Background(), rc), rc.Plugin, in), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func TestRunFnsStateFunctions(t *testing.T) {
	a, rc, store := runRig(t, nil)
	res, he := callAs(t, a, rc, sdk.FnRunInfo, map[string]any{})
	var info sdk.RunInfo
	if he != nil || jsonx.UnmarshalPinned(res, &info) != nil || info.RunID != rc.RunID.String() || info.Name != "import" ||
		info.Phase != sdk.RunPhaseFetch || !strings.Contains(string(info.Params), "site") || len(info.Hosts) != 1 || info.UploadID != "" {
		t.Fatalf("run.info: %s %v", res, he)
	}
	if _, he := callAs(t, a, rc, sdk.FnRunCheckpoint, sdk.RunCheckpointArgs{Cursor: "page-2"}); he != nil {
		t.Fatal(he)
	}
	if got, _ := store.Get(context.Background(), rc.RunID); got.Cursor != "page-2" {
		t.Fatalf("cursor %q", got.Cursor)
	}
	if _, he := callAs(t, a, rc, sdk.FnRunCheckpoint, sdk.RunCheckpointArgs{Cursor: strings.Repeat("c", plugrun.MaxCursorLen+1)}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("oversize cursor: %v", he)
	}
	if _, he := callAs(t, a, rc, sdk.FnRunProgress, sdk.RunProgressArgs{Done: 3, Total: 10, Message: "posts"}); he != nil {
		t.Fatal(he)
	}
	if got, _ := store.Get(context.Background(), rc.RunID); !strings.Contains(string(got.Progress), `"done"`) {
		t.Fatalf("progress %s", got.Progress)
	}
	if _, he := callAs(t, a, rc, sdk.FnRunProgress, sdk.RunProgressArgs{Done: -1}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("negative progress: %v", he)
	}
	// A run that ended no longer takes writes.
	if err := store.Fail(context.Background(), rc.RunID, "x"); err != nil {
		t.Fatal(err)
	}
	if _, he := callAs(t, a, rc, sdk.FnRunCheckpoint, sdk.RunCheckpointArgs{Cursor: "late"}); he == nil || he.Code != sdk.ErrUnavailable {
		t.Fatalf("checkpoint after the run ended: %v", he)
	}
}

func TestRunFnsBuffer(t *testing.T) {
	a, fetch, _ := runRig(t, nil)
	put := func(k, v string) *sdk.HostError {
		_, he := callAs(t, a, fetch, sdk.FnBufferPut, sdk.BufferPutArgs{Key: k, ValueB64: base64.StdEncoding.EncodeToString([]byte(v))})
		return he
	}
	for i := range 3 {
		if he := put(fmt.Sprintf("post/%d", i), fmt.Sprintf("v%d", i)); he != nil {
			t.Fatal(he)
		}
	}
	if he := put("", "x"); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("empty key: %v", he)
	}
	if he := put("big", strings.Repeat("x", plugrun.MaxBufferValue+1)); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("oversize value: %v", he)
	}
	if _, he := callAs(t, a, fetch, sdk.FnBufferPut, sdk.BufferPutArgs{Key: "k", ValueB64: "!!"}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("bad base64: %v", he)
	}
	// Reads belong to the transform phase.
	tr := fetch
	tr.Phase = sdk.RunPhaseTransform
	res, he := callAs(t, a, tr, sdk.FnBufferGet, sdk.BufferGetArgs{Key: "post/1"})
	var v sdk.BufferValue
	_ = jsonx.UnmarshalPinned(res, &v)
	if he != nil || !v.Found || v.ValueB64 != base64.StdEncoding.EncodeToString([]byte("v1")) {
		t.Fatalf("buffer.get: %s %v", res, he)
	}
	res, _ = callAs(t, a, tr, sdk.FnBufferGet, sdk.BufferGetArgs{Key: "nope"})
	if string(res) != `{"found":false}` {
		t.Fatalf("missing key: %s", res)
	}
	res, he = callAs(t, a, tr, sdk.FnBufferList, sdk.BufferListArgs{Prefix: "post/", Limit: 2})
	var l sdk.BufferKeys
	_ = jsonx.UnmarshalPinned(res, &l)
	if he != nil || strings.Join(l.Keys, ",") != "post/0,post/1" || l.Next != "post/1" {
		t.Fatalf("page 1: %s %v", res, he)
	}
	res, _ = callAs(t, a, tr, sdk.FnBufferList, sdk.BufferListArgs{Prefix: "post/", After: l.Next, Limit: 2})
	l = sdk.BufferKeys{}
	_ = jsonx.UnmarshalPinned(res, &l)
	if strings.Join(l.Keys, ",") != "post/2" || l.Next != "" {
		t.Fatalf("page 2: %s", res)
	}
	// The fetch phase cannot read the buffer back.
	if _, he := callAs(t, a, fetch, sdk.FnBufferGet, sdk.BufferGetArgs{Key: "post/1"}); he == nil || he.Code != sdk.ErrPhaseDenied {
		t.Fatalf("buffer.get in fetch: %v", he)
	}
}

func TestRunFnsUploadRead(t *testing.T) {
	up := &fakeUploads{id: uuid.New(), data: []byte("0123456789")}
	a, rc, _ := runRig(t, func(r *plugrun.Run, s *Services) {
		up.plugin = r.Plugin
		r.UploadID = &up.id
		s.Uploads = up
	})
	read := func(off, n int64) (sdk.UploadChunk, *sdk.HostError) {
		res, he := callAs(t, a, rc, sdk.FnUploadRead, sdk.UploadReadArgs{UploadID: up.id.String(), Offset: off, Len: n})
		var c sdk.UploadChunk
		_ = jsonx.UnmarshalPinned(res, &c)
		return c, he
	}
	c, he := read(2, 4)
	if raw, _ := base64.StdEncoding.DecodeString(c.DataB64); he != nil || string(raw) != "2345" || c.Size != 10 || c.EOF {
		t.Fatalf("middle chunk: %+v %v", c, he)
	}
	c, he = read(8, 100)
	if raw, _ := base64.StdEncoding.DecodeString(c.DataB64); he != nil || string(raw) != "89" || !c.EOF {
		t.Fatalf("last chunk: %+v %v", c, he)
	}
	if c, he = read(10, 5); he != nil || c.DataB64 != "" || !c.EOF {
		t.Fatalf("at the end: %+v %v", c, he)
	}
	if _, he := read(0, maxUploadChunk+1); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("oversize chunk: %v", he)
	}
	if _, he := read(-1, 1); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("negative offset: %v", he)
	}
	// Another upload id is not the run's.
	if _, he := callAs(t, a, rc, sdk.FnUploadRead, sdk.UploadReadArgs{UploadID: uuid.NewString(), Len: 1}); he == nil || he.Code != sdk.ErrNotFound {
		t.Fatalf("foreign upload: %v", he)
	}
	// Not in the transform phase.
	tr := rc
	tr.Phase = sdk.RunPhaseTransform
	if _, he := callAs(t, a, tr, sdk.FnUploadRead, sdk.UploadReadArgs{UploadID: up.id.String(), Len: 1}); he == nil || he.Code != sdk.ErrPhaseDenied {
		t.Fatalf("upload.read in transform: %v", he)
	}
}
