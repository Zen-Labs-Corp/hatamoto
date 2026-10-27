// Package guest is the Go side of the plugin ABI: a thin layer over the
// Extism Go PDK that turns typed handlers into the JSON-in/JSON-out exports
// the host calls (see docs/PLUGIN_API.md).
//
// Build a plugin with the standard Go toolchain:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
//
// A plugin registers handlers in init() and forwards each export to Run.
// //go:wasmexport must sit on a function in package main, so every hook the
// plugin handles needs one line:
//
//	func init() {
//		guest.OnAction(sdk.EntityPublished, func(ev sdk.EntityEvent) error { ... })
//		guest.OnFilter(sdk.JSONLDEnrich, func(d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) { ... })
//	}
//
//	//go:wasmexport on_entity_published
//	func onEntityPublished() int32 { return guest.Run(sdk.EntityPublished.Export()) }
//
//	//go:wasmexport filter_jsonld_enrich
//	func filterJSONLDEnrich() int32 { return guest.Run(sdk.JSONLDEnrich.Export()) }
//
//	func main() {}
//
// Run (and the PDK wrappers Log and Config) exist only for GOOS=wasip1.
package guest
