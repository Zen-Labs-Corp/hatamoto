package pluginhost

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

type redirectRig struct {
	rt     *Runtime
	filter *RedirectFilter
	logs   *lockedBuf
}

func newRedirectRig(t *testing.T) *redirectRig {
	t.Helper()
	logs := &lockedBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	engine := hooks.New(log)
	rt, err := NewRuntime(log, engine, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	return &redirectRig{rt: rt, filter: NewRedirectFilter(rt, engine), logs: logs}
}

func redirectManifest(name, mode string, paths ...string) sdk.Manifest {
	if len(paths) == 0 {
		paths = []string{"/go", "/go/"}
	}
	return sdk.Manifest{
		Name: name, Version: "0.1.0", SDK: "^0.22",
		Cookies: []string{"pref"}, RedirectPaths: paths,
		Config: map[string]string{"mode": mode},
	}
}

func (r *redirectRig) load(t *testing.T, m sdk.Manifest) sdk.Manifest {
	t.Helper()
	if st := r.rt.Load(context.Background(), m, plugintest.Build(t, "redirect")); !st.Loaded {
		t.Fatalf("%s not loaded: %s", m.Name, st.Error)
	}
	return m
}

func goInput(cookies map[string]string) sdk.RedirectContext {
	return sdk.RedirectContext{Path: "/go", Query: "a=1&b=2", Country: "XX", Cookies: cookies}
}

// echoed decodes the query of the echo mode's Location.
func echoed(t *testing.T, out sdk.RedirectContext) url.Values {
	t.Helper()
	if out.Redirect == nil || out.Redirect.Status != 307 {
		t.Fatalf("redirect = %+v", out.Redirect)
	}
	rest, ok := strings.CutPrefix(out.Redirect.Location, "/echo?")
	if !ok {
		t.Fatalf("location %q", out.Redirect.Location)
	}
	q, err := url.ParseQuery(rest)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestRedirectPluginSeesOnlyDeclaredInput(t *testing.T) {
	r := newRedirectRig(t)
	r.load(t, redirectManifest("rdr", "echo"))
	out, ok := r.filter.Apply(context.Background(), goInput(map[string]string{
		"hmp_rdr_pref":   "a",
		"hmp_rdr_hidden": "b", // a cookie the manifest does not declare
		"hmp_other_pref": "c", // another plugin's cookie
		"session":        "d",
	}))
	if !ok {
		t.Fatal("not answered")
	}
	q := echoed(t, out)
	for k, want := range map[string]string{"path": "/go", "query": "a=1&b=2", "country": "XX", "cookies": "pref", "n": "1"} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestRedirectAnswerAndNamespacedCookie(t *testing.T) {
	r := newRedirectRig(t)
	r.load(t, redirectManifest("rdr", "redirect"))
	out, ok := r.filter.Apply(context.Background(), goInput(map[string]string{"hmp_rdr_pref": "p1"}))
	if !ok || out.Redirect == nil {
		t.Fatalf("not answered: %+v", out)
	}
	if out.Redirect.Status != 302 || out.Redirect.Location != "/go?c=XX&pref=p1" {
		t.Errorf("redirect = %+v", out.Redirect)
	}
	if len(out.SetCookies) != 1 || out.SetCookies[0].Name != "hmp_rdr_pref" || out.SetCookies[0].Value != "x" {
		t.Errorf("cookies = %+v", out.SetCookies)
	}
}

func TestRedirectCookieOnlyAnswer(t *testing.T) {
	r := newRedirectRig(t)
	r.load(t, redirectManifest("rdr", "cookie"))
	out, ok := r.filter.Apply(context.Background(), goInput(nil))
	if !ok || out.Redirect != nil || len(out.SetCookies) != 1 || out.SetCookies[0].Name != "hmp_rdr_pref" {
		t.Fatalf("ok=%v out=%+v", ok, out)
	}
}

func TestRedirectUndeclaredPathNeverCallsPlugin(t *testing.T) {
	r := newRedirectRig(t)
	r.load(t, redirectManifest("rdr", "echo"))
	for _, p := range []string{"/other", "/go/x", "/GO", ""} {
		if r.filter.Declares(p) {
			t.Errorf("Declares(%q)", p)
		}
		in := goInput(nil)
		in.Path = p
		if _, ok := r.filter.Apply(context.Background(), in); ok {
			t.Errorf("%q answered", p)
		}
	}
	// The plugin counts its own calls: the first real call is number 1.
	out, ok := r.filter.Apply(context.Background(), goInput(nil))
	if !ok || echoed(t, out).Get("n") != "1" {
		t.Fatalf("guest was called for undeclared paths: ok=%v out=%+v", ok, out)
	}
	// A trailing slash is a distinct, separately declared path.
	if !r.filter.Declares("/go") || !r.filter.Declares("/go/") {
		t.Error("declared paths not found")
	}
}

func TestRedirectBadAnswersAreDroppedWhole(t *testing.T) {
	for _, tc := range []struct{ mode, logged string }{
		{"undeclared", "not declared in the manifest"},
		{"badloc", "not a same-site path"},
		{"protoloc", "not a same-site path"},
		{"status301", "not 302 or 307"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			r := newRedirectRig(t)
			r.load(t, redirectManifest("rdr", tc.mode))
			in := goInput(nil)
			out, ok := r.filter.Apply(context.Background(), in)
			if ok || out.Redirect != nil || len(out.SetCookies) != 0 {
				t.Fatalf("answered: ok=%v out=%+v", ok, out)
			}
			if logs := r.logs.String(); !strings.Contains(logs, "hooks: filter failed") || !strings.Contains(logs, tc.logged) {
				t.Errorf("not logged as %q:\n%s", tc.logged, logs)
			}
		})
	}
}

func TestRedirectFailOpen(t *testing.T) {
	for _, mode := range []string{"trap", "sleep"} {
		t.Run(mode, func(t *testing.T) {
			r := newRedirectRig(t)
			m := redirectManifest("rdr", mode)
			m.Limits.HookTimeoutsMS = map[string]uint64{sdk.RequestRedirect.Name: 100}
			r.load(t, m)
			// The fourth call is refused by the open breaker, also an error.
			for i := range 4 {
				if out, ok := r.filter.Apply(context.Background(), goInput(nil)); ok {
					t.Fatalf("call %d answered: %+v", i, out)
				}
			}
			if !strings.Contains(r.logs.String(), "hooks: filter failed") {
				t.Errorf("failure not logged:\n%s", r.logs.String())
			}
		})
	}
}

func TestRedirectEmptyAnswerIsNoAnswer(t *testing.T) {
	r := newRedirectRig(t)
	r.load(t, redirectManifest("rdr", "empty"))
	if out, ok := r.filter.Apply(context.Background(), goInput(nil)); ok {
		t.Fatalf("answered: %+v", out)
	}
	if strings.Contains(r.logs.String(), "filter failed") {
		t.Errorf("an empty answer is not a failure:\n%s", r.logs.String())
	}
}

func TestRedirectPathOwnership(t *testing.T) {
	r := newRedirectRig(t)
	r.load(t, redirectManifest("first", "redirect", "/go"))
	r.load(t, redirectManifest("second", "empty", "/go", "/start"))
	if got := r.rt.redirectOwner("/go"); got != "first" {
		t.Errorf("owner of /go = %q", got)
	}
	if got := r.rt.redirectOwner("/start"); got != "second" {
		t.Errorf("owner of /start = %q", got)
	}
	if !strings.Contains(r.logs.String(), "already owned by another plugin") {
		t.Errorf("collision not logged:\n%s", r.logs.String())
	}
	// Only the owner is called: the answer is first's redirect.
	if out, ok := r.filter.Apply(context.Background(), goInput(nil)); !ok || out.Redirect == nil {
		t.Errorf("owner did not answer: ok=%v out=%+v", ok, out)
	}
	// Unloading the owner hands the path to the plugin that lost it.
	r.rt.Unload(context.Background(), "first")
	if got := r.rt.redirectOwner("/go"); got != "second" {
		t.Errorf("owner of /go after unload = %q", got)
	}
	r.rt.Unload(context.Background(), "second")
	if r.filter.Declares("/go") || r.filter.Declares("/start") {
		t.Error("paths still declared after unload")
	}
}

func TestRedirectReservedPathRefusedAtLoad(t *testing.T) {
	r := newRedirectRig(t)
	st := r.rt.Load(context.Background(), redirectManifest("rdr", "empty", "/go", "/admin"), plugintest.Build(t, "redirect"))
	if st.Loaded || !strings.Contains(st.Error, `"/admin"`) {
		t.Fatalf("loaded=%v error=%q", st.Loaded, st.Error)
	}
	if r.filter.Declares("/go") {
		t.Error("a refused plugin owns paths")
	}
}

func TestRedirectFilterNilSafe(t *testing.T) {
	var f *RedirectFilter
	if f.Declares("/go") {
		t.Error("nil filter declares a path")
	}
	if _, ok := f.Apply(context.Background(), goInput(nil)); ok {
		t.Error("nil filter answered")
	}
	if NewRedirectFilter(nil, nil) != nil {
		t.Error("nil runtime gave a filter")
	}
}

func TestStrictRedirectLocation(t *testing.T) {
	for l, want := range map[string]bool{
		"/":                     true,
		"/go?c=XX&next=/a/../b": true,
		"/a/./b":                true,
		"/a//b":                 true,
		"/a/../b#frag":          true,
		"/./\\x":                false,
		"/../\\x":               false,
		"/a/../\\x":             false,
		"/.//x":                 false,
		"/././/x":               false,
		"/../..//x":             false,
		"/%2e//x":               false,
		"/%2E/%2e/\\x":          false,
		"//x":                   false,
		"/\\x":                  false,
		"\\\\x":                 false,
		"/a\\b":                 false,
		"/\t/x":                 false,
		"/\n/x":                 false,
		"/\r/x":                 false,
		"/%2F%2Fx":              false,
		"/%2f/x":                false,
		"/%5Cx":                 false,
		"/a%5cb":                false,
		"https://x":             false,
		"javascript:alert(1)":   false,
		"x":                     false,
		"":                      false,
	} {
		if got := strictRedirectLocation(l); got != want {
			t.Errorf("strictRedirectLocation(%q) = %v, want %v", l, got, want)
		}
	}
}
