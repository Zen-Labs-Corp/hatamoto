package server

import (
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"
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
