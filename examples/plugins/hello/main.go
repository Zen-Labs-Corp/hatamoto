// Command hello is the reference Hatamoto plugin. It is a WASM module built
// with the standard Go toolchain (see `make plugin-hello`) and shows both
// hook kinds:
//
//   - action entity.published: logs the published entity;
//   - filter jsonld.enrich: adds x-hello (the "greeting" config value) to the
//     entity's JSON-LD.
package main

import (
	"fmt"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.EntityPublished, func(ev sdk.EntityEvent) error {
		guest.Log(guest.LogInfo, fmt.Sprintf("published %s/%s (%s)", ev.SchemaSlug, ev.Slug, ev.ID))
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
		d.Doc["x-hello"] = greeting
		return d, nil
	})
}

//go:wasmexport on_entity_published
func onEntityPublished() int32 { return guest.Run(sdk.EntityPublished.Export()) }

//go:wasmexport filter_jsonld_enrich
func filterJSONLDEnrich() int32 { return guest.Run(sdk.JSONLDEnrich.Export()) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
