//go:build wasip1

package guest

import "hatamoto.dev/sdk"

// PackagesInspect asks core to download the https .hmp at req.URL and returns
// core's verdict. Problems with the package are reported in the result's
// Problems, not as an error; the body is never returned. Needs the
// packages.inspect capability.
func PackagesInspect(req sdk.InspectRequest) (sdk.InspectResult, error) {
	var out sdk.InspectResult
	err := Call(sdk.FnPackagesInspect, req, &out)
	return out, err
}
