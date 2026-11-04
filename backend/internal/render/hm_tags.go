package render

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"html"
	"html/template"
	"sync/atomic"
)

// TagsScriptName is the asset name of the tag manager script under AssetPrefix.
const TagsScriptName = "hm-tags.js"

//go:embed hm_tags.js
var hmTagsSrc []byte

// TagVendor is one enabled vendor as the served script reads it. ID is the
// vendor key, Value the owner's id for it, Aux the optional second id and Src
// the loader URL with the id already substituted.
type TagVendor struct {
	ID       string `json:"id"`
	Boot     string `json:"boot"`
	Category string `json:"category"`
	Src      string `json:"src"`
	Aux      string `json:"aux"`
	Value    string `json:"value"`
}

// TagsConfig is the tag manager configuration the served script is generated
// from. Noscript is markup for the page body, not part of the script.
type TagsConfig struct {
	ExcludeSignedIn bool   `json:"excludeSignedIn"`
	Member          string `json:"member"` // marker cookie name
	// Consent is set when the consent banner is on: a vendor then loads only
	// once hm-consent.js is present and allows it, so a blocked or uncached
	// consent script fails closed.
	Consent  bool          `json:"consent"`
	Vendors  []TagVendor   `json:"vendors"`
	Noscript template.HTML `json:"-"`
}

// hash identifies the configuration, noscript included; it is the
// cache-busting query of the tag, so cached pages and the script they load
// always agree.
func (c TagsConfig) hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(append(b, []byte(c.Noscript)...))
	return hex.EncodeToString(sum[:])[:12]
}

var tagsFn atomic.Pointer[func(context.Context) (TagsConfig, bool)]

// SetTags installs the tag manager hook: when f reports true, public pages
// link the tag script and the asset is served. nil turns it off (the
// default), leaving every page byte-identical.
func SetTags(f func(context.Context) (TagsConfig, bool)) {
	if f == nil {
		tagsFn.Store(nil)
		return
	}
	tagsFn.Store(&f)
}

func tagsConfig(ctx context.Context) (TagsConfig, bool) {
	if f := tagsFn.Load(); f != nil {
		return (*f)(ctx)
	}
	return TagsConfig{}, false
}

// tagsMarkup is the deferred script tag plus the optional noscript block.
func tagsMarkup(c TagsConfig) template.HTML {
	m := `<script src="` + html.EscapeString(AssetPrefix+TagsScriptName+"?v="+c.hash()) + `" defer></script>`
	if c.Noscript != "" {
		m += "<noscript>" + string(c.Noscript) + "</noscript>"
	}
	return template.HTML(m) // #nosec G203 -- constant path and a hex hash; the noscript markup comes from the vendor table
}

// tagsScript renders the script with the configuration substituted.
func tagsScript(c TagsConfig) []byte {
	b, err := json.Marshal(c)
	if err != nil {
		b = []byte(`{"vendors":[]}`)
	}
	return rewriteAPIPrefix(bytes.ReplaceAll(hmTagsSrc, []byte("__HM_TAGS__"), b))
}
