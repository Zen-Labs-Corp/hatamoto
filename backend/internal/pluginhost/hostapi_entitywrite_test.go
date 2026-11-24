package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// fakeWriter records what reaches the service and answers with err.
type fakeWriter struct {
	mu         sync.Mutex
	calls      []string
	err        error
	gotWrite   entity.PluginWrite
	hadVisitor bool
}

func (f *fakeWriter) record(ctx context.Context, what string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, what)
	_, f.hadVisitor = authzctx.From(ctx)
}

func (f *fakeWriter) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func fakeEntity(w entity.PluginWrite, id uuid.UUID) *entity.Entity {
	return &entity.Entity{ID: id, Name: w.Name, Slug: "s", Status: entity.StatusPublished, Attributes: jsontext.Value(`{}`),
		Schema: &entity.EntitySchema{Slug: w.Schema}}
}

func (f *fakeWriter) PluginCreate(ctx context.Context, _ string, w entity.PluginWrite) (*entity.Entity, error) {
	f.record(ctx, "create")
	f.gotWrite = w
	if f.err != nil {
		return nil, f.err
	}
	return fakeEntity(w, uuid.New()), nil
}

func (f *fakeWriter) PluginUpdate(ctx context.Context, _ string, id uuid.UUID, w entity.PluginWrite) (*entity.Entity, error) {
	f.record(ctx, "update")
	if f.err != nil {
		return nil, f.err
	}
	return fakeEntity(w, id), nil
}

func (f *fakeWriter) PluginDelete(ctx context.Context, _, schema string, _ uuid.UUID) (*entity.Entity, error) {
	f.record(ctx, "delete")
	if f.err != nil {
		return nil, f.err
	}
	return &entity.Entity{Name: "gone", Schema: &entity.EntitySchema{Slug: schema}}, nil
}

type auditRow struct {
	plugin, action, target string
	detail                 map[string]any
}

type auditRec struct {
	mu   sync.Mutex
	rows []auditRow
}

func (r *auditRec) record(_ context.Context, plugin, action, target string, detail map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, auditRow{plugin, action, target, detail})
}

func (r *auditRec) all() []auditRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]auditRow(nil), r.rows...)
}

type writeRig struct {
	api   *API
	fake  *fakeWriter
	audit *auditRec
}

// newWriteRig grants entities.write.own to the plugins in granted; every plugin
// owns the schema "story" with the fields url and text.
func newWriteRig(t *testing.T, granted ...string) *writeRig {
	t.Helper()
	g := NewGrantStore(nil)
	for _, p := range granted {
		if err := g.Set(context.Background(), p, []sdk.Permission{sdk.PermEntitiesWriteOwn}); err != nil {
			t.Fatal(err)
		}
	}
	r := &writeRig{fake: &fakeWriter{}, audit: &auditRec{}}
	r.api = NewAPI(Services{EntityWrites: r.fake, Audit: r.audit.record}, g, func(p string) (sdk.Manifest, bool) {
		return sdk.Manifest{Name: p, SDK: "^0.27", Permissions: []string{sdk.PermEntitiesWriteOwn}, EntitySchema: "story", EntityFields: []string{"url", "text"}}, true
	}, nil)
	return r
}

func (r *writeRig) call(t *testing.T, plugin, fn string, args any) sdk.CallResponse {
	t.Helper()
	return r.callCtx(t, context.Background(), plugin, fn, args)
}

func (r *writeRig) callCtx(t *testing.T, ctx context.Context, plugin, fn string, args any) sdk.CallResponse {
	t.Helper()
	raw, err := jsonx.MarshalPinned(args)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: fn, Args: raw})
	var out sdk.CallResponse
	if err := jsonx.UnmarshalPinned(r.api.Call(ctx, plugin, in), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func code(o sdk.CallResponse) string {
	if o.OK || o.Error == nil {
		return "ok"
	}
	return o.Error.Code
}

func TestEntityWriteGuards(t *testing.T) {
	t.Parallel()
	r := newWriteRig(t, "capa")
	id := uuid.NewString()
	for _, fn := range []string{sdk.FnEntitiesCreate, sdk.FnEntitiesUpdate, sdk.FnEntitiesDelete} {
		if got := code(r.call(t, "stranger", fn, map[string]any{"id": id, "name": "x"})); got != sdk.ErrCapabilityDenied {
			t.Errorf("%s without a grant = %s", fn, got)
		}
	}
	big := strings.Repeat("a", changeset.MaxCreateString+1)
	for name, c := range map[string]struct {
		fn   string
		args map[string]any
	}{
		"key outside entity_fields": {sdk.FnEntitiesCreate, map[string]any{"name": "x", "attributes": map[string]any{"body": "y"}}},
		"status archived":           {sdk.FnEntitiesCreate, map[string]any{"name": "x", "status": "archived"}},
		"string over the cap":       {sdk.FnEntitiesCreate, map[string]any{"name": "x", "attributes": map[string]any{"text": big}}},
		"create without a name":     {sdk.FnEntitiesCreate, map[string]any{"attributes": map[string]any{"url": "u"}}},
		"create with an id":         {sdk.FnEntitiesCreate, map[string]any{"id": id, "name": "x"}},
		"update with a bad id":      {sdk.FnEntitiesUpdate, map[string]any{"id": "nope", "name": "x"}},
		"update without an id":      {sdk.FnEntitiesUpdate, map[string]any{"name": "x"}},
		"update naming a slug":      {sdk.FnEntitiesUpdate, map[string]any{"id": id, "slug": "other"}},
		"update bad status":         {sdk.FnEntitiesUpdate, map[string]any{"id": id, "status": "scheduled"}},
		"update key outside":        {sdk.FnEntitiesUpdate, map[string]any{"id": id, "attributes": map[string]any{"title": "y"}}},
		"delete with a bad id":      {sdk.FnEntitiesDelete, map[string]any{"id": "nope"}},
		"unknown argument":          {sdk.FnEntitiesCreate, map[string]any{"name": "x", "origin": "owner"}},
	} {
		if got := code(r.call(t, "capa", c.fn, c.args)); got != sdk.ErrInvalidArgument {
			t.Errorf("%s = %s, want invalid_argument", name, got)
		}
	}
	if r.fake.n() != 0 || len(r.audit.all()) != 0 {
		t.Fatalf("refused calls reached the service (%d) or the audit (%d)", r.fake.n(), len(r.audit.all()))
	}

	r.fake.err = entity.ErrNotPluginOwned
	for fn, args := range map[string]map[string]any{
		sdk.FnEntitiesCreate: {"name": "x"}, sdk.FnEntitiesUpdate: {"id": id, "name": "x"}, sdk.FnEntitiesDelete: {"id": id},
	} {
		o := r.call(t, "capa", fn, args)
		if code(o) != sdk.ErrNotOwned || strings.Contains(o.Error.Message, "story") {
			t.Errorf("%s ownership refusal = %+v", fn, o.Error)
		}
	}
	if len(r.audit.all()) != 0 {
		t.Fatalf("a refusal was audited: %+v", r.audit.all())
	}

	// The service sees the manifest's schema and whitelist, never the plugin's choice.
	r.fake.err = nil
	if o := r.call(t, "capa", sdk.FnEntitiesCreate, map[string]any{"name": "x", "attributes": map[string]any{"url": "u"}}); !o.OK {
		t.Fatalf("create: %+v", o.Error)
	}
	if w := r.fake.gotWrite; w.Schema != "story" || len(w.Fields) != 2 || w.Status != "" {
		t.Fatalf("service saw %+v", w)
	}
}

func TestEntityWriteRateLimit(t *testing.T) {
	t.Parallel()
	r := newWriteRig(t, "noisy", "quiet")
	for i := range 20 {
		if o := r.call(t, "noisy", sdk.FnEntitiesCreate, map[string]any{"name": "x"}); !o.OK {
			t.Fatalf("write %d inside the burst: %+v", i+1, o.Error)
		}
	}
	o := r.call(t, "noisy", sdk.FnEntitiesCreate, map[string]any{"name": "x"})
	if code(o) != sdk.ErrRateLimited || o.Error.Message != "entities.create: 60 per minute" {
		t.Fatalf("21st create = %+v", o)
	}
	// The bucket is shared by the three functions.
	if o := r.call(t, "noisy", sdk.FnEntitiesUpdate, map[string]any{"id": uuid.NewString(), "name": "x"}); code(o) != sdk.ErrRateLimited {
		t.Fatalf("update shares the bucket: %+v", o)
	}
	if o := r.call(t, "noisy", sdk.FnEntitiesDelete, map[string]any{"id": uuid.NewString()}); code(o) != sdk.ErrRateLimited {
		t.Fatalf("delete shares the bucket: %+v", o)
	}
	if r.fake.n() != 20 {
		t.Fatalf("service calls = %d, want 20 (refused ones must not reach it)", r.fake.n())
	}
	if o := r.call(t, "quiet", sdk.FnEntitiesCreate, map[string]any{"name": "x"}); !o.OK {
		t.Fatalf("another plugin is limited too: %+v", o.Error)
	}
	// An invalid call spends no token.
	r2 := newWriteRig(t, "capa")
	for range 30 {
		r2.call(t, "capa", sdk.FnEntitiesCreate, map[string]any{"name": "x", "status": "archived"})
	}
	if o := r2.call(t, "capa", sdk.FnEntitiesCreate, map[string]any{"name": "x"}); !o.OK {
		t.Fatalf("refusals spent tokens: %+v", o)
	}
}

func TestEntityWriteAudit(t *testing.T) {
	t.Parallel()
	r := newWriteRig(t, "capa")
	created := r.call(t, "capa", sdk.FnEntitiesCreate, map[string]any{"name": "First", "attributes": map[string]any{"url": "u"}})
	if !created.OK {
		t.Fatalf("create: %+v", created.Error)
	}
	var dto struct{ ID string }
	if err := jsonx.UnmarshalPinned(created.Result, &dto); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if o := r.call(t, "capa", sdk.FnEntitiesUpdate, map[string]any{"id": id, "name": "Second", "status": "draft", "attributes": map[string]any{"url": "u", "text": "t"}}); !o.OK {
		t.Fatalf("update: %+v", o.Error)
	}
	if o := r.call(t, "capa", sdk.FnEntitiesDelete, map[string]any{"id": id}); !o.OK {
		t.Fatalf("delete: %+v", o.Error)
	}
	rows := r.audit.all()
	if len(rows) != 3 {
		t.Fatalf("%d audit rows, want 3: %+v", len(rows), rows)
	}
	want := []struct{ action, target string }{
		{"plugin.entity.create", dto.ID}, {"plugin.entity.update", id}, {"plugin.entity.delete", id},
	}
	for i, w := range want {
		if rows[i].plugin != "capa" || rows[i].action != w.action || rows[i].target != w.target || rows[i].detail["schema"] != "story" {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
	if rows[0].detail["name"] != "First" || rows[2].detail["name"] != "gone" {
		t.Errorf("names: %v / %v", rows[0].detail, rows[2].detail)
	}
	if f, _ := rows[1].detail["fields"].([]string); strings.Join(f, ",") != "name,status,text,url" {
		t.Errorf("update fields = %v", rows[1].detail["fields"])
	}
}

func TestEntityWriteDropsVisitorSubject(t *testing.T) {
	t.Parallel()
	r := newWriteRig(t, "capa")
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "u1", Role: "owner"})
	if _, ok := authzctx.From(ctx); !ok {
		t.Fatal("test setup: no subject on ctx")
	}
	if o := r.callCtx(t, ctx, "capa", sdk.FnEntitiesCreate, map[string]any{"name": "x"}); !o.OK {
		t.Fatalf("create: %+v", o.Error)
	}
	if r.fake.hadVisitor {
		t.Fatal("the visitor's subject reached the service")
	}
}

func TestEntityWriteNotUnavailable(t *testing.T) {
	t.Parallel()
	r := newWriteRig(t, "capa")
	for fn, args := range map[string]map[string]any{
		sdk.FnEntitiesCreate: {"name": "x"},
		sdk.FnEntitiesUpdate: {"id": uuid.NewString()},
		sdk.FnEntitiesDelete: {"id": uuid.NewString()},
	} {
		if o := r.call(t, "capa", fn, args); code(o) == sdk.ErrUnavailable || !o.OK {
			t.Errorf("%s with a service = %+v", fn, o)
		}
	}
	// No service: unavailable, even for an argument the guards would refuse.
	g := NewGrantStore(nil)
	_ = g.Set(context.Background(), "capa", []sdk.Permission{sdk.PermEntitiesWriteOwn})
	a := NewAPI(Services{}, g, func(p string) (sdk.Manifest, bool) { return sdk.Manifest{Name: p, EntitySchema: "story"}, true }, nil)
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: sdk.FnEntitiesCreate, Args: jsontext.Value(`{}`)})
	var out sdk.CallResponse
	if err := jsonx.UnmarshalPinned(a.Call(context.Background(), "capa", in), &out); err != nil || code(out) != sdk.ErrUnavailable {
		t.Fatalf("no service: %+v %v", out, err)
	}
}
