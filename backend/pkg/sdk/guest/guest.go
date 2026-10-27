package guest

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"hatamoto.dev/sdk"
)

// handler maps the raw JSON input of an export to its raw JSON output. An
// action's output is nil.
type handler func(in []byte) ([]byte, error)

var (
	mu       sync.Mutex
	handlers = map[string]handler{}
)

func register(export string, h handler) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := handlers[export]; dup {
		panic("guest: duplicate handler for " + export)
	}
	handlers[export] = h
}

// OnAction registers fn for action a. The host fires actions asynchronously
// and ignores the result; a returned error is logged by the host.
func OnAction[T any](a sdk.Action[T], fn func(T) error) {
	register(a.Export(), func(in []byte) ([]byte, error) {
		var v T
		if err := json.Unmarshal(in, &v); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", a.Name, err)
		}
		return nil, fn(v)
	})
}

// OnFilter registers fn for filter f. fn returns the (possibly modified)
// value; a returned error makes the host keep the value it had before this
// plugin ran. For entity.before_save, return Reject to refuse the save.
func OnFilter[T any](f sdk.Filter[T], fn func(T) (T, error)) {
	register(f.Export(), func(in []byte) ([]byte, error) {
		var v T
		if err := json.Unmarshal(in, &v); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", f.Name, err)
		}
		out, err := fn(v)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	})
}

// Reject is the error an entity.before_save filter returns to refuse the
// save; reason is shown to the API client.
func Reject(reason string) error { return errors.New(sdk.RejectPrefix + reason) }

// Handle runs the handler registered for export on in. It is what Run does
// around the PDK input and output, exposed so handlers can be unit tested on
// any platform.
func Handle(export string, in []byte) ([]byte, error) {
	mu.Lock()
	h := handlers[export]
	mu.Unlock()
	if h == nil {
		return nil, fmt.Errorf("guest: no handler registered for %s", export)
	}
	return h(in)
}

// HTTPExport is the export that serves a plugin's manifest routes.
const HTTPExport = "http_handle"

// OnHTTP registers the handler of the plugin's routes (export http_handle).
// A returned error becomes a 500 for the client.
func OnHTTP(fn func(sdk.HTTPRequest) (sdk.HTTPResponse, error)) {
	register(HTTPExport, func(in []byte) ([]byte, error) {
		var req sdk.HTTPRequest
		if err := json.Unmarshal(in, &req); err != nil {
			return nil, fmt.Errorf("decode http request: %w", err)
		}
		res, err := fn(req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	})
}

// CronExport is the export that runs the manifest cron job called name.
func CronExport(name string) string { return "on_cron_" + name }

// OnCron registers fn for the manifest cron job called name.
func OnCron(name string, fn func(sdk.CronEvent) error) {
	register(CronExport(name), func(in []byte) ([]byte, error) {
		var ev sdk.CronEvent
		if err := json.Unmarshal(in, &ev); err != nil {
			return nil, fmt.Errorf("decode cron event: %w", err)
		}
		return nil, fn(ev)
	})
}

// FuncExport is the export behind the manifest template func called name.
func FuncExport(name string) string { return "func_" + name }

// OnFunc registers the template func called name. It receives the template's
// arguments as JSON values and returns plain text (the theme escapes it).
func OnFunc(name string, fn func(args []json.RawMessage) (string, error)) {
	register(FuncExport(name), func(in []byte) ([]byte, error) {
		var args []json.RawMessage
		if err := json.Unmarshal(in, &args); err != nil {
			return nil, fmt.Errorf("decode func args: %w", err)
		}
		text, err := fn(args)
		if err != nil {
			return nil, err
		}
		return []byte(text), nil
	})
}

// CapabilityDenied reports whether err is a host call refused because the
// owner has not granted a capability, and which one:
//
//	if capability, denied := guest.CapabilityDenied(err); denied { ... }
func CapabilityDenied(err error) (capability string, denied bool) {
	var he *sdk.HostError
	if errors.As(err, &he) && he.Code == sdk.ErrCapabilityDenied {
		return he.Capability, true
	}
	return "", false
}

// Invoke runs the host function fn with args and decodes its result into out
// (nil to ignore it), through call: the host's hm_call. A failed call returns
// *sdk.HostError. Call is the wasm implementation of call; tests pass a fake.
func Invoke(call func([]byte) ([]byte, error), fn string, args, out any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	req, err := json.Marshal(sdk.CallRequest{Fn: fn, Args: raw})
	if err != nil {
		return err
	}
	resp, err := call(req)
	if err != nil {
		return err
	}
	var r sdk.CallResponse
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("decode host response: %w", err)
	}
	if !r.OK {
		if r.Error == nil {
			return &sdk.HostError{Code: sdk.ErrInternal, Message: "host call failed"}
		}
		return r.Error
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}
