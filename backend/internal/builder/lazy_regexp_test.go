package builder

import (
	"regexp"
	"testing"
)

// TestLazyRegexpsCompile forces every lazily compiled package-level pattern, so
// a bad pattern still fails the test run instead of the first request.
func TestLazyRegexpsCompile(t *testing.T) {
	lazy := map[string]func() *regexp.Regexp{
		"anchorRe": anchorRe, "bblurRe": bblurRe, "bindSegRe": bindSegRe, "bwRe": bwRe,
		"extParamNameRe": extParamNameRe, "extRe": extRe, "gainRe": gainRe, "gradRe": gradRe,
		"grayRe": grayRe, "gtcRe": gtcRe, "hexRe": hexRe, "hueRe": hueRe, "idRe": idRe,
		"inlineLinkRe": inlineLinkRe, "lenRe": lenRe, "lhRe": lhRe, "lsRe": lsRe,
		"mediaRe": mediaRe, "packKindRe": packKindRe, "pluginNameRe": pluginNameRe,
		"presetRe": presetRe, "rotRe": rotRe, "scaleRe": scaleRe, "scrollLenRe": scrollLenRe,
		"shadowRe": shadowRe, "shiftRe": shiftRe, "signedLenRe": signedLenRe, "skewRe": skewRe,
		"tokenLenRe": tokenLenRe, "uuidRe": uuidRe, "widgetNameRe": widgetNameRe,
	}
	for name, get := range lazy {
		if get() == nil {
			t.Errorf("%s compiled to nil", name)
		}
	}
}
