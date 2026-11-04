//go:build nodynamic

package modern

// StaticBuild is true when the binary was built with the nodynamic tag, which
// keeps the encoder libraries from loading a system copy at run time.
const StaticBuild = true
