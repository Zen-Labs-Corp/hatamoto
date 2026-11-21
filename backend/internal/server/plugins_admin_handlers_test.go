package server

import (
	"bytes"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/testutil"
	"hatamoto.dev/sdk"
)

func TestListPlugins_ListsRedirectPaths(t *testing.T) {
	s := &Server{pluginStatusFn: func() []pluginhost.Status {
		return []pluginhost.Status{
			{Name: "paths", Loaded: true, Manifest: sdk.Manifest{Name: "paths", RedirectPaths: []string{"/go", "/go/"}}},
			{Name: "plain", Loaded: true, Manifest: sdk.Manifest{Name: "plain"}},
		}
	}}
	rec := httptest.NewRecorder()
	s.handleListPlugins(rec, httptest.NewRequest(http.MethodGet, "/plugins", nil))

	testutil.RequireEqual(t, http.StatusOK, rec.Code)
	var body struct {
		Plugins []map[string]jsontext.Value `json:"plugins"`
	}
	testutil.RequireNoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &body))
	testutil.RequireLen(t, body.Plugins, 2)
	testutil.JSONEqual(t, `["/go","/go/"]`, string(body.Plugins[0]["redirect_paths"]))
	if _, ok := body.Plugins[1]["redirect_paths"]; ok {
		t.Error("a plugin without redirect paths lists the field")
	}
}

func TestListPlugins_ListsTemplatesRefused(t *testing.T) {
	// The render plugin registry is process-wide: the name is unique to this test.
	testutil.Error(t, render.RegisterPluginSection("acme-json", "Bad", "x"))
	t.Cleanup(func() { render.RemovePlugin("acme-json") })
	s := &Server{pluginStatusFn: func() []pluginhost.Status {
		return []pluginhost.Status{
			{Name: "acme-json", Loaded: true, Manifest: sdk.Manifest{Name: "acme-json"}},
			{Name: "acme-clean", Loaded: true, Manifest: sdk.Manifest{Name: "acme-clean"}},
		}
	}}
	rec := httptest.NewRecorder()
	s.handleListPlugins(rec, httptest.NewRequest(http.MethodGet, "/plugins", nil))

	testutil.RequireEqual(t, http.StatusOK, rec.Code)
	var body struct {
		Plugins []map[string]jsontext.Value `json:"plugins"`
	}
	testutil.RequireNoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &body))
	testutil.RequireLen(t, body.Plugins, 2)
	var refused []string
	testutil.RequireNoError(t, jsonx.Unmarshal(body.Plugins[0]["templates_refused"], &refused))
	testutil.RequireLen(t, refused, 1)
	if _, ok := body.Plugins[1]["templates_refused"]; ok {
		t.Error("a plugin without refusals lists the field")
	}
}

func TestListPlugins_OmitsTemplatesPausedWhenNone(t *testing.T) {
	s := &Server{pluginStatusFn: func() []pluginhost.Status {
		return []pluginhost.Status{{Name: "acme-clean", Loaded: true, Manifest: sdk.Manifest{Name: "acme-clean"}}}
	}}
	rec := httptest.NewRecorder()
	s.handleListPlugins(rec, httptest.NewRequest(http.MethodGet, "/plugins", nil))

	testutil.RequireEqual(t, http.StatusOK, rec.Code)
	var body struct {
		Plugins []map[string]jsontext.Value `json:"plugins"`
	}
	testutil.RequireNoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &body))
	testutil.RequireLen(t, body.Plugins, 1)
	if _, ok := body.Plugins[0]["templates_paused"]; ok {
		t.Error("a plugin without paused templates lists the field")
	}
}

func TestListPlugins_ListsTemplatesPaused(t *testing.T) {
	// The render plugin registry and range breaker are process-wide: the name
	// is unique to this test and removed afterwards, which clears the breaker.
	const plugin = "acme-paused"
	testutil.RequireNoError(t, render.RegisterPluginSection(plugin, "loop", `{{define "section-loop"}}<p>{{range $i := .Content.n}}x{{end}}</p>{{end}}`))
	t.Cleanup(func() { render.RemovePlugin(plugin) })
	e, err := render.New(render.Options{})
	testutil.RequireNoError(t, err)
	for range 4 {
		v := &render.PageView{Site: render.SiteView{Name: "Test Site"}, Page: render.PageMeta{Title: "Fixture"}, BaseURL: "https://example.test", Path: "/fixture"}
		v.Sections = []render.Section{{ID: "id-loop", Type: "loop", Content: map[string]any{"n": 1000000000}}}
		testutil.RequireNoError(t, e.RenderPage(&bytes.Buffer{}, v))
	}
	s := &Server{pluginStatusFn: func() []pluginhost.Status {
		return []pluginhost.Status{{Name: plugin, Loaded: true, Manifest: sdk.Manifest{Name: plugin}}}
	}}
	rec := httptest.NewRecorder()
	s.handleListPlugins(rec, httptest.NewRequest(http.MethodGet, "/plugins", nil))

	testutil.RequireEqual(t, http.StatusOK, rec.Code)
	var body struct {
		Plugins []map[string]jsontext.Value `json:"plugins"`
	}
	testutil.RequireNoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &body))
	testutil.RequireLen(t, body.Plugins, 1)
	raw, ok := body.Plugins[0]["templates_paused"]
	if !ok {
		t.Fatal("a plugin with a paused template omits templates_paused")
	}
	var paused []pausedTemplateView
	testutil.RequireNoError(t, jsonx.Unmarshal(raw, &paused))
	testutil.RequireLen(t, paused, 1)
	testutil.RequireEqual(t, "section", paused[0].Kind)
	testutil.RequireEqual(t, "loop", paused[0].Name)
}

func TestPausedViewsMapsKindNameUntilUTC(t *testing.T) {
	if got := pausedViews(nil); got != nil {
		t.Errorf("pausedViews(nil) = %v, want nil", got)
	}
	zone := time.FixedZone("plus2", 2*60*60)
	until := time.Date(2026, 11, 21, 16, 32, 0, 0, zone)
	got := pausedViews([]render.PausedTemplate{{Kind: "section", Name: "gallery_grid", Until: until}})
	raw, err := jsonx.Marshal(got)
	testutil.RequireNoError(t, err)
	testutil.JSONEqual(t, `[{"kind":"section","name":"gallery_grid","until":"2026-11-21T14:32:00Z"}]`, string(raw))
}

func TestListPlugins_InvalidSettingsSchemaDoesNotBreakList(t *testing.T) {
	s := &Server{pluginStatusFn: func() []pluginhost.Status {
		return []pluginhost.Status{
			{Name: "broken", Manifest: sdk.Manifest{Name: "broken", SettingsSchema: []byte(`{nope`)}},
			{Name: "fine", Loaded: true, Manifest: sdk.Manifest{Name: "fine", SettingsSchema: []byte(`{"type":"object"}`)}},
		}
	}}
	rec := httptest.NewRecorder()
	s.handleListPlugins(rec, httptest.NewRequest(http.MethodGet, "/plugins", nil))

	testutil.RequireEqual(t, http.StatusOK, rec.Code)
	var body struct {
		Plugins []struct {
			Name           string         `json:"name"`
			SettingsSchema jsontext.Value `json:"settings_schema"`
		} `json:"plugins"`
	}
	testutil.RequireNoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &body))
	testutil.RequireLen(t, body.Plugins, 2)
	testutil.Empty(t, body.Plugins[0].SettingsSchema)
	testutil.JSONEqual(t, `{"type":"object"}`, string(body.Plugins[1].SettingsSchema))
}
