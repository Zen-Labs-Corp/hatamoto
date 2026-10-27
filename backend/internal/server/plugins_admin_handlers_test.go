package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/testutil"
	"hatamoto.dev/sdk"
)

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
			Name           string          `json:"name"`
			SettingsSchema json.RawMessage `json:"settings_schema"`
		} `json:"plugins"`
	}
	testutil.RequireNoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	testutil.RequireLen(t, body.Plugins, 2)
	testutil.Empty(t, body.Plugins[0].SettingsSchema)
	testutil.JSONEqual(t, `{"type":"object"}`, string(body.Plugins[1].SettingsSchema))
}
