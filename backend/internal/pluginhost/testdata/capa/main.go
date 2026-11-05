//go:build wasip1

// Command capa is the test plugin of the capability layer: its exports call
// whatever host function the test asks for and report the typed result.
package main

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func main() {}

// The options the plugin wire is written and read with; this plugin only
// needs the part that changes bytes here.
var (
	marshalOpts = jsonv2.JoinOptions(
		jsonv2.Deterministic(true),
		jsontext.EscapeForHTML(true),
		jsontext.EscapeForJS(true),
		jsonv2.FormatNilSliceAsNull(true),
		jsonv2.FormatNilMapAsNull(true),
	)
	unmarshalOpts = jsonv2.JoinOptions(
		jsonv2.MatchCaseInsensitiveNames(true),
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
	)
)

func marshal(v any) ([]byte, error)    { return jsonv2.Marshal(v, marshalOpts) }
func unmarshal(in []byte, v any) error { return jsonv2.Unmarshal(in, v, unmarshalOpts) }

// outcome is what call_host returns: the host's result or its typed error.
type outcome struct {
	Result jsontext.Value `json:"result,omitzero"`
	Code   string         `json:"code,omitempty"`
	Cap    string         `json:"capability,omitempty"`
	Msg    string         `json:"message,omitempty"`
}

func callHost(fn string, args any) outcome {
	var res jsontext.Value
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
			b, _ := marshal(req)
			return sdk.HTTPResponse{Status: 201, Headers: map[string]string{"Content-Type": "application/json", "Set-Cookie": "x=1", "X-Evil": "1"}, Body: string(b)}, nil
		case req.Path == "/ck": // reports the cookies it was given and sets those named in ?set=
			b, _ := marshal(req.Cookies)
			res := sdk.HTTPResponse{Body: string(b)}
			for _, n := range req.Query["set"] {
				res.SetCookies = append(res.SetCookies, sdk.SetCookie{Name: n, Value: "v-" + n, MaxAge: 60})
			}
			return res, nil
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

	guest.OnFunc("shout", func(args []jsontext.Value) (string, error) {
		var s string
		if len(args) > 0 {
			_ = unmarshal(args[0], &s)
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

// call_host: input is a CallRequest; output an outcome.
//
//go:wasmexport call_host
func callHostExport() int32 {
	var req sdk.CallRequest
	in := guest.Input()
	if err := unmarshal(in, &req); err != nil {
		return 1
	}
	var args any
	if len(req.Args) > 0 {
		_ = unmarshal(req.Args, &args)
	}
	guest.OutputJSON(callHost(req.Fn, args))
	return 0
}
