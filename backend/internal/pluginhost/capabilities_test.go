package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/wasmhost"
	"hatamoto.dev/sdk"
)

// --- fakes for the entity service ------------------------------------------------

type memSchemas struct {
	entity.SchemaRepository
	schema *entity.EntitySchema
}

func (f *memSchemas) GetByID(context.Context, uuid.UUID) (*entity.EntitySchema, error) {
	return f.schema, nil
}
func (f *memSchemas) GetBySlug(_ context.Context, slug string) (*entity.EntitySchema, error) {
	if slug != f.schema.Slug {
		return nil, entity.ErrSchemaNotFound
	}
	return f.schema, nil
}

type memEntities struct {
	entity.EntityRepository
	mu   sync.Mutex
	list []*entity.Entity
}

func (f *memEntities) Create(_ context.Context, _ *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := &entity.Entity{ID: uuid.New(), SchemaID: in.SchemaID, Name: in.Name, Slug: in.Slug,
		Status: entity.StatusDraft, Attributes: in.Attributes, Version: 1}
	f.list = append(f.list, e)
	return e, nil
}

func (f *memEntities) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.list {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, entity.ErrEntityNotFound
}

func (f *memEntities) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.list {
		out = append(out, e.Name)
	}
	return out
}

// --- the rig -------------------------------------------------------------------

type capaRig struct {
	rt     *Runtime
	engine *hooks.Engine
	ents   *memEntities
	svc    *entity.Service
	logs   *lockedBuf
	sched  *fakeScheduler
}

type fakeScheduler struct {
	mu   sync.Mutex
	jobs map[string]time.Duration
	fns  map[string]func(context.Context) error
}

func (f *fakeScheduler) Schedule(id string, every time.Duration, fn func(context.Context) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jobs == nil {
		f.jobs, f.fns = map[string]time.Duration{}, map[string]func(context.Context) error{}
	}
	f.jobs[id], f.fns[id] = every, fn
	return nil
}

func (f *fakeScheduler) Unschedule(prefix string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.jobs {
		if strings.HasPrefix(id, prefix) {
			delete(f.jobs, id)
			delete(f.fns, id)
		}
	}
}

func newCapaRig(t *testing.T) *capaRig {
	t.Helper()
	logs := &lockedBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	engine := hooks.New(log)
	rt, err := NewRuntime(log, engine, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })

	schema := &entity.EntitySchema{ID: uuid.New(), Slug: "post", Name: "Post",
		Schema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`)}
	ents := &memEntities{}
	svc := entity.NewService(&memSchemas{schema: schema}, ents, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetBeforeSave(BeforeSave(engine))
	sched := &fakeScheduler{}
	rt.Configure(Config{Services: Services{Entities: svc, Fetcher: NewFetcher()}, Scheduler: sched})
	return &capaRig{rt: rt, engine: engine, ents: ents, svc: svc, logs: logs, sched: sched}
}

func capaManifest(mutate func(*sdk.Manifest)) sdk.Manifest {
	m := sdk.Manifest{Name: "capa", Version: "0.1.0", SDK: "^0.6",
		Permissions: []string{sdk.PermEntitiesRead, sdk.PermEntitiesWrite, sdk.PermKV, sdk.PermFormsRead, sdk.PermHTTPFetch, sdk.PermDB, sdk.PermLog},
		Cookies:     []string{"unlock", "visited"}, Funcs: []string{"shout"},
		Cron: []sdk.CronJob{{Name: "tick", Every: "5m"}},
		Routes: []sdk.Route{
			{Method: "GET", Path: "/echo"},
			{Method: "POST", Path: "/echo", Roles: []string{"owner", "editor"}},
			{Method: "GET", Path: "/who/{name}", Public: true},
			{Method: "GET", Path: "/redirect"},
			{Method: "GET", Path: "/cache", Roles: []string{"viewer"}},
			{Method: "GET", Path: "/open", Roles: []string{"viewer"}},
			{Method: "GET", Path: "/admin/{rest...}"},
			{Method: "GET", Path: "/items/{id}/"},
		}}
	if mutate != nil {
		mutate(&m)
	}
	return m
}

func (r *capaRig) load(t *testing.T, mutate func(*sdk.Manifest)) {
	t.Helper()
	st := r.rt.Load(context.Background(), capaManifest(mutate), plugintest.Build(t, "capa"))
	if !st.Loaded {
		t.Fatalf("capa not loaded: %s", st.Error)
	}
}

type outcome struct {
	Result json.RawMessage `json:"result"`
	Code   string          `json:"code"`
	Cap    string          `json:"capability"`
	Msg    string          `json:"message"`
}

// do calls a host function from inside the plugin and returns its outcome.
func (r *capaRig) do(t *testing.T, fn string, args any) outcome {
	t.Helper()
	return r.doCtx(t, context.Background(), fn, args)
}

func (r *capaRig) doCtx(t *testing.T, ctx context.Context, fn string, args any) outcome {
	t.Helper()
	raw, _ := json.Marshal(args)
	in, _ := json.Marshal(sdk.CallRequest{Fn: fn, Args: raw})
	out, err := r.rt.Host().Call(ctx, "capa", "call_host", in)
	if err != nil {
		t.Fatalf("call_host %s: %v", fn, err)
	}
	var o outcome
	if err := json.Unmarshal(out, &o); err != nil {
		t.Fatalf("outcome %q: %v", out, err)
	}
	return o
}

func (r *capaRig) grant(t *testing.T, perms ...string) {
	t.Helper()
	if _, err := r.rt.Grant(context.Background(), "capa", perms); err != nil {
		t.Fatal(err)
	}
}

// --- tests ---------------------------------------------------------------------

func TestDeniedUntilGranted(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)

	for _, c := range []struct {
		fn   string
		args any
		perm string
	}{
		{sdk.FnEntitiesCreate, map[string]any{"schema_slug": "post", "name": "x"}, sdk.PermEntitiesWrite},
		{sdk.FnEntitiesList, map[string]any{}, sdk.PermEntitiesRead},
		{sdk.FnKVGet, map[string]any{"key": "k"}, sdk.PermKV},
		{sdk.FnHTTPFetch, map[string]any{"url": "http://example.com/"}, sdk.PermHTTPFetch},
		{sdk.FnDBQuery, map[string]any{"sql": "select 1"}, sdk.PermDB},
		{sdk.FnFormsList, map[string]any{}, sdk.PermFormsRead},
		{sdk.FnMailSend, map[string]any{}, sdk.PermMailSend},
		{sdk.FnSettingsGet, map[string]any{}, sdk.PermSettings},
		{sdk.FnLog, map[string]any{"level": "info", "message": "x"}, sdk.PermLog},
		{sdk.FnMediaGet, map[string]any{}, sdk.PermMediaRead},
		{sdk.FnTermsSave, map[string]any{}, sdk.PermTermsWrite},
		{sdk.FnMenusSave, map[string]any{}, sdk.PermMenusWrite},
		{sdk.FnRedirectsCreate, map[string]any{}, sdk.PermRedirectsWrite},
	} {
		o := r.do(t, c.fn, c.args)
		if o.Code != sdk.ErrCapabilityDenied || o.Cap != c.perm || !strings.Contains(o.Msg, c.perm) {
			t.Errorf("%s before grant: %+v, want capability_denied naming %q", c.fn, o, c.perm)
		}
	}
	// Every permissioned host function is denied without its grant: a new
	// function that forgot its perm fails here (only "permissions" is open).
	for fn, h := range hostFns {
		if h.perm == "" {
			if fn != "permissions" {
				t.Errorf("host function %q has no permission", fn)
			}
			continue
		}
		if o := r.do(t, fn, map[string]any{}); o.Code != sdk.ErrCapabilityDenied || o.Cap != h.perm || !strings.Contains(o.Msg, h.perm) {
			t.Errorf("%s before grant: %+v, want capability_denied naming %q", fn, o, h.perm)
		}
	}
	if len(r.ents.names()) != 0 {
		t.Fatal("a denied write reached the entity service")
	}

	r.grant(t, sdk.PermEntitiesWrite, sdk.PermEntitiesRead)
	if o := r.do(t, sdk.FnEntitiesCreate, map[string]any{"schema_slug": "post", "name": "Hello"}); o.Code != "" {
		t.Fatalf("create after grant: %+v", o)
	}
	// Other capabilities stay denied.
	if o := r.do(t, sdk.FnKVGet, map[string]any{"key": "k"}); o.Code != sdk.ErrCapabilityDenied {
		t.Fatalf("kv leaked through an entities grant: %+v", o)
	}
	// Revoking is granting less.
	r.grant(t)
	if o := r.do(t, sdk.FnEntitiesList, map[string]any{}); o.Code != sdk.ErrCapabilityDenied {
		t.Fatalf("revoked grant still works: %+v", o)
	}
}

func TestGrantMustBeRequested(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, func(m *sdk.Manifest) { m.Permissions = []string{sdk.PermKV} })
	if _, err := r.rt.Grant(context.Background(), "capa", []string{sdk.PermEntitiesWrite}); !errors.Is(err, ErrNotRequested) {
		t.Fatalf("granting an unrequested permission: %v", err)
	}
	if _, err := r.rt.Grant(context.Background(), "capa", []string{"nope"}); err == nil {
		t.Fatal("an unknown permission must be refused")
	}
	if _, err := r.rt.Grant(context.Background(), "ghost", nil); err == nil {
		t.Fatal("granting to an unknown plugin must fail")
	}
	if g := r.rt.Grants().Granted("capa"); len(g) != 0 {
		t.Fatalf("a refused grant changed the grants: %v", g)
	}
}

func TestTypedErrors(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	r.grant(t, sdk.PermEntitiesRead, sdk.PermEntitiesWrite, sdk.PermKV, sdk.PermHTTPFetch)

	for name, c := range map[string]struct {
		fn   string
		args any
		code string
	}{
		"unknown function":   {"nope.nope", map[string]any{}, sdk.ErrUnknownFunction},
		"bad id":             {sdk.FnEntitiesGet, map[string]any{"id": "not-a-uuid"}, sdk.ErrInvalidArgument},
		"missing id":         {sdk.FnEntitiesGet, map[string]any{}, sdk.ErrInvalidArgument},
		"unknown field":      {sdk.FnEntitiesGet, map[string]any{"idd": "x"}, sdk.ErrInvalidArgument},
		"not found":          {sdk.FnEntitiesGet, map[string]any{"id": uuid.NewString()}, sdk.ErrNotFound},
		"unknown schema":     {sdk.FnEntitiesCreate, map[string]any{"schema_slug": "nope", "name": "x"}, sdk.ErrNotFound},
		"invalid attributes": {sdk.FnEntitiesCreate, map[string]any{"schema_slug": "post", "name": "x", "attributes": map[string]any{"bogus": 1}}, sdk.ErrInvalidArgument},
		"no database":        {sdk.FnKVGet, map[string]any{"key": "k"}, sdk.ErrUnavailable},
		"blocked host":       {sdk.FnHTTPFetch, map[string]any{"url": "http://127.0.0.1/"}, sdk.ErrBlocked},
	} {
		if o := r.do(t, c.fn, c.args); o.Code != c.code {
			t.Errorf("%s: %+v, want code %s", name, o, c.code)
		}
	}
}

func TestEntityWriteGuardAndReentrancy(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	r.grant(t, sdk.PermEntitiesWrite, sdk.PermEntitiesRead)
	ctx := context.Background()

	schema, _ := r.svc.GetSchemaBySlug(ctx, "post")
	// An editor's save runs entity.before_save; the plugin writes a child from
	// inside it (depth 1: allowed), and the child's own before_save may not
	// write again.
	ent, err := r.svc.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: schema.ID, Name: "x", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	got := r.ents.names()
	if len(got) != 2 {
		t.Fatalf("entities = %v, want the child and the parent only (recursion cut at depth 1)", got)
	}
	if got[0] != "child-of-x[reentrant]" || got[1] != "x[child-ok]" {
		t.Fatalf("names = %q", got)
	}
	if ent.Name != "x[child-ok]" {
		t.Fatalf("the saved entity = %q", ent.Name)
	}

	// A write made while already inside a plugin write is refused at once.
	nested := context.WithValue(ctx, depthKey{}, 1)
	if o := r.doCtx(t, nested, sdk.FnEntitiesCreate, map[string]any{"schema_slug": "post", "name": "y"}); o.Code != sdk.ErrReentrant {
		t.Fatalf("nested write: %+v", o)
	}
	// Reads are not guarded.
	if o := r.doCtx(t, nested, sdk.FnEntitiesGet, map[string]any{"id": ent.ID.String()}); o.Code != "" {
		t.Fatalf("nested read: %+v", o)
	}
}

func TestEntityActionsInheritWriteDepth(t *testing.T) {
	// An entity.created action fired by a plugin write sees depth 1, so a
	// plugin writing from its entity.created handler cannot loop.
	engine := hooks.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	seen := make(chan int, 1)
	engine.AddAction("entity.created", 0, func(ctx context.Context, _ any) { seen <- entityWriteDepth(ctx) })
	engine.Fire(context.WithValue(context.Background(), depthKey{}, 1), "entity.created", struct{}{})
	engine.Wait()
	if d := <-seen; d != 1 {
		t.Fatalf("action saw depth %d, want 1", d)
	}
}

// The route that is authorised is the route the plugin sees: a path the mux
// would clean or redirect is a 404, never another route's handler.
func TestRoutesAuthorisedPathIsTheDispatchedPath(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	admin := r.rt.AdminRoutes()
	viewer := &sdk.HTTPUser{ID: "u3", Role: "viewer"}
	serve := func(rest string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		admin.Serve(rec, httptest.NewRequest("GET", "/x/", nil), "capa", rest, viewer)
		return rec
	}
	if rec := serve("open"); rec.Code != 200 || rec.Body.String() != "open" {
		t.Fatalf("a viewer on /open: %d %s", rec.Code, rec.Body)
	}
	if rec := serve("admin/x"); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer on the owner-only /admin/...: %d", rec.Code)
	}
	// "admin/../open" is what r.PathValue("rest") yields for admin%2F..%2Fopen.
	for _, rest := range []string{"admin/../open", "admin/./x", "open/", "/open", "items//1/", "items/1", "x/../open"} {
		rec := serve(rest)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "ADMIN-ONLY") || strings.Contains(rec.Body.String(), "items:") {
			t.Errorf("%q = %d %s, want 404", rest, rec.Code, rec.Body)
		}
	}
	// The plugin is also told which declared route matched.
	owner := &sdk.HTTPUser{ID: "u1", Role: "owner"}
	rec := httptest.NewRecorder()
	r.rt.PublicRoutes().Serve(rec, httptest.NewRequest("GET", "/x/", nil), "capa", "who/ada", nil)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	rec = httptest.NewRecorder()
	admin.Serve(rec, httptest.NewRequest("GET", "/x/", nil), "capa", "echo", owner)
	var echoed sdk.HTTPRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &echoed); err != nil || echoed.Pattern != "/echo" {
		t.Fatalf("pattern = %q %v", echoed.Pattern, err)
	}
}

func TestRouteResponseHeaders(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	admin := r.rt.AdminRoutes()
	owner := &sdk.HTTPUser{ID: "u1", Role: "owner"}
	serve := func(rest, query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		admin.Serve(rec, httptest.NewRequest("GET", "/x/?"+query, nil), "capa", rest, owner)
		return rec
	}
	// Location: same-site paths only.
	for loc, ok := range map[string]bool{"/ok": true, "/a/b?c=d": true, "//evil.example": false, "https://evil.example/x": false, "/\\evil.example": false, "javascript:alert(1)": false, "/\t/evil.example": false, "/\t\\evil.example": false, "/a b": false} {
		rec := serve("redirect", "to="+url.QueryEscape(loc))
		if ok && (rec.Code != 302 || rec.Header().Get("Location") != loc) {
			t.Errorf("%q: %d %v", loc, rec.Code, rec.Header())
		}
		if !ok && (rec.Code == 302 || rec.Header().Get("Location") != "") {
			t.Errorf("%q was passed through: %d %v", loc, rec.Code, rec.Header())
		}
	}
	// An authenticated response is never shared-cacheable, whatever the plugin says.
	viewer := &sdk.HTTPUser{ID: "u3", Role: "viewer"}
	rec := httptest.NewRecorder()
	admin.Serve(rec, httptest.NewRequest("GET", "/x/", nil), "capa", "cache", viewer)
	cc := rec.Header().Get("Cache-Control")
	if strings.Contains(cc, "public") || strings.Contains(cc, "s-maxage") || !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers: %v", rec.Header())
	}
}

func TestRoutesServeAndSwap(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	admin, public := r.rt.AdminRoutes(), r.rt.PublicRoutes()

	serve := func(g *RouteRegistry, method, rest string, user *sdk.HTTPUser, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/x/"+rest+"?a=1&a=2", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Cookie", "session=secret")
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		g.Serve(rec, req, "capa", rest, user)
		return rec
	}
	owner := &sdk.HTTPUser{ID: "u1", Role: "owner"}

	rec := serve(admin, "GET", "echo", owner, "")
	if rec.Code != 201 {
		t.Fatalf("GET /echo = %d %s", rec.Code, rec.Body)
	}
	var echoed sdk.HTTPRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &echoed); err != nil {
		t.Fatal(err)
	}
	if echoed.Method != "GET" || echoed.Path != "/echo" || len(echoed.Query["a"]) != 2 || echoed.User == nil || echoed.User.Role != "owner" {
		t.Fatalf("request seen by the plugin: %+v", echoed)
	}
	if _, ok := echoed.Headers["authorization"]; ok || echoed.Headers["content-type"] != "text/plain" {
		t.Fatalf("headers seen by the plugin: %v (credentials must never be forwarded)", echoed.Headers)
	}
	if rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("X-Evil") != "" {
		t.Fatalf("plugin response headers outside the allowlist were passed: %v", rec.Header())
	}
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("plugin responses must be sandboxed and uncached: %v", rec.Header())
	}

	// Roles come from the manifest: GET /echo is owner only, POST allows editors.
	editor := &sdk.HTTPUser{ID: "u2", Role: "editor"}
	if c := serve(admin, "GET", "echo", editor, "").Code; c != http.StatusForbidden {
		t.Fatalf("editor on an owner route = %d", c)
	}
	if c := serve(admin, "POST", "echo", editor, "hi").Code; c != 201 {
		t.Fatalf("editor on a POST route that allows editors = %d", c)
	}
	if c := serve(admin, "GET", "echo", nil, "").Code; c != http.StatusForbidden {
		t.Fatalf("no user on an admin route = %d", c)
	}
	if rec := serve(admin, "DELETE", "echo", owner, ""); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") == "" {
		t.Fatalf("wrong method = %d allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	if c := serve(admin, "GET", "missing", owner, "").Code; c != http.StatusNotFound {
		t.Fatalf("unknown route = %d", c)
	}
	// Public routes live in the other registry and need no user.
	if rec := serve(public, "GET", "who/ada", nil, ""); rec.Code != 200 || rec.Body.String() != "ada" {
		t.Fatalf("public route = %d %q", rec.Code, rec.Body)
	}
	if c := serve(public, "GET", "echo", nil, "").Code; c != http.StatusNotFound {
		t.Fatalf("an admin route must not be served publicly: %d", c)
	}
	if c := serve(admin, "GET", "who/ada", owner, "").Code; c != http.StatusNotFound {
		t.Fatalf("a public route must not be served on the admin mount: %d", c)
	}
	if c := (func() int {
		rec := httptest.NewRecorder()
		admin.Serve(rec, httptest.NewRequest("GET", "/", nil), "ghost", "echo", owner)
		return rec.Code
	})(); c != 404 {
		t.Fatalf("unknown plugin = %d", c)
	}

	// Swap without a restart: reload with different routes, then remove them.
	r.load(t, func(m *sdk.Manifest) {
		m.Routes = []sdk.Route{{Method: "GET", Path: "/who/{name}"}}
	})
	if c := serve(admin, "GET", "echo", owner, "").Code; c != http.StatusNotFound {
		t.Fatalf("a removed route still answers: %d", c)
	}
	if rec := serve(admin, "GET", "who/bob", owner, ""); rec.Code != 200 || rec.Body.String() != "bob" {
		t.Fatalf("an added route: %d %q", rec.Code, rec.Body)
	}
	if c := serve(public, "GET", "who/bob", nil, "").Code; c != http.StatusNotFound {
		t.Fatalf("a route that became admin-only is still public: %d", c)
	}
	r.rt.Unload(context.Background(), "capa")
	if c := serve(admin, "GET", "who/bob", owner, "").Code; c != http.StatusNotFound {
		t.Fatalf("an unloaded plugin's route answers: %d", c)
	}
}

func TestRouteSwapUnderLoad(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	admin := r.rt.AdminRoutes()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := httptest.NewRecorder()
				admin.Serve(rec, httptest.NewRequest("GET", "/", nil), "capa", "echo", &sdk.HTTPUser{Role: "owner"})
				if rec.Code != 201 && rec.Code != 404 {
					t.Errorf("status during swap = %d", rec.Code)
					return
				}
			}
		}()
	}
	for i := range 200 {
		if i%2 == 0 {
			admin.Set("capa", nil)
		} else {
			admin.Set("capa", capaManifest(nil).Routes)
		}
	}
	close(stop)
	wg.Wait()
}

func TestCronRegistration(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	r.sched.mu.Lock()
	every, ok := r.sched.jobs["plugin:capa:tick"]
	fn := r.sched.fns["plugin:capa:tick"]
	r.sched.mu.Unlock()
	if !ok || every != 5*time.Minute {
		t.Fatalf("jobs = %v", r.sched.jobs)
	}
	// The job runs the plugin's export: with kv not granted it reports the
	// typed denial, which proves it got as far as the host function.
	if err := fn(context.Background()); err == nil || !strings.Contains(err.Error(), "capability_denied") {
		t.Fatalf("cron run = %v", err)
	}
	// Reloading without the job unschedules it; unloading removes the rest.
	r.load(t, func(m *sdk.Manifest) { m.Cron = nil })
	if len(r.sched.jobs) != 0 {
		t.Fatalf("a dropped job is still scheduled: %v", r.sched.jobs)
	}
	r.load(t, nil)
	r.rt.Unload(context.Background(), "capa")
	if len(r.sched.jobs) != 0 {
		t.Fatalf("jobs after unload: %v", r.sched.jobs)
	}

	for _, every := range []string{"30s", "59s", "nope", ""} {
		m := capaManifest(func(m *sdk.Manifest) { m.Cron = []sdk.CronJob{{Name: "tick", Every: every}} })
		if err := m.Validate(); err == nil {
			t.Errorf("cron every %q must be refused", every)
		}
	}
}

func TestTemplateFuncsAndSections(t *testing.T) {
	r := newCapaRig(t)
	st := r.rt.LoadWith(context.Background(), capaManifest(nil), plugintest.Build(t, "capa"), Assets{
		Partials: map[string]string{"badge": `<b>{{.}}</b>`},
		Sections: map[string]string{"promo": `{{define "section-promo"}}<p>{{.Title}}</p>{{end}}`},
	})
	if !st.Loaded {
		t.Fatal(st.Error)
	}
	if err := render.CheckPluginFunc("capa", "shout", func() string { return "" }); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("func not registered: %v", err)
	}
	if err := render.RegisterPluginSection("other", "promo", "x"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("section not registered: %v", err)
	}
	r.rt.Unload(context.Background(), "capa")
	if err := render.CheckPluginFunc("capa", "shout", func() string { return "" }); err != nil {
		t.Fatalf("func survived unload: %v", err)
	}
	if err := render.RegisterPluginSection("other", "promo", "x"); err != nil {
		t.Fatalf("section survived unload: %v", err)
	}
	render.RemovePlugin("other")
}

func TestRenderPageFilter(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	pf := NewPageFilter(r.engine)
	if !pf.Active() {
		t.Fatal("a plugin with filter_render_page must make the page filter active")
	}
	blocks := []sdk.Block{{ID: "intro", Type: "hero", Title: "Hi"}, {ID: "secret", Type: "text", Title: "Members only"}}
	apply := func(cookies map[string]string) sdk.PageContext {
		return pf.Apply(context.Background(), sdk.PageContext{Path: "/p", Cookies: cookies, Blocks: blocks})
	}

	out := apply(map[string]string{"session": "abc"})
	if len(out.Blocks) != 1 || out.Blocks[0].ID != "intro" {
		t.Fatalf("blocks = %+v, want the secret one hidden", out.Blocks)
	}
	if len(out.SetCookies) != 1 || out.SetCookies[0].Name != "hmp_capa_visited" {
		t.Fatalf("cookies = %+v", out.SetCookies)
	}
	if _, leaked := out.Cookies["session"]; !leaked {
		t.Fatalf("the chain's full cookie set was lost: %v", out.Cookies)
	}

	out = apply(map[string]string{"hmp_capa_unlock": "1"})
	if len(out.Blocks) != 2 {
		t.Fatalf("with the unlock cookie the block stays: %+v", out.Blocks)
	}

	// A plugin that does not declare the cookie never sees it.
	r.load(t, func(m *sdk.Manifest) { m.Cookies = []string{"visited"} })
	if out := apply(map[string]string{"hmp_capa_unlock": "1"}); len(out.Blocks) != 1 {
		t.Fatalf("an undeclared cookie reached the plugin: %+v", out.Blocks)
	}

	// Setting a cookie the manifest did not declare fails the whole result
	// (fail open): the page keeps all its blocks and gets no cookie.
	r.load(t, func(m *sdk.Manifest) {
		m.Cookies = []string{"unlock"}
	})
	if out := apply(map[string]string{}); len(out.Blocks) != 2 || len(out.SetCookies) != 0 {
		t.Fatalf("a result with an undeclared cookie was applied: %+v", out)
	}
	if !strings.Contains(r.logs.String(), "not declared in the manifest") {
		t.Fatalf("the refusal was not logged:\n%s", r.logs.String())
	}
}

// The plugin only ever receives its own namespaced cookies, under the short
// name: never the session or CSRF cookie, another plugin's cookie, or an
// un-namespaced one with a declared name.
func TestRenderPageCookiesAreNamespaced(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	pf := NewPageFilter(r.engine)
	out := pf.Apply(context.Background(), sdk.PageContext{Path: "/echo-cookies", Cookies: map[string]string{
		"hatamoto_session": "SESSION-TOKEN", "hatamoto_csrf": "CSRF", "__Host-x": "h", "__Secure-y": "s",
		"unlock": "raw-undeclared-prefix", "hmp_other_unlock": "other-plugin", "hmp_capa_unlock": "mine", "hmp_capa_secret": "undeclared",
	}})
	var got []string
	for _, b := range out.Blocks {
		got = append(got, b.ID+"="+b.Title)
	}
	if len(got) != 1 || got[0] != "ck:unlock=mine" {
		t.Fatalf("the plugin received %v, want only its own declared cookie", got)
	}
	// What a plugin sets is written under its namespace, whatever name it uses.
	out = pf.Apply(context.Background(), sdk.PageContext{Path: "/p"})
	if len(out.SetCookies) != 1 || out.SetCookies[0].Name != "hmp_capa_visited" {
		t.Fatalf("set cookies = %+v", out.SetCookies)
	}
	for _, name := range []string{"hatamoto_session", "hatamoto_csrf", "__Host-a", "__Secure-a", "hmp_x"} {
		m := capaManifest(func(m *sdk.Manifest) { m.Cookies = []string{name} })
		if err := m.Validate(); err == nil {
			t.Errorf("a manifest declaring cookie %q validated", name)
		}
	}
}

// By default (SDK 0.9) the plugin sees slim blocks and the host puts the
// original content back by id; a replacement block carries its own content.
func TestRenderPageSlimBlocks(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, slimManifest)
	pf := NewPageFilter(r.engine)
	rich := map[string]any{"headline": "H", "body": "<p>long rich text</p>", "variant": "dark", "items": []any{map[string]any{"a": "b"}}}
	blocks := []sdk.Block{{ID: "intro", Type: "hero", Title: "Hi", Content: rich}, {ID: "secret", Type: "text", Title: "Members only", Content: rich}}
	out := pf.Apply(context.Background(), sdk.PageContext{Path: "/p", Blocks: blocks})
	if len(out.Blocks) != 1 || out.Blocks[0].ID != "intro" || out.Blocks[0].Content["body"] != "<p>long rich text</p>" || out.Blocks[0].Fields != nil {
		t.Fatalf("original content not re-attached: %+v", out.Blocks)
	}
	if got := slimBlocks(blocks)[0]; got.Content != nil || got.Fields["variant"] != "dark" || len(got.Fields) != 1 {
		t.Fatalf("slim block = %+v", got)
	}

	// A plugin built for an older SDK, or asking for it, gets whole blocks.
	m := capaManifest(nil)
	if !m.FullPageBlocks() {
		t.Fatal("a ^0.6 plugin must get full blocks")
	}
	m = capaManifest(slimManifest)
	if m.FullPageBlocks() {
		t.Fatal("a ^0.9 plugin must get slim blocks")
	}
	m.RenderPage = &sdk.PageOptions{FullBlocks: true}
	if !m.FullPageBlocks() {
		t.Fatal("render_page.full_blocks must give full blocks")
	}
}

func TestMergePageResultSlim(t *testing.T) {
	m := sdk.Manifest{Name: "p", SDK: "^0.9"}
	cur := sdk.PageContext{Blocks: []sdk.Block{
		{ID: "a", Type: "hero", Content: map[string]any{"x": 1}}, {ID: "b", Type: "text", Content: map[string]any{"y": 2}}}}
	got, err := mergePageResult(cur, sdk.PageContext{Blocks: []sdk.Block{
		{ID: "b", Type: "text", Fields: map[string]any{"variant": "z"}}, // reordered, untouched
		{ID: "a", Content: map[string]any{"new": "content"}},            // replaced, type kept
		{ID: "c", Type: "cta", Content: map[string]any{"z": 3}},         // added
	}}, m)
	if err != nil || len(got.Blocks) != 3 || got.Blocks[0].Content["y"] != 2 || got.Blocks[0].Fields != nil ||
		got.Blocks[1].Type != "hero" || got.Blocks[1].Content["new"] != "content" || got.Blocks[2].ID != "c" {
		t.Fatalf("merge = %+v %v", got.Blocks, err)
	}
	if _, err := mergePageResult(cur, sdk.PageContext{Blocks: []sdk.Block{{ID: "new", Content: map[string]any{"a": 1}}}}, m); err == nil {
		t.Fatal("a new block without a type was accepted")
	}
}

func TestMergePageResultValidation(t *testing.T) {
	m := sdk.Manifest{Name: "p", Cookies: []string{"c"}}
	cur := sdk.PageContext{Blocks: []sdk.Block{{ID: "a"}}}
	for name, res := range map[string]sdk.PageContext{
		"undeclared":  {SetCookies: []sdk.SetCookie{{Name: "x", Value: "1"}}},
		"bad value":   {SetCookies: []sdk.SetCookie{{Name: "c", Value: "a;b"}}},
		"huge value":  {SetCookies: []sdk.SetCookie{{Name: "c", Value: strings.Repeat("a", maxCookieValue+1)}}},
		"bad max age": {SetCookies: []sdk.SetCookie{{Name: "c", Value: "1", MaxAge: maxCookieAge + 1}}},
		"bad path":    {SetCookies: []sdk.SetCookie{{Name: "c", Value: "1", Path: "x"}}},
		"too many":    {SetCookies: make([]sdk.SetCookie, maxSetCookies+1)},
		"too blocks":  {Blocks: make([]sdk.Block, maxPageBlocks+1)},
	} {
		if _, err := mergePageResult(cur, res, m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	got, err := mergePageResult(cur, sdk.PageContext{
		Blocks: []sdk.Block{{ID: "a"}, {ID: "b"}, {ID: "c", Hidden: true}}, Hide: []string{"b"},
		SetCookies: []sdk.SetCookie{{Name: "c", Value: "ok", MaxAge: 60}},
	}, m)
	if err != nil || len(got.Blocks) != 1 || got.Blocks[0].ID != "a" || len(got.SetCookies) != 1 || got.SetCookies[0].Name != "hmp_p_c" {
		t.Fatalf("merge = %+v %v", got, err)
	}
}

func TestFormSubmittedNeedsFormsRead(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	ev := sdk.FormSubmittedEvent{FormID: "f", FormSlug: "contact", SubmissionID: "s", Fields: map[string]any{"email": "a@b.c"}}

	// No grant: the plugin is not even called (the event carries visitor data).
	r.engine.Fire(context.Background(), sdk.FormSubmitted.Name, ev)
	r.engine.Wait()
	if strings.Contains(r.logs.String(), "plugin action failed") {
		t.Fatalf("form.submitted reached a plugin without forms.read:\n%s", r.logs.String())
	}

	// With forms.read the action runs; its kv write is denied (not granted),
	// which shows the delivery without needing a database.
	r.grant(t, sdk.PermFormsRead)
	r.engine.Fire(context.Background(), sdk.FormSubmitted.Name, ev)
	r.engine.Wait()
	if !strings.Contains(r.logs.String(), "capability_denied") {
		t.Fatalf("form.submitted was not delivered:\n%s", r.logs.String())
	}
}

func TestHostCallLimits(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	r.grant(t, sdk.PermLog)
	var limited bool
	for range 100 {
		if o := r.do(t, sdk.FnLog, map[string]any{"level": "info", "message": "x"}); o.Code == sdk.ErrRateLimited {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("log is not rate limited")
	}
}

// A failing plugin func renders as empty text (never a 500). Only
// funcFailLimit consecutive failures pause that one func: a single failure,
// the plugin's other funcs, and saturation do not.
func TestTemplateFuncFailsSoftAndPausesPerFunc(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	good := r.rt.templateFunc("capa", "shout", "func_shout")
	if out, err := good("hi"); err != nil || out != "HI<b>" {
		t.Fatalf("working func = %q %v", out, err)
	}
	ghost := r.rt.templateFunc("ghost", "x", "func_x") // not loaded: the call fails
	for i := 1; i < funcFailLimit; i++ {
		if out, err := ghost(); err != nil || out != "" {
			t.Fatalf("failing func = %q %v, want empty and no error", out, err)
		}
		if r.rt.funcs.paused("ghost/func_x") {
			t.Fatalf("paused after %d failure(s), want %d", i, funcFailLimit)
		}
	}
	_, _ = ghost()
	if !r.rt.funcs.paused("ghost/func_x") {
		t.Fatalf("%d consecutive failures must pause the func", funcFailLimit)
	}
	if r.rt.funcs.paused("ghost/func_y") || r.rt.funcs.paused("capa/func_shout") {
		t.Fatal("another func was paused")
	}
}

func TestFuncBreakerStreakAndSaturation(t *testing.T) {
	var b funcBreaker
	for i := 0; i < funcFailLimit-1; i++ {
		b.fail("p/f")
	}
	b.ok("p/f") // a success ends the streak
	b.fail("p/f")
	if b.paused("p/f") {
		t.Fatal("non-consecutive failures paused the func")
	}

	// Saturation (plugin merely busy) and breaker rejections are not strikes.
	for _, err := range []error{wasmhost.ErrSaturated, fmt.Errorf("x: %w", wasmhost.ErrCircuitOpen)} {
		if countsAsFuncFailure(err) {
			t.Errorf("%v counted as a func failure", err)
		}
	}
	if !countsAsFuncFailure(errors.New("timeout")) {
		t.Error("a timeout must count")
	}
}

// A blank caused by a failing plugin func, and every call skipped during the
// pause, must mark the render degraded so the page handler does not cache it.
func TestTemplateFuncFailureMarksRenderDegraded(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	before := render.DegradedCount()
	good := r.rt.templateFunc("capa", "shout", "func_shout")
	if _, err := good("hi"); err != nil || render.DegradedCount() != before {
		t.Fatalf("a working func marked the render degraded (count %d, was %d)", render.DegradedCount(), before)
	}
	ghost := r.rt.templateFunc("ghost", "x", "func_x") // not loaded: the call fails
	_, _ = ghost()
	afterFail := render.DegradedCount()
	if afterFail == before {
		t.Fatal("a failed func did not mark the render degraded")
	}
	_, _ = ghost() // skipped by the breaker pause
	if render.DegradedCount() == afterFail {
		t.Fatal("a func skipped by the pause did not mark the render degraded")
	}
}

type fakeMailer struct {
	sent [][]string
}

func (f *fakeMailer) IsConfigured() bool { return true }
func (f *fakeMailer) Send(_ context.Context, to []string, _, _, _ string) error {
	f.sent = append(f.sent, to)
	return nil
}

func TestMailSendValidationAndRateLimit(t *testing.T) {
	r := newCapaRig(t)
	mailer := &fakeMailer{}
	svc := r.rt.caps.api.svc
	svc.Mail = mailer
	r.rt.Configure(Config{Services: svc, Scheduler: r.sched})
	r.load(t, func(m *sdk.Manifest) { m.Permissions = append(m.Permissions, sdk.PermMailSend) })
	r.grant(t, sdk.PermMailSend)

	send := func(to []string, subject string) outcome {
		return r.do(t, sdk.FnMailSend, map[string]any{"to": to, "subject": subject, "text": "hi"})
	}
	for name, o := range map[string]outcome{
		"CRLF in subject":      send([]string{"a@example.com"}, "hi\r\nBcc: x@example.com"),
		"display-name address": send([]string{"A <a@example.com>"}, "s"),
		"too many recipients":  send(make([]string, 11), "s"),
		"no recipients":        send(nil, "s"),
		"not an address":       send([]string{"nope"}, "s"),
		"empty subject":        send([]string{"a@example.com"}, ""),
	} {
		if o.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s: %+v", name, o)
		}
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("invalid mail was sent: %v", mailer.sent)
	}
	// The limit counts recipients, not calls: 10 go out at once, the next one waits.
	ten := make([]string, 10)
	for i := range ten {
		ten[i] = fmt.Sprintf("u%d@example.com", i)
	}
	if o := send(ten, "s"); o.Code != "" {
		t.Fatalf("10 recipients within the burst: %+v", o)
	}
	if o := send([]string{"x@example.com"}, "s"); o.Code != sdk.ErrRateLimited {
		t.Fatalf("the 11th recipient inside a minute: %+v", o)
	}
}

// A render.page result may use core/theme types and its own section types, but
// not a type another plugin registered (that plugin's grants, not this one's,
// decide what its markup may do). A block the page already had keeps its type.
func TestMergePageResultRefusesAnotherPluginsSectionType(t *testing.T) {
	if err := render.RegisterPluginSection("granted-one", "promo_x", `{{define "section-promo_x"}}<b>x</b>{{end}}`); err != nil {
		t.Fatal(err)
	}
	if err := render.RegisterPluginSection("mine", "mine_x", `{{define "section-mine_x"}}<b>x</b>{{end}}`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { render.RemovePlugin("granted-one"); render.RemovePlugin("mine") })
	m := sdk.Manifest{Name: "mine", SDK: "^0.9"}
	cur := sdk.PageContext{Blocks: []sdk.Block{{ID: "kept", Type: "promo_x", Content: map[string]any{"a": 1}}}}
	if _, err := mergePageResult(cur, sdk.PageContext{Blocks: []sdk.Block{{ID: "new", Type: "promo_x", Content: map[string]any{"a": 1}}}}, m); err == nil {
		t.Error("a new block of another plugin's section type was accepted")
	}
	if _, err := mergePageResult(cur, sdk.PageContext{Blocks: []sdk.Block{{ID: "kept", Type: "mine_x", Content: map[string]any{"a": 1}}, {ID: "h", Type: "hero", Content: map[string]any{"a": 1}}}}, m); err != nil {
		t.Errorf("its own and core types: %v", err)
	}
	if _, err := mergePageResult(cur, sdk.PageContext{Blocks: []sdk.Block{{ID: "kept", Type: "promo_x", Content: map[string]any{"a": 1}}}}, m); err != nil {
		t.Errorf("a block the page already had: %v", err)
	}
	if _, err := mergePageResult(sdk.PageContext{Blocks: []sdk.Block{{ID: "kept", Type: "hero"}}}, sdk.PageContext{Blocks: []sdk.Block{{ID: "kept", Type: "promo_x", Content: map[string]any{"a": 1}}}}, m); err == nil {
		t.Error("retyping a block to another plugin's type was accepted")
	}
}

func TestGrantPolicyRefuses(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, func(m *sdk.Manifest) { m.Permissions = []string{sdk.PermKV, sdk.PermSiteScripts} })
	r.rt.SetGrantPolicy(func(_ string, perms []sdk.Permission) error {
		if slices.Contains(perms, sdk.PermSiteScripts) {
			return ErrCommunityScripts
		}
		return nil
	})
	if _, err := r.rt.Grant(context.Background(), "capa", []string{sdk.PermKV, sdk.PermSiteScripts}); !errors.Is(err, ErrCommunityScripts) {
		t.Fatalf("policy did not refuse: %v", err)
	}
	if g := r.rt.Grants().Granted("capa"); len(g) != 0 {
		t.Fatalf("a refused grant changed the grants: %v", g)
	}
	if _, err := r.rt.Grant(context.Background(), "capa", []string{sdk.PermKV}); err != nil {
		t.Fatalf("a grant without site.scripts: %v", err)
	}
}
