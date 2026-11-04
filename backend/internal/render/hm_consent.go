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
	"regexp"
	"sync/atomic"
)

// ConsentScriptName is the asset name of the consent banner script under AssetPrefix.
const ConsentScriptName = "hm-consent.js"

//go:embed hm_consent.js
var hmConsentSrc []byte

// ConsentConfig is the cookie consent configuration the served script is
// generated from. Strings are the dialog and button labels keyed by id; Title
// and Text are the owner's banner copy.
type ConsentConfig struct {
	Version    string            `json:"version"`
	Categories []string          `json:"categories"`
	Region     string            `json:"region"`
	Title      string            `json:"title"`
	Text       string            `json:"text"`
	Strings    map[string]string `json:"strings"`
}

// hash identifies the configuration; it is the cache-busting query of the tag,
// so cached pages and the script they load always agree.
func (c ConsentConfig) hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

var consentFn atomic.Pointer[func(context.Context) (ConsentConfig, bool)]

// SetConsent installs the consent banner hook: when f reports true, public
// pages carry the consent script and the asset is served. nil turns the
// banner off (the default), leaving every page byte-identical.
func SetConsent(f func(context.Context) (ConsentConfig, bool)) {
	if f == nil {
		consentFn.Store(nil)
		return
	}
	consentFn.Store(&f)
}

func consentConfig(ctx context.Context) (ConsentConfig, bool) {
	if f := consentFn.Load(); f != nil {
		return (*f)(ctx)
	}
	return ConsentConfig{}, false
}

// consentTag is the script tag placed first in <head>.
func consentTag(c ConsentConfig) template.HTML {
	return template.HTML(`<script src="` + html.EscapeString(AssetPrefix+ConsentScriptName+"?v="+c.hash()) + `"></script>`) // #nosec G203 -- constant path and a hex hash
}

// consentScript renders the script with the configuration substituted. The
// JSON is escaped for HTML-safe embedding even though it is served as JS.
func consentScript(c ConsentConfig) []byte {
	b, err := json.Marshal(c)
	if err != nil {
		b = []byte("{}")
	}
	return rewriteAPIPrefix(bytes.ReplaceAll(hmConsentSrc, []byte("__HM_CONSENT__"), b))
}

var headOpenRE = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)

// spliceHeadStart inserts markup right after the opening <head> tag so it runs
// before every other script; a page with no <head> falls back to spliceHead.
func spliceHeadStart(page []byte, tag template.HTML) []byte {
	loc := headOpenRE.FindIndex(page)
	if loc == nil {
		return spliceHead(page, tag)
	}
	out := make([]byte, 0, len(page)+len(tag))
	out = append(out, page[:loc[1]]...)
	out = append(out, tag...)
	return append(out, page[loc[1]:]...)
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
