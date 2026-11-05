package pluginhost

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

// The committed guest in testdata/legacy was built once with the v1 SDK. It
// must keep loading and answering after the host moves to JSON v2.
func TestLegacyWasmLoadsAndRuns(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "legacy")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for src, dst := range map[string]string{"plugin.json": ManifestFile, "legacy.wasm": WasmFile} {
		b, err := os.ReadFile(filepath.Join("testdata", "legacy", src))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pdir, dst), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	logs := &lockedBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	engine := hooks.New(log)
	rt, err := NewRuntime(log, engine, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	sched := &fakeScheduler{}
	rt.Configure(Config{Services: Services{Fetcher: NewFetcher()}, Scheduler: sched})

	listed, err := ListDir(dir)
	if err != nil || len(listed) != 1 || listed[0].Error != "" {
		t.Fatalf("ListDir = %+v, %v", listed, err)
	}
	if sts := rt.LoadDir(context.Background(), dir); len(sts) != 1 || !sts[0].Loaded {
		t.Fatalf("legacy did not load: %+v", sts)
	}

	// Route: the guest echoes the request it was handed.
	rec := httptest.NewRecorder()
	rt.AdminRoutes().Serve(rec, httptest.NewRequest("GET", "/x/?a=1", nil), "legacy", "echo", &sdk.HTTPUser{ID: "u1", Role: "owner"})
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("route = %d %v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	for _, want := range []string{`"method":"GET"`, `"pattern":"/echo"`, `"query":{"a":["1"]}`} {
		if !strings.Contains(body, want) {
			t.Errorf("route body %s lacks %s", body, want)
		}
	}

	// Template func: args in, plain string out.
	out, err := rt.Host().Call(context.Background(), "legacy", "func_shout", []byte(`["ada"]`))
	if err != nil || string(out) != "ADA<b>" {
		t.Fatalf("shout = %q, %v", out, err)
	}

	// Filter: hides the block, sets a namespaced cookie.
	pf := NewPageFilter(engine)
	got := pf.Apply(context.Background(), sdk.PageContext{Path: "/p", Blocks: []sdk.Block{{ID: "intro", Type: "hero"}, {ID: "secret", Type: "text"}}})
	if len(got.Blocks) != 1 || got.Blocks[0].ID != "intro" || len(got.SetCookies) != 1 || got.SetCookies[0].Name != "hmp_legacy_visited" {
		t.Fatalf("filter result = %+v", got)
	}

	// Cron and action: both reach kv.set, which is denied (not granted).
	sched.mu.Lock()
	fn := sched.fns["plugin:legacy:tick"]
	every := sched.jobs["plugin:legacy:tick"]
	sched.mu.Unlock()
	if every != 5*time.Minute {
		t.Fatalf("cron every = %v", every)
	}
	if err := fn(context.Background()); err == nil || !strings.Contains(err.Error(), "capability_denied") {
		t.Fatalf("cron run = %v", err)
	}
	if _, err := rt.Grant(context.Background(), "legacy", []string{sdk.PermFormsRead}); err != nil {
		t.Fatal(err)
	}
	engine.Fire(context.Background(), sdk.FormSubmitted.Name, sdk.FormSubmittedEvent{FormID: "f", FormSlug: "contact", Fields: map[string]any{"email": "a@b.c"}})
	engine.Wait()
	if !strings.Contains(logs.String(), "capability_denied") {
		t.Fatalf("action did not reach the host:\n%s", logs.String())
	}

	// call_host: the typed error the host wrote is read back by the old guest.
	in := []byte(`{"fn":"kv.get","args":{"key":"k"}}`)
	res, err := rt.Host().Call(context.Background(), "legacy", "call_host", in)
	if err != nil || !strings.Contains(string(res), `"code":"capability_denied"`) || !strings.Contains(string(res), `"capability":"kv"`) {
		t.Fatalf("call_host = %s, %v", res, err)
	}
}
