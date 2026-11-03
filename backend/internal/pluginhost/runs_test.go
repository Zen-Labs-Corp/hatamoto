package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

// A manifest that declares runs must ship the on_run_step export: a plugin
// that cannot be driven is refused at load, and nothing of it stays loaded.
func TestLoadRefusesRunsWithoutExport(t *testing.T) {
	r := newCapaRig(t)
	st := r.rt.Load(context.Background(), capaManifest(func(m *sdk.Manifest) {
		m.SDK = "^0.17"
		m.Runs = []sdk.Run{{Name: "import", Title: "Import", Stages: []string{sdk.RunPhaseFetch, sdk.RunPhaseTransform}}}
	}), plugintest.Build(t, "capa"))
	if st.Loaded || !strings.Contains(st.Error, sdk.RunStepExport) {
		t.Fatalf("loaded=%v error=%q", st.Loaded, st.Error)
	}
	if r.rt.Host().HasExport("capa", "http_handle") {
		t.Fatal("the refused module is still loaded")
	}
	// The same plugin without runs loads.
	if st := r.rt.Load(context.Background(), capaManifest(nil), plugintest.Build(t, "capa")); !st.Loaded {
		t.Fatalf("plain capa not loaded: %s", st.Error)
	}
}

func TestRunInputsValidation(t *testing.T) {
	d := sdk.Run{Name: "import", Inputs: []sdk.RunInput{
		{Name: "site", Label: "Site", Type: "url", Required: true},
		{Name: "pages", Label: "Pages", Type: "number"},
		{Name: "mode", Label: "Mode", Type: "select", Options: []string{"fast", "full"}},
	}}
	if raw, err := checkRunInputs(d, map[string]string{"site": "https://example.com", "pages": "3", "mode": "full"}); err != nil ||
		string(raw) != `{"mode":"full","pages":3,"site":"https://example.com"}` {
		t.Fatalf("valid inputs: %s %v", raw, err)
	}
	for name, in := range map[string]map[string]string{
		"missing required": {},
		"bad url":          {"site": "ftp://x"},
		"bad number":       {"site": "https://x.test", "pages": "many"},
		"bad option":       {"site": "https://x.test", "mode": "slow"},
		"unknown input":    {"site": "https://x.test", "extra": "1"},
	} {
		var bad *InvalidRunInputError
		if _, err := checkRunInputs(d, in); !errors.As(err, &bad) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := checkRunHosts([]string{"https://example.com/x"}); err == nil {
		t.Error("a URL was accepted as a host")
	}
	if h, err := checkRunHosts([]string{" Example.COM ", "", "*.cdn.test:8443"}); err != nil || len(h) != 2 || h[0] != "example.com" {
		t.Errorf("hosts %v %v", h, err)
	}
}
