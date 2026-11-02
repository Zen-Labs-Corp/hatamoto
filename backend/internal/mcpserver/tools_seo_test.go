package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/seosvc"
)

type fakeSEO struct {
	actor   seosvc.Actor
	patch   map[string]json.RawMessage
	updErr  error
	doc     seosvc.Doc
	prevErr error
}

func (f *fakeSEO) Get(context.Context) (seosvc.Doc, error) { return f.doc, nil }
func (f *fakeSEO) Update(_ context.Context, a seosvc.Actor, p map[string]json.RawMessage) (seosvc.Doc, error) {
	f.actor, f.patch = a, p
	return f.doc, f.updErr
}
func (f *fakeSEO) Preview(context.Context, uuid.UUID) (seosvc.Preview, error) {
	return seosvc.Preview{}, f.prevErr
}
func (f *fakeSEO) Analyze(context.Context, uuid.UUID) (seosvc.Panel, error) {
	return seosvc.Panel{}, f.prevErr
}

type fakeNotFound struct{ got redirect.NotFoundListParams }

func (f *fakeNotFound) List404Entries(_ context.Context, p redirect.NotFoundListParams) ([]*redirect.NotFoundEntry, int, error) {
	f.got = p
	return []*redirect.NotFoundEntry{{ID: uuid.New(), Path: "/gone", HitCount: 3}}, 7, nil
}

func callSEO(t *testing.T, scopes []hmauth.APIKeyScope, d Deps, tool string, args map[string]any) (string, bool) {
	t.Helper()
	ts := newTestServer(t, scopes, d)
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	sess, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.URL, HTTPClient: &http.Client{Transport: bearerRT{"good"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].(*mcp.TextContent).Text
	}
	return text, res.IsError
}

func TestSEOGetReadOnlyKey(t *testing.T) {
	f := &fakeSEO{doc: seosvc.Doc{"separator": map[string]any{"char": "-"}}}
	text, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeReadSEO}, Deps{SEO: f}, "seo_get", map[string]any{})
	if isErr || !strings.Contains(text, `"separator"`) {
		t.Fatalf("seo_get = %v %q", isErr, text)
	}
	text, isErr = callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteSEO}, Deps{SEO: f}, "seo_get", map[string]any{})
	if isErr {
		t.Fatalf("write:seo key must read: %q", text)
	}
}

func TestSEOUpdateNeedsWriteScope(t *testing.T) {
	f := &fakeSEO{}
	text, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeReadSEO}, Deps{SEO: f}, "seo_update", map[string]any{"patch": map[string]any{"home": map[string]any{}}})
	if !isErr || !strings.Contains(text, "write:seo") || f.patch != nil {
		t.Fatalf("seo_update with read:seo = %v %q (patch %v)", isErr, text, f.patch)
	}
}

func TestSEOUpdateAuditsKeyName(t *testing.T) {
	f := &fakeSEO{doc: seosvc.Doc{}}
	aud := &recAudit{}
	_, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteSEO}, Deps{SEO: f, Audit: aud}, "seo_update",
		map[string]any{"patch": map[string]any{"separator": map[string]any{"char": "|"}}})
	if isErr {
		t.Fatal("seo_update failed")
	}
	if f.actor.Via != "mcp" || f.actor.KeyName != "t" || f.actor.KeyID != fakeKeyID.String() || f.actor.Label() != "api-key:t" {
		t.Errorf("actor = %+v", f.actor)
	}
	if string(f.patch["separator"]) != `{"char":"|"}` {
		t.Errorf("patch = %s", f.patch["separator"])
	}
	if len(aud.entries) != 1 || aud.entries[0].KeyName != "t" || aud.entries[0].Tool != "seo_update" {
		t.Errorf("audit rows = %+v", aud.entries)
	}
}

func TestSEOUpdateValidationReachesAgent(t *testing.T) {
	f := &fakeSEO{updErr: &seosvc.ValidationError{Fields: map[string]string{"type[doc][title]": "too long", "indexnow": "read-only"}}}
	text, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteSEO}, Deps{SEO: f}, "seo_update", map[string]any{"patch": map[string]any{"x": 1}})
	if !isErr || !strings.Contains(text, "indexnow: read-only") || !strings.Contains(text, "type[doc][title]: too long") || strings.Contains(text, "internal error") {
		t.Fatalf("validation text = %v %q", isErr, text)
	}
}

func TestSEOUnavailableAndNotFound(t *testing.T) {
	text, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeReadSEO}, Deps{}, "seo_get", map[string]any{})
	if !isErr || !strings.Contains(text, "not available") {
		t.Errorf("no service: %v %q", isErr, text)
	}
	f := &fakeSEO{prevErr: seosvc.ErrNotFound}
	text, isErr = callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeReadSEO}, Deps{SEO: f}, "seo_preview", map[string]any{"entity_id": uuid.NewString()})
	if !isErr || !strings.Contains(text, "not found") {
		t.Errorf("preview draft: %v %q", isErr, text)
	}
}

func TestSEO404List(t *testing.T) {
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadSEO}
	for _, v := range []string{"", "people", "bots"} {
		nf := &fakeNotFound{}
		text, isErr := callSEO(t, scopes, Deps{NotFound: nf}, "seo_404_list", map[string]any{"search": "gone", "visitor": v, "page": 2, "per_page": 500})
		want := redirect.NotFoundListParams{Search: "gone", Visitor: v, Page: 2, PerPage: maxListLen}
		if isErr || nf.got != want {
			t.Errorf("visitor %q: err=%v params=%+v", v, isErr, nf.got)
		}
		items, total, _ := nf.List404Entries(context.Background(), want)
		var out struct {
			Items []redirect.NotFoundEntry `json:"items"`
			Total int                      `json:"total"`
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil || out.Total != total || len(out.Items) != len(items) || out.Items[0].Path != items[0].Path {
			t.Errorf("visitor %q: output %q", v, text)
		}
	}
	nf := &fakeNotFound{}
	text, isErr := callSEO(t, scopes, Deps{NotFound: nf}, "seo_404_list", map[string]any{"visitor": "robots"})
	if !isErr || !strings.Contains(text, "visitor") || nf.got != (redirect.NotFoundListParams{}) {
		t.Errorf("visitor robots: %v %q %+v", isErr, text, nf.got)
	}
}
