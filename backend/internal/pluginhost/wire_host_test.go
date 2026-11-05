package pluginhost

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// The expectations in this file are string literals written against the
// encoding/json v1 implementation. They pin the bytes that cross the host and
// guest boundary, so a later change of encoder cannot alter them unnoticed.

// recorderSource is a guest that forwards its input, unchanged, to hm_call and
// answers "{}". echo_call returns hm_call's reply as its own output.
const recorderSource = `//go:build wasip1

package main

import "github.com/extism/go-pdk"

func main() {}

//go:wasmimport extism:host/user hm_call
func hmCall(offset uint64) uint64

func send(req []byte) []byte {
	in := pdk.AllocateBytes(req)
	defer in.Free()
	out := pdk.FindMemory(hmCall(in.Offset()))
	defer out.Free()
	return out.ReadBytes()
}

func forward() int32 {
	send(pdk.Input())
	pdk.Output([]byte("{}"))
	return 0
}

//go:wasmexport on_form_submitted
func onFormSubmitted() int32 { return forward() }

//go:wasmexport filter_render_page
func filterRenderPage() int32 { return forward() }

//go:wasmexport http_handle
func httpHandle() int32 { return forward() }

//go:wasmexport on_cron_tick
func onCronTick() int32 { return forward() }

//go:wasmexport func_echo
func funcEcho() int32 { return forward() }

//go:wasmexport on_run_step
func onRunStep() int32 { return forward() }

//go:wasmexport echo_call
func echoCall() int32 {
	pdk.Output(send(pdk.Input()))
	return 0
}
`

var (
	recorderOnce sync.Once
	recorderWasm []byte
	recorderErr  error
)

func buildRecorder(t *testing.T) []byte {
	t.Helper()
	recorderOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wire-recorder")
		if err != nil {
			recorderErr = err
			return
		}
		defer os.RemoveAll(dir)
		for _, f := range []string{"go.mod", "go.sum"} {
			b, err := os.ReadFile(filepath.Join("..", "wasmhost", "testdata", "hostile", f))
			if err != nil {
				recorderErr = err
				return
			}
			if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
				recorderErr = err
				return
			}
		}
		if recorderErr = os.WriteFile(filepath.Join(dir, "main.go"), []byte(recorderSource), 0o644); recorderErr != nil {
			return
		}
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", "recorder.wasm", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK=off")
		if b, err := cmd.CombinedOutput(); err != nil {
			recorderErr = fmt.Errorf("%w\n%s", err, b)
			return
		}
		recorderWasm, recorderErr = os.ReadFile(filepath.Join(dir, "recorder.wasm"))
	})
	if recorderErr != nil {
		t.Fatal(recorderErr)
	}
	return recorderWasm
}

type wireRig struct {
	rt     *Runtime
	engine *hooks.Engine
	sched  *fakeScheduler

	mu   sync.Mutex
	seen [][]byte // what the guest sent to hm_call, in order
}

func (w *wireRig) hostCall(_ context.Context, _ string, in []byte) []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, append([]byte(nil), in...))
	return []byte(`{"ok":true}`)
}

// take returns the one payload the guest forwarded since the last call.
func (w *wireRig) take(t *testing.T) string {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.seen) != 1 {
		t.Fatalf("guest forwarded %d payloads, want 1: %q", len(w.seen), w.seen)
	}
	s := string(w.seen[0])
	w.seen = nil
	return s
}

func newWireRig(t *testing.T) *wireRig {
	t.Helper()
	wasm := buildRecorder(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := hooks.New(log)
	rt, err := NewRuntime(log, engine, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	sched := &fakeScheduler{}
	rt.Configure(Config{Services: Services{Fetcher: NewFetcher()}, Scheduler: sched})
	w := &wireRig{rt: rt, engine: engine, sched: sched}
	rt.Host().SetHostCall(w.hostCall)
	m := sdk.Manifest{Name: "wire", Version: "0.1.0", SDK: "^0.6",
		Permissions: []string{sdk.PermFormsRead}, Funcs: []string{"echo"}, Cookies: []string{"a", "b"},
		Cron:   []sdk.CronJob{{Name: "tick", Every: "5m"}},
		Routes: []sdk.Route{{Method: "POST", Path: "/in/{id}"}}}
	if st := rt.Load(context.Background(), m, wasm); !st.Loaded {
		t.Fatal(st.Error)
	}
	if _, err := rt.Grant(context.Background(), "wire", []string{sdk.PermFormsRead}); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWireHostToGuestPayloads(t *testing.T) {
	w := newWireRig(t)
	ctx := context.Background()

	t.Run("action", func(t *testing.T) {
		w.engine.Fire(ctx, sdk.FormSubmitted.Name, sdk.FormSubmittedEvent{
			FormID: "f1", FormSlug: "contact", SubmissionID: "s1",
			Fields:      map[string]any{"b": "<x>", "a": 1, "n": nil, "ü": "a\xffb"},
			SubmittedAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		})
		w.engine.Wait()
		const want = `{"form_id":"f1","form_slug":"contact","submission_id":"s1","fields":{"a":1,"b":"\u003cx\u003e","n":null,"ü":"a` + "\xef\xbf\xbd" + `b"},"submitted_at":"2024-01-02T03:04:05Z"}`
		if got := w.take(t); got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})

	t.Run("filter", func(t *testing.T) {
		pf := NewPageFilter(w.engine)
		pf.Apply(ctx, sdk.PageContext{Path: "/p?x=<1>", Cookies: map[string]string{"hmp_wire_b": "2", "hmp_wire_a": "1", "session": "s"}, Blocks: nil})
		const want = `{"path":"/p?x=\u003c1\u003e","cookies":{"a":"1","b":"2"},"blocks":null}`
		if got := w.take(t); got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})

	t.Run("route", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/x/in/7?b=2&a=1&a=%3C", strings.NewReader("h\u00e9llo<"))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("X-Request-Id", "r-1")
		rec := httptest.NewRecorder()
		w.rt.AdminRoutes().Serve(rec, req, "wire", "in/7", &sdk.HTTPUser{ID: "u1", Role: "owner"})
		const want = `{"method":"POST","path":"/in/7","pattern":"/in/{id}","path_values":{"id":"7"},` +
			`"query":{"a":["1","\u003c"],"b":["2"]},"headers":{"content-type":"text/plain","x-request-id":"r-1"},` +
			`"body":"héllo\u003c","user":{"id":"u1","role":"owner"}}`
		if got := w.take(t); got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}

		req = httptest.NewRequest("POST", "/x/in/7", strings.NewReader("\xff\xfe"))
		w.rt.AdminRoutes().Serve(httptest.NewRecorder(), req, "wire", "in/7", &sdk.HTTPUser{ID: "u1", Role: "owner"})
		const wantBin = `{"method":"POST","path":"/in/7","pattern":"/in/{id}","path_values":{"id":"7"},"body_b64":"//4=","user":{"id":"u1","role":"owner"}}`
		if got := w.take(t); got != wantBin {
			t.Fatalf("got  %s\nwant %s", got, wantBin)
		}
	})

	t.Run("cron", func(t *testing.T) {
		w.sched.mu.Lock()
		fn := w.sched.fns["plugin:wire:tick"]
		w.sched.mu.Unlock()
		if err := fn(ctx); err != nil {
			t.Fatal(err)
		}
		got := w.take(t)
		const prefix, suffix = `{"name":"tick","scheduled_at":"`, `Z"}`
		if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) || len(got) != len(prefix)+len("2006-01-02T15:04:05")+len(suffix) {
			t.Fatalf("cron event = %s", got)
		}
	})

	t.Run("func args", func(t *testing.T) {
		out, err := w.rt.templateFunc("wire", "echo", "func_echo")("a<", 1, 2.5, nil, true, map[string]any{"k": "v"}, []string{"x"}, []string(nil))
		if err != nil || out != "{}" {
			t.Fatalf("func = %q, %v", out, err)
		}
		const want = `["a\u003c",1,2.5,null,true,{"k":"v"},["x"],null]`
		if got := w.take(t); got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})
}

// The host writes some payloads the guest never echoes (a run step, the
// progress and changeset documents). They are pinned here by value, under the
// encoder the host uses now and the pinned set it moves to.
func TestWireHostPayloadsByValue(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"run step", sdk.RunStep{RunID: "r1", Phase: "fetch"}, `{"run_id":"r1","phase":"fetch","cursor":""}`},
		{"run step cursor", sdk.RunStep{RunID: "r1", Phase: "transform", Cursor: "c<1"}, `{"run_id":"r1","phase":"transform","cursor":"c\u003c1"}`},
		{"call error", sdk.CallResponse{Error: &sdk.HostError{Code: "x", Message: "m"}}, `{"ok":false,"error":{"code":"x","message":"m"}}`},
	}
	for _, c := range cases {
		v1, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		pinned, err := jsonx.MarshalPinned(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(v1) != c.want || string(pinned) != c.want {
			t.Errorf("%s:\n v1     %s\n pinned %s\n want   %s", c.name, v1, pinned, c.want)
		}
	}
}

// wire.result and wire.args are test-only host functions.
func withWireFns(t *testing.T, result func() any) {
	t.Helper()
	hostFns["wire.result"] = hostFn{run: func(context.Context, *API, *callEnv, json.RawMessage) (any, error) { return result(), nil }}
	hostFns["wire.args"] = hostFn{run: func(_ context.Context, _ *API, _ *callEnv, raw json.RawMessage) (any, error) {
		args, err := decodeArgs[wireArgs](raw)
		if err != nil {
			return nil, err
		}
		return args, nil
	}}
	t.Cleanup(func() { delete(hostFns, "wire.result"); delete(hostFns, "wire.args") })
}

type wireArgs struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Tags  []string `json:"tags"`
}

func wireAPI(t *testing.T) *API {
	t.Helper()
	g := NewGrantStore(nil)
	if err := g.Set(context.Background(), "p", []string{sdk.PermLog, sdk.PermKV}); err != nil {
		t.Fatal(err)
	}
	return NewAPI(Services{}, g, func(string) (sdk.Manifest, bool) { return sdk.Manifest{Name: "p"}, true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestWireCallResponses(t *testing.T) {
	var result any
	withWireFns(t, func() any { return result })
	a := wireAPI(t)
	call := func(in string) string { return string(a.Call(context.Background(), "p", []byte(in))) }

	for _, c := range []struct {
		name   string
		result any
		want   string
	}{
		{"raw result is compacted and escaped", json.RawMessage("{\n \"b\": 1,\n \"a\" : \"<x>\" }"), `{"ok":true,"result":{"b":1,"a":"\u003cx\u003e"}}`},
		{"nil slice", []string(nil), `{"ok":true,"result":null}`},
		{"nil map", map[string]int(nil), `{"ok":true,"result":null}`},
		{"empty slice", []string{}, `{"ok":true,"result":[]}`},
		{"map keys are sorted", map[string]int{"b": 1, "a": 2, "B": 3}, `{"ok":true,"result":{"B":3,"a":2,"b":1}}`},
		{"invalid UTF-8", "a\xffb", `{"ok":true,"result":"a` + "\xef\xbf\xbd" + `b"}`},
		{"line separators", "a\u2028b\u2029", `{"ok":true,"result":"a\u2028b\u2029"}`},
		{"empty result", map[string]any{}, `{"ok":true,"result":{}}`},
		{"unencodable", make(chan int), `{"ok":false,"error":{"code":"internal","message":"result not encodable"}}`},
	} {
		result = c.result
		if got := call(`{"fn":"wire.result"}`); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}

	for _, c := range []struct{ name, in, want string }{
		{"not JSON", `nope`, `{"ok":false,"error":{"code":"invalid_argument","message":"request is not a JSON {fn, args}"}}`},
		{"unknown function", `{"fn":"nope"}`, `{"ok":false,"error":{"code":"unknown_function","message":"no host function \"nope\""}}`},
		{"denied", `{"fn":"mail.send"}`, `{"ok":false,"error":{"code":"capability_denied","capability":"mail.send","message":"mail.send needs the \"mail.send\" permission, which the owner has not granted"}}`},
	} {
		if got := call(c.in); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
}

// How the host reads the bytes a guest writes: whatever v1 accepted from an
// untrusted sender stays accepted.
func TestWireHostReadsGuestBytes(t *testing.T) {
	withWireFns(t, func() any { return nil })
	a := wireAPI(t)
	call := func(in string) sdk.CallResponse {
		var resp sdk.CallResponse
		if err := json.Unmarshal(a.Call(context.Background(), "p", []byte(in)), &resp); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		return resp
	}
	ok := func(in, want string) {
		t.Helper()
		resp := call(in)
		if !resp.OK || string(resp.Result) != want {
			t.Errorf("%s:\n got  %+v %s\n want %s", in, resp.Error, resp.Result, want)
		}
	}
	bad := func(in, code, msgPart string) {
		t.Helper()
		resp := call(in)
		if resp.OK || resp.Error == nil || resp.Error.Code != code || !strings.Contains(resp.Error.Message, msgPart) {
			t.Errorf("%s: got %+v, want %s containing %q", in, resp, code, msgPart)
		}
	}

	// Names match without regard to case, in the request and in the arguments.
	ok(`{"FN":"wire.args","ARGS":{"NAME":"x","Count":2}}`, `{"name":"x","count":2,"tags":null}`)
	// A repeated name: the last one wins.
	ok(`{"fn":"nope","fn":"wire.args","args":{"name":"a","name":"b","count":1,"Count":3}}`, `{"name":"b","count":3,"tags":null}`)
	// Invalid UTF-8 is read as U+FFFD; null members leave the zero value.
	ok(`{"fn":"wire.args","args":{"name":"a`+"\xff"+`b","count":null,"tags":null}}`, `{"name":"a`+"\xef\xbf\xbd"+`b","count":0,"tags":null}`)
	ok(`{"fn":"wire.args","args":null}`, `{"name":"","count":0,"tags":null}`)
	ok(`{"fn":"wire.args"}`, `{"name":"","count":0,"tags":null}`)
	ok(`{"fn":"wire.args","args":{"tags":["a",null]}}`, `{"name":"","count":0,"tags":["a",""]}`)
	bad(`{"fn":"`+"\xff"+`"}`, sdk.ErrUnknownFunction, "no host function")
	// An argument the function does not know is refused, whatever its case.
	bad(`{"fn":"wire.args","args":{"name":"a","extra":1}}`, sdk.ErrInvalidArgument, "bad arguments")
	bad(`{"fn":"wire.args","args":{"Extra":1}}`, sdk.ErrInvalidArgument, "bad arguments")
	bad(`{"fn":"wire.args","args":{"count":"x"}}`, sdk.ErrInvalidArgument, "bad arguments")
	bad(`{"fn":"wire.args","args":[1]}`, sdk.ErrInvalidArgument, "bad arguments")
}

func TestWireKVSetValue(t *testing.T) {
	a := wireAPI(t)
	call := func(in string) sdk.CallResponse {
		var resp sdk.CallResponse
		if err := json.Unmarshal(a.Call(context.Background(), "p", []byte(in)), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// A value that is valid JSON gets past the check (and then finds no
	// database); one that is not is refused with invalid_argument.
	for _, v := range []string{`{"a":1}`, `{"a":1,"a":2}`, `"a` + "\xff" + `b"`, `null`, ` [1, 2] `, `1e400`} {
		if resp := call(`{"fn":"kv.set","args":{"key":"k","value":` + v + `}}`); resp.Error == nil || resp.Error.Code == sdk.ErrInvalidArgument {
			t.Errorf("value %q was refused: %+v", v, resp.Error)
		}
	}
	if resp := call(`{"fn":"kv.set","args":{"key":"k"}}`); resp.Error == nil || resp.Error.Code != sdk.ErrInvalidArgument || !strings.Contains(resp.Error.Message, "value: JSON") {
		t.Errorf("missing value: %+v", resp.Error)
	}
	if resp := call(`{"fn":"kv.set","args":{"key":"k","value":1,"extra":true}}`); resp.Error == nil || resp.Error.Code != sdk.ErrInvalidArgument || !strings.Contains(resp.Error.Message, "bad arguments") {
		t.Errorf("unknown member: %+v", resp.Error)
	}
}

func TestWireDecodeOutput(t *testing.T) {
	type doc struct {
		Path  string            `json:"path"`
		Title string            `json:"title,omitempty"`
		Tags  []string          `json:"tags"`
		Meta  map[string]string `json:"meta"`
	}
	in := []byte(`{"path":"/a","title":"T","tags":["x"],"meta":{"k":"v"}}`)
	base := doc{Path: "/a", Title: "T", Tags: []string{"x"}, Meta: map[string]string{"k": "v"}}
	for _, c := range []struct {
		name, out string
		want      doc
	}{
		{"top-level null keeps the input", `null`, base},
		{"empty object keeps the input", `{}`, base},
		{"a returned field replaces the input's", `{"title":"U"}`, doc{Path: "/a", Title: "U", Tags: []string{"x"}, Meta: map[string]string{"k": "v"}}},
		{"null member zeroes the field", `{"title":null,"tags":null}`, doc{Path: "/a", Meta: map[string]string{"k": "v"}}},
		{"duplicate names: the last one wins", `{"title":"U","title":"V"}`, doc{Path: "/a", Title: "V", Tags: []string{"x"}, Meta: map[string]string{"k": "v"}}},
		{"case variants sort before the input's lower-case key, which wins", `{"Title":"U","TITLE":"V"}`, base},
		{"a case variant of an input key loses to it", `{"PATH":"/b"}`, doc{Path: "/a", Title: "T", Tags: []string{"x"}, Meta: map[string]string{"k": "v"}}},
		{"invalid UTF-8", `{"title":"a` + "\xff" + `b"}`, doc{Path: "/a", Title: "a\ufffdb", Tags: []string{"x"}, Meta: map[string]string{"k": "v"}}},
		{"unknown members are ignored", `{"zzz":1}`, base},
	} {
		got, err := decodeOutput(doc{}, in, []byte(c.out))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if g, _ := json.Marshal(got); string(g) != mustJSON(c.want) {
			t.Errorf("%s:\n got  %s\n want %s", c.name, g, mustJSON(c.want))
		}
	}
	for _, out := range []string{`[1]`, `"x"`, `{"title":1}`, `{`, ``, `{"a":1} {"b":2}`} {
		if _, err := decodeOutput(doc{}, in, []byte(out)); err == nil {
			t.Errorf("output %q was accepted", out)
		}
	}
	// v1 never checked raw values for duplicate names: a nested duplicate
	// in a returned member must survive, last one included.
	type rawDoc struct {
		Path string         `json:"path"`
		Meta jsontext.Value `json:"meta"`
	}
	raw, err := decodeOutput(rawDoc{}, []byte(`{"path":"/a","meta":{"x":1,"x":2}}`), []byte(`{"meta":{"k":{"a":1,"a":2},"k":3}}`))
	if err != nil {
		t.Errorf("nested duplicate names: %v", err)
	} else if g := raw.(rawDoc); g.Path != "/a" || string(g.Meta) != `{"k":{"a":1,"a":2},"k":3}` {
		t.Errorf("nested duplicate names: got %s %s", g.Path, g.Meta)
	}
	if err := defaultShape.check([]byte(`{"a":1,"a":"` + "\xff" + `"}`)); err != nil {
		t.Errorf("shape check refused duplicate names or invalid UTF-8: %v", err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestWireDecodeQueryArgs(t *testing.T) {
	got, err := DecodeArgs(json.RawMessage(`[1, -0, 2.0, 1e2, 9007199254740993, 12345678901234567890, -9223372036854775808, "s", true, null, {"b":[2,"x",null],"a":1}, []]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []any{int64(1), int64(0), float64(2), float64(100), int64(9007199254740993), float64(12345678901234567890),
		int64(-9223372036854775808), "s", true, nil, `{"a":1,"b":[2,"x",null]}`, `[]`}
	if len(got) != len(want) {
		t.Fatalf("got %d args: %#v", len(got), got)
	}
	for i := range want {
		if fmt.Sprintf("%T %v", got[i], got[i]) != fmt.Sprintf("%T %v", want[i], want[i]) {
			t.Errorf("arg %d = %T %v, want %T %v", i, got[i], got[i], want[i], want[i])
		}
	}
	for in, n := range map[string]int{``: 0, `  `: 0, `[]`: 0, `null`: 0} {
		if got, err := DecodeArgs(json.RawMessage(in)); err != nil || len(got) != n {
			t.Errorf("DecodeArgs(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{`{"a":1}`, `"x"`, `1`, `[1,`} {
		if _, err := DecodeArgs(json.RawMessage(in)); err == nil {
			t.Errorf("DecodeArgs(%q) succeeded", in)
		}
	}
}

// denial replies of the hm_call import itself (wasmhost): what a guest reads
// when the host has no handler or the handler fails.
func TestWireHostCallReplies(t *testing.T) {
	w := newWireRig(t)
	ctx := context.Background()
	call := func(in string) string {
		out, err := w.rt.Host().Call(ctx, "wire", "echo_call", []byte(in))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	big := make([]byte, 1<<20+1)
	for i := range big {
		big[i] = 'x'
	}
	if got := call(string(big)); got != `{"ok":false,"error":{"code":"invalid_argument","message":"request too large"}}` {
		t.Errorf("oversized request: %s", got)
	}
	// Plugins loaded after SetHostCall(nil) have no host functions at all.
	w.rt.Host().SetHostCall(nil)
	w.rt.Unload(ctx, "wire")
	m := sdk.Manifest{Name: "wire", Version: "0.1.0", SDK: "^0.6"}
	if st := w.rt.Load(ctx, m, buildRecorder(t)); !st.Loaded {
		t.Fatal(st.Error)
	}
	if got := call(`{"fn":"x"}`); got != `{"ok":false,"error":{"code":"unavailable","message":"no host functions"}}` {
		t.Errorf("no handler: %s", got)
	}
	w.rt.Unload(ctx, "wire")
	w.rt.Host().SetHostCall(func(context.Context, string, []byte) []byte { panic("boom") })
	if st := w.rt.Load(ctx, m, buildRecorder(t)); !st.Loaded {
		t.Fatal(st.Error)
	}
	if got := call(`{"fn":"x"}`); got != `{"ok":false,"error":{"code":"internal","message":"host call failed"}}` {
		t.Errorf("panicking handler: %s", got)
	}
}

// TestWireHostResultTypes pins the bytes of host-defined result types. The
// expectations are the v1 bytes: omitempty never dropped a non-nil pointer to
// an empty value, and dropped a false bool.
func TestWireHostResultTypes(t *testing.T) {
	empty := ""
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"db.query", &QueryResult{Columns: []string{"id"}, Rows: [][]any{{1}}}, `{"columns":["id"],"rows":[[1]]}`},
		{"db.query truncated", &QueryResult{Columns: []string{"id"}, Rows: [][]any{}, Truncated: true}, `{"columns":["id"],"rows":[],"truncated":true}`},
		{"http.fetch", &FetchResponse{Status: 200, Headers: map[string]string{"a": "b"}, Body: "ok"}, `{"status":200,"headers":{"a":"b"},"body":"ok"}`},
		{"media.get cleared alt text", mediaDTO{ID: "m1", Filename: "a.png", MimeType: "image/png", FileSize: 3, AltText: &empty}, `{"id":"m1","filename":"a.png","mime_type":"image/png","file_size":3,"alt_text":""}`},
		{"forms.list empty validation", map[string]any{"fields": []form.FormField{{ID: "f1", Type: "text", Label: "L", Name: "n", Validation: &form.FieldValidation{}}}},
			`{"fields":[{"id":"f1","type":"text","label":"L","name":"n","required":false,"validation":{},"position":0}]}`},
	}
	for _, c := range cases {
		got, err := jsonx.MarshalPinned(c.v)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
}

// TestWirePayloadKeepsRawDuplicateNames: a changeset payload built from the
// plugin's raw attributes must keep them, duplicate names included (v1 passed
// raw values through), and an encoding failure must surface as an error
// instead of an empty payload that stages as {}.
func TestWirePayloadKeepsRawDuplicateNames(t *testing.T) {
	got, err := payload(map[string]any{"schema": "post", "name": "N", "attributes": jsontext.Value(`{"title":"a","title":"b"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"attributes":{"title":"a","title":"b"},"name":"N","schema":"post"}`; string(got) != want {
		t.Errorf("payload = %s, want %s", got, want)
	}
	if got, err := payload(make(chan int)); err == nil {
		t.Errorf("an unencodable payload returned %q without an error", got)
	}
}
