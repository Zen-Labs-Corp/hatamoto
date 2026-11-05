package media

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Limits for SanitizeSVG. They bound the work a hostile file can cause.
const (
	// MaxSVGBytes is the largest SVG accepted.
	MaxSVGBytes = 512 << 10
	maxSVGDepth = 64
	maxSVGNodes = 20000

	svgNS   = "http://www.w3.org/2000/svg"
	xlinkNS = "http://www.w3.org/1999/xlink"
)

var svgElements = setOf(
	"svg", "g", "defs", "symbol", "use", "path", "rect", "circle", "ellipse", "line",
	"polyline", "polygon", "text", "tspan", "title", "desc", "linearGradient",
	"radialGradient", "stop", "clipPath", "mask", "pattern", "image", "style", "marker",
)

var svgAttrs = setOf(
	"x", "y", "width", "height", "cx", "cy", "r", "rx", "ry", "x1", "y1", "x2", "y2",
	"fx", "fy", "points", "d", "viewBox", "preserveAspectRatio", "transform",
	"fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-linecap",
	"stroke-linejoin", "stroke-miterlimit", "stroke-dasharray", "stroke-dashoffset",
	"stroke-opacity", "opacity", "class", "id", "style", "offset", "stop-color",
	"stop-opacity", "gradientUnits", "gradientTransform", "spreadMethod",
	"clipPathUnits", "clip-path", "clip-rule", "mask", "maskUnits", "maskContentUnits",
	"patternUnits", "patternContentUnits", "patternTransform", "markerWidth",
	"markerHeight", "markerUnits", "refX", "refY", "orient", "marker-start",
	"marker-mid", "marker-end", "font-family", "font-size", "font-weight",
	"font-style", "text-anchor", "dominant-baseline", "letter-spacing", "dx", "dy",
	"rotate", "textLength", "lengthAdjust", "display", "visibility", "filter",
	"color", "overflow", "vector-effect", "mix-blend-mode", "version", "href",
)

var (
	svgDataHref  = regexp.MustCompile(`^data:image/(png|jpeg);base64,[A-Za-z0-9+/=\s]*$`)
	svgURLRef    = regexp.MustCompile(`(?i)url\(\s*['"]?\s*([^)]*)`)
	svgBadStyles = []string{"@import", "image-set", "src(", "//", "expression", "javascript:", "vbscript:", "behavior", "-moz-binding", "\\", "<"}
)

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// SanitizeSVG is the only entry point that makes an SVG storable. It parses
// the document with an allowlist and writes it back itself, so what is stored
// is never the input bytes. Anything not on the allowlist is dropped and
// listed in removed; a document that cannot be made safe (DOCTYPE or ENTITY,
// malformed XML, too large, too deep, nothing left) returns an *SVGError.
func SanitizeSVG(in []byte) (clean []byte, removed []string, err error) {
	if len(in) > MaxSVGBytes {
		return nil, nil, &SVGError{Reason: "it is larger than 512 KB"}
	}
	dec := xml.NewDecoder(bytes.NewReader(in))
	s := &svgSanitizer{dec: dec}
	if err := s.run(); err != nil {
		return nil, nil, err
	}
	return s.out.Bytes(), s.removed, nil
}

type svgSanitizer struct {
	dec       *xml.Decoder
	out       bytes.Buffer
	styleText bytes.Buffer
	removed   []string
	seen      map[string]bool
}

func (s *svgSanitizer) fail(reason string) error {
	line, _ := s.dec.InputPos()
	return &SVGError{Reason: reason, Line: line}
}

func (s *svgSanitizer) drop(what string) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if !s.seen[what] {
		s.seen[what] = true
		s.removed = append(s.removed, what)
	}
}

func (s *svgSanitizer) run() error {
	var (
		depth, skip, nodes, kept int
		rootDone                 bool
		inStyle                  []bool
	)
	for {
		tok, err := s.dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var se *xml.SyntaxError
			if errors.As(err, &se) {
				return &SVGError{Reason: "the XML is malformed: " + se.Msg, Line: se.Line}
			}
			return s.fail("the XML is malformed")
		}
		switch t := tok.(type) {
		case xml.Directive:
			return s.fail("it contains a DOCTYPE or ENTITY declaration")
		case xml.ProcInst:
			if t.Target != "xml" {
				s.drop("<?" + t.Target + "?> instruction")
			}
		case xml.Comment:
			// dropped
		case xml.StartElement:
			depth++
			nodes++
			if depth > maxSVGDepth {
				return s.fail("it is nested too deeply")
			}
			if nodes > maxSVGNodes {
				return s.fail("it has too many elements")
			}
			if rootDone {
				return s.fail("it has more than one root element")
			}
			if depth == 1 && (t.Name.Local != "svg" || (t.Name.Space != "" && t.Name.Space != svgNS)) {
				return s.fail("the root element is not svg")
			}
			if skip > 0 {
				skip++
				continue
			}
			if len(inStyle) > 0 && inStyle[len(inStyle)-1] {
				// <style> holds text only; an element inside it would split
				// the text that is vetted as one piece.
				s.drop("<" + t.Name.Local + "> element inside style")
				skip = 1
				continue
			}
			if (t.Name.Space != "" && t.Name.Space != svgNS) || !svgElements[t.Name.Local] {
				s.drop("<" + t.Name.Local + "> element")
				skip = 1
				continue
			}
			if depth > 1 {
				kept++
			}
			s.start(t, depth == 1)
			inStyle = append(inStyle, t.Name.Local == "style")
		case xml.EndElement:
			depth--
			if skip > 0 {
				skip--
				continue
			}
			if inStyle[len(inStyle)-1] {
				if text := s.styleText.String(); safeCSS(text) {
					_ = xml.EscapeText(&s.out, []byte(text))
				} else {
					s.drop("style rule with an external or script reference")
				}
				s.styleText.Reset()
			}
			s.out.WriteString("</" + t.Name.Local + ">")
			inStyle = inStyle[:len(inStyle)-1]
			if depth == 0 {
				rootDone = true
			}
		case xml.CharData:
			if depth == 0 {
				if len(bytes.TrimSpace(t)) > 0 {
					return s.fail("it has text outside the svg element")
				}
				continue
			}
			if skip > 0 {
				continue
			}
			if inStyle[len(inStyle)-1] {
				// Vetted whole at the end tag: comments and CDATA split the
				// text into several tokens.
				s.styleText.Write(t)
				continue
			}
			_ = xml.EscapeText(&s.out, t)
		}
	}
	if !rootDone {
		return s.fail("it has no svg element")
	}
	if kept == 0 {
		if len(s.removed) > 0 {
			return s.fail("it contains " + strings.Join(s.removed, ", ") + ", and nothing is left after removing unsafe content")
		}
		return s.fail("nothing is left after removing unsafe content")
	}
	return nil
}

func (s *svgSanitizer) start(t xml.StartElement, root bool) {
	s.out.WriteString("<" + t.Name.Local)
	if root {
		s.out.WriteString(` xmlns="` + svgNS + `" xmlns:xlink="` + xlinkNS + `"`)
	}
	used := map[string]bool{}
	for _, a := range t.Attr {
		name, val := a.Name.Local, a.Value
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && name == "xmlns") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(name), "on") {
			s.drop(name + " attribute")
			continue
		}
		out := name
		switch a.Name.Space {
		case "":
		case xlinkNS:
			if name != "href" {
				continue
			}
			out = "xlink:href"
		default:
			continue
		}
		if !svgAttrs[name] || used[out] {
			continue
		}
		if name == "href" {
			if !strings.HasPrefix(val, "#") && !svgDataHref.MatchString(val) {
				s.drop("link to " + shortRef(val))
				continue
			}
		} else if name == "style" {
			if !safeCSS(val) {
				s.drop("style with an external or script reference")
				continue
			}
		} else if !safeValue(val) {
			s.drop(name + " attribute with an external or script reference")
			continue
		}
		used[out] = true
		s.out.WriteString(" " + out + `="`)
		_ = xml.EscapeText(&s.out, []byte(val))
		s.out.WriteString(`"`)
	}
	s.out.WriteString(">")
}

func shortRef(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexAny(v, ":/"); i > 0 && v[i] == ':' {
		return v[:i+1] + " address"
	}
	return "an external address"
}

// safeValue vets a presentation attribute value: no script scheme, and any
// url() must point at a fragment of the same document.
func safeValue(v string) bool {
	l := strings.ToLower(v)
	if strings.Contains(l, "javascript:") || strings.Contains(l, "vbscript:") ||
		strings.Contains(l, "expression(") || strings.Contains(l, "<") ||
		strings.Contains(l, "image-set") || strings.Contains(l, "src(") || strings.Contains(l, "//") {
		return false
	}
	return onlyLocalURLs(l)
}

func onlyLocalURLs(l string) bool {
	for _, m := range svgURLRef.FindAllStringSubmatch(l, -1) {
		if !strings.HasPrefix(m[1], "#") {
			return false
		}
	}
	return true
}

// safeCSS vets style text. It is stricter than needed on purpose: a rule it
// cannot be sure about is dropped.
func safeCSS(css string) bool {
	l := strings.ToLower(css)
	for _, bad := range svgBadStyles {
		if strings.Contains(l, bad) {
			return false
		}
	}
	return onlyLocalURLs(l)
}
