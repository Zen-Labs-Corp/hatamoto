//go:build wasip1

// Command redirect is the test plugin of the request.redirect filter: its
// behaviour is picked by the plugin config key "mode".
package main

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func main() {}

// calls counts the invocations of this guest instance.
var calls int

func init() {
	guest.OnFilter(sdk.RequestRedirect, func(p sdk.RedirectContext) (sdk.RedirectContext, error) {
		calls++
		mode, _ := guest.Config("mode")
		pref := sdk.SetCookie{Name: "pref", Value: "x", MaxAge: 3600}
		switch mode {
		case "redirect": // 302 to /go carrying the country and the pref cookie
			q := url.Values{"c": {p.Country}, "pref": {p.Cookies["pref"]}}
			p.Redirect = &sdk.Redirect{Status: 302, Location: "/go?" + q.Encode()}
			p.SetCookies = []sdk.SetCookie{pref}
		case "cookie":
			p.SetCookies = []sdk.SetCookie{pref}
		case "empty":
		case "trap":
			panic("trap")
		case "sleep":
			for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			}
		case "badloc":
			p.Redirect = &sdk.Redirect{Status: 302, Location: "https://x"}
		case "protoloc":
			p.Redirect = &sdk.Redirect{Status: 302, Location: "//x"}
		case "config-location": // answers with the config key "location" as is
			loc, _ := guest.Config("location")
			p.Redirect = &sdk.Redirect{Status: 302, Location: loc}
		case "status301":
			p.Redirect = &sdk.Redirect{Status: 301, Location: "/go"}
		case "undeclared":
			p.Redirect = &sdk.Redirect{Status: 302, Location: "/go"}
			p.SetCookies = []sdk.SetCookie{{Name: "other", Value: "x"}}
		case "echo": // reports everything it was given in the Location
			names := make([]string, 0, len(p.Cookies))
			for n := range p.Cookies {
				names = append(names, n)
			}
			slices.Sort(names)
			q := url.Values{"path": {p.Path}, "query": {p.Query}, "country": {p.Country}, "cookies": {strings.Join(names, ",")}, "n": {strconv.Itoa(calls)}}
			p.Redirect = &sdk.Redirect{Status: 307, Location: "/echo?" + q.Encode()}
		}
		return p, nil
	})
}

//go:wasmexport filter_request_redirect
func filterRequestRedirect() int32 { return guest.Run(sdk.RequestRedirect.Export()) }
