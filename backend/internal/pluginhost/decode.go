package pluginhost

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"reflect"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// shape bounds the JSON a plugin may return for one payload type. Go values
// are much bigger than their JSON (`{}` is 2 bytes and a struct of hundreds),
// so output is scanned against these limits before it is decoded.
type shape struct {
	maxElems  int // longest array anywhere in the document
	maxTokens int // all tokens of the document
	maxDepth  int
}

var (
	headShape    = shape{maxElems: 64, maxTokens: 20_000, maxDepth: 16}                       // render.head: at most 64 tags; render.site_graph: same limits
	sitemapShape = shape{maxElems: sdk.MaxSitemapAdditions, maxTokens: 200_000, maxDepth: 16} // sitemap.urls: additions only
	// builder.data: at most 64 results or items, nothing deeper than an item's
	// attrs. A full contract answer (16 requests of 48 items with 16 attrs,
	// about 45 tokens an item) is under 40k tokens; 64k leaves room.
	builderDataShape = shape{maxElems: 64, maxTokens: 64_000, maxDepth: 6}
	jsonldShape      = shape{maxElems: 1_000, maxTokens: 100_000, maxDepth: 32}
	// request.redirect: a few declared cookies, one redirect, no nesting.
	redirectShape = shape{maxElems: 16, maxTokens: 2_000, maxDepth: 4}
	defaultShape  = shape{maxElems: 10_000, maxTokens: 200_000, maxDepth: 32}
)

func shapeOf(v any) shape {
	switch v.(type) {
	case sdk.HeadContext, sdk.SiteGraph:
		return headShape
	case sdk.SitemapURLs:
		return sitemapShape
	case sdk.JSONLDDoc:
		return jsonldShape
	case sdk.BuilderDataRequest:
		return builderDataShape
	case sdk.RedirectContext:
		return redirectShape
	}
	return defaultShape
}

// check scans raw and fails as soon as it exceeds a limit, so memory use is
// bounded by the limits rather than by the document.
func (s shape) check(raw []byte) error {
	dec := jsontext.NewDecoder(bytes.NewReader(raw), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	var counts []int // per open container: array element count, -1 for objects
	tokens := 0
	for {
		tok, err := dec.ReadToken()
		if errors.Is(err, io.EOF) {
			if len(counts) > 0 {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		if err != nil {
			return err
		}
		if tokens++; tokens > s.maxTokens {
			return fmt.Errorf("more than %d JSON tokens", s.maxTokens)
		}
		k := tok.Kind()
		isDelim := k == '{' || k == '['
		if k == '}' || k == ']' {
			counts = counts[:len(counts)-1]
			continue
		}
		if n := len(counts); n > 0 && counts[n-1] >= 0 {
			if counts[n-1]++; counts[n-1] > s.maxElems {
				return fmt.Errorf("array with more than %d elements", s.maxElems)
			}
		}
		if isDelim {
			if len(counts) >= s.maxDepth {
				return fmt.Errorf("nesting deeper than %d", s.maxDepth)
			}
			if k == '[' {
				counts = append(counts, 0)
			} else {
				counts = append(counts, -1)
			}
		}
	}
}

// decodeOutput turns a plugin's output into a value of v's type. in is v
// marshalled. Top-level fields the plugin leaves out keep their input value,
// so a plugin written in another language may return only what it changed;
// a field it does return replaces the input's wholesale.
func decodeOutput(v any, in, out []byte) (any, error) {
	if err := shapeOf(v).check(out); err != nil {
		return nil, err
	}
	var base, over map[string]jsontext.Value
	if err := jsonx.UnmarshalPinned(in, &base); err != nil {
		return nil, err
	}
	if err := jsonx.UnmarshalPinned(out, &over); err != nil {
		return nil, err
	}
	for k, val := range over {
		base[k] = val
	}
	merged, err := jsonx.MarshalPinned(base)
	if err != nil {
		return nil, err
	}
	res := reflect.New(reflect.TypeOf(v))
	if err := jsonx.UnmarshalPinned(merged, res.Interface()); err != nil {
		return nil, err
	}
	return res.Elem().Interface(), nil
}
