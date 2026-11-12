package builder

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/jsonx"
)

// Param types of an items source (sdk.BuilderParam).
const (
	ParamText   = "text"
	ParamSelect = "select"
	ParamBool   = "bool"
	ParamNumber = "number"
)

// Limits of a source's params; they mirror the SDK manifest limits.
const (
	maxExtParams       = 8
	maxExtParamOptions = 50
	// MaxParamValue is the longest text or option value, in bytes.
	MaxParamValue = 200
	// maxStoredParams is the most entries a loop may store in its params.
	maxStoredParams = 32
)

// ExtParam is one typed filter of a plugin items source.
type ExtParam struct {
	Name, Label, Type string
	Options           []ExtOption // select only
}

// ExtOption is one choice of a select param.
type ExtOption struct{ Value, Label string }

var extParamNameRe = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`) })

func cloneExtParams(ps []ExtParam) []ExtParam {
	if len(ps) == 0 {
		return nil
	}
	out := slices.Clone(ps)
	for i := range out {
		out[i].Options = slices.Clone(out[i].Options)
	}
	return out
}

// cleanExtParams returns a private copy of ps with the invalid entries
// dropped; a value source has none.
func cleanExtParams(kind string, ps []ExtParam) []ExtParam {
	if kind != ExtItems {
		return nil
	}
	var out []ExtParam
	seen := map[string]bool{}
	for _, p := range cloneExtParams(ps) {
		if len(out) == maxExtParams || seen[p.Name] || !extParamNameRe().MatchString(p.Name) {
			continue
		}
		switch p.Type {
		case ParamSelect:
			if len(p.Options) == 0 || len(p.Options) > maxExtParamOptions {
				continue
			}
		case ParamText, ParamBool, ParamNumber:
			p.Options = nil
		default:
			continue
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	return out
}

// SanitizeParams keeps the values of raw that decl accepts and returns them,
// or nil when none survive. Unknown names, strings over MaxParamValue bytes,
// select values outside the options, wrongly typed values and empty values
// (an empty string, false) are dropped. Numbers are normalised to float64.
// It is the single source of truth: render builds the request key from it and
// the plugin host re-applies it to what the plugin receives.
func SanitizeParams(decl []ExtParam, raw map[string]any) map[string]any {
	var out map[string]any
	for _, p := range decl {
		v, ok := raw[p.Name]
		if !ok {
			continue
		}
		var keep any
		switch p.Type {
		case ParamText:
			if s, ok := v.(string); ok && s != "" && len(s) <= MaxParamValue {
				keep = s
			}
		case ParamSelect:
			if s, ok := v.(string); ok && slices.ContainsFunc(p.Options, func(o ExtOption) bool { return o.Value == s }) {
				keep = s
			}
		case ParamBool:
			if b, ok := v.(bool); ok && b {
				keep = true
			}
		case ParamNumber:
			if f, ok := paramFloat(v); ok {
				keep = f
			}
		}
		if keep == nil {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[p.Name] = keep
	}
	return out
}

// paramFloat reads a finite JSON number.
func paramFloat(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

// CanonicalParams encodes sanitized params with sorted keys, so equal params
// give equal bytes whatever the map order or number spelling was.
func CanonicalParams(p map[string]any) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		v := p[k]
		if f, ok := paramFloat(v); ok {
			v = f
		}
		kb, _ := jsonx.MarshalPinned(k)
		vb, err := jsonx.MarshalPinned(v)
		if err != nil {
			vb = []byte("null")
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.String()
}

// StructuralParamsErr checks the shape of a loop's stored params, not their
// meaning: an object of at most 32 scalar values, strings up to MaxParamValue
// bytes. The declarations are checked at request time, so a plugin update
// that changes its options never makes a saved page unsaveable.
func StructuralParamsErr(raw any) error {
	// Round-trip so a Go-built value checks like a decoded one.
	b, err := jsonx.Marshal(raw)
	if err != nil {
		return fmt.Errorf("params: %w", err)
	}
	var v any
	if err := jsonx.Unmarshal(b, &v, jsonx.Stored); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("params must be an object")
	}
	if len(m) > maxStoredParams {
		return fmt.Errorf("params has more than %d entries", maxStoredParams)
	}
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if len(x) > MaxParamValue {
				return fmt.Errorf("params.%s is longer than %d bytes", k, MaxParamValue)
			}
		case bool, float64, int, int64:
		default:
			return fmt.Errorf("params.%s must be a string, number or boolean", k)
		}
	}
	return nil
}
