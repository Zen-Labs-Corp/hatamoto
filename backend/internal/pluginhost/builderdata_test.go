package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

var (
	dsOnce sync.Once
	dsWasm []byte
	dsMan  sdk.Manifest
	dsErr  error
)

// dsBuild builds examples/plugins/datasource-demo once.
func dsBuild(t *testing.T) ([]byte, sdk.Manifest) {
	t.Helper()
	dsOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dsdemo")
		if err != nil {
			dsErr = err
			return
		}
		defer os.RemoveAll(dir)
		out := filepath.Join(dir, WasmFile)
		if dsErr = buildWasm(dir, "../../../examples/plugins/datasource-demo", out); dsErr != nil {
			return
		}
		if dsWasm, dsErr = os.ReadFile(out); dsErr != nil {
			return
		}
		raw, err := os.ReadFile("../../../examples/plugins/datasource-demo/plugin.json")
		if err != nil {
			dsErr = err
			return
		}
		dsErr = json.Unmarshal(raw, &dsMan)
	})
	if dsErr != nil {
		t.Fatal(dsErr)
	}
	return dsWasm, dsMan
}

// dsLoad loads the example under name with config overrides and grants it
// builder.data when grant is set.
func dsLoad(t *testing.T, rt *Runtime, name string, cfg map[string]string, grant bool) {
	t.Helper()
	dsLoadWith(t, rt, name, cfg, grant, nil)
}

// dsLoadWith is dsLoad with a manifest change applied first (mod may be nil).
// The cached manifest's maps are copied, so a change never leaks to other tests.
func dsLoadWith(t *testing.T, rt *Runtime, name string, cfg map[string]string, grant bool, mod func(*sdk.Manifest)) {
	t.Helper()
	wasm, m := dsBuild(t)
	timeouts := map[string]uint64{}
	for k, v := range m.Limits.HookTimeoutsMS {
		timeouts[k] = v
	}
	m.Limits.HookTimeoutsMS = timeouts
	if mod != nil {
		mod(&m)
	}
	m.Name = name
	m.Config = map[string]string{}
	for k, v := range cfg {
		m.Config[k] = v
	}
	if grant {
		if err := rt.Grants().Set(context.Background(), name, []sdk.Permission{sdk.PermBuilderData}); err != nil {
			t.Fatal(err)
		}
	}
	if st := rt.Load(context.Background(), m, wasm); !st.Loaded {
		t.Fatalf("%s: %s", name, st.Error)
	}
}

func req(id, plugin, source string, limit int) render.ExtRequest {
	return render.ExtRequest{ID: id, Source: "ext." + plugin + "." + source, Limit: limit}
}

func countCalls(logs *lockedBuf) int { return strings.Count(logs.String(), "builder.data requests=") }

func TestBuilderDataValueAndItems(t *testing.T) {
	rt, engine, _ := newRuntime(t)
	dsLoad(t, rt, "datasource-demo", map[string]string{"temp": "18°C"}, true)
	fn := ExtSources(engine)
	res := fn(context.Background(), render.ExtBatch{Requests: []render.ExtRequest{
		req("a", "datasource-demo", "temp", 0), req("b", "datasource-demo", "forecast", 3), req("c", "datasource-demo", "link", 0),
	}})
	if r := res["a"]; !r.OK || r.Value != "18°C" {
		t.Fatalf("temp = %+v", r)
	}
	if r := res["b"]; !r.OK || len(r.Items) != 3 || r.Items[0].Title != "Day 1" || r.Items[1].Attrs["temp"] != "17°C" {
		t.Fatalf("forecast = %+v", r)
	}
	if r := res["c"]; !r.OK || r.Value != "https://example.com/docs" {
		t.Fatalf("link = %+v", r)
	}
}

func TestBuilderDataOneCallPerBatch(t *testing.T) {
	rt, engine, logs := newRuntime(t)
	dsLoad(t, rt, "datasource-demo", nil, true)
	var rq []render.ExtRequest
	for i := 0; i < 10; i++ {
		rq = append(rq, req(fmt.Sprintf("r%d", i), "datasource-demo", "temp", 0))
	}
	res := ExtSources(engine)(context.Background(), render.ExtBatch{Requests: rq})
	if len(res) != 10 {
		t.Fatalf("results = %d", len(res))
	}
	if n := countCalls(logs); n != 1 {
		t.Fatalf("wasm calls = %d, want 1", n)
	}
}

func degradedBy(ctx context.Context, f func(context.Context)) int64 {
	ctx, scope, done := render.BindDegradeScope(ctx)
	defer done()
	f(ctx)
	return scope.Count()
}

func TestBuilderDataFailuresDegrade(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		budget time.Duration
	}{{"trap", 2 * time.Second}, {"slow", 2 * time.Second}, {"big", 2 * time.Second}} {
		t.Run(tc.mode, func(t *testing.T) {
			rt, engine, _ := newRuntime(t)
			dsLoad(t, rt, "datasource-demo", map[string]string{"mode": tc.mode}, true)
			start := time.Now()
			var res map[string]render.ExtResult
			n := degradedBy(context.Background(), func(ctx context.Context) {
				res = ExtSources(engine)(ctx, render.ExtBatch{Requests: []render.ExtRequest{req("f", "datasource-demo", "forecast", 0)}})
			})
			if r, ok := res["f"]; ok && r.OK {
				t.Fatalf("%s: result delivered: %+v", tc.mode, r)
			}
			if n == 0 {
				t.Fatalf("%s: render not marked degraded", tc.mode)
			}
			if d := time.Since(start); d > tc.budget {
				t.Fatalf("%s took %v", tc.mode, d)
			}
		})
	}
}

func TestBuilderDataSlowStopsAtManifestLimit(t *testing.T) {
	rt, engine, _ := newRuntime(t)
	dsLoad(t, rt, "datasource-demo", map[string]string{"mode": "slow"}, true)
	start := time.Now()
	ExtSources(engine)(context.Background(), render.ExtBatch{Requests: []render.ExtRequest{req("t", "datasource-demo", "temp", 0)}})
	if d := time.Since(start); d < 250*time.Millisecond || d > 1500*time.Millisecond {
		t.Fatalf("slow plugin ran %v, want about the 300ms manifest limit", d)
	}
}

func TestBuilderDataOwnership(t *testing.T) {
	rt, engine, _ := newRuntime(t)
	dsLoad(t, rt, "ds-a", map[string]string{"temp": "from-a"}, true)
	dsLoad(t, rt, "ds-liar", map[string]string{"mode": "liar", "steal": "r2", "temp": "from-liar"}, true)
	res := ExtSources(engine)(context.Background(), render.ExtBatch{Requests: []render.ExtRequest{
		req("r1", "ds-liar", "temp", 0), req("r2", "ds-a", "temp", 0),
	}})
	if res["r2"].Value != "from-a" {
		t.Fatalf("r2 = %+v: another plugin answered it", res["r2"])
	}
	if res["r1"].Value != "from-liar" {
		t.Fatalf("r1 = %+v", res["r1"])
	}
	// A request for a source the plugin never declared is not forwarded.
	res = ExtSources(engine)(context.Background(), render.ExtBatch{Requests: []render.ExtRequest{req("x", "ds-a", "nope", 0), req("y", "ghost", "temp", 0)}})
	if len(res) != 0 {
		t.Fatalf("undeclared sources answered: %+v", res)
	}
}

func TestBuilderDataNeedsGrantAndRevokes(t *testing.T) {
	rt, engine, logs := newRuntime(t)
	dsLoad(t, rt, "datasource-demo", nil, false)
	if engine.HasFilter(sdk.BuilderData.Name) {
		t.Fatal("filter registered without the grant")
	}
	if got := rt.ExtSourceList(); len(got) != 0 {
		t.Fatalf("sources listed without the grant: %+v", got)
	}
	if n := len(ExtSources(engine)(context.Background(), render.ExtBatch{Requests: []render.ExtRequest{req("a", "datasource-demo", "temp", 0)}})); n != 0 || countCalls(logs) != 0 {
		t.Fatal("ungranted plugin was called")
	}
	if _, err := rt.Grant(context.Background(), "datasource-demo", []sdk.Permission{sdk.PermBuilderData}); err != nil {
		t.Fatal(err)
	}
	if !engine.HasFilter(sdk.BuilderData.Name) || len(rt.ExtSourceList()) != 3 {
		t.Fatalf("grant did not register: filter=%v sources=%v", engine.HasFilter(sdk.BuilderData.Name), rt.ExtSourceList())
	}
	if _, err := rt.Grant(context.Background(), "datasource-demo", nil); err != nil {
		t.Fatal(err)
	}
	if engine.HasFilter(sdk.BuilderData.Name) || len(rt.ExtSourceList()) != 0 {
		t.Fatal("revoke left the filter registered")
	}
}

func TestBuilderDataConcurrent(t *testing.T) {
	rt, engine, _ := newRuntime(t)
	// This checks that concurrent batches get the right answers, not the
	// plugin's speed: with the demo's 300 ms builder.data timeout a loaded CI
	// runner timed out three cold calls, the circuit breaker opened and the
	// other calls came back empty. Speed limits have their own tests.
	dsLoadWith(t, rt, "datasource-demo", nil, true, func(m *sdk.Manifest) {
		m.Limits.HookTimeoutsMS[sdk.BuilderData.Name] = 10_000
	})
	fn := ExtSources(engine)
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res := fn(ctx, render.ExtBatch{Requests: []render.ExtRequest{req("a", "datasource-demo", "temp", 0), req("b", "datasource-demo", "forecast", 2)}})
			if res["a"].Value != "21°C" || len(res["b"].Items) != 2 {
				errs <- fmt.Sprintf("%+v", res)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestCheckBuilderResult(t *testing.T) {
	long := strings.Repeat("x", sdk.MaxBuilderDataString+1)
	attrs := map[string]string{}
	for i := 0; i <= sdk.MaxBuilderDataAttrs; i++ {
		attrs[fmt.Sprint("k", i)] = "v"
	}
	for name, tc := range map[string]struct {
		kind string
		r    sdk.BuilderDataResult
		bad  bool
	}{
		"ok value":       {sdk.BuilderSourceValue, sdk.BuilderDataResult{Value: "x"}, false},
		"long value":     {sdk.BuilderSourceValue, sdk.BuilderDataResult{Value: long}, true},
		"bad utf8":       {sdk.BuilderSourceValue, sdk.BuilderDataResult{Value: "\xff"}, true},
		"items on value": {sdk.BuilderSourceValue, sdk.BuilderDataResult{Items: []sdk.BuilderDataItem{{}}}, true},
		"value on items": {sdk.BuilderSourceItems, sdk.BuilderDataResult{Value: "x"}, true},
		"long title":     {sdk.BuilderSourceItems, sdk.BuilderDataResult{Items: []sdk.BuilderDataItem{{Title: long}}}, true},
		"many attrs":     {sdk.BuilderSourceItems, sdk.BuilderDataResult{Items: []sdk.BuilderDataItem{{Attrs: attrs}}}, true},
		"bad date":       {sdk.BuilderSourceItems, sdk.BuilderDataResult{Items: []sdk.BuilderDataItem{{PublishedAt: "yesterday"}}}, true},
		"good item":      {sdk.BuilderSourceItems, sdk.BuilderDataResult{Items: []sdk.BuilderDataItem{{Title: "t", PublishedAt: "2026-01-02"}}}, false},
		"plugin error":   {sdk.BuilderSourceValue, sdk.BuilderDataResult{Error: "boom"}, false},
	} {
		if err := checkBuilderResult(tc.kind, tc.r); (err != nil) != tc.bad {
			t.Errorf("%s: err = %v, want bad=%v", name, err, tc.bad)
		}
	}
}
