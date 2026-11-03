//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

// hostCall is one entities.* host function call made from inside a loaded
// plugin, over the real services and the public site's routing.
type hostCall struct {
	t  *testing.T
	rt *pluginhost.Runtime
}

type hostOutcome struct {
	Result json.RawMessage `json:"result"`
	Code   string          `json:"code"`
	Msg    string          `json:"message"`
}

func newHostCall(t *testing.T, r *nestedRig, base string) *hostCall {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt, err := pluginhost.NewRuntime(log, hooks.New(log), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	r.srv.cfg = &config.Config{}
	r.srv.cfg.PublicSite.BaseURL = base
	rt.Configure(pluginhost.Config{Services: pluginhost.Services{Entities: r.svc, Paths: pluginEntityPaths{r.srv}}})
	m := sdk.Manifest{Name: "capa", Version: "0.1.0", SDK: "^0.6", Permissions: []string{sdk.PermEntitiesRead}}
	if st := rt.Load(r.ctx, m, plugintest.Build(t, "capa")); !st.Loaded {
		t.Fatalf("capa not loaded: %s", st.Error)
	}
	if _, err := rt.Grant(r.ctx, "capa", []string{sdk.PermEntitiesRead}); err != nil {
		t.Fatal(err)
	}
	return &hostCall{t: t, rt: rt}
}

func (h *hostCall) do(ctx context.Context, fn string, args any) hostOutcome {
	h.t.Helper()
	raw, _ := json.Marshal(args)
	in, _ := json.Marshal(sdk.CallRequest{Fn: fn, Args: raw})
	out, err := h.rt.Host().Call(ctx, "capa", "call_host", in)
	if err != nil {
		h.t.Fatalf("call_host %s: %v", fn, err)
	}
	var o hostOutcome
	if err := json.Unmarshal(out, &o); err != nil {
		h.t.Fatalf("outcome %q: %v", out, err)
	}
	return o
}

type dtoView struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	ParentID string `json:"parent_id"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	Status   string `json:"status"`
}

func (h *hostCall) ok(fn string, args any) dtoView {
	h.t.Helper()
	o := h.do(context.Background(), fn, args)
	if o.Code != "" {
		h.t.Fatalf("%s %v: %s %s", fn, args, o.Code, o.Msg)
	}
	var d dtoView
	if err := json.Unmarshal(o.Result, &d); err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *hostCall) fails(fn string, args any, code string) {
	h.t.Helper()
	if o := h.do(context.Background(), fn, args); o.Code != code {
		h.t.Fatalf("%s %v: code %q (%s), want %q", fn, args, o.Code, o.Msg, code)
	}
}

// create adds an entity through the entity service (plugins no longer write
// content directly) and returns it as the plugin sees it.
func (h *hostCall) create(r *nestedRig, schema uuid.UUID, slug string, parent *uuid.UUID, publish bool) dtoView {
	h.t.Helper()
	e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: schema, Name: slug, Slug: slug, ParentID: parent, Attributes: json.RawMessage(`{}`)})
	if err != nil {
		h.t.Fatalf("create %s: %v", slug, err)
	}
	if publish {
		if _, err := r.svc.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
			h.t.Fatalf("publish %s: %v", slug, err)
		}
	}
	return h.ok(sdk.FnEntitiesGet, map[string]any{"id": e.ID.String()})
}

// A plugin reads a tree under a parent, each node's path and url, and finds
// nodes again by path or by parent and slug.
func TestPluginEntityTree_Integration(t *testing.T) {
	r := newNestedRig(t)
	h := newHostCall(t, r, "https://site.test/")
	services := r.page("services", nil)

	mk := func(slug string, parent string, publish bool) dtoView {
		var pid *uuid.UUID
		if parent != "" {
			id := uuid.MustParse(parent)
			pid = &id
		}
		return h.create(r, r.pageSch, slug, pid, publish)
	}
	seo := mk("seo", services.ID.String(), false)
	if seo.ParentID != services.ID.String() || seo.Path != "/services/seo" || seo.URL != "https://site.test/services/seo" {
		t.Fatalf("child = %+v", seo)
	}
	local := mk("local", seo.ID, false)
	if local.Path != "/services/seo/local" {
		t.Fatalf("grandchild path = %q", local.Path)
	}
	root := mk("loose", "", false)
	if root.ParentID != "" || root.Path != "/loose" {
		t.Fatalf("root = %+v", root)
	}

	// By id the DTO carries the same fields.
	if got := h.ok(sdk.FnEntitiesGet, map[string]any{"id": local.ID}); got.Path != local.Path || got.ParentID != seo.ID {
		t.Fatalf("get by id = %+v", got)
	}
	// By parent and slug: exact where a bare slug is ambiguous. Drafts are found.
	if got := h.ok(sdk.FnEntitiesGet, map[string]any{"parent_id": seo.ID, "slug": "local"}); got.ID != local.ID {
		t.Fatalf("get by parent+slug = %+v", got)
	}
	if got := h.ok(sdk.FnEntitiesGet, map[string]any{"parent_id": nil, "slug": "loose"}); got.ID != root.ID {
		t.Fatalf("get root by slug = %+v", got)
	}
	// Slugs are unique per schema: a note and a page may share a root slug.
	note := h.create(r, r.noteSch, "loose", nil, false)
	h.fails(sdk.FnEntitiesGet, map[string]any{"parent_id": nil, "slug": "loose"}, sdk.ErrInvalidArgument)
	if got := h.ok(sdk.FnEntitiesGet, map[string]any{"parent_id": nil, "slug": "loose", "schema_slug": "nested-page"}); got.ID != root.ID {
		t.Fatalf("get root page by schema = %+v", got)
	}
	if got := h.ok(sdk.FnEntitiesGet, map[string]any{"parent_id": nil, "slug": "loose", "schema_slug": "note"}); got.ID != note.ID {
		t.Fatalf("get root note by schema = %+v", got)
	}
	h.fails(sdk.FnEntitiesGet, map[string]any{"parent_id": services.ID.String(), "slug": "local"}, sdk.ErrNotFound)
	// schema_slug without parent_id, or mixed selectors, are refused, never guessed.
	h.fails(sdk.FnEntitiesGet, map[string]any{"slug": "loose", "schema_slug": "note"}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesGet, map[string]any{"id": root.ID, "slug": "loose"}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesGet, map[string]any{"path": "/loose", "schema_slug": "note"}, sdk.ErrInvalidArgument)

	// By path: the public resolver serves published pages only.
	h.fails(sdk.FnEntitiesGet, map[string]any{"path": "/services/seo/local"}, sdk.ErrNotFound)
	if _, err := r.svc.UpdateEntityStatus(r.ctx, uuid.MustParse(local.ID), nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	if got := h.ok(sdk.FnEntitiesGet, map[string]any{"path": "/services/seo/local/"}); got.ID != local.ID || got.Status != "published" {
		t.Fatalf("get by path = %+v", got)
	}
	h.fails(sdk.FnEntitiesGet, map[string]any{"path": "relative"}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesGet, map[string]any{"path": "/a?b=1"}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesGet, map[string]any{"path": "/nope"}, sdk.ErrNotFound)

	// A move made by the owner shows in the plugin's read: the path follows the parent.
	svcID := services.ID
	if _, err := r.svc.UpdateEntity(r.ctx, uuid.MustParse(local.ID), nil, &entity.UpdateEntityInput{ParentID: &svcID}); err != nil {
		t.Fatal(err)
	}
	if moved := h.ok(sdk.FnEntitiesGet, map[string]any{"id": local.ID}); moved.ParentID != services.ID.String() || moved.Path != "/services/local" {
		t.Fatalf("moved = %+v", moved)
	}
}

// Without a configured origin the url is the path.
func TestPluginEntityURLWithoutBase_Integration(t *testing.T) {
	r := newNestedRig(t)
	h := newHostCall(t, r, "")
	e := h.create(r, r.pageSch, "solo", nil, false)
	if e.Path != "/solo" || e.URL != "/solo" {
		t.Fatalf("no base: %+v", e)
	}
}
