package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

// grantable are the permissions the phase tests hold, so that a refusal is
// the phase gate's and never a missing grant.
func grantable() []string {
	var out []string
	for _, p := range sdk.Permissions {
		switch p {
		case sdk.PermHTTPFetchAny, sdk.PermBuilderData, sdk.PermBuilderIsland, sdk.PermSiteScripts:
		default:
			out = append(out, p)
		}
	}
	return out
}

// Every direct write permission is refused at load with the replacement named,
// every removed function answers `unavailable` with the same pointer, and
// none is offered to a plugin.
func TestWritePermissionsRefused(t *testing.T) {
	r := newCapaRig(t)
	for perm, repl := range map[string]string{
		"entities.write":  sdk.FnChangesetEntityUpsert,
		"terms.write":     sdk.FnChangesetTermUpsert,
		"menus.write":     sdk.FnChangesetMenuUpsert,
		"media.write":     sdk.FnChangesetMediaAttach,
		"redirects.write": sdk.FnChangesetRedirectUpsert,
	} {
		st := r.rt.Load(context.Background(), capaManifest(func(m *sdk.Manifest) { m.Permissions = []string{sdk.PermKV, perm} }), plugintest.Build(t, "capa"))
		if st.Loaded {
			t.Fatalf("%s: plugin loaded", perm)
		}
		for _, want := range []string{`plugin "capa" not loaded`, `"` + perm + `" was removed in Hatamoto 1.1`, repl, "Runs and changesets"} {
			if !strings.Contains(st.Error, want) {
				t.Errorf("%s: refusal %q lacks %q", perm, st.Error, want)
			}
		}
	}

	r.load(t, func(m *sdk.Manifest) { m.Permissions = grantable() })
	r.grant(t, grantable()...)
	if len(removedFns) != 12 {
		t.Fatalf("removedFns has %d entries, want the 12 removed functions", len(removedFns))
	}
	all := HostFunctionsFor(sdk.Permissions)
	for fn, repl := range removedFns {
		o := r.do(t, fn, map[string]any{})
		if o.Code != sdk.ErrUnavailable || !strings.Contains(o.Msg, fn+" was removed in SDK 0.17") || !strings.Contains(o.Msg, repl) {
			t.Errorf("%s: %+v, want unavailable pointing at %s", fn, o, repl)
		}
		if _, ok := hostFns[fn]; ok || slices.Contains(all, fn) {
			t.Errorf("%s is still a host function", fn)
		}
	}
}

// A grant row left over from before 0.17 is neither rewritten nor a problem:
// it cannot be set again, and reading it works.
func TestRemovedGrantRowsTolerated(t *testing.T) {
	g := NewGrantStore(nil)
	g.m["old"] = []string{"entities.write", sdk.PermKV}
	if !g.Has("old", sdk.PermKV) || !g.Has("old", "entities.write") || len(g.Granted("old")) != 2 {
		t.Fatalf("stored grants not read: %v", g.Granted("old"))
	}
	if err := g.Set(context.Background(), "old", []string{"entities.write"}); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("Set of a removed permission: %v", err)
	}
	if len(g.Granted("old")) != 2 {
		t.Fatal("a refused Set changed the stored grants")
	}
}

// ISC-9: every registered function is classified, and the gate follows the
// table in each phase and outside a run.
func TestRunPhaseIsolation(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, func(m *sdk.Manifest) { m.Permissions = grantable() })
	r.grant(t, grantable()...)

	for fn := range hostFns {
		if _, ok := fnPhases[fn]; !ok {
			t.Errorf("host function %q has no phase classification", fn)
		}
	}
	for fn := range runOnly {
		if _, ok := fnPhases[fn]; !ok {
			t.Errorf("run-only function %q has no phase classification", fn)
		}
	}
	for _, fn := range []string{sdk.FnKVGet, sdk.FnKVSet, sdk.FnKVDelete, sdk.FnKVList, sdk.FnDBQuery, sdk.FnDBExec} {
		if fnPhases[fn] != phNone {
			t.Errorf("%s must be closed in both phases", fn)
		}
	}

	run := RunCtx{RunID: uuid.New(), Plugin: "capa", Name: "import", Hosts: []string{"example.com"}}
	for fn := range hostFns {
		for _, phase := range []string{"", sdk.RunPhaseFetch, sdk.RunPhaseTransform} {
			ctx := context.Background()
			wantDenied := runOnly[fn]
			if phase != "" {
				rc := run
				rc.Phase = phase
				ctx = WithRun(ctx, rc)
				wantDenied = !fnPhases[fn].allows(phase)
			}
			o := r.doCtx(t, ctx, fn, map[string]any{})
			if got := o.Code == sdk.ErrPhaseDenied; got != wantDenied {
				t.Errorf("%s in %q: %+v, phase_denied = %v, want %v", fn, phase, o, got, wantDenied)
			}
		}
	}

	// The brief's cells, spelled out. A function whose implementation lands
	// with a later change is not registered yet and is skipped; the loop above
	// covers it from the day it is.
	for phase, fns := range map[string][]string{
		sdk.RunPhaseFetch:     {sdk.FnBufferGet, sdk.FnBufferList, sdk.FnSiteInfo, sdk.FnEntitiesSlugTaken, sdk.FnEntitiesGet, sdk.FnTermsList, sdk.FnMenusList, sdk.FnMediaList, sdk.FnRedirectsList, sdk.FnKVGet, sdk.FnDBQuery},
		sdk.RunPhaseTransform: {sdk.FnHTTPFetch, sdk.FnUploadRead, sdk.FnBufferPut, sdk.FnMediaImportURL, sdk.FnKVSet, sdk.FnDBExec},
	} {
		rc := run
		rc.Phase = phase
		for _, fn := range fns {
			if _, registered := hostFns[fn]; !registered {
				continue
			}
			if o := r.doCtx(t, WithRun(context.Background(), rc), fn, map[string]any{}); o.Code != sdk.ErrPhaseDenied {
				t.Errorf("%s in the %s phase: %+v, want phase_denied", fn, phase, o)
			}
		}
	}
}

// Inside a run's fetch phase http.fetch calls the run's hosts, not the
// manifest's.
func TestRunHostsReplaceAllowedHosts(t *testing.T) {
	manifest := []string{"manifest.example"}
	if got := effectiveHosts(context.Background(), manifest); !slices.Equal(got, manifest) {
		t.Fatalf("outside a run: %v", got)
	}
	rc := RunCtx{Phase: sdk.RunPhaseFetch, Hosts: []string{"run.example"}}
	if got := effectiveHosts(WithRun(context.Background(), rc), manifest); !slices.Equal(got, rc.Hosts) {
		t.Fatalf("fetch phase: %v", got)
	}
}

func hostCallAPI(t *testing.T) *API {
	t.Helper()
	g := NewGrantStore(nil)
	if err := g.Set(context.Background(), "p", []string{sdk.PermLog}); err != nil {
		t.Fatal(err)
	}
	return NewAPI(Services{}, g, func(string) (sdk.Manifest, bool) { return sdk.Manifest{Name: "p"}, true }, nil)
}

func callLog(t *testing.T, a *API, args string) sdk.CallResponse {
	t.Helper()
	in := []byte(`{"fn":"log","args":` + args + `}`)
	var resp sdk.CallResponse
	if err := json.Unmarshal(a.Call(context.Background(), "p", in), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestChangesetArgsSize(t *testing.T) {
	a := hostCallAPI(t)
	if resp := callLog(t, a, `{"level":"info","message":"ok"}`); !resp.OK {
		t.Fatalf("a small call failed: %+v", resp.Error)
	}
	resp := callLog(t, a, `{"level":"info","message":"`+strings.Repeat("x", maxRequestBytes)+`"}`)
	if resp.OK || resp.Error.Code != sdk.ErrInvalidArgument || !strings.Contains(resp.Error.Message, "too large") {
		t.Fatalf("1 MiB call: %+v", resp)
	}
}

// The element, token and depth limits cover the run and changeset functions
// only: a function that predates runs keeps accepting what it always did.
func TestRunFnArgsShape(t *testing.T) {
	a := hostCallAPI(t)
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	call := func(fn, args string) sdk.CallResponse {
		in := []byte(`{"fn":"` + fn + `","args":` + args + `}`)
		var resp sdk.CallResponse
		if err := json.Unmarshal(a.Call(context.Background(), "p", in), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := call(sdk.FnBufferPut, `{"key":"k","value":`+deep(40)+`}`); resp.OK || !strings.Contains(resp.Error.Message, "nesting deeper") {
		t.Fatalf("40 levels deep to a run function: %+v", resp)
	}
	long := "[" + strings.Repeat("1,", 20000) + "1]"
	if resp := call(sdk.FnBufferPut, `{"key":"k","value":`+long+`}`); resp.OK || !strings.Contains(resp.Error.Message, "more than") {
		t.Fatalf("20000 elements to a run function: %+v", resp)
	}
	// An existing function gets past the shape check (its own strict decoding
	// then refuses the unknown field, which is not a shape error).
	for _, extra := range []string{deep(40), long} {
		resp := callLog(t, a, `{"level":"info","message":"x","extra":`+extra+`}`)
		if resp.OK || strings.Contains(resp.Error.Message, "nesting deeper") || strings.Contains(resp.Error.Message, "more than") {
			t.Fatalf("an existing function was shape-checked: %+v", resp)
		}
	}
}

func TestChangesetArgsUnknownField(t *testing.T) {
	a := hostCallAPI(t)
	resp := callLog(t, a, `{"level":"info","message":"x","bogus":1}`)
	if resp.OK || resp.Error.Code != sdk.ErrInvalidArgument {
		t.Fatalf("unknown field: %+v", resp)
	}
}
