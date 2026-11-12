package wpimport

import (
	"regexp"
	"testing"
)

// TestLazyRegexpsCompile forces every lazily compiled pattern, so a bad one
// still fails the test run instead of the first import.
func TestLazyRegexpsCompile(t *testing.T) {
	lazy := map[string]func() *regexp.Regexp{
		"attrPairRe": attrPairRe, "blankLinesRe": blankLinesRe, "blockStart": blockStart,
		"canonicalRe": canonicalRe, "captionImg": captionImg, "captionRe": captionRe,
		"commentRe": commentRe, "embedSCRe": embedSCRe, "linkTagRe": linkTagRe,
		"metaContentRE": metaContentRE, "metaTagRe": metaTagRe, "shortcodeRe": shortcodeRe,
		"tagRe": tagRe, "titleRe": titleRe, "twitterURLRE": twitterURLRE,
		"vimeoRe": vimeoRe, "youtubeRe": youtubeRe,
	}
	for name, get := range lazy {
		if get() == nil {
			t.Errorf("%s compiled to nil", name)
		}
	}
	for plugin, spec := range seoSpecs {
		for _, get := range []func() *regexp.Regexp{spec.typeTitle, spec.typeDesc, spec.taxTitle, spec.typeNoindex, spec.taxNoindex} {
			if get != nil && get() == nil {
				t.Errorf("%s: spec pattern compiled to nil", plugin)
			}
		}
	}
}
