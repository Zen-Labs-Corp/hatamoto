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
	// runInputs has two forms: an object fills the Runs tab form (the op above, unchanged),
	// a run name starts that run through core's confirm (run.start).
	for _, want := range []string{
		`send({ op: "inputs", values: nameOrValues })`,
		`send({ op: "run.start", run: nameOrValues, inputs })`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginsdk.js lost %q", want)
		}
	}
}

// run.start is core's own op: the owner's page, a recent click and core's confirm (outside
// the frame) come before the one POST to the owner-only start route, never the bridge.
func TestPluginFrameStartsRunsThroughCoreConfirm(t *testing.T) {
	t.Parallel()

	js := string(readFileT(t, "../../web/admin/js/pluginframe.js"))
	for _, want := range []string{
		`m.op === "run.start"`,
		`holder.dataset.runs`,
		`navigator.userActivation?.isActive`,
		`askConfirm`,
		`"X-CSRF-Token": csrf()`,
		`Accept: "application/json"`,
		`error: "cancelled"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginframe.js lost %q", want)
		}
	}
	if i, j := strings.Index(js, `return startRun(`), strings.Index(js, `await fetch(bridge`); i < 0 || j < 0 || i > j {
		t.Error("run.start must return before the bridge request")
	}
	// Hardening of the confirm: click-through guard, cancel cooldown, quoted and cleaned text, named host.
	for _, want := range []string{`CLICK_GUARD_MS`, `Date.now() - openedAt < CLICK_GUARD_MS`, `cancels >= MAX_CANCELS`, `Date.now() < coolUntil`, `cancels++`,
		`JSON.stringify(t.length > 80`, `characters)`, `\u202e`, `\u2066`, `run.host`, `It may reach`} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginframe.js lost %q", want)
		}
	}
	// The one-shot listeners live here, not in components.js, which the builder's JS budget counts.
	for _, want := range []string{`function askConfirm(`, `e.preventDefault()`, `removeEventListener("submit"`, `removeEventListener("close"`} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginframe.js lost %q", want)
		}
	}
}

// camera.capture is core's own op: it needs the page's camera grant (the
// data-camera flag), opens core's dialog and never reaches the bridge request.
func TestPluginFrameGatesCameraOnTheGrant(t *testing.T) {
	t.Parallel()

	js := string(readFileT(t, "../../web/admin/js/pluginframe.js"))
	for _, want := range []string{
		`holder.dataset.camera === "1"`,
		`m.op === "camera.capture"`,
		`if (!camera) throw new Error(`,
		`navigator.userActivation?.isActive`,
		`await import("@hm/plugincamera")`,
		`camera, nonce })`,
		`capture(document.getElementById("plugin-camera"))`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginframe.js lost %q", want)
		}
	}
	gate, fetch := strings.Index(js, `if (!camera) throw`), strings.Index(js, `await fetch(bridge`)
	if gate < 0 || fetch < 0 || gate > fetch {
		t.Error("the camera gate must come before the bridge request")
	}
	if strings.Contains(js, `import { capture }`) {
		t.Error("plugincamera.js must load on demand, not statically: ungranted pages stay as they were")
	}
	sdk := string(readFileT(t, "../../web/admin/js/pluginsdk.js"))
	for _, want := range []string{`capture: () => send({ op: "camera.capture" })`, `available: false`, `m.camera === true`} {
		if !strings.Contains(sdk, want) {
			t.Errorf("pluginsdk.js lost %q", want)
		}
	}
}
