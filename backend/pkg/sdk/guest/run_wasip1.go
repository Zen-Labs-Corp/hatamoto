//go:build wasip1

package guest

import (
	"encoding/json"

	"github.com/extism/go-pdk"
)

// Run executes the handler registered for export: it reads the call input,
// writes the result and returns the export's status code (0 = ok, 1 = error,
// with the message set for the host).
func Run(export string) int32 {
	out, err := Handle(export, pdk.Input())
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	if out != nil {
		pdk.Output(out)
	}
	return 0
}

// Log levels for Log.
const (
	LogDebug = "debug"
	LogInfo  = "info"
	LogWarn  = "warn"
	LogError = "error"
)

// Log writes a line to the host's log, tagged with the plugin name. Unknown
// levels log as info.
func Log(level, msg string) {
	l := pdk.LogInfo
	switch level {
	case LogDebug:
		l = pdk.LogDebug
	case LogWarn:
		l = pdk.LogWarn
	case LogError:
		l = pdk.LogError
	}
	pdk.Log(l, msg)
}

// Config returns the manifest config value for key.
func Config(key string) (string, bool) { return pdk.GetConfig(key) }

//go:wasmimport extism:host/user hm_call
func hmCall(offset uint64) uint64

func callHost(req []byte) ([]byte, error) {
	in := pdk.AllocateBytes(req)
	defer in.Free()
	out := pdk.FindMemory(hmCall(in.Offset()))
	defer out.Free()
	return out.ReadBytes(), nil
}

// Call runs the host function fn (see docs/PLUGIN_API.md) with args and
// decodes its result into out; nil ignores the result. A failed call returns
// *sdk.HostError (code capability_denied until the owner granted the
// capability, and so on).
func Call(fn string, args, out any) error { return Invoke(callHost, fn, args, out) }

// Input returns the call's raw input, for exports that are not hooks (see
// docs/PLUGIN_API.md: a plugin may export extra functions for its own tests).
func Input() []byte { return pdk.Input() }

// OutputJSON writes v as the call's output.
func OutputJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		pdk.SetError(err)
		return
	}
	pdk.Output(b)
}
