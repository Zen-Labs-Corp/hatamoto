// Package sdk is the Hatamoto plugin contract: hook names and payload DTOs,
// the plugin.json manifest, and the export-name rules of the WASM ABI. It has
// no dependencies beyond the standard library, so hosts and guests share it.
// Subpackage guest is the Go guest layer over the Extism PDK.
//
// Versioning: semantic. Minor releases are additive only; breaking changes
// bump the major version (before 1.0, the minor). A manifest declares the
// range it was built against and the host refuses a plugin whose range does
// not contain Version.
//
// Plugin authors start with the guest subpackage and the worked example in
// examples/plugins/hello of the Hatamoto repository; the payload schemas of
// every hook are in docs/PLUGIN_API.md. This module never imports Hatamoto's
// internal packages, and the server is not a library.
package sdk
