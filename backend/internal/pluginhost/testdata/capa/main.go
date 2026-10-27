//go:build wasip1

// Command capa is the test plugin of the capability layer: its exports call
// whatever host function the test asks for and report the typed result.
package main

import (
	"encoding/json"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func main() {}

// outcome is what call_host returns: the host's result or its typed error.
type outcome struct {
	Result json.RawMessage `json:"result,omitempty"`
	Code   string          `json:"code,omitempty"`
	Cap    string          `json:"capability,omitempty"`
	Msg    string          `json:"message,omitempty"`
}

func callHost(fn string, args any) outcome {
	var res json.RawMessage
	if err := guest.Call(fn, args, &res); err != nil {
		if he, ok := err.(*sdk.HostError); ok {
			return outcome{Code: he.Code, Cap: he.Capability, Msg: he.Message}
		}
		return outcome{Code: "guest", Msg: err.Error()}
	}
	return outcome{Result: res}
}

func init() {
	// call_host: input {"fn": ..., "args": ...}; output an outcome.
	guest.OnHTTP(func(req sdk.HTTPRequest) (sdk.HTTPResponse, error) {
		switch {
		case req.Path == "/echo":
			b, _ := json.Marshal(req)
			return sdk.HTTPResponse{Status: 201, Headers: map[string]string{"Content-Type": "application/json", "Set-Cookie": "x=1", "X-Evil": "1"}, Body: string(b)}, nil
		case req.Path == "/redirect":
			return sdk.HTTPResponse{Status: 302, Headers: map[string]string{"Location": req.Query["to"][0]}}, nil
		case req.Path == "/cache":
			return sdk.HTTPResponse{Headers: map[string]string{"Cache-Control": "public, s-maxage=600", "Content-Type": "text/html"}, Body: "<b>x</b>"}, nil
		case req.Path == "/open":
			return sdk.HTTPResponse{Body: "open"}, nil
		case strings.HasPrefix(req.Path, "/admin/"): // a plugin that routes by hand on the path
			return sdk.HTTPResponse{Body: "ADMIN-ONLY"}, nil
		case strings.HasPrefix(req.Path, "/items/"):
			return sdk.HTTPResponse{Body: "items:" + req.Path}, nil
		case strings.HasPrefix(req.Path, "/who/"):
			return sdk.HTTPResponse{Body: strings.TrimPrefix(req.Path, "/who/")}, nil
		}
		return sdk.HTTPResponse{Status: 404, Body: "nope"}, nil
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

	// render.page: hide the block "secret" unless the cookie "unlock" is "1";
	// always ask for the cookie "visited". Config "evil" sets an undeclared cookie.
	guest.OnFilter(sdk.RenderPage, func(p sdk.PageContext) (sdk.PageContext, error) {
		if p.Path == "/echo-cookies" { // reports every cookie the host passed in
			p.Blocks = nil
			for name, v := range p.Cookies {
				p.Blocks = append(p.Blocks, sdk.Block{ID: "ck:" + name, Type: "content", Title: v, Content: map[string]any{"headline": "ck:" + name + "=" + v, "body": "<p>" + v + "</p>"}})
			}
			return p, nil
		}
		if p.Cookies["unlock"] != "1" {
			p.Hide = append(p.Hide, "secret")
		}
		name := "visited"
		if v, _ := guest.Config("evil"); v == "1" {
			name = "evil"
		}
		p.SetCookies = append(p.SetCookies, sdk.SetCookie{Name: name, Value: "1", MaxAge: 3600})
		return p, nil
	})

	// entity.before_save: create a child entity from inside the hook (the
	// re-entrancy guard allows one level) and tag the draft with the outcome.
	guest.OnFilter(sdk.EntityBeforeSave, func(d sdk.EntityDraft) (sdk.EntityDraft, error) {
		o := callHost(sdk.FnEntitiesCreate, map[string]any{"schema_slug": d.SchemaSlug, "name": "child-of-" + d.Name})
		switch o.Code {
		case "":
			d.Name += "[child-ok]"
		default:
			d.Name += "[" + o.Code + "]"
		}
		return d, nil
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

//go:wasmexport filter_entity_before_save
func filterEntityBeforeSave() int32 { return guest.Run(sdk.EntityBeforeSave.Export()) }

// call_host: input is a CallRequest; output an outcome.
//
//go:wasmexport call_host
func callHostExport() int32 {
	var req sdk.CallRequest
	in := guest.Input()
	if err := json.Unmarshal(in, &req); err != nil {
		return 1
	}
	var args any
	if len(req.Args) > 0 {
		_ = json.Unmarshal(req.Args, &args)
	}
	guest.OutputJSON(callHost(req.Fn, args))
	return 0
}
