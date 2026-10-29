package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

type fakeKeys struct{ scopes []hmauth.APIKeyScope }

var fakeKeyID, fakeOwner = uuid.New(), uuid.New()

func (f fakeKeys) ValidateKey(_ context.Context, plain string) (*hmauth.APIKey, error) {
	if plain != "good" {
		return nil, hmauth.ErrAPIKeyNotFound
	}
	owner := fakeOwner
	return &hmauth.APIKey{ID: fakeKeyID, Name: "t", Scopes: f.scopes, CreatedBy: &owner, CreatorRole: "owner", IsActive: true}, nil
}
func (fakeKeys) RecordUsage(context.Context, uuid.UUID, string) error { return nil }

type nopAudit struct{}

func (nopAudit) Record(context.Context, AuditEntry) {}

type countLimiter struct {
	n   atomic.Int32
	max int32
}

func (l *countLimiter) Allow(uuid.UUID, int) bool { return l.n.Add(1) <= l.max }

func newTestServer(t *testing.T, scopes []hmauth.APIKeyScope, d Deps) *httptest.Server {
	t.Helper()
	d.Keys = fakeKeys{scopes}
	if d.Audit == nil {
		d.Audit = nopAudit{}
	}
	ts := httptest.NewServer(New(d, "test"))
	t.Cleanup(ts.Close)
	return ts
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func post(t *testing.T, url string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(initBody))
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// Behind Caddy/nginx on the same machine the connection is loopback but Host
// is the public name; that must work (the SDK's rebinding guard is off).
func TestPublicHostOverLoopback(t *testing.T) {
	ts := newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeReadEntities}, Deps{})
	if r := post(t, ts.URL, map[string]string{"Host": "hatamoto.dev"}); r.StatusCode != http.StatusOK {
		t.Fatalf("Host hatamoto.dev over loopback: status %d, want 200", r.StatusCode)
	}
}

func TestOriginValidation(t *testing.T) {
	ts := newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeReadEntities}, Deps{
		PublicOrigin: "https://hatamoto.dev", AllowedOrigins: []string{"https://admin.example"}})
	for origin, want := range map[string]int{
		"":                       http.StatusOK, // not a browser
		"https://hatamoto.dev":   http.StatusOK,
		"https://hatamoto.dev/":  http.StatusOK,
		"https://admin.example":  http.StatusOK,
		"https://evil.example":   http.StatusForbidden,
		"http://hatamoto.dev":    http.StatusForbidden,
		"https://hatamoto.dev.x": http.StatusForbidden,
		"null":                   http.StatusForbidden,
	} {
		hdr := map[string]string{"Host": "hatamoto.dev"}
		if origin != "" {
			hdr["Origin"] = origin
		}
		if r := post(t, ts.URL, hdr); r.StatusCode != want {
			t.Errorf("Origin %q: status %d, want %d", origin, r.StatusCode, want)
		}
	}
}

func TestOriginWithoutPublicOriginRefusesBrowsers(t *testing.T) {
	ts := newTestServer(t, nil, Deps{})
	if r := post(t, ts.URL, map[string]string{"Origin": "https://evil.example"}); r.StatusCode != http.StatusForbidden {
		t.Errorf("status %d, want 403", r.StatusCode)
	}
}

// Every HTTP request counts against the key's limit, not only tool calls.
func TestRateLimitPerRequest(t *testing.T) {
	ts := newTestServer(t, nil, Deps{Limiter: &countLimiter{max: 2}})
	var got []int
	for range 3 {
		got = append(got, post(t, ts.URL, nil).StatusCode)
	}
	if got[0] != 200 || got[1] != 200 || got[2] != http.StatusTooManyRequests {
		t.Errorf("statuses %v, want [200 200 429]", got)
	}
}

// write:entities does not grant media, menus, redirects or terms; each of
// those has its own scope. The check runs before any service is touched.
func TestSiteWriteScopesAreSeparate(t *testing.T) {
	tools := map[string]struct {
		scope hmauth.APIKeyScope
		args  map[string]any
	}{
		"media_upload":     {hmauth.ScopeWriteMedia, map[string]any{"filename": "a.png", "data_base64": "AA=="}},
		"terms_create":     {hmauth.ScopeWriteTerms, map[string]any{"kind": "tag", "name": "x"}},
		"menus_update":     {hmauth.ScopeWriteMenus, map[string]any{"id": uuid.NewString()}},
		"redirects_create": {hmauth.ScopeWriteRedirects, map[string]any{"source_path": "/a", "target_url": "/b"}},
		"redirects_delete": {hmauth.ScopeWriteRedirects, map[string]any{"id": uuid.NewString()}},
	}
	// Keys holding every other scope (including write:entities) are refused.
	others := func(skip hmauth.APIKeyScope) []hmauth.APIKeyScope {
		var s []hmauth.APIKeyScope
		for _, sc := range hmauth.ValidScopes() {
			if sc != skip && sc != hmauth.ScopeAll {
				s = append(s, sc)
			}
		}
		return s
	}
	for name, tc := range tools {
		ts := newTestServer(t, others(tc.scope), Deps{})
		c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
		sess, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
			Endpoint: ts.URL, HTTPClient: &http.Client{Transport: bearerRT{"good"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: tc.args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := ""
		if len(res.Content) > 0 {
			text, _ = res.Content[0].(*mcp.TextContent).Text, true
		}
		if !res.IsError || !strings.Contains(text, "forbidden") || !strings.Contains(text, string(tc.scope)) {
			t.Errorf("%s with every scope but %s: isError=%v %q", name, tc.scope, res.IsError, text)
		}
		sess.Close()
	}
}

type bearerRT struct{ key string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.key)
	return http.DefaultTransport.RoundTrip(r)
}

func TestDestructiveAnnotations(t *testing.T) {
	ts := newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeAll}, Deps{})
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	sess, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.URL, HTTPClient: &http.Client{Transport: bearerRT{"good"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	list, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"redirects_delete": true, "menus_update": true, "entities_update": true, "entities_unpublish": true,
		"entities_create": false, "entities_publish": false, "media_upload": false, "terms_create": false, "redirects_create": false}
	for _, tool := range list.Tools {
		if w, ok := want[tool.Name]; ok {
			got := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
			if got != w {
				t.Errorf("%s destructiveHint = %v, want %v", tool.Name, got, w)
			}
			delete(want, tool.Name)
		}
	}
	if len(want) > 0 {
		t.Errorf("tools missing: %v", want)
	}
}

func TestPublicError_PageDocErrorsKeepTheirText(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("%w: v must be 2", entity.ErrInvalidDoc),
		entity.ErrPageSectionsRemoved,
	} {
		if got := publicError(nil, "entities_update", err); got.Error() != err.Error() {
			t.Errorf("publicError(%v) = %q, want the message kept", err, got)
		}
	}
}

func TestEntitySummaryCarriesLocale(t *testing.T) {
	e := &entity.Entity{ID: uuid.New(), Name: "About", Slug: "about", Locale: "fr", Status: entity.StatusDraft}
	raw, err := json.Marshal((&Server{}).summary(context.Background(), e, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"locale":"fr"`) {
		t.Errorf("summary JSON = %s, want locale fr", raw)
	}
}

func TestPublicError_BadLocaleKeepsItsText(t *testing.T) {
	_, err := entity.NormalizeLocale("x")
	if !errors.Is(err, entity.ErrValidationFailed) {
		t.Fatalf("NormalizeLocale(x) = %v", err)
	}
	if got := publicError(nil, "entities_create", err); got.Error() != err.Error() {
		t.Errorf("publicError = %q, want %q", got, err)
	}
}

func TestPlanMenuItemsNoneAndSettings(t *testing.T) {
	got, err := planMenuItems([]menuItemIn{
		{Label: "Product", LinkType: "none"},
		{Label: "Page", LinkType: "entity"}, // no target: stored, renders as text
		{Label: "Go", LinkType: "external", URL: "/go", CSSClasses: "footer-cta  extra",
			Attrs: &menu.MenuItemAttrs{Style: "button", ShowOn: "all", Rel: []string{"nofollow"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].LinkType != "none" || got[1].EntityID != nil {
		t.Errorf("none and empty entity: %+v %+v", got[0], got[1])
	}
	if got[2].CSSClasses == nil || *got[2].CSSClasses != "footer-cta extra" || got[2].Attrs.Style != "button" || got[2].Attrs.ShowOn != "" {
		t.Errorf("settings not mapped: %+v", got[2])
	}
	// Old payloads carry none of the new keys and keep their meaning.
	old, err := planMenuItems([]menuItemIn{{Label: "A", LinkType: "custom", URL: "/a"}})
	if err != nil || old[0].CSSClasses != nil || old[0].Attrs.Style != "" || len(old[0].Attrs.Rel) != 0 {
		t.Errorf("old payload changed: %+v %v", old, err)
	}
	for name, bad := range map[string]menuItemIn{
		"external needs url": {Label: "X", LinkType: "external"},
		"bad class":          {Label: "X", LinkType: "none", CSSClasses: "1bad"},
		"bad rel":            {Label: "X", LinkType: "none", Attrs: &menu.MenuItemAttrs{Rel: []string{"evil"}}},
		"bad type":           {Label: "X", LinkType: "nope"},
	} {
		if _, err := planMenuItems([]menuItemIn{bad}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
