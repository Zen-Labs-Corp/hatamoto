package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/seosvc"
	"hatamoto.dev/core/internal/testutil"
)

// fakeSEOService records what the handler passes and refuses a patch holding
// the key "bad", leaving the document untouched like the real service.
type fakeSEOService struct {
	doc     seosvc.Doc
	updates int
	actor   seosvc.Actor
	err     error
}

func (f *fakeSEOService) Get(context.Context) (seosvc.Doc, error) { return f.doc, nil }

func (f *fakeSEOService) Update(_ context.Context, a seosvc.Actor, p map[string]json.RawMessage) (seosvc.Doc, error) {
	if _, bad := p["bad"]; bad {
		return nil, &seosvc.ValidationError{Fields: map[string]string{"type[doc][title]": "too long"}}
	}
	if f.err != nil {
		return nil, f.err
	}
	f.updates++
	f.actor = a
	f.doc = seosvc.Doc{"separator": "|"}
	return f.doc, nil
}

func (f *fakeSEOService) Preview(context.Context, uuid.UUID) (seosvc.Preview, error) {
	return seosvc.Preview{}, nil
}

func (f *fakeSEOService) Analyze(context.Context, uuid.UUID) (seosvc.Panel, error) {
	return seosvc.Panel{}, nil
}

type seoKeyValidator struct{ keys map[string]*auth.APIKey }

func (v seoKeyValidator) ValidateKey(_ context.Context, raw string) (*auth.APIKey, error) {
	if k, ok := v.keys[raw]; ok {
		return k, nil
	}
	return nil, auth.ErrAPIKeyNotFound
}

func (seoKeyValidator) RecordUsage(context.Context, uuid.UUID, string) error { return nil }

func seoKey(name string, scopes ...auth.APIKeyScope) *auth.APIKey {
	creator := uuid.New()
	return &auth.APIKey{ID: uuid.New(), Name: name, Scopes: scopes, CreatedBy: &creator, CreatorRole: "editor", IsActive: true}
}

// seoRouter mounts the SEO routes under /api/v1 behind the same key-or-session
// auth the server uses; the session fallback injects role (none when "").
func seoRouter(role string, svc seosvc.Service, keys map[string]*auth.APIKey) http.Handler {
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "/api/v1", middleware.AuthOrAPIKey(seoKeyValidator{keys}, nil, injectRole(role)))
	mountSEOSettings(r, &seoSettingsHandlers{svc: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return mux
}

func doSEO(h http.Handler, method, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/settings/seo/", strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSEOSettings_AccessMatrix(t *testing.T) {
	keys := map[string]*auth.APIKey{
		"read":  seoKey("reader", auth.ScopeReadSEO),
		"write": seoKey("writer", auth.ScopeWriteSEO),
		"none":  seoKey("other", auth.ScopeReadEntities),
	}
	cases := []struct {
		name, role, key, method string
		want                    int
	}{
		{"owner get", "owner", "", http.MethodGet, 200},
		{"owner patch", "owner", "", http.MethodPatch, 200},
		{"editor get", "editor", "", http.MethodGet, 403},
		{"editor patch", "editor", "", http.MethodPatch, 403},
		{"anonymous", "", "", http.MethodGet, 401},
		{"read key get", "", "read", http.MethodGet, 200},
		{"read key patch", "", "read", http.MethodPatch, 403},
		{"write key patch", "", "write", http.MethodPatch, 200},
		{"write key get (write implies read)", "", "write", http.MethodGet, 200},
		{"no scope get", "", "none", http.MethodGet, 403},
		{"no scope patch", "", "none", http.MethodPatch, 403},
		{"unknown key", "owner", "nope", http.MethodGet, 401},
	}
	for _, c := range cases {
		svc := &fakeSEOService{doc: seosvc.Doc{"separator": "-"}}
		rec := doSEO(seoRouter(c.role, svc, keys), c.method, c.key, `{"separator":"|"}`)
		testutil.Equal(t, c.want, rec.Code, c.name)
		if c.want != 200 {
			testutil.Equal(t, 0, svc.updates, c.name+": nothing written")
		}
	}
}

func TestSEOSettings_WriteKeyReadsToo(t *testing.T) {
	keys := map[string]*auth.APIKey{"rw": seoKey("rw", auth.ScopeReadSEO, auth.ScopeWriteSEO)}
	svc := &fakeSEOService{doc: seosvc.Doc{"separator": "-"}}
	h := seoRouter("", svc, keys)
	testutil.Equal(t, 200, doSEO(h, http.MethodPatch, "rw", `{"separator":"|"}`).Code)
	rec := doSEO(h, http.MethodGet, "rw", "")
	testutil.Equal(t, 200, rec.Code)
	if !strings.Contains(rec.Body.String(), `"separator":"|"`) {
		t.Fatalf("GET body: %s", rec.Body)
	}
}

func TestSEOSettings_Validation422(t *testing.T) {
	svc := &fakeSEOService{doc: seosvc.Doc{"separator": "-"}}
	rec := doSEO(seoRouter("owner", svc, nil), http.MethodPatch, "", `{"bad":1}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var got struct {
		Error  string            `json:"error"`
		Fields map[string]string `json:"fields"`
	}
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	testutil.Equal(t, "validation failed", got.Error)
	testutil.Equal(t, "too long", got.Fields["type[doc][title]"])
	testutil.Equal(t, 0, svc.updates, "nothing changed after 422")
	testutil.Equal(t, "-", svc.doc["separator"].(string), "document unchanged")
}

func TestSEOSettings_BadBodyAndServiceError(t *testing.T) {
	svc := &fakeSEOService{}
	h := seoRouter("owner", svc, nil)
	for _, body := range []string{`{`, `null`, `[]`} {
		testutil.Equal(t, http.StatusBadRequest, doSEO(h, http.MethodPatch, "", body).Code, body)
	}
	svc.err = errors.New("db down")
	rec := doSEO(h, http.MethodPatch, "", `{}`)
	testutil.Equal(t, http.StatusInternalServerError, rec.Code)
	if strings.Contains(rec.Body.String(), "db down") {
		t.Fatalf("internal error leaked: %s", rec.Body)
	}
}

func TestSEOSettings_ActorPassedToService(t *testing.T) {
	k := seoKey("deploy-bot", auth.ScopeWriteSEO)
	svc := &fakeSEOService{}
	testutil.Equal(t, 200, doSEO(seoRouter("", svc, map[string]*auth.APIKey{"k": k}), http.MethodPatch, "k", `{}`).Code)
	testutil.Equal(t, "api", svc.actor.Via)
	testutil.Equal(t, "deploy-bot", svc.actor.KeyName)
	testutil.Equal(t, k.ID.String(), svc.actor.KeyID)

	svc = &fakeSEOService{}
	testutil.Equal(t, 200, doSEO(seoRouter("owner", svc, nil), http.MethodPatch, "", `{}`).Code)
	testutil.Equal(t, "", svc.actor.KeyName)
	if svc.actor.UserID == "" {
		t.Fatal("owner actor lacks user id")
	}
}

func TestSEOSettings_NoServiceIs503(t *testing.T) {
	h := seoRouter("owner", nil, nil)
	testutil.Equal(t, http.StatusServiceUnavailable, doSEO(h, http.MethodGet, "", "").Code)
	testutil.Equal(t, http.StatusServiceUnavailable, doSEO(h, http.MethodPatch, "", `{}`).Code)
}
