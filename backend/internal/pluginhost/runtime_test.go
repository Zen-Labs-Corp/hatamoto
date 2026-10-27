package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

var (
	helloDir    string // examples/plugins/hello/build, produced by TestMain
	hostileWasm []byte
	cacheDir    string // shared compiled-code cache: compiling dominates test time
)

// buildWasm builds a Go main package for wasip1 as a c-shared reactor.
func buildWasm(dir, pkgDir, out string) error {
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".")
	cmd.Dir = pkgDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		return errors.New(err.Error() + "\n" + string(b))
	}
	return nil
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pluginhost")
	if err != nil {
		panic(err)
	}
	// The reference plugin is built here rather than committed.
	helloDir = filepath.Join(dir, "plugins", "hello")
	if err := os.MkdirAll(helloDir, 0o755); err != nil {
		panic(err)
	}
	if err := buildWasm(dir, "../../../examples/plugins/hello", filepath.Join(helloDir, WasmFile)); err != nil {
		panic("building examples/plugins/hello: " + err.Error())
	}
	manifest, err := os.ReadFile("../../../examples/plugins/hello/plugin.json")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(helloDir, ManifestFile), manifest, 0o644); err != nil {
		panic(err)
	}
	cacheDir = filepath.Join(dir, "cache")
	out := filepath.Join(dir, "hostile.wasm")
	if err := buildWasm(dir, "../wasmhost/testdata/hostile", out); err != nil {
		panic("building hostile guest: " + err.Error())
	}
	if hostileWasm, err = os.ReadFile(out); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// lockedBuf is a log sink safe for concurrent writers.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newRuntime(t *testing.T) (*Runtime, *hooks.Engine, *lockedBuf) {
	t.Helper()
	logs := &lockedBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	engine := hooks.New(log)
	rt, err := NewRuntime(log, engine, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	return rt, engine, logs
}

func helloLoaded(t *testing.T, rt *Runtime) {
	t.Helper()
	sts := rt.LoadDir(context.Background(), filepath.Dir(helloDir))
	if len(sts) != 1 || !sts[0].Loaded {
		t.Fatalf("hello did not load: %+v", sts)
	}
}

func hostile(t *testing.T, rt *Runtime, name, mode string) {
	t.Helper()
	m := sdk.Manifest{Name: name, Version: "0.1.0", SDK: "^0.5", Config: map[string]string{"mode": mode},
		Limits: sdk.Limits{MemoryPages: 256, HookTimeoutsMS: map[string]uint64{"entity.published": 400}}}
	if st := rt.Load(context.Background(), m, hostileWasm); !st.Loaded {
		t.Fatalf("%s: %s", name, st.Error)
	}
}

func enrich(e *hooks.Engine) sdk.JSONLDDoc {
	in := sdk.JSONLDDoc{
		Entity: sdk.EntityRef{ID: "e1", SchemaSlug: "post", Slug: "hi", Name: "Hi"},
		Doc:    map[string]any{"@type": "Article", "name": "Hi"},
	}
	return e.Apply(context.Background(), sdk.JSONLDEnrich.Name, in).(sdk.JSONLDDoc)
}

// The reference plugin, built from source, loads from a directory and handles
// both hooks through the real host.
func TestHelloLoadsAndHandlesHooks(t *testing.T) {
	rt, engine, logs := newRuntime(t)
	helloLoaded(t, rt)

	st := rt.Statuses()[0]
	if st.Name != "hello" || !st.Loaded || strings.Join(st.Hooks, ",") != "entity.published,jsonld.enrich" {
		t.Fatalf("status = %+v", st)
	}

	out := enrich(engine)
	if out.Doc["x-hello"] != "konnichiwa" || out.Doc["@type"] != "Article" || out.Entity.Slug != "hi" {
		t.Fatalf("jsonld.enrich result: %+v", out)
	}

	engine.Fire(context.Background(), sdk.EntityPublished.Name,
		sdk.EntityEvent{ID: "e1", SchemaSlug: "post", Slug: "hi", Name: "Hi", Status: "published", Version: 3})
	engine.Wait()
	if !strings.Contains(logs.String(), "published post/hi (e1)") {
		t.Fatalf("entity.published not handled; logs:\n%s", logs.String())
	}
}

// Hostile plugins sitting before and after hello on every hook: the request
// still gets hello's result, within the time limits, and nothing leaks.
func TestHostilePluginsCannotAffectRequests(t *testing.T) {
	rt, engine, logs := newRuntime(t)
	for _, mode := range []string{"loop", "alloc", "panic", "garbage", "error"} {
		hostile(t, rt, "evil-"+mode, mode)
	}
	helloLoaded(t, rt)

	start := time.Now()
	out := enrich(engine)
	if out.Doc["x-hello"] != "konnichiwa" || out.Doc["name"] != "Hi" {
		t.Fatalf("request result damaged by hostile plugins: %+v", out)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("request took %s", d)
	}
	for _, mode := range []string{"loop", "alloc", "panic", "garbage", "error"} {
		if got := rt.Host().Discarded("evil-" + mode); mode != "garbage" && mode != "error" && got < 1 {
			t.Errorf("evil-%s: failing instance not discarded (%d)", mode, got)
		}
	}
	if !strings.Contains(logs.String(), "filter failed") {
		t.Errorf("failures must be logged:\n%s", logs.String())
	}

	// Actions: hostile handlers fail in the background, hello's still runs.
	engine.Fire(context.Background(), sdk.EntityPublished.Name, sdk.EntityEvent{ID: "e2", SchemaSlug: "post", Slug: "x"})
	engine.Wait()
	if !strings.Contains(logs.String(), "published post/x (e2)") {
		t.Fatalf("hello's action did not run next to hostile plugins:\n%s", logs.String())
	}

	// And a second request works: pools recovered.
	if out := enrich(engine); out.Doc["x-hello"] != "konnichiwa" {
		t.Fatalf("second request: %+v", out)
	}
}

func TestBeforeSaveRejectAndFailOpen(t *testing.T) {
	rt, engine, _ := newRuntime(t)
	hostile(t, rt, "crashy", "panic")
	draft := sdk.EntityDraft{Operation: "create", SchemaSlug: "post", Name: "n", Slug: "n"}

	// A crash is fail-open: the save goes ahead with the draft untouched.
	out, err := engine.ApplyStrict(context.Background(), sdk.EntityBeforeSave.Name, draft)
	if err != nil || out.(sdk.EntityDraft).Name != "n" {
		t.Fatalf("crash must fail open: %+v %v", out, err)
	}

	// A deliberate rejection refuses the save with its reason.
	hostile(t, rt, "gatekeeper", "reject")
	_, err = engine.ApplyStrict(context.Background(), sdk.EntityBeforeSave.Name, draft)
	var rej *sdk.Reject
	if !errors.As(err, &rej) || rej.Reason != "no thanks" {
		t.Fatalf("want a rejection with its reason, got %v", err)
	}
}

func TestLoadDirIsolatesBadPlugins(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, manifest string, wasm []byte) {
		d := filepath.Join(dir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, ManifestFile), []byte(manifest), 0o644)
		if wasm != nil {
			os.WriteFile(filepath.Join(d, WasmFile), wasm, 0o644)
		}
	}
	good, _ := json.Marshal(sdk.Manifest{Name: "good", Version: "1.0.0", SDK: "^0.5"})
	mk("good", string(good), hostileWasm)
	mk("badjson", `{nope`, hostileWasm)
	mk("nowasm", `{"name":"nowasm","version":"1.0.0","sdk":"^0.5"}`, nil)
	mk("garbage", `{"name":"garbage","version":"1.0.0","sdk":"^0.5"}`, []byte("not wasm"))
	mk("oldsdk", `{"name":"oldsdk","version":"1.0.0","sdk":"^0.4"}`, hostileWasm)
	mk("mismatch", `{"name":"other","version":"1.0.0","sdk":"^0.5"}`, hostileWasm)
	mk("tiny", `{"name":"tiny","version":"1.0.0","sdk":"^0.5","limits":{"memory_pages":1}}`, hostileWasm)

	rt, _, _ := newRuntime(t)
	loaded := map[string]bool{}
	for _, st := range rt.LoadDir(context.Background(), dir) {
		loaded[st.Name] = st.Loaded
		if !st.Loaded && st.Error == "" {
			t.Errorf("%s failed without an error", st.Name)
		}
	}
	for name, want := range map[string]bool{"good": true, "badjson": false, "nowasm": false, "garbage": false,
		"oldsdk": false, "mismatch": false, "tiny": false} {
		if got, ok := loaded[name]; !ok || got != want {
			t.Errorf("%s loaded=%v (present=%v), want %v", name, got, ok, want)
		}
	}
	if got := rt.Host().Loaded(); len(got) != 1 || got[0] != "good" {
		t.Fatalf("host has %v loaded", got)
	}
	if got := rt.LoadDir(context.Background(), filepath.Join(dir, "missing")); got != nil {
		t.Fatalf("missing dir: %v", got)
	}
}

func TestUnloadRemovesHooks(t *testing.T) {
	rt, engine, _ := newRuntime(t)
	helloLoaded(t, rt)
	rt.Unload(context.Background(), "hello")
	if out := enrich(engine); out.Doc["x-hello"] != nil {
		t.Fatalf("unloaded plugin still enriches: %+v", out)
	}
	if len(rt.Statuses()) != 0 {
		t.Fatal("status of an unloaded plugin must go")
	}
}

// The real request shape: one 2 s chain deadline and a looping plugin with the
// default timeout sorted BEFORE hello. Hello must still run, every time, and
// once the breaker opens the page costs next to nothing.
func TestSpinningPluginDoesNotStarveLaterPlugins(t *testing.T) {
	rt, engine, logs := newRuntime(t)
	m := sdk.Manifest{Name: "aaa", Version: "0.1.0", SDK: "^0.5", Config: map[string]string{"mode": "loop"}}
	if st := rt.Load(context.Background(), m, hostileWasm); !st.Loaded {
		t.Fatal(st.Error)
	}
	helloLoaded(t, rt)

	page := func() (sdk.JSONLDDoc, time.Duration) {
		ctx, cancel := context.WithTimeout(context.Background(), filterTimeout)
		defer cancel()
		in := sdk.JSONLDDoc{
			Entity: sdk.EntityRef{ID: "e1", SchemaSlug: "post", Slug: "hi", Name: "Hi"},
			Doc:    map[string]any{"@type": "Article", "name": "Hi"},
		}
		start := time.Now()
		out := engine.Apply(ctx, sdk.JSONLDEnrich.Name, in).(sdk.JSONLDDoc)
		return out, time.Since(start)
	}
	for i := range 4 {
		out, d := page()
		if out.Doc["x-hello"] != "konnichiwa" {
			t.Fatalf("page %d: hello starved by the spinning plugin (%s)", i, d)
		}
		if d > filterTimeout+500*time.Millisecond {
			t.Fatalf("page %d took %s", i, d)
		}
		if i == 3 && d > 300*time.Millisecond {
			t.Fatalf("breaker did not open: page %d still took %s", i, d)
		}
	}
	if !strings.Contains(logs.String(), "disabled after repeated failures") {
		t.Fatalf("breaker opening must be logged:\n%s", logs.String())
	}
}

// Tiny JSON that would decode into millions of Go values is refused, and the
// request carries on unchanged.
func TestOutputAmplificationIsContained(t *testing.T) {
	rt, engine, _ := newRuntime(t)
	m := sdk.Manifest{Name: "flood", Version: "0.1.0", SDK: "^0.5",
		Config: map[string]string{"mode": "bigtags", "bytes": "3000000"},
		Limits: sdk.Limits{MemoryPages: 256, MaxOutputBytes: sdk.MaxOutputBytes}}
	if st := rt.Load(context.Background(), m, hostileWasm); !st.Loaded {
		t.Fatal(st.Error)
	}
	in := sdk.HeadContext{Path: "/x", Tags: []sdk.HeadTag{{Kind: "meta"}}}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out := engine.Apply(context.Background(), sdk.RenderHead.Name, in).(sdk.HeadContext)
	runtime.ReadMemStats(&after)
	if len(out.Tags) != 1 {
		t.Fatalf("flood reached the page: %d tags", len(out.Tags))
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 200<<20 {
		t.Fatalf("host allocated %d MiB", got>>20)
	}
}

func TestShapeLimits(t *testing.T) {
	for name, tc := range map[string]struct {
		raw string
		ok  bool
	}{
		"head 64 tags": {`{"tags":[` + strings.TrimSuffix(strings.Repeat(`{},`, 64), ",") + `]}`, true},
		"head 65 tags": {`{"tags":[` + strings.TrimSuffix(strings.Repeat(`{},`, 65), ",") + `]}`, false},
		"deep":         {strings.Repeat("[", 100) + strings.Repeat("]", 100), false},
		"truncated":    {`{"tags":[{}`, false},
		"empty object": {`{}`, true},
	} {
		if err := headShape.check([]byte(tc.raw)); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", name, err, tc.ok)
		}
	}
}

// A guest that returns only what it changed must not wipe the other fields.
func TestPartialOutputKeepsOtherFields(t *testing.T) {
	draft := sdk.EntityDraft{Operation: "update", SchemaSlug: "post", Name: "Old", Slug: "old",
		Attributes: json.RawMessage(`{"body":"keep me"}`)}
	in, _ := json.Marshal(draft)
	got, err := decodeOutput(draft, in, []byte(`{"name":"New title"}`))
	if err != nil {
		t.Fatal(err)
	}
	d := got.(sdk.EntityDraft)
	if d.Name != "New title" || d.Slug != "old" || string(d.Attributes) != `{"body":"keep me"}` || d.SchemaSlug != "post" {
		t.Fatalf("partial output damaged the draft: %+v", d)
	}
	if _, err := decodeOutput(draft, in, []byte(`[1]`)); err == nil {
		t.Fatal("non-object output accepted")
	}
}

func TestReloadDoesNotDuplicateHooks(t *testing.T) {
	rt, engine, logs := newRuntime(t)
	helloLoaded(t, rt)
	helloLoaded(t, rt)
	engine.Fire(context.Background(), sdk.EntityPublished.Name, sdk.EntityEvent{ID: "e9", SchemaSlug: "post", Slug: "once"})
	engine.Wait()
	if n := strings.Count(logs.String(), "published post/once (e9)"); n != 1 {
		t.Fatalf("hook ran %d times after a re-load", n)
	}

	// A failed re-load keeps the old module running and says so.
	bad, _ := os.ReadFile(filepath.Join(helloDir, ManifestFile))
	var m sdk.Manifest
	_ = json.Unmarshal(bad, &m)
	st := rt.Load(context.Background(), m, []byte("not wasm"))
	if !st.Loaded || !strings.Contains(st.Error, "reload failed") {
		t.Fatalf("failed reload status: %+v", st)
	}
	if out := enrich(engine); out.Doc["x-hello"] != "konnichiwa" {
		t.Fatal("old module stopped after a failed reload")
	}
}

func TestSymlinkedPluginDirAndListDirErrors(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ManifestFile), []byte(`{"name":"linked","version":"1.0.0","sdk":"^0.5"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, WasmFile), hostileWasm, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(src, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if sts, err := ListDir(dir); err != nil || len(sts) != 1 || sts[0].Name != "linked" {
		t.Fatalf("ListDir ignored the symlink: %+v %v", sts, err)
	}
	rt, _, _ := newRuntime(t)
	if sts := rt.LoadDir(context.Background(), dir); len(sts) != 1 || !sts[0].Loaded {
		t.Fatalf("symlinked plugin not loaded: %+v", sts)
	}
	if _, err := ListDir(filepath.Join(dir, "linked", ManifestFile)); err == nil {
		t.Fatal("ListDir on a non-directory must report the error")
	}
}

// A plugin built for a newer SDK than the host serves is refused with the
// shared compatibility message.
func TestLoadRefusesNewerSDKRange(t *testing.T) {
	rt, _, _ := newRuntime(t)
	m := sdk.Manifest{Name: "future", Version: "0.1.0", SDK: "^99.0"}
	st := rt.Load(context.Background(), m, hostileWasm)
	if st.Loaded {
		t.Fatal("plugin with an sdk range the host does not serve loaded")
	}
	for _, part := range []string{"^99.0", sdk.Version, "serves plugins built for SDK"} {
		if !strings.Contains(st.Error, part) {
			t.Errorf("error %q lacks %q", st.Error, part)
		}
	}
}
