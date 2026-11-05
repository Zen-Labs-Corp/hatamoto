package render

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"html/template"
	"sync/atomic"

	"hatamoto.dev/core/internal/jsonx"
)

// AdditionalCSSKey is the theme_extra key holding the owner's own CSS.
const AdditionalCSSKey = "additional_css"

// AdditionalCSS reads the owner's CSS from a raw theme_extra document; "" when
// unset or unreadable.
func AdditionalCSS(themeExtra jsontext.Value) string {
	if len(themeExtra) == 0 {
		return ""
	}
	var m map[string]jsontext.Value
	if jsonx.Unmarshal(themeExtra, &m, jsonx.Stored) != nil {
		return ""
	}
	var css string
	if jsonx.Unmarshal(m[AdditionalCSSKey], &css, jsonx.Stored) != nil {
		return ""
	}
	return css
}

var customFn atomic.Pointer[func(context.Context) string]

// SetCustomCSS installs the source of the owner's stylesheet, read when the
// asset is requested. nil turns it off (the default).
func SetCustomCSS(f func(context.Context) string) {
	if f == nil {
		customFn.Store(nil)
		return
	}
	customFn.Store(&f)
}

// CustomCSSName is the fingerprinted file name for css, or "" when css is
// blank or refused by ValidateCustomCSS. Head and handler both use it, so a
// cached page and the file it links always agree.
func CustomCSSName(css string) string {
	body, ok := SanitizeCustomCSS(css)
	if !ok {
		return ""
	}
	sum := sha256.Sum256([]byte(body))
	return "custom." + hex.EncodeToString(sum[:])[:12] + ".css"
}

// customCSS returns the current stylesheet and its file name; ok is false when
// none is set or it no longer validates.
func customCSS(ctx context.Context) (body []byte, name string, ok bool) {
	f := customFn.Load()
	if f == nil {
		return nil, "", false
	}
	css := (*f)(ctx)
	if name = CustomCSSName(css); name == "" {
		return nil, "", false
	}
	return []byte(css), name, true
}

// customLink is the stylesheet link placed after the theme's own.
func customLink(name string) template.HTML {
	return template.HTML(`<link rel="stylesheet" href="` + AssetPrefix + name + `">`) // #nosec G203 -- constant prefix and a hex-hash name
}
