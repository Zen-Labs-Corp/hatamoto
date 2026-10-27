package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/pluginhost"

	"hatamoto.dev/sdk"
)

type memSettings struct{ data map[string]json.RawMessage }

func (m *memSettings) Get(_ context.Context, p string) (json.RawMessage, error) {
	if v, ok := m.data[p]; ok {
		return v, nil
	}
	return json.RawMessage(`{}`), nil
}
func (m *memSettings) Put(_ context.Context, p string, v json.RawMessage) error {
	m.data[p] = v
	return nil
}

const testPluginSchema = `{"type":"object","required":["greeting"],"additionalProperties":false,
"properties":{"greeting":{"type":"string","minLength":2,"default":"hi"},
"api_key":{"type":"string","writeOnly":true}}}`

func pluginTestServer() (*Server, *memSettings, http.Handler) {
	store := &memSettings{data: map[string]json.RawMessage{}}
	s := &Server{
		logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		pluginSettingsStore: store,
		pluginStatusFn: func() []pluginhost.Status {
			return []pluginhost.Status{
				{Name: "hello", Loaded: true, Hooks: []string{"jsonld.enrich"},
					Manifest: sdk.Manifest{Name: "hello", Version: "0.1.0", SDK: "^0.5", Description: "d", SettingsSchema: []byte(testPluginSchema)}},
				{Name: "noschema", Error: "boom", Manifest: sdk.Manifest{Name: "noschema"}},
			}
		},
	}
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	r.HandleFunc("GET /plugins", s.handleListPlugins)
	r.HandleFunc("GET /plugins/{name}/settings", s.handleGetPluginSettings)
	r.HandleFunc("PUT /plugins/{name}/settings", s.handlePutPluginSettings)
	return s, store, mux
}

func doPlugin(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPluginList(t *testing.T) {
	_, _, h := pluginTestServer()
	rec := doPlugin(h, "GET", "/plugins", "")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var out struct {
		Plugins []map[string]any `json:"plugins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Plugins) != 2 {
		t.Fatalf("bad body %s (%v)", rec.Body, err)
	}
	p := out.Plugins[0]
	if p["name"] != "hello" || p["version"] != "0.1.0" || p["sdk"] != "^0.5" || p["settings_schema"] == nil || p["loaded"] != true {
		t.Fatalf("unexpected view %v", p)
	}
	if out.Plugins[1]["error"] != "boom" {
		t.Fatalf("error missing: %v", out.Plugins[1])
	}
}

func TestPluginSettingsGetMergesDefaults(t *testing.T) {
	_, _, h := pluginTestServer()
	rec := doPlugin(h, "GET", "/plugins/hello/settings", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"greeting":"hi"}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPluginSettingsNotFound(t *testing.T) {
	_, _, h := pluginTestServer()
	for _, name := range []string{"missing", "noschema"} {
		if rec := doPlugin(h, "GET", "/plugins/"+name+"/settings", ""); rec.Code != 404 {
			t.Errorf("%s: GET status %d", name, rec.Code)
		}
		if rec := doPlugin(h, "PUT", "/plugins/"+name+"/settings", `{}`); rec.Code != 404 {
			t.Errorf("%s: PUT status %d", name, rec.Code)
		}
	}
}

func TestPluginSettingsPutValidation(t *testing.T) {
	_, store, h := pluginTestServer()
	rec := doPlugin(h, "PUT", "/plugins/hello/settings", `{"greeting":"x"}`)
	if rec.Code != 400 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Error  string `json:"error"`
		Fields []struct{ Path, Message string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error != "validation failed" || len(body.Fields) == 0 || body.Fields[0].Path != "/greeting" {
		t.Fatalf("bad error shape: %s", rec.Body)
	}
	if len(store.data) != 0 {
		t.Fatal("invalid settings were persisted")
	}
	if rec := doPlugin(h, "PUT", "/plugins/hello/settings", `[1]`); rec.Code != 400 {
		t.Fatalf("non-object status %d", rec.Code)
	}
}

func TestPluginSettingsPutMasksAndPreservesSecret(t *testing.T) {
	_, store, h := pluginTestServer()
	rec := doPlugin(h, "PUT", "/plugins/hello/settings", `{"greeting":"hey","api_key":"s3cret"}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(string(store.data["hello"]), "s3cret") {
		t.Fatal("secret not persisted")
	}
	// Re-saving with the masked/absent secret keeps the stored one.
	if rec := doPlugin(h, "PUT", "/plugins/hello/settings", `{"greeting":"yo","api_key":""}`); rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(string(store.data["hello"]), "s3cret") || !strings.Contains(string(store.data["hello"]), "yo") {
		t.Fatalf("stored %s", store.data["hello"])
	}
	get := doPlugin(h, "GET", "/plugins/hello/settings", "")
	if strings.Contains(get.Body.String(), "s3cret") || !strings.Contains(get.Body.String(), `"api_key":""`) {
		t.Fatalf("GET leaked or lacks mask: %s", get.Body)
	}
}
