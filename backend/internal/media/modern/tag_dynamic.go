//go:build !nodynamic

package modern

// StaticBuild is false when the binary was built without the nodynamic tag.
// Such a binary may be dynamically linked; product builds always set the tag.
const StaticBuild = false
