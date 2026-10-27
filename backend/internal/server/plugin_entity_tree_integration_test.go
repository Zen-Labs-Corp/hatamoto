//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
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
	m := sdk.Manifest{Name: "capa", Version: "0.1.0", SDK: "^0.6", Permissions: []string{sdk.PermEntitiesRead, sdk.PermEntitiesWrite}}
	if st := rt.Load(r.ctx, m, plugintest.Build(t, "capa")); !st.Loaded {
		t.Fatalf("capa not loaded: %s", st.Error)
	}
	if _, err := rt.Grant(r.ctx, "capa", []string{sdk.PermEntitiesRead, sdk.PermEntitiesWrite}); err != nil {
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

// A plugin builds a tree under a parent, reads each node's path and url, and
// finds nodes again by path or by parent and slug; the tree rules are the
// entity service's.
func TestPluginEntityTree_Integration(t *testing.T) {
	r := newNestedRig(t)
	h := newHostCall(t, r, "https://site.test/")
	services := r.page("services", nil)

	mk := func(slug string, parent string) dtoView {
		args := map[string]any{"schema_slug": "nested-page", "name": slug, "slug": slug}
		if parent != "" {
			args["parent_id"] = parent
		}
		return h.ok(sdk.FnEntitiesCreate, args)
	}
	seo := mk("seo", services.ID.String())
	if seo.ParentID != services.ID.String() || seo.Path != "/services/seo" || seo.URL != "https://site.test/services/seo" {
		t.Fatalf("created child = %+v", seo)
	}
	local := mk("local", seo.ID)
	if local.Path != "/services/seo/local" {
		t.Fatalf("grandchild path = %q", local.Path)
	}
	root := mk("loose", "")
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
	note := h.ok(sdk.FnEntitiesCreate, map[string]any{"schema_slug": "note", "name": "loose", "slug": "loose"})
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
	h.ok(sdk.FnEntitiesUpdate, map[string]any{"id": local.ID, "name": "local"}) // still a draft
	h.ok(sdk.FnEntitiesStatus, map[string]any{"id": local.ID, "status": "published"})
	if got := h.ok(sdk.FnEntitiesGet, map[string]any{"path": "/services/seo/local/"}); got.ID != local.ID || got.Status != "published" {
		t.Fatalf("get by path = %+v", got)
	}
	h.fails(sdk.FnEntitiesGet, map[string]any{"path": "relative"}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesGet, map[string]any{"path": "/a?b=1"}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesGet, map[string]any{"path": "/nope"}, sdk.ErrNotFound)

	// Update moves under another parent (the path follows), null makes a root.
	moved := h.ok(sdk.FnEntitiesUpdate, map[string]any{"id": local.ID, "parent_id": services.ID.String()})
	if moved.ParentID != services.ID.String() || moved.Path != "/services/local" {
		t.Fatalf("moved = %+v", moved)
	}
	up := h.ok(sdk.FnEntitiesUpdate, map[string]any{"id": local.ID, "parent_id": nil})
	if up.ParentID != "" || up.Path != "/local" {
		t.Fatalf("made root = %+v", up)
	}
	kept := h.ok(sdk.FnEntitiesUpdate, map[string]any{"id": seo.ID, "name": "SEO"}) // parent untouched
	if kept.ParentID != services.ID.String() {
		t.Fatalf("an update without parent_id changed the parent: %+v", kept)
	}

	// Validation is the service's: missing parent, cycle, own parent, too deep.
	h.fails(sdk.FnEntitiesCreate, map[string]any{"schema_slug": "nested-page", "name": "x", "parent_id": uuid.NewString()}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesCreate, map[string]any{"schema_slug": "nested-page", "name": "x", "parent_id": "nope"}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesUpdate, map[string]any{"id": services.ID.String(), "parent_id": seo.ID}, sdk.ErrInvalidArgument)
	h.fails(sdk.FnEntitiesUpdate, map[string]any{"id": seo.ID, "parent_id": seo.ID}, sdk.ErrInvalidArgument)
	parent := root.ID
	for i := 0; i < 7; i++ { // /loose + 7 = the 8 levels a URL can express
		parent = mk("d"+strings.Repeat("x", i+1), parent).ID
	}
	h.fails(sdk.FnEntitiesCreate, map[string]any{"schema_slug": "nested-page", "name": "deep", "parent_id": parent}, sdk.ErrInvalidArgument)
}

// Without a configured origin the url is the path.
func TestPluginEntityURLWithoutBase_Integration(t *testing.T) {
	r := newNestedRig(t)
	h := newHostCall(t, r, "")
	e := h.ok(sdk.FnEntitiesCreate, map[string]any{"schema_slug": "nested-page", "name": "solo", "slug": "solo"})
	if e.Path != "/solo" || e.URL != "/solo" {
		t.Fatalf("no base: %+v", e)
	}
}
