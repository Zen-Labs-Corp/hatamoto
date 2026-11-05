//go:build wasip1

// Command legacy is a frozen guest: legacy.wasm was built from this source
// against the SDK of commit dec7b490 (encoding/json v1) with the Go 1.26.6
// toolchain, whose encoding/json is the real v1 encoder (see README.md), and
// is never rebuilt: it stands for a plugin compiled against an older SDK, so
// the host's current decoder is tested against bytes the old guest wrote.
// scripts/check-jsonv1.sh allow-lists this file by path: it must keep v1.
package main

import (
	"encoding/json"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func main() {}

func init() {
	guest.OnHTTP(func(req sdk.HTTPRequest) (sdk.HTTPResponse, error) {
		b, _ := json.Marshal(req)
		return sdk.HTTPResponse{Status: 201, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
	})
	guest.OnCron("tick", func(ev sdk.CronEvent) error {
		return guest.Call(sdk.FnKVSet, map[string]any{"key": "cron:" + ev.Name, "value": ev.ScheduledAt}, nil)
	})
	guest.OnFunc("shout", func(args []json.RawMessage) (string, error) {
		var s string
		if len(args) > 0 {
			_ = json.Unmarshal(args[0], &s)
		}
		return strings.ToUpper(s) + "<b>", nil
	})
	guest.OnAction(sdk.FormSubmitted, func(ev sdk.FormSubmittedEvent) error {
		return guest.Call(sdk.FnKVSet, map[string]any{"key": "form:" + ev.FormSlug, "value": ev.Fields}, nil)
	})
	guest.OnFilter(sdk.RenderPage, func(p sdk.PageContext) (sdk.PageContext, error) {
		if p.Cookies["unlock"] != "1" {
			p.Hide = append(p.Hide, "secret")
		}
		p.SetCookies = append(p.SetCookies, sdk.SetCookie{Name: "visited", Value: "1", MaxAge: 3600})
		return p, nil
	})
}

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

//go:wasmexport on_cron_tick
func onCronTick() int32 { return guest.Run(guest.CronExport("tick")) }

//go:wasmexport func_shout
func funcShout() int32 { return guest.Run(guest.FuncExport("shout")) }

//go:wasmexport on_form_submitted
func onFormSubmitted() int32 { return guest.Run(sdk.FormSubmitted.Export()) }

//go:wasmexport filter_render_page
func filterRenderPage() int32 { return guest.Run(sdk.RenderPage.Export()) }

// call_host: input is a CallRequest; output {"result":...} or the typed error.
//
//go:wasmexport call_host
func callHostExport() int32 {
	var req sdk.CallRequest
	if err := json.Unmarshal(guest.Input(), &req); err != nil {
		return 1
	}
	var args any
	if len(req.Args) > 0 {
		_ = json.Unmarshal(req.Args, &args)
	}
	var res json.RawMessage
	out := map[string]any{}
	if err := guest.Call(req.Fn, args, &res); err != nil {
		if he, ok := err.(*sdk.HostError); ok {
			out["code"], out["capability"], out["message"] = he.Code, he.Capability, he.Message
		} else {
			out["code"], out["message"] = "guest", err.Error()
		}
	} else {
		out["result"] = res
	}
	guest.OutputJSON(out)
	return 0
}
