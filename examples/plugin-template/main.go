// Command hatamoto-plugin-template is a starting point for a Hatamoto plugin.
// It is a WASM module built with the standard Go toolchain (no TinyGo). It
// logs every published entity (an action) and adds a field to the JSON-LD of
// each page (a filter). Replace both handlers with your own.
package main

import (
	"fmt"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.EntityPublished, func(ev sdk.EntityEvent) error {
		guest.Log(guest.LogInfo, fmt.Sprintf("published %s/%s", ev.SchemaSlug, ev.Slug))
		return nil
	})

	guest.OnFilter(sdk.JSONLDEnrich, func(d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
		greeting, ok := guest.Config("greeting")
		if !ok {
			greeting = "hello"
		}
		if d.Doc == nil {
			d.Doc = map[string]any{}
		}
		d.Doc["x-template"] = greeting
		return d, nil
	})
}

// One //go:wasmexport line per hook the plugin handles. The export name comes
// from the hook (see docs/PLUGIN_API.md in the Hatamoto repository).

//go:wasmexport on_entity_published
func onEntityPublished() int32 { return guest.Run(sdk.EntityPublished.Export()) }

//go:wasmexport filter_jsonld_enrich
func filterJSONLDEnrich() int32 { return guest.Run(sdk.JSONLDEnrich.Export()) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
