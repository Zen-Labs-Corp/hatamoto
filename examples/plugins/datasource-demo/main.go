// Command datasource-demo is the reference builder data source. It answers
// the builder.data filter for three sources declared in plugin.json:
//
//   - ext.datasource-demo.temp     value (text): the "temp" config value;
//   - ext.datasource-demo.link     value (url):  the "link" config value;
//   - ext.datasource-demo.forecast items: five days, with loop.item.attributes.temp.
//
// The "mode" config value drives failures so a site can see how the page
// degrades: ok (default), slow (runs into the timeout), trap (panics), big
// (100 items, over the cap) and liar (also answers the id in "steal", which
// belongs to another plugin and is ignored by the host).
package main

import (
	"fmt"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func cfg(key, def string) string {
	if v, ok := guest.Config(key); ok && v != "" {
		return v
	}
	return def
}

func forecast(n int) []sdk.BuilderDataItem {
	items := make([]sdk.BuilderDataItem, 0, n)
	for i := 1; i <= n; i++ {
		items = append(items, sdk.BuilderDataItem{
			Title:    fmt.Sprintf("Day %d", i),
			URL:      fmt.Sprintf("/forecast/day-%d", i),
			Excerpt:  fmt.Sprintf("Expected %d degrees.", 15+i),
			Category: "Weather",
			Attrs:    map[string]string{"temp": fmt.Sprintf("%d°C", 15+i)},
		})
	}
	return items
}

func init() {
	guest.OnFilter(sdk.BuilderData, func(req sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
		guest.Log(guest.LogInfo, fmt.Sprintf("builder.data requests=%d", len(req.Requests)))
		mode := cfg("mode", "ok")
		switch mode {
		case "trap":
			panic("datasource-demo: trap mode")
		case "slow":
			for {
			}
		}
		for _, q := range req.Requests {
			res := sdk.BuilderDataResult{ID: q.ID}
			// The host only sends this plugin's own sources, ext.<plugin>.<name>.
			name := q.Source[strings.LastIndex(q.Source, ".")+1:]
			switch name {
			case "temp":
				res.Value = cfg("temp", "21°C")
			case "link":
				res.Value = cfg("link", "https://example.com/docs")
			case "forecast":
				n := 5
				if mode == "big" {
					n = 100
				} else if q.Limit > 0 && q.Limit < n {
					n = q.Limit
				}
				res.Items = forecast(n)
			default:
				res.Error = "unknown source"
			}
			req.Results = append(req.Results, res)
		}
		if mode == "liar" {
			req.Results = append(req.Results, sdk.BuilderDataResult{ID: cfg("steal", "r-other"), Value: "stolen"})
		}
		return req, nil
	})
}

//go:wasmexport filter_builder_data
func filterBuilderData() int32 { return guest.Run(sdk.BuilderData.Export()) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
