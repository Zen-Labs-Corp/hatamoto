package admin

import (
	"strings"
	"testing"
)

// The run-inputs op is handled by the host script alone: it fills the hidden
// fields of core's start form and never reaches the bridge endpoint, which
// answers 400 to an op it does not know.
func TestPluginFrameHandlesRunInputsLocally(t *testing.T) {
	t.Parallel()

	js := string(readFileT(t, "../../web/admin/js/pluginframe.js"))
	for _, want := range []string{
		`m.op === "inputs"`,
		`[data-hm-run-form]`,
		`form.dataset.hmRunInputNames`,
		`input_${CSS.escape(name)}`,
		`MAX_INPUT = 4096`,
		`Array.isArray(values)`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginframe.js lost %q", want)
		}
	}
	// The inputs branch returns before the fetch to the bridge.
	if i, j := strings.Index(js, `return setRunInputs(m.values)`), strings.Index(js, `await fetch(bridge`); i < 0 || j < 0 || i > j {
		t.Error("the inputs op must return before the bridge request")
	}
}

func TestPluginSDKSendsRunInputs(t *testing.T) {
	t.Parallel()

	js := string(readFileT(t, "../../web/admin/js/pluginsdk.js"))
	if !strings.Contains(js, `runInputs: (values) => send({ op: "inputs", values })`) {
		t.Error("pluginsdk.js lost hatamoto.runInputs")
	}
}
